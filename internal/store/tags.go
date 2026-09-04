package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Batch is one print run of tags.
type Batch struct {
	ID           string
	CreatedAt    time.Time
	CreatedBy    string
	TagCount     int
	FirstOrdinal uint64
	ManifestHash string
	AnchorTxID   string
	// Species is the profile this run was printed for, and is a hint rather
	// than a fact: what each animal actually was is decided at activation.
	Species   string
	SecretsAt *time.Time
}

// Tag is one physical tag.
//
// Ordinal is the only thing needed to regenerate the tag's spending key from
// the master seed, which is what makes a lost print sheet survivable and an
// unrecaptured reward reclaimable.
type Tag struct {
	TagID     string
	BatchID   string
	Ordinal   uint64
	PubKeyHex string
	Status    Status
	// Species is the profile the tag was armed for, empty until it is armed.
	Species string
	// AnimalName is empty until the animal is named, by the tagger or by
	// whoever first finds it. NamedBy is that person's identity key.
	AnimalName    string
	NamedBy       string
	Generation    uint32
	LiveTxID      string
	LiveVout      uint32
	LiveSatoshis  uint64
	CreatedAt     time.Time
	ActivatedAt   *time.Time
	LastEventAt   *time.Time
	CooldownUntil *time.Time
	SweepAfter    *time.Time
	RetiredAt     *time.Time
}

// Event is one thing that happened to a tag, and the transaction that recorded it.
type Event struct {
	ID         int64
	TagID      string
	Generation uint32
	Kind       string
	TxID       string
	Vout       uint32
	Satoshis   uint64
	// PayloadJSON is the observation: the half a person signed. SettlementJSON
	// is the half the programme added -- what it paid and against which output.
	// They are stored apart because only the first one has a signature over it.
	PayloadJSON    string
	SettlementJSON string
	AttestPubKey   string
	Status         EventStatus
	Err            string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Escrow is a re-release bonus owed to an earlier reporter.
//
// It exists in the database and, independently, as the escrowFor commitment
// inside the locking script on chain. The audit compares them; that is the
// point of keeping both.
type Escrow struct {
	TagID       string
	Generation  uint32
	Beneficiary string
	Satoshis    uint64
	CreatedAt   time.Time
	PaidTxID    string
	PaidAt      *time.Time

	// PaidPrefix, PaidSuffix and PaidVout locate the payment once it is made.
	// The beneficiary is not present at that moment, so their wallet needs
	// these to derive the key the output was locked to.
	PaidPrefix string
	PaidSuffix string
	PaidVout   uint32
}

// CreateBatch records a print run.
func (s *Store) CreateBatch(ctx context.Context, b Batch) error {
	return s.exec(ctx,
		`INSERT INTO batches (id, created_at, created_by, tag_count, first_ordinal, manifest_hash, anchor_txid, species)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, s.timeValue(&b.CreatedAt), b.CreatedBy, b.TagCount, b.FirstOrdinal, b.ManifestHash, b.AnchorTxID, b.Species)
}

// SetBatchAnchor records the txid that anchored a batch manifest.
func (s *Store) SetBatchAnchor(ctx context.Context, batchID, txid string) error {
	return s.exec(ctx, `UPDATE batches SET anchor_txid = ? WHERE id = ?`, txid, batchID)
}

// MarkSecretsExported stamps the one-shot secret export. The stamp is what
// makes it one-shot, and the audit log records who did it.
func (s *Store) MarkSecretsExported(ctx context.Context, batchID string) error {
	return s.exec(ctx, `UPDATE batches SET secrets_exported_at = ? WHERE id = ?`, s.now(), batchID)
}

// GetBatch reads one batch.
func (s *Store) GetBatch(ctx context.Context, id string) (*Batch, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(
		`SELECT id, created_at, created_by, tag_count, first_ordinal, manifest_hash, anchor_txid, species, secrets_exported_at
		 FROM batches WHERE id = ?`), id)

	var b Batch
	var created, secrets any
	if err := row.Scan(&b.ID, &created, &b.CreatedBy, &b.TagCount, &b.FirstOrdinal, &b.ManifestHash, &b.AnchorTxID, &b.Species, &secrets); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: batch %s", ErrNotFound, id)
		}
		return nil, fmt.Errorf("store: get batch: %w", err)
	}
	b.CreatedAt, _ = scanTime(created)
	if t, ok := scanTime(secrets); ok {
		b.SecretsAt = &t
	}
	return &b, nil
}

// ListBatches returns print runs, newest first.
func (s *Store) ListBatches(ctx context.Context, limit int) ([]Batch, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT id, created_at, created_by, tag_count, first_ordinal, manifest_hash, anchor_txid, species, secrets_exported_at
		 FROM batches ORDER BY created_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, fmt.Errorf("store: list batches: %w", err)
	}
	defer rows.Close()

	var out []Batch
	for rows.Next() {
		var b Batch
		var created, secrets any
		if err := rows.Scan(&b.ID, &created, &b.CreatedBy, &b.TagCount, &b.FirstOrdinal, &b.ManifestHash, &b.AnchorTxID, &b.Species, &secrets); err != nil {
			return nil, fmt.Errorf("store: scan batch: %w", err)
		}
		b.CreatedAt, _ = scanTime(created)
		if t, ok := scanTime(secrets); ok {
			b.SecretsAt = &t
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// NextOrdinal returns the ordinal a new batch should start at.
//
// Ordinals are never reused, because reuse would mean two physical tags sharing
// a spending key -- and the second one printed could spend the first one's
// reward.
func (s *Store) NextOrdinal(ctx context.Context) (uint64, error) {
	var maxOrd sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(ordinal) FROM tags`).Scan(&maxOrd); err != nil {
		return 0, fmt.Errorf("store: next ordinal: %w", err)
	}
	if !maxOrd.Valid {
		return 0, nil
	}
	return uint64(maxOrd.Int64) + 1, nil
}

// InsertTags writes a whole batch of tags in one transaction, so a partial
// print run never reaches the database.
func (s *Store) InsertTags(ctx context.Context, tags []Tag) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, s.rebind(
		`INSERT INTO tags (tag_id, batch_id, ordinal, pubkey_hex, status, generation, created_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?)`))
	if err != nil {
		return fmt.Errorf("store: prepare insert tag: %w", err)
	}
	defer stmt.Close()

	for _, t := range tags {
		if _, err := stmt.ExecContext(ctx, t.TagID, t.BatchID, t.Ordinal, t.PubKeyHex, StatusMinted, s.timeValue(&t.CreatedAt)); err != nil {
			return fmt.Errorf("store: insert tag %s: %w", t.TagID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit tags: %w", err)
	}
	return nil
}

const tagColumns = `tag_id, batch_id, ordinal, pubkey_hex, status, species, animal_name, named_by, generation,
	live_txid, live_vout, live_satoshis, created_at, activated_at, last_event_at,
	cooldown_until, sweep_after, retired_at`

func (s *Store) scanTag(sc interface{ Scan(...any) error }) (*Tag, error) {
	var t Tag
	var created, activated, lastEvent, cooldown, sweep, retired any
	if err := sc.Scan(&t.TagID, &t.BatchID, &t.Ordinal, &t.PubKeyHex, &t.Status, &t.Species, &t.AnimalName, &t.NamedBy, &t.Generation,
		&t.LiveTxID, &t.LiveVout, &t.LiveSatoshis, &created, &activated, &lastEvent,
		&cooldown, &sweep, &retired); err != nil {
		return nil, err
	}
	t.CreatedAt, _ = scanTime(created)
	if v, ok := scanTime(activated); ok {
		t.ActivatedAt = &v
	}
	if v, ok := scanTime(lastEvent); ok {
		t.LastEventAt = &v
	}
	if v, ok := scanTime(cooldown); ok {
		t.CooldownUntil = &v
	}
	if v, ok := scanTime(sweep); ok {
		t.SweepAfter = &v
	}
	if v, ok := scanTime(retired); ok {
		t.RetiredAt = &v
	}
	return &t, nil
}

// GetTag reads one tag.
func (s *Store) GetTag(ctx context.Context, tagID string) (*Tag, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(`SELECT `+tagColumns+` FROM tags WHERE tag_id = ?`), tagID)
	t, err := s.scanTag(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: tag %s", ErrNotFound, tagID)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get tag: %w", err)
	}
	return t, nil
}

// ListTags returns tags, optionally filtered by status, newest first.
func (s *Store) ListTags(ctx context.Context, status Status, limit, offset int) ([]Tag, error) {
	q := `SELECT ` + tagColumns + ` FROM tags`
	args := []any{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC, tag_id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, fmt.Errorf("store: list tags: %w", err)
	}
	defer rows.Close()

	var out []Tag
	for rows.Next() {
		t, err := s.scanTag(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// TagsByBatch returns a batch's tags in ordinal order, which is print order.
func (s *Store) TagsByBatch(ctx context.Context, batchID string) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT `+tagColumns+` FROM tags WHERE batch_id = ? ORDER BY ordinal`), batchID)
	if err != nil {
		return nil, fmt.Errorf("store: tags by batch: %w", err)
	}
	defer rows.Close()

	var out []Tag
	for rows.Next() {
		t, err := s.scanTag(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// Transition moves a tag from one status to another, atomically.
//
// This is the application's single-writer primitive. Two redemption requests
// racing on the same tag both try to move it out of "active"; exactly one
// UPDATE reports a row, and the loser gets ErrTagNotAvailable rather than a
// second attempt to spend an output that is already being spent.
//
// The chain would catch a genuine double spend anyway, but only after both
// requests had built transactions and one had been rejected -- which is a much
// worse experience than a clear refusal, and leaves the loser's crabber
// staring at an error on a boat.
func (s *Store) Transition(ctx context.Context, tagID string, from, to Status) error {
	res, err := s.db.ExecContext(ctx, s.rebind(
		`UPDATE tags SET status = ? WHERE tag_id = ? AND status = ?`), to, tagID, from)
	if err != nil {
		return fmt.Errorf("store: transition tag %s: %w", tagID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: transition rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s is not in state %q", ErrTagNotAvailable, tagID, from)
	}
	return nil
}

// ActivateTag records a successful activation and the output it created.
//
// The species is written here and never again. It is what a later report is
// checked against, and the check is only meaningful if the value cannot move.
func (s *Store) ActivateTag(ctx context.Context, tagID, speciesCode, txid string, vout uint32, satoshis uint64, sweepAfter time.Time) error {
	now := time.Now().UTC()
	return s.exec(ctx,
		`UPDATE tags SET status = ?, species = ?, generation = 0, live_txid = ?, live_vout = ?, live_satoshis = ?,
		        activated_at = ?, last_event_at = ?, sweep_after = ?, cooldown_until = NULL, retired_at = NULL
		 WHERE tag_id = ?`,
		StatusActive, speciesCode, txid, vout, satoshis, s.timeValue(&now), s.timeValue(&now), s.timeValue(&sweepAfter), tagID)
}

// AdvanceTag records a recapture that left the tag alive: a new generation, a
// new escrow output, and a cooldown before it can be reported again.
func (s *Store) AdvanceTag(ctx context.Context, tagID string, generation uint32, txid string, vout uint32, satoshis uint64, cooldownUntil time.Time) error {
	now := time.Now().UTC()
	return s.exec(ctx,
		`UPDATE tags SET status = ?, generation = ?, live_txid = ?, live_vout = ?, live_satoshis = ?,
		        last_event_at = ?, cooldown_until = ?
		 WHERE tag_id = ?`,
		StatusCooldown, generation, txid, vout, satoshis, s.timeValue(&now), s.timeValue(&cooldownUntil), tagID)
}

// RearmTag brings a cooled-down tag back into service on a fresh output.
func (s *Store) RearmTag(ctx context.Context, tagID string, generation uint32, txid string, vout uint32, satoshis uint64) error {
	now := time.Now().UTC()
	return s.exec(ctx,
		`UPDATE tags SET status = ?, generation = ?, live_txid = ?, live_vout = ?, live_satoshis = ?,
		        last_event_at = ?, cooldown_until = NULL
		 WHERE tag_id = ?`,
		StatusActive, generation, txid, vout, satoshis, s.timeValue(&now), tagID)
}

// RetireTag ends a tag's life: the crab was kept, or the reward was swept.
func (s *Store) RetireTag(ctx context.Context, tagID string) error {
	now := time.Now().UTC()
	return s.exec(ctx,
		`UPDATE tags SET status = ?, live_txid = '', live_vout = 0, live_satoshis = 0,
		        retired_at = ?, last_event_at = ?, cooldown_until = NULL
		 WHERE tag_id = ?`,
		StatusRetired, s.timeValue(&now), s.timeValue(&now), tagID)
}

// SweepableTags lists active tags whose sweep date has passed.
//
// Animals die and some shed their tags, so a real fraction of a batch never
// comes back. Without this the rewards on those tags stay locked forever.
func (s *Store) SweepableTags(ctx context.Context, asOf time.Time, limit int) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT `+tagColumns+` FROM tags
		 WHERE status IN (?, ?) AND sweep_after IS NOT NULL AND sweep_after <= ?
		 ORDER BY sweep_after LIMIT ?`),
		StatusActive, StatusCooldown, s.timeValue(&asOf), limit)
	if err != nil {
		return nil, fmt.Errorf("store: sweepable tags: %w", err)
	}
	defer rows.Close()

	var out []Tag
	for rows.Next() {
		t, err := s.scanTag(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// CooledDownTags lists tags whose cooldown has expired and which are waiting to
// be re-armed.
func (s *Store) CooledDownTags(ctx context.Context, asOf time.Time, limit int) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT `+tagColumns+` FROM tags
		 WHERE status = ? AND cooldown_until IS NOT NULL AND cooldown_until <= ?
		 ORDER BY cooldown_until LIMIT ?`),
		StatusCooldown, s.timeValue(&asOf), limit)
	if err != nil {
		return nil, fmt.Errorf("store: cooled down tags: %w", err)
	}
	defer rows.Close()

	var out []Tag
	for rows.Next() {
		t, err := s.scanTag(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan tag: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// NameAnimal records what an animal is called, once.
//
// The WHERE clause is the whole point: a name is set by whoever gets there
// first -- the tagger at tagging, or the first finder if the tagger left it
// blank -- and never changed after. It is written into a signed, permanent
// record, so a later rename would leave the database disagreeing with the
// chain. A second attempt reports no rows and the caller is told the animal is
// already named.
func (s *Store) NameAnimal(ctx context.Context, tagID, name, byIdentityKey string) error {
	res, err := s.db.ExecContext(ctx, s.rebind(
		`UPDATE tags SET animal_name = ?, named_by = ? WHERE tag_id = ? AND animal_name = ''`),
		name, byIdentityKey, tagID)
	if err != nil {
		return fmt.Errorf("store: name animal %s: %w", tagID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: name animal rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s already has a name", ErrTagNotAvailable, tagID)
	}
	return nil
}
