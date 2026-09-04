package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is a logged-in administrator.
type Session struct {
	Token       string
	IdentityKey string
	Label       string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Admin is an identity key permitted to administer the program.
//
// Administrators are identified by BRC-100 identity key rather than by
// username, so every activation carries an attestation naming the biologist who
// made it -- a field a movement study genuinely wants and which a shared
// password cannot provide.
type Admin struct {
	IdentityKey string
	Label       string
	CreatedAt   time.Time
}

// AuditEntry is one administrative action.
type AuditEntry struct {
	ID     int64
	At     time.Time
	Actor  string
	Action string
	Detail string
}

// AddAdmin authorises an identity key.
func (s *Store) AddAdmin(ctx context.Context, identityKey, label string) error {
	now := time.Now().UTC()
	q := `INSERT INTO admins (identity_key, label, created_at) VALUES (?, ?, ?)`
	if s.postgres {
		q += ` ON CONFLICT (identity_key) DO UPDATE SET label = EXCLUDED.label`
	} else {
		q += ` ON CONFLICT (identity_key) DO UPDATE SET label = excluded.label`
	}
	return s.exec(ctx, q, identityKey, label, s.timeValue(&now))
}

// RemoveAdmin revokes an identity key. Existing sessions for it are dropped in
// the same breath, because a revocation that leaves a live session is not one.
func (s *Store) RemoveAdmin(ctx context.Context, identityKey string) error {
	if err := s.exec(ctx, `DELETE FROM admins WHERE identity_key = ?`, identityKey); err != nil {
		return err
	}
	return s.exec(ctx, `DELETE FROM sessions WHERE identity_key = ?`, identityKey)
}

// IsAdmin reports whether an identity key is authorised.
func (s *Store) IsAdmin(ctx context.Context, identityKey string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT COUNT(1) FROM admins WHERE identity_key = ?`), identityKey).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: is admin: %w", err)
	}
	return n > 0, nil
}

// ListAdmins returns the authorised identity keys.
func (s *Store) ListAdmins(ctx context.Context) ([]Admin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT identity_key, label, created_at FROM admins ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: list admins: %w", err)
	}
	defer rows.Close()

	var out []Admin
	for rows.Next() {
		var a Admin
		var created any
		if err := rows.Scan(&a.IdentityKey, &a.Label, &created); err != nil {
			return nil, fmt.Errorf("store: scan admin: %w", err)
		}
		a.CreatedAt, _ = scanTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountAdmins reports how many identity keys are authorised. A zero here is
// what tells the server it is unconfigured and needs a bootstrap.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM admins`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count admins: %w", err)
	}
	return n, nil
}

// CreateSession stores a login.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	return s.exec(ctx,
		`INSERT INTO sessions (token, identity_key, label, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		sess.Token, sess.IdentityKey, sess.Label, s.timeValue(&sess.CreatedAt), s.timeValue(&sess.ExpiresAt))
}

// LookupSession resolves a session token, refusing expired ones.
func (s *Store) LookupSession(ctx context.Context, token string) (*Session, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(
		`SELECT token, identity_key, label, created_at, expires_at FROM sessions WHERE token = ?`), token)

	var sess Session
	var created, expires any
	if err := row.Scan(&sess.Token, &sess.IdentityKey, &sess.Label, &created, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: lookup session: %w", err)
	}
	sess.CreatedAt, _ = scanTime(created)
	sess.ExpiresAt, _ = scanTime(expires)

	if time.Now().UTC().After(sess.ExpiresAt) {
		// Delete on read rather than waiting for the sweeper: an expired token
		// presented once will be presented again.
		_ = s.exec(ctx, `DELETE FROM sessions WHERE token = ?`, token)
		return nil, ErrNotFound
	}
	return &sess, nil
}

// DeleteSession logs an administrator out.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	return s.exec(ctx, `DELETE FROM sessions WHERE token = ?`, token)
}

// PurgeExpiredSessions clears out stale logins.
func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	return s.exec(ctx, `DELETE FROM sessions WHERE expires_at < ?`, s.now())
}

// Audit records an administrative action.
//
// The one this exists for is the secret export: a batch's tag secrets can be
// read out exactly once, and who did it and when is not something to leave to
// a log file that rotates.
func (s *Store) Audit(ctx context.Context, actor, action, detail string) error {
	return s.exec(ctx, `INSERT INTO audit_log (at, actor, action, detail) VALUES (?, ?, ?, ?)`,
		s.now(), actor, action, detail)
}

// AuditTrail returns recent administrative actions, newest first.
func (s *Store) AuditTrail(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT id, at, actor, action, detail FROM audit_log ORDER BY at DESC, id DESC LIMIT ?`), limit)
	if err != nil {
		return nil, fmt.Errorf("store: audit trail: %w", err)
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at any
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Detail); err != nil {
			return nil, fmt.Errorf("store: scan audit entry: %w", err)
		}
		e.At, _ = scanTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Stats is what the public dashboard leads with.
type Stats struct {
	TagsMinted     int    `json:"tags_minted"`
	TagsActive     int    `json:"tags_active"`
	TagsCooldown   int    `json:"tags_cooldown"`
	TagsRetired    int    `json:"tags_retired"`
	Recaptures     int    `json:"recaptures"`
	SatoshisPaid   uint64 `json:"satoshis_paid"`
	SatoshisLocked uint64 `json:"satoshis_locked"`
	EscrowOwed     uint64 `json:"escrow_owed"`
}

// Stats computes the dashboard summary.
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	var st Stats

	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(1), COALESCE(SUM(live_satoshis), 0) FROM tags GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("store: stats by status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status Status
		var count int
		var locked uint64
		if err := rows.Scan(&status, &count, &locked); err != nil {
			return nil, fmt.Errorf("store: scan stats: %w", err)
		}
		switch status {
		case StatusMinted:
			st.TagsMinted = count
		case StatusActive, StatusRedeeming:
			st.TagsActive += count
			st.SatoshisLocked += locked
		case StatusCooldown:
			st.TagsCooldown = count
			st.SatoshisLocked += locked
		case StatusRetired:
			st.TagsRetired = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx, s.rebind(
		`SELECT COUNT(1) FROM tag_events WHERE kind = 'REC' AND status <> ?`), EventFailed).Scan(&st.Recaptures); err != nil {
		return nil, fmt.Errorf("store: stats recaptures: %w", err)
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(satoshis), 0) FROM escrows WHERE paid_txid = ''`).Scan(&st.EscrowOwed); err != nil {
		return nil, fmt.Errorf("store: stats escrow: %w", err)
	}

	return &st, nil
}

// SetPaidTotal is recorded by the redemption path rather than derived, because
// what a crabber was actually paid is a fact about a transaction, not a sum the
// database should be inferring.
func (s *Store) SatoshisPaid(ctx context.Context) (uint64, error) {
	var total uint64
	err := s.db.QueryRowContext(ctx, s.rebind(
		`SELECT COALESCE(SUM(satoshis), 0) FROM tag_events WHERE kind = 'REC' AND status <> ?`),
		EventFailed).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("store: satoshis paid: %w", err)
	}
	return total, nil
}
