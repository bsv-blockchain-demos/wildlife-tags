package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "tags.db"), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedBatch(t *testing.T, s *Store, n int) []Tag {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	if err := s.CreateBatch(ctx, Batch{
		ID: "B1", CreatedAt: now, CreatedBy: "02bio", TagCount: n,
		FirstOrdinal: 0, ManifestHash: "deadbeef",
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	tags := make([]Tag, n)
	for i := range tags {
		tags[i] = Tag{
			TagID:     fmt.Sprintf("TAG%04d", i),
			BatchID:   "B1",
			Ordinal:   uint64(i),
			PubKeyHex: fmt.Sprintf("02%062d", i),
			CreatedAt: now,
		}
	}
	if err := s.InsertTags(ctx, tags); err != nil {
		t.Fatalf("insert tags: %v", err)
	}
	return tags
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		s, err := Open(t.Context(), filepath.Join(dir, "tags.db"), "")
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

func TestATagStartsMinted(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 3)

	got, err := s.GetTag(t.Context(), "TAG0000")
	if err != nil {
		t.Fatalf("get tag: %v", err)
	}
	if got.Status != StatusMinted {
		t.Errorf("status is %q, want %q", got.Status, StatusMinted)
	}
	if got.LiveSatoshis != 0 {
		t.Errorf("a minted tag holds %d satoshis", got.LiveSatoshis)
	}
}

func TestMissingThingsReportErrNotFound(t *testing.T) {
	s := open(t)
	if _, err := s.GetTag(t.Context(), "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get tag: got %v, want ErrNotFound", err)
	}
	if _, err := s.GetBatch(t.Context(), "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get batch: got %v, want ErrNotFound", err)
	}
	if _, err := s.LookupSession(t.Context(), "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Errorf("lookup session: got %v, want ErrNotFound", err)
	}
}

// TestOnlyOneRedemptionCanClaimATag is the guard that matters most in this
// package. Two crabbers pulling the same trap, or one crabber double-tapping a
// button on a bad connection, must not produce two attempts to spend one
// output.
func TestOnlyOneRedemptionCanClaimATag(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 1)
	ctx := t.Context()

	if err := s.ActivateTag(ctx, "TAG0000", "CALSAP", "aa11", 0, 20000, time.Now().Add(540*24*time.Hour)); err != nil {
		t.Fatalf("activate: %v", err)
	}

	const racers = 16
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
		losers  int
	)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.Transition(ctx, "TAG0000", StatusActive, StatusRedeeming)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners++
			case errors.Is(err, ErrTagNotAvailable):
				losers++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if winners != 1 {
		t.Fatalf("%d goroutines claimed the same tag; exactly one must win", winners)
	}
	if losers != racers-1 {
		t.Fatalf("%d clean refusals, want %d", losers, racers-1)
	}
}

func TestAFailedRedemptionReturnsTheTagToService(t *testing.T) {
	// Everything before SignAction is retractable. A tag stuck in "redeeming"
	// because a fee estimate failed is a reward nobody can ever claim.
	s := open(t)
	seedBatch(t, s, 1)
	ctx := t.Context()

	if err := s.ActivateTag(ctx, "TAG0000", "CALSAP", "aa11", 0, 20000, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := s.Transition(ctx, "TAG0000", StatusActive, StatusRedeeming); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.Transition(ctx, "TAG0000", StatusRedeeming, StatusActive); err != nil {
		t.Fatalf("release: %v", err)
	}
	got, _ := s.GetTag(ctx, "TAG0000")
	if got.Status != StatusActive {
		t.Fatalf("status is %q, want %q", got.Status, StatusActive)
	}
}

func TestATagCannotBeRedeemedBeforeItIsActivated(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 1)
	if err := s.Transition(t.Context(), "TAG0000", StatusActive, StatusRedeeming); !errors.Is(err, ErrTagNotAvailable) {
		t.Fatalf("got %v, want ErrTagNotAvailable", err)
	}
}

func TestTheFullTagLifecycle(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 1)
	ctx := t.Context()
	sweep := time.Now().UTC().Add(540 * 24 * time.Hour)

	if err := s.ActivateTag(ctx, "TAG0000", "CALSAP", "aa11", 0, 20000, sweep); err != nil {
		t.Fatalf("activate: %v", err)
	}
	tag, _ := s.GetTag(ctx, "TAG0000")
	if tag.Status != StatusActive || tag.Generation != 0 || tag.LiveSatoshis != 20000 {
		t.Fatalf("after activation: %+v", tag)
	}
	if tag.ActivatedAt == nil || tag.SweepAfter == nil {
		t.Fatal("activation did not stamp its timestamps")
	}

	// Recapture one: released, so the tag advances and cools down.
	cooldown := time.Now().UTC().Add(7 * 24 * time.Hour)
	if err := s.AdvanceTag(ctx, "TAG0000", 1, "bb22", 1, 20000, cooldown); err != nil {
		t.Fatalf("advance: %v", err)
	}
	tag, _ = s.GetTag(ctx, "TAG0000")
	if tag.Status != StatusCooldown || tag.Generation != 1 || tag.LiveTxID != "bb22" {
		t.Fatalf("after recapture: %+v", tag)
	}

	if err := s.RearmTag(ctx, "TAG0000", 1, "bb22", 1, 20000); err != nil {
		t.Fatalf("rearm: %v", err)
	}
	tag, _ = s.GetTag(ctx, "TAG0000")
	if tag.Status != StatusActive || tag.CooldownUntil != nil {
		t.Fatalf("after rearm: %+v", tag)
	}

	if err := s.RetireTag(ctx, "TAG0000"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	tag, _ = s.GetTag(ctx, "TAG0000")
	if tag.Status != StatusRetired || tag.LiveSatoshis != 0 || tag.RetiredAt == nil {
		t.Fatalf("after retirement: %+v", tag)
	}
}

// TestATagCannotReportTheSameGenerationTwice is the database half of the
// double-report guard. The chain enforces it too -- the output spends once --
// but catching it here means the second reporter gets a clear answer instead of
// a broadcast rejection.
func TestATagCannotReportTheSameGenerationTwice(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 1)
	ctx := t.Context()

	e := Event{TagID: "TAG0000", Generation: 1, Kind: "REC", PayloadJSON: "{}"}
	if _, err := s.BeginEvent(ctx, e); err != nil {
		t.Fatalf("first report: %v", err)
	}
	if _, err := s.BeginEvent(ctx, e); err == nil {
		t.Fatal("the same generation was reported twice")
	}
}

func TestAnAttemptedEventCanBeRetractedButABroadcastOneCannot(t *testing.T) {
	// Signing is broadcasting. Before that the record is provisional; after it
	// the record has to survive even if this process never learns the outcome,
	// or the audit has no way to find a spend the application forgot.
	s := open(t)
	seedBatch(t, s, 1)
	ctx := t.Context()

	id, err := s.BeginEvent(ctx, Event{TagID: "TAG0000", Generation: 0, Kind: "ACT", PayloadJSON: "{}"})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.RetractEvent(ctx, id); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if events, _ := s.EventsForTag(ctx, "TAG0000"); len(events) != 0 {
		t.Fatalf("retracted event survived: %+v", events)
	}

	id, err = s.BeginEvent(ctx, Event{TagID: "TAG0000", Generation: 0, Kind: "ACT", PayloadJSON: "{}"})
	if err != nil {
		t.Fatalf("begin again: %v", err)
	}
	if err := s.BroadcastEvent(ctx, id, "cc33", 0, 20000); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if err := s.RetractEvent(ctx, id); err != nil {
		t.Fatalf("retract: %v", err)
	}
	events, _ := s.EventsForTag(ctx, "TAG0000")
	if len(events) != 1 || events[0].TxID != "cc33" {
		t.Fatalf("a broadcast event was retracted: %+v", events)
	}
}

func TestEventStatusPromotion(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 1)
	ctx := t.Context()

	id, err := s.BeginEvent(ctx, Event{TagID: "TAG0000", Generation: 0, Kind: "ACT", PayloadJSON: `{"t":"act"}`})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if inflight, _ := s.InFlightEvents(ctx, 10); len(inflight) != 1 {
		t.Fatalf("expected one in-flight event, got %d", len(inflight))
	}
	if err := s.BroadcastEvent(ctx, id, "cc33", 0, 20000); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if err := s.MarkEventMined(ctx, "cc33"); err != nil {
		t.Fatalf("mine: %v", err)
	}
	events, _ := s.EventsForTag(ctx, "TAG0000")
	if len(events) != 1 || events[0].Status != EventMined {
		t.Fatalf("status is %+v", events)
	}
	if inflight, _ := s.InFlightEvents(ctx, 10); len(inflight) != 0 {
		t.Fatalf("a mined event is still in flight: %+v", inflight)
	}
}

func TestEscrowIsOwedUntilTheNextRecaptureReleasesIt(t *testing.T) {
	// This is the split-bonus mechanism: reporter one's bonus sits unpaid until
	// the tag is seen again, which is what corroborates the release.
	s := open(t)
	seedBatch(t, s, 1)
	ctx := t.Context()

	if err := s.CreateEscrow(ctx, Escrow{
		TagID: "TAG0000", Generation: 1, Beneficiary: "02reporter1",
		Satoshis: 15000, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create escrow: %v", err)
	}

	got, err := s.PendingEscrow(ctx, "TAG0000", 1)
	if err != nil {
		t.Fatalf("pending escrow: %v", err)
	}
	if got.Beneficiary != "02reporter1" || got.Satoshis != 15000 {
		t.Fatalf("escrow: %+v", got)
	}
	if unpaid, _ := s.UnpaidEscrows(ctx, 10); len(unpaid) != 1 {
		t.Fatalf("expected one unpaid escrow, got %d", len(unpaid))
	}

	if err := s.PayEscrow(ctx, "TAG0000", 1, "dd44", "pfx", "sfx", 1); err != nil {
		t.Fatalf("pay escrow: %v", err)
	}
	if _, err := s.PendingEscrow(ctx, "TAG0000", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a paid escrow is still pending: %v", err)
	}
	if unpaid, _ := s.UnpaidEscrows(ctx, 10); len(unpaid) != 0 {
		t.Fatalf("a paid escrow is still listed as unpaid: %+v", unpaid)
	}
}

func TestOrdinalsAreNeverReused(t *testing.T) {
	// Two tags sharing an ordinal share a spending key, and the second one
	// printed could spend the first one's reward.
	s := open(t)
	seedBatch(t, s, 5)
	ctx := t.Context()

	next, err := s.NextOrdinal(ctx)
	if err != nil {
		t.Fatalf("next ordinal: %v", err)
	}
	if next != 5 {
		t.Fatalf("next ordinal is %d, want 5", next)
	}

	dup := []Tag{{TagID: "OTHER", BatchID: "B1", Ordinal: 0, PubKeyHex: "02ff", CreatedAt: time.Now().UTC()}}
	if err := s.InsertTags(ctx, dup); err == nil {
		t.Fatal("a duplicate ordinal was accepted")
	}
}

func TestAPartialBatchNeverReachesTheDatabase(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 2)
	ctx := t.Context()

	// The third tag collides on ordinal, so the whole insert must roll back.
	batch := []Tag{
		{TagID: "NEW1", BatchID: "B1", Ordinal: 10, PubKeyHex: "02aa", CreatedAt: time.Now().UTC()},
		{TagID: "NEW2", BatchID: "B1", Ordinal: 11, PubKeyHex: "02bb", CreatedAt: time.Now().UTC()},
		{TagID: "NEW3", BatchID: "B1", Ordinal: 0, PubKeyHex: "02cc", CreatedAt: time.Now().UTC()},
	}
	if err := s.InsertTags(ctx, batch); err == nil {
		t.Fatal("a colliding batch was accepted")
	}
	for _, id := range []string{"NEW1", "NEW2"} {
		if _, err := s.GetTag(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s survived a rolled-back batch", id)
		}
	}
}

func TestSweepFindsOnlyTagsPastTheirDate(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 2)
	ctx := t.Context()
	now := time.Now().UTC()

	if err := s.ActivateTag(ctx, "TAG0000", "CALSAP", "aa11", 0, 20000, now.Add(-time.Hour)); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := s.ActivateTag(ctx, "TAG0001", "CALSAP", "bb22", 0, 20000, now.Add(24*time.Hour)); err != nil {
		t.Fatalf("activate: %v", err)
	}

	sweepable, err := s.SweepableTags(ctx, now, 10)
	if err != nil {
		t.Fatalf("sweepable: %v", err)
	}
	if len(sweepable) != 1 || sweepable[0].TagID != "TAG0000" {
		t.Fatalf("sweepable tags: %+v", sweepable)
	}
}

func TestCooldownGatesRearming(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 2)
	ctx := t.Context()
	now := time.Now().UTC()

	if err := s.AdvanceTag(ctx, "TAG0000", 1, "aa11", 1, 20000, now.Add(-time.Minute)); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := s.AdvanceTag(ctx, "TAG0001", 1, "bb22", 1, 20000, now.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("advance: %v", err)
	}

	ready, err := s.CooledDownTags(ctx, now, 10)
	if err != nil {
		t.Fatalf("cooled down: %v", err)
	}
	if len(ready) != 1 || ready[0].TagID != "TAG0000" {
		t.Fatalf("cooled-down tags: %+v", ready)
	}
}

func TestAnExpiredSessionIsRefusedAndDeleted(t *testing.T) {
	s := open(t)
	ctx := t.Context()
	now := time.Now().UTC()

	if err := s.CreateSession(ctx, Session{
		Token: "expired", IdentityKey: "02admin", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.LookupSession(ctx, "expired"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired session was accepted: %v", err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(1) FROM sessions WHERE token = 'expired'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Error("an expired session was left in the table after being refused")
	}
}

func TestRevokingAnAdminKillsTheirSessions(t *testing.T) {
	// A revocation that leaves a live session is not a revocation.
	s := open(t)
	ctx := t.Context()
	now := time.Now().UTC()

	if err := s.AddAdmin(ctx, "02admin", "Graham"); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	if err := s.CreateSession(ctx, Session{
		Token: "live", IdentityKey: "02admin", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := s.LookupSession(ctx, "live"); err != nil {
		t.Fatalf("session should be live: %v", err)
	}

	if err := s.RemoveAdmin(ctx, "02admin"); err != nil {
		t.Fatalf("remove admin: %v", err)
	}
	if _, err := s.LookupSession(ctx, "live"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a revoked admin still has a session: %v", err)
	}
	if ok, _ := s.IsAdmin(ctx, "02admin"); ok {
		t.Error("a revoked admin is still an admin")
	}
}

func TestAddAdminIsIdempotentAndUpdatesTheLabel(t *testing.T) {
	s := open(t)
	ctx := t.Context()

	if err := s.AddAdmin(ctx, "02admin", "first"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.AddAdmin(ctx, "02admin", "second"); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	admins, err := s.ListAdmins(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(admins) != 1 || admins[0].Label != "second" {
		t.Fatalf("admins: %+v", admins)
	}
}

func TestTheSecretExportIsStamped(t *testing.T) {
	// The stamp is what makes the export one-shot, and the audit log records
	// who did it.
	s := open(t)
	ctx := t.Context()
	seedBatch(t, s, 1)

	b, _ := s.GetBatch(ctx, "B1")
	if b.SecretsAt != nil {
		t.Fatal("a fresh batch is already stamped as exported")
	}
	if err := s.MarkSecretsExported(ctx, "B1"); err != nil {
		t.Fatalf("mark exported: %v", err)
	}
	if err := s.Audit(ctx, "02admin", "batch.secrets.export", "B1"); err != nil {
		t.Fatalf("audit: %v", err)
	}
	b, _ = s.GetBatch(ctx, "B1")
	if b.SecretsAt == nil {
		t.Fatal("the export was not stamped")
	}
	trail, _ := s.AuditTrail(ctx, 10)
	if len(trail) != 1 || trail[0].Action != "batch.secrets.export" {
		t.Fatalf("audit trail: %+v", trail)
	}
}

func TestStatsSummariseTheProgram(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 4)
	ctx := t.Context()
	now := time.Now().UTC()

	if err := s.ActivateTag(ctx, "TAG0000", "CALSAP", "aa11", 0, 20000, now.Add(time.Hour)); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := s.ActivateTag(ctx, "TAG0001", "CALSAP", "bb22", 0, 20000, now.Add(time.Hour)); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := s.RetireTag(ctx, "TAG0002"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	id, _ := s.BeginEvent(ctx, Event{TagID: "TAG0000", Generation: 1, Kind: "REC", PayloadJSON: "{}"})
	if err := s.BroadcastEvent(ctx, id, "cc33", 0, 5000); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if err := s.CreateEscrow(ctx, Escrow{TagID: "TAG0000", Generation: 1, Beneficiary: "02r1", Satoshis: 15000, CreatedAt: now}); err != nil {
		t.Fatalf("escrow: %v", err)
	}

	st, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.TagsActive != 2 {
		t.Errorf("active tags: got %d, want 2", st.TagsActive)
	}
	if st.TagsMinted != 1 {
		t.Errorf("minted tags: got %d, want 1", st.TagsMinted)
	}
	if st.TagsRetired != 1 {
		t.Errorf("retired tags: got %d, want 1", st.TagsRetired)
	}
	if st.SatoshisLocked != 40000 {
		t.Errorf("locked satoshis: got %d, want 40000", st.SatoshisLocked)
	}
	if st.Recaptures != 1 {
		t.Errorf("recaptures: got %d, want 1", st.Recaptures)
	}
	if st.EscrowOwed != 15000 {
		t.Errorf("escrow owed: got %d, want 15000", st.EscrowOwed)
	}

	paid, err := s.SatoshisPaid(ctx)
	if err != nil {
		t.Fatalf("satoshis paid: %v", err)
	}
	if paid != 5000 {
		t.Errorf("satoshis paid: got %d, want 5000", paid)
	}
}

func TestTagsAreListedInPrintOrderWithinABatch(t *testing.T) {
	s := open(t)
	seedBatch(t, s, 5)

	tags, err := s.TagsByBatch(t.Context(), "B1")
	if err != nil {
		t.Fatalf("tags by batch: %v", err)
	}
	if len(tags) != 5 {
		t.Fatalf("got %d tags, want 5", len(tags))
	}
	for i, tag := range tags {
		if tag.Ordinal != uint64(i) {
			t.Fatalf("tag %d has ordinal %d; print order is not preserved", i, tag.Ordinal)
		}
	}
}

// TestAColumnAddedLaterReachesAnExistingDatabase covers a failure that only
// appears on a deployment that already has data.
//
// The schema is created with CREATE TABLE IF NOT EXISTS, which does nothing at
// all to a table that is already there. A column added to the statement is
// therefore present on a fresh database and absent on every existing one, and
// the first query naming it fails at runtime with "no such column" -- long
// after the change looked fine in testing.
func TestAColumnAddedLaterReachesAnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tags.db")

	// A database from before the column existed.
	first, err := Open(t.Context(), path, "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := first.DB().Exec(`ALTER TABLE tags DROP COLUMN crab_name`); err != nil {
		t.Skipf("this backend cannot drop a column to simulate the old schema: %v", err)
	}
	if _, err := first.DB().Exec(`ALTER TABLE tags DROP COLUMN named_by`); err != nil {
		t.Fatalf("drop named_by: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening must repair it rather than fail on the next query.
	second, err := Open(t.Context(), path, "")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	seedBatch(t, second, 1)
	tag, err := second.GetTag(t.Context(), "TAG0000")
	if err != nil {
		t.Fatalf("a query naming the new column failed after reopening: %v", err)
	}
	if tag.AnimalName != "" {
		t.Errorf("a repaired column has value %q, want empty", tag.AnimalName)
	}
	if err := second.NameAnimal(t.Context(), "TAG0000", "Old Bertha", "02someone"); err != nil {
		t.Fatalf("naming failed on a repaired database: %v", err)
	}
}
