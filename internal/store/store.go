// Package store is the application's own database: which tags exist, what has
// happened to each one, and who is owed an escrowed bonus.
//
// It is deliberately separate from the wallet database the toolbox owns. The
// wallet knows about coins; this knows about crabs. The two are reconciled by
// the audit command, and a disagreement between them is a finding rather than
// something to paper over.
//
// # What is not stored here
//
// Tag secrets. Ever. A tag's spending key is derived from the master seed and
// its ordinal, so the ordinal is enough to regenerate it when DNR needs to
// sweep a stranded reward -- and storing the secret itself would turn a
// database leak into a drained treasury. The public key is stored because it is
// public and because the audit needs to check locking scripts against it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres driver
	_ "modernc.org/sqlite"             // pure-Go sqlite driver, so the image stays CGO-free
)

// Status is where a tag sits in its life.
type Status string

const (
	// StatusMinted means printed but not yet armed: no money, no crab.
	StatusMinted Status = "minted"
	// StatusActive means a reward is locked and the crab is in the water.
	StatusActive Status = "active"
	// StatusRedeeming is held for the few seconds a redemption is being built
	// and broadcast. It is what makes two crabbers scanning the same tag at the
	// same moment resolve to one winner and one clear refusal, rather than two
	// transactions racing to spend one output.
	StatusRedeeming Status = "redeeming"
	// StatusCooldown means a recapture was reported and released; the tag is
	// waiting to be re-armed. The cooldown exists so the reporter who just
	// walked away cannot immediately claim the next reward too.
	StatusCooldown Status = "cooldown"
	// StatusRetired means the crab was kept, or the tag was swept.
	StatusRetired Status = "retired"
)

// EventStatus tracks a transaction's life, mirroring the arcade lifecycle
// closely enough to be useful without pretending to be authoritative about it.
type EventStatus string

const (
	// EventAttempting is written *before* signing. Signing is broadcasting:
	// past that point the transaction may exist on the network whatever happens
	// locally, so the record has to exist first or a crash leaves a spend
	// nobody knows about.
	EventAttempting EventStatus = "attempting"
	EventBroadcast  EventStatus = "broadcast"
	EventMined      EventStatus = "mined"
	EventFailed     EventStatus = "failed"
)

var (
	// ErrNotFound is returned instead of sql.ErrNoRows so callers do not have
	// to import database/sql to handle a missing tag.
	ErrNotFound = errors.New("store: not found")
	// ErrTagNotAvailable means the tag is not in a state that permits the
	// requested transition. It is the single-writer guard for redemption.
	ErrTagNotAvailable = errors.New("store: tag is not available for this operation")
)

// Store is the application database.
type Store struct {
	db       *sql.DB
	postgres bool
}

// Open connects to the application database.
//
// An empty dsn selects SQLite at the given path; a non-empty one selects
// Postgres. Both halves of a deployment (wallet and application) can then share
// one Postgres instance, which is what production wants, while a laptop gets a
// single file.
func Open(ctx context.Context, sqlitePath, postgresDSN string) (*Store, error) {
	var (
		db  *sql.DB
		err error
		pg  bool
	)
	if strings.TrimSpace(postgresDSN) != "" {
		db, err = sql.Open("pgx", postgresDSN)
		pg = true
	} else {
		// _txlock=immediate takes the write lock at BEGIN rather than at the
		// first write, which turns SQLite's "database is locked" surprise at
		// commit time into an ordinary contended BEGIN.
		db, err = sql.Open("sqlite", sqlitePath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate")
	}
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}

	// database/sql defaults to unlimited connections. rule-110-arcade exhausted
	// a stock Postgres's 200 slots in seconds this way, and here a redemption
	// that cannot write its pre-signing record is a redemption that must be
	// refused -- so the cap is deliberate, not incidental.
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	s := &Store{db: db, postgres: pg}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for the audit command's read-only queries.
func (s *Store) DB() *sql.DB { return s.db }

// rebind converts ? placeholders to $N for Postgres. Writing every query once
// in the portable form and translating here beats maintaining two dialects.
func (s *Store) rebind(q string) string {
	if !s.postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

func (s *Store) exec(ctx context.Context, q string, args ...any) error {
	_, err := s.db.ExecContext(ctx, s.rebind(q), args...)
	return err
}

// timestampType is the column type for an instant. Postgres wants a real
// timestamptz; SQLite has no date type and stores RFC3339 text, which sorts
// correctly and stays readable in a dump.
func (s *Store) timestampType() string {
	if s.postgres {
		return "TIMESTAMPTZ"
	}
	return "TEXT"
}

func (s *Store) autoIncrementPK() string {
	if s.postgres {
		return "BIGSERIAL PRIMARY KEY"
	}
	return "INTEGER PRIMARY KEY AUTOINCREMENT"
}

// migrate creates the schema. The statements are idempotent rather than
// versioned: this schema is small, and a goose stream here would be ceremony
// around six tables.
func (s *Store) migrate(ctx context.Context) error {
	ts := s.timestampType()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS batches (
			id           TEXT PRIMARY KEY,
			created_at   ` + ts + ` NOT NULL,
			created_by   TEXT NOT NULL,
			tag_count    INTEGER NOT NULL,
			first_ordinal BIGINT NOT NULL,
			manifest_hash TEXT NOT NULL,
			anchor_txid  TEXT NOT NULL DEFAULT '',
			-- The species this run was printed for. A hint, not a fact: the
			-- QR density a tag can carry depends on the stock it is printed
			-- on, and a deer ear tag is not a 1x2in crab tag. What the animal
			-- actually was is decided at activation and recorded on chain.
			species      TEXT NOT NULL DEFAULT '',
			secrets_exported_at ` + ts + `
		)`,

		`CREATE TABLE IF NOT EXISTS tags (
			tag_id        TEXT PRIMARY KEY,
			batch_id      TEXT NOT NULL,
			ordinal       BIGINT NOT NULL UNIQUE,
			pubkey_hex    TEXT NOT NULL,
			status        TEXT NOT NULL,
			-- The species this tag was armed for, empty until it is armed. It
			-- is fixed at activation: a report naming a different species is a
			-- finding, not something to reconcile silently.
			species       TEXT NOT NULL DEFAULT '',
			-- What the animal is called, or empty until somebody names it. This
			-- is a cache: the authoritative name is in the first on-chain
			-- record that carries one, and the audit compares the two.
			animal_name   TEXT NOT NULL DEFAULT '',
			named_by      TEXT NOT NULL DEFAULT '',
			generation    INTEGER NOT NULL DEFAULT 0,
			live_txid     TEXT NOT NULL DEFAULT '',
			live_vout     INTEGER NOT NULL DEFAULT 0,
			live_satoshis BIGINT NOT NULL DEFAULT 0,
			created_at    ` + ts + ` NOT NULL,
			activated_at  ` + ts + `,
			last_event_at ` + ts + `,
			cooldown_until ` + ts + `,
			sweep_after   ` + ts + `,
			retired_at    ` + ts + `
		)`,

		`CREATE TABLE IF NOT EXISTS tag_events (
			id            ` + s.autoIncrementPK() + `,
			tag_id        TEXT NOT NULL,
			generation    INTEGER NOT NULL,
			kind          TEXT NOT NULL,
			txid          TEXT NOT NULL DEFAULT '',
			vout          INTEGER NOT NULL DEFAULT 0,
			satoshis      BIGINT NOT NULL DEFAULT 0,
			payload_json  TEXT NOT NULL DEFAULT '',
			settlement_json TEXT NOT NULL DEFAULT '',
			attest_pubkey TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL,
			err           TEXT NOT NULL DEFAULT '',
			created_at    ` + ts + ` NOT NULL,
			updated_at    ` + ts + ` NOT NULL
		)`,

		// One record per tag per generation per kind. This is the constraint
		// that makes a double report impossible at the database layer as well
		// as the chain layer.
		`CREATE UNIQUE INDEX IF NOT EXISTS tag_events_unique
			ON tag_events (tag_id, generation, kind)`,

		// The primary key is the wrong column order for "what has this tag done
		// lately", which is the question the tag page asks on every load.
		`CREATE INDEX IF NOT EXISTS tag_events_by_tag ON tag_events (tag_id, generation DESC)`,

		`CREATE INDEX IF NOT EXISTS tag_events_by_txid ON tag_events (txid)`,

		// A partial index, because "which transactions are still in flight" is
		// asked on a timer and status is a low-cardinality column: a plain
		// index on it degrades to a full scan.
		`CREATE INDEX IF NOT EXISTS tag_events_inflight ON tag_events (updated_at)
			WHERE status NOT IN ('mined', 'failed')`,

		`CREATE INDEX IF NOT EXISTS tags_by_status ON tags (status)`,

		// Who is owed a re-release bonus, and whether it has been paid. This
		// mirrors the escrowFor commitment written into the locking script, so
		// the audit can compare the two.
		`CREATE TABLE IF NOT EXISTS escrows (
			tag_id             TEXT NOT NULL,
			generation         INTEGER NOT NULL,
			beneficiary_pubkey TEXT NOT NULL,
			satoshis           BIGINT NOT NULL,
			created_at         ` + ts + ` NOT NULL,
			paid_txid          TEXT NOT NULL DEFAULT '',
			paid_at            ` + ts + `,
			-- The beneficiary is not present when their bonus is paid, so the
			-- BRC-29 derivation has to be stored: without it their wallet has
			-- no way to find the output when they eventually come back. These
			-- are not secret; they travel in every payment remittance.
			paid_prefix        TEXT NOT NULL DEFAULT '',
			paid_suffix        TEXT NOT NULL DEFAULT '',
			paid_vout          INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tag_id, generation)
		)`,

		`CREATE INDEX IF NOT EXISTS escrows_unpaid ON escrows (beneficiary_pubkey) WHERE paid_txid = ''`,

		`CREATE TABLE IF NOT EXISTS sessions (
			token        TEXT PRIMARY KEY,
			identity_key TEXT NOT NULL,
			label        TEXT NOT NULL DEFAULT '',
			created_at   ` + ts + ` NOT NULL,
			expires_at   ` + ts + ` NOT NULL
		)`,

		`CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions (expires_at)`,

		`CREATE TABLE IF NOT EXISTS admins (
			identity_key TEXT PRIMARY KEY,
			label        TEXT NOT NULL,
			created_at   ` + ts + ` NOT NULL
		)`,

		// Every administrative action, especially the one-shot secret export.
		`CREATE TABLE IF NOT EXISTS audit_log (
			id      ` + s.autoIncrementPK() + `,
			at      ` + ts + ` NOT NULL,
			actor   TEXT NOT NULL,
			action  TEXT NOT NULL,
			detail  TEXT NOT NULL DEFAULT ''
		)`,

		`CREATE INDEX IF NOT EXISTS audit_log_at ON audit_log (at DESC)`,
	}

	for i, q := range stmts {
		if err := s.exec(ctx, q); err != nil {
			return fmt.Errorf("store: migrate statement %d: %w", i, err)
		}
	}

	// Columns added after a deployment already exists.
	//
	// CREATE TABLE IF NOT EXISTS does nothing to a table that is already there,
	// so a new column never reaches a live database and every query naming it
	// fails at runtime. These run separately and are skipped when the column is
	// already present.
	// A column renamed after a deployment already exists. The rename happens
	// before the additions below, so the "does it exist" probe for animal_name
	// sees the renamed column rather than adding a second, empty one and
	// quietly losing every name already recorded.
	if err := s.renameColumnIfPresent(ctx, "tags", "crab_name", "animal_name"); err != nil {
		return err
	}

	for _, c := range []struct{ table, column, def string }{
		{"tags", "animal_name", "TEXT NOT NULL DEFAULT ''"},
		{"tags", "named_by", "TEXT NOT NULL DEFAULT ''"},
		{"tags", "species", "TEXT NOT NULL DEFAULT ''"},
		{"tag_events", "settlement_json", "TEXT NOT NULL DEFAULT ''"},
		{"batches", "species", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.addColumnIfMissing(ctx, c.table, c.column, c.def); err != nil {
			return err
		}
	}
	return nil
}

// renameColumnIfPresent renames a column that still has its old name.
//
// Both backends support ALTER TABLE ... RENAME COLUMN, and the presence check is
// a SELECT for the same reason addColumnIfMissing uses one: the two backends
// disagree about where their catalogue lives, and a failed SELECT answers the
// question on both.
//
// Renaming rather than adding matters. A tag armed under the old schema has a
// name in the old column, and that name is in a signed record on chain; adding
// an empty new column beside it would leave the database disagreeing with the
// chain about every animal already named.
func (s *Store) renameColumnIfPresent(ctx context.Context, table, from, to string) error {
	//nolint:gosec // table and column names are compile-time constants
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("SELECT %s FROM %s LIMIT 1", from, table)); err != nil {
		return nil // already renamed, or the table is new
	}
	//nolint:gosec // as above
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("SELECT %s FROM %s LIMIT 1", to, table)); err == nil {
		return nil // both present: somebody has already dealt with this
	}
	//nolint:gosec // as above
	q := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", table, from, to)
	if err := s.exec(ctx, q); err != nil {
		return fmt.Errorf("store: rename %s.%s to %s: %w", table, from, to, err)
	}
	return nil
}

// addColumnIfMissing adds a column to an existing table, once.
//
// The presence check is a SELECT rather than a catalogue query, because the two
// backends disagree about where that catalogue lives and a failed SELECT tells
// us exactly what we need to know on both.
func (s *Store) addColumnIfMissing(ctx context.Context, table, column, definition string) error {
	//nolint:gosec // table and column are compile-time constants, never input
	probe := fmt.Sprintf("SELECT %s FROM %s LIMIT 1", column, table)
	if _, err := s.db.ExecContext(ctx, probe); err == nil {
		return nil
	}
	//nolint:gosec // as above
	alter := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition)
	if err := s.exec(ctx, alter); err != nil {
		return fmt.Errorf("store: add %s.%s: %w", table, column, err)
	}
	return nil
}

// SQLite has no partial-index support gap worth working around and Postgres
// accepts the same syntax, so the WHERE clauses above are portable as written.

func (s *Store) timeValue(t *time.Time) any {
	if t == nil {
		return nil
	}
	if s.postgres {
		return t.UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *Store) now() any { n := time.Now().UTC(); return s.timeValue(&n) }

// scanTime reads a column written by timeValue back.
func scanTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		return t.UTC(), true
	case string:
		if t == "" {
			return time.Time{}, false
		}
		parsed, err := time.Parse(time.RFC3339Nano, t)
		if err != nil {
			return time.Time{}, false
		}
		return parsed.UTC(), true
	case []byte:
		return scanTime(string(t))
	}
	return time.Time{}, false
}
