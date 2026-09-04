package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BeginEvent writes a tag event before anything is signed, and returns its id.
//
// The ordering is load-bearing. Signing is broadcasting: once SignAction
// returns, the transaction may be on the network whatever happens in this
// process afterwards. A crash between signing and recording would leave a spend
// nobody knows about and a tag the application still believes is live. So the
// record goes in first, in the "attempting" state, and is either promoted or
// retracted.
//
// The unique index on (tag_id, generation, kind) means a duplicate attempt
// fails here rather than on chain.
func (s *Store) BeginEvent(ctx context.Context, e Event) (int64, error) {
	now := time.Now().UTC()
	q := `INSERT INTO tag_events (tag_id, generation, kind, payload_json, settlement_json, attest_pubkey, status, created_at, updated_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if s.postgres {
		var id int64
		err := s.db.QueryRowContext(ctx, s.rebind(q+` RETURNING id`),
			e.TagID, e.Generation, e.Kind, e.PayloadJSON, e.SettlementJSON, e.AttestPubKey, EventAttempting,
			s.timeValue(&now), s.timeValue(&now)).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("store: begin event: %w", err)
		}
		return id, nil
	}

	res, err := s.db.ExecContext(ctx, s.rebind(q),
		e.TagID, e.Generation, e.Kind, e.PayloadJSON, e.SettlementJSON, e.AttestPubKey, EventAttempting,
		s.timeValue(&now), s.timeValue(&now))
	if err != nil {
		return 0, fmt.Errorf("store: begin event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: begin event id: %w", err)
	}
	return id, nil
}

// BroadcastEvent promotes an attempted event once it has a txid.
func (s *Store) BroadcastEvent(ctx context.Context, id int64, txid string, vout uint32, satoshis uint64) error {
	return s.exec(ctx,
		`UPDATE tag_events SET status = ?, txid = ?, vout = ?, satoshis = ?, err = '', updated_at = ?
		 WHERE id = ?`,
		EventBroadcast, txid, vout, satoshis, s.now(), id)
}

// FailEvent marks an event failed, recording why.
func (s *Store) FailEvent(ctx context.Context, id int64, reason string) error {
	return s.exec(ctx,
		`UPDATE tag_events SET status = ?, err = ?, updated_at = ? WHERE id = ?`,
		EventFailed, reason, s.now(), id)
}

// RetractEvent deletes an event that never reached the network.
//
// Only safe before SignAction. Past that point the transaction may exist and
// the record has to stay, even if this process never learned its fate --
// otherwise the audit has no way to find a spend the application forgot about.
func (s *Store) RetractEvent(ctx context.Context, id int64) error {
	return s.exec(ctx, `DELETE FROM tag_events WHERE id = ? AND status = ?`, id, EventAttempting)
}

// MarkEventMined records that a transaction has a verified merkle proof.
func (s *Store) MarkEventMined(ctx context.Context, txid string) error {
	return s.exec(ctx,
		`UPDATE tag_events SET status = ?, updated_at = ? WHERE txid = ? AND status <> ?`,
		EventMined, s.now(), txid, EventMined)
}

const eventColumns = `id, tag_id, generation, kind, txid, vout, satoshis,
	payload_json, settlement_json, attest_pubkey, status, err, created_at, updated_at`

func (s *Store) scanEvent(sc interface{ Scan(...any) error }) (*Event, error) {
	var e Event
	var created, updated any
	if err := sc.Scan(&e.ID, &e.TagID, &e.Generation, &e.Kind, &e.TxID, &e.Vout, &e.Satoshis,
		&e.PayloadJSON, &e.SettlementJSON, &e.AttestPubKey, &e.Status, &e.Err, &created, &updated); err != nil {
		return nil, err
	}
	e.CreatedAt, _ = scanTime(created)
	e.UpdatedAt, _ = scanTime(updated)
	return &e, nil
}

// EventsForTag returns a tag's whole history, oldest first, which is the order
// the provenance receipt reads it in.
func (s *Store) EventsForTag(ctx context.Context, tagID string) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT `+eventColumns+` FROM tag_events WHERE tag_id = ? ORDER BY generation, id`), tagID)
	if err != nil {
		return nil, fmt.Errorf("store: events for tag: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		e, err := s.scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// RecentEvents feeds the public dashboard.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT `+eventColumns+` FROM tag_events WHERE status <> ? ORDER BY id DESC LIMIT ?`),
		EventAttempting, limit)
	if err != nil {
		return nil, fmt.Errorf("store: recent events: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		e, err := s.scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// InFlightEvents lists transactions that have not reached a terminal state.
// This is the query the partial index exists for.
func (s *Store) InFlightEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT `+eventColumns+` FROM tag_events
		 WHERE status NOT IN ('mined', 'failed') ORDER BY updated_at LIMIT ?`), limit)
	if err != nil {
		return nil, fmt.Errorf("store: in-flight events: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		e, err := s.scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// AllEvents streams the whole dataset for export and audit.
func (s *Store) AllEvents(ctx context.Context, fn func(Event) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventColumns+` FROM tag_events ORDER BY tag_id, generation, id`)
	if err != nil {
		return fmt.Errorf("store: all events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		e, err := s.scanEvent(rows)
		if err != nil {
			return fmt.Errorf("store: scan event: %w", err)
		}
		if err := fn(*e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// CreateEscrow records a re-release bonus owed to the reporter who put the crab
// back. It mirrors the escrowFor commitment written into the locking script.
func (s *Store) CreateEscrow(ctx context.Context, e Escrow) error {
	return s.exec(ctx,
		`INSERT INTO escrows (tag_id, generation, beneficiary_pubkey, satoshis, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		e.TagID, e.Generation, e.Beneficiary, e.Satoshis, s.timeValue(&e.CreatedAt))
}

// PendingEscrow returns the unpaid bonus attached to a tag's current
// generation, if any. The next recapture is what releases it.
func (s *Store) PendingEscrow(ctx context.Context, tagID string, generation uint32) (*Escrow, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(
		`SELECT tag_id, generation, beneficiary_pubkey, satoshis, created_at, paid_txid, paid_at, paid_prefix, paid_suffix, paid_vout
		 FROM escrows WHERE tag_id = ? AND generation = ? AND paid_txid = ''`), tagID, generation)

	var e Escrow
	var created, paid any
	if err := row.Scan(&e.TagID, &e.Generation, &e.Beneficiary, &e.Satoshis, &created, &e.PaidTxID, &paid, &e.PaidPrefix, &e.PaidSuffix, &e.PaidVout); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: escrow for %s generation %d", ErrNotFound, tagID, generation)
		}
		return nil, fmt.Errorf("store: pending escrow: %w", err)
	}
	e.CreatedAt, _ = scanTime(created)
	if t, ok := scanTime(paid); ok {
		e.PaidAt = &t
	}
	return &e, nil
}

// PayEscrow marks a bonus paid, recording where the payment landed.
//
// The derivation is stored because the beneficiary was not present when the
// payment was made. Their wallet can derive the key, but only if it is told
// which prefix and suffix to derive with -- so this row is the only route from
// "you are owed a bonus" to "here is the output holding it".
func (s *Store) PayEscrow(ctx context.Context, tagID string, generation uint32, txid, prefix, suffix string, vout uint32) error {
	return s.exec(ctx,
		`UPDATE escrows SET paid_txid = ?, paid_at = ?, paid_prefix = ?, paid_suffix = ?, paid_vout = ?
		 WHERE tag_id = ? AND generation = ? AND paid_txid = ''`,
		txid, s.now(), prefix, suffix, vout, tagID, generation)
}

// UnpaidEscrows lists outstanding bonuses, for the dashboard and the audit.
func (s *Store) UnpaidEscrows(ctx context.Context, limit int) ([]Escrow, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT tag_id, generation, beneficiary_pubkey, satoshis, created_at, paid_txid, paid_at, paid_prefix, paid_suffix, paid_vout
		 FROM escrows WHERE paid_txid = '' ORDER BY created_at LIMIT ?`), limit)
	if err != nil {
		return nil, fmt.Errorf("store: unpaid escrows: %w", err)
	}
	defer rows.Close()

	var out []Escrow
	for rows.Next() {
		var e Escrow
		var created, paid any
		if err := rows.Scan(&e.TagID, &e.Generation, &e.Beneficiary, &e.Satoshis, &created, &e.PaidTxID, &paid, &e.PaidPrefix, &e.PaidSuffix, &e.PaidVout); err != nil {
			return nil, fmt.Errorf("store: scan escrow: %w", err)
		}
		e.CreatedAt, _ = scanTime(created)
		if t, ok := scanTime(paid); ok {
			e.PaidAt = &t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FailEventByTxID marks an event failed when the network reports a rejection.
//
// Scoped to non-mined rows so a late rejection notice for a transaction that
// has since been mined cannot un-mine it. Arcade delivers at least once and not
// necessarily in order, so an out-of-order event has to be harmless.
func (s *Store) FailEventByTxID(ctx context.Context, txid, reason string) error {
	return s.exec(ctx,
		`UPDATE tag_events SET status = ?, err = ?, updated_at = ?
		 WHERE txid = ? AND status NOT IN ('mined', 'failed')`,
		EventFailed, reason, s.now(), txid)
}
