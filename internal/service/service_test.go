package service

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

type harness struct {
	svc    *Service
	ledger *fakeLedger
	store  *store.Store
	t      *testing.T
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	cfg := chain.DefaultConfig()
	cfg.PublicURL = "https://tags.dnr.sc.gov"
	cfg.DataDir = t.TempDir()

	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tags.db"), "")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ledger := newFakeLedger(cfg)
	svc := New(ledger, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &harness{svc: svc, ledger: ledger, store: st, t: t}
}

// mintOne creates a batch of one and returns its tag id.
func (h *harness) mintOne() string {
	h.t.Helper()
	_, tags, err := h.svc.MintBatch(h.t.Context(), 1, "", "02biologist")
	if err != nil {
		h.t.Fatalf("mint: %v", err)
	}
	return tags[0].TagID
}

// arm walks the two-step activation the way the console does.
func (h *harness) arm(tagID string) {
	h.t.Helper()
	h.armNamed(tagID, "")
}

// armNamed is the same, with the tagger naming the animal.
func (h *harness) armNamed(tagID, name string) {
	h.t.Helper()
	// The browser learns its identity key before asking for the record, because
	// the biologist's key is part of what they are being asked to sign.
	walletKey, _ := h.ledger.WalletKey()
	pub := hex.EncodeToString(walletKey.PubKey().Compressed())

	in := ActivationInput{
		TagID:        tagkey.ID(tagID),
		Lat:          32.7765,
		Lon:          -79.9311,
		AccuracyM:    4.8,
		Meas:         map[string]int{"cw": 142},
		Attr:         map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"},
		Name:         name,
		AttestPubHex: pub,
	}
	preview, err := h.svc.PrepareActivation(h.t.Context(), in)
	if err != nil {
		h.t.Fatalf("prepare activation: %v", err)
	}

	sig, _, err := SelfAttest(preview.Observation, walletKey, tagID)
	if err != nil {
		h.t.Fatalf("attest: %v", err)
	}
	in.Observation, in.AttestSig = preview.Observation, sig

	if _, err := h.svc.Activate(h.t.Context(), in); err != nil {
		h.t.Fatalf("activate: %v", err)
	}
}

// report walks a whole recapture: quote, attest, prepare, complete.
func (h *harness) report(tagID string, payee *ec.PrivateKey, disp species.Disposition, lat, lon float64) *RedeemReceipt {
	h.t.Helper()
	return h.reportNamed(tagID, payee, disp, lat, lon, "")
}

// reportNamed is the same, with the finder naming the animal.
func (h *harness) reportNamed(tagID string, payee *ec.PrivateKey, disp species.Disposition, lat, lon float64, name string) *RedeemReceipt {
	h.t.Helper()
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())

	meas := map[string]int{"cw": 149}
	attr := map[string]string{"sex": "M", "gear": "TROTLINE", species.DispositionKey: string(disp)}

	details := RecaptureDetails{
		Lat: lat, Lon: lon, AccuracyM: 6.2,
		Meas: meas, Attr: attr, PayeePubHex: payeeHex, Name: name,
	}
	quote, err := h.svc.QuoteRecapture(h.t.Context(), tagID, details)
	if err != nil {
		h.t.Fatalf("quote: %v", err)
	}
	observation, err := hex.DecodeString(quote.ObservationHex)
	if err != nil {
		h.t.Fatalf("observation: %v", err)
	}
	sig, err := attest(h.t, payee, tagID, observation)
	if err != nil {
		h.t.Fatalf("sign: %v", err)
	}

	draft, err := h.svc.PrepareRedeem(h.t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: lat, Lon: lon, AccuracyM: 6.2,
		Meas: meas, Attr: attr, PayeePubHex: payeeHex, Name: name,
		Observation: observation, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	})
	if err != nil {
		h.t.Fatalf("prepare redeem: %v", err)
	}

	receipt, err := h.svc.CompleteRedeem(h.t.Context(), draft.Reference, []byte{0xaa})
	if err != nil {
		h.t.Fatalf("complete redeem: %v", err)
	}
	return receipt
}

// sampleMeas and sampleAttr are one hard-shell legal male blue crab, which is
// the animal every case here that is not about the species uses.
func sampleMeas() map[string]int { return map[string]int{"cw": 149} }

func sampleAttr(disp species.Disposition) map[string]string {
	return map[string]string{"sex": "M", "gear": "TRAP", species.DispositionKey: string(disp)}
}

func key(t *testing.T) *ec.PrivateKey {
	t.Helper()
	k, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return k
}

// TestTheEscrowSurvivesTwoRecaptures is the test this whole package's interface
// exists for.
//
// It walks the design's central claim end to end: reporter one puts a crab
// back and is paid the base but NOT the bonus; the bonus is escrowed against
// their identity key; the crab is caught again weeks later; reporter two is
// paid their own base; and reporter one's bonus is released in that same
// transaction, because the tag turning up again is the only corroboration a
// release can have.
func TestTheEscrowSurvivesTwoRecaptures(t *testing.T) {
	h := newHarness(t)
	cfg := h.svc.Config()
	tagID := h.mintOne()
	h.arm(tagID)

	reporter1, reporter2 := key(t), key(t)
	r1Hex := hex.EncodeToString(reporter1.PubKey().Compressed())

	// --- first recapture: released ---
	receipt1 := h.report(tagID, reporter1, species.Released, 32.79, -79.90)
	if receipt1.PayoutSats != cfg.BaseSatoshis {
		t.Fatalf("reporter one was paid %d, want the base reward %d", receipt1.PayoutSats, cfg.BaseSatoshis)
	}
	if receipt1.Retired {
		t.Fatal("a released crab retired its tag")
	}

	tag, err := h.store.GetTag(t.Context(), tagID)
	if err != nil {
		t.Fatalf("get tag: %v", err)
	}
	if tag.Status != store.StatusCooldown {
		t.Fatalf("after a release the tag is %s, want cooldown", tag.Status)
	}
	if tag.Generation != 1 {
		t.Fatalf("tag is at generation %d, want 1", tag.Generation)
	}

	escrow, err := h.store.PendingEscrow(t.Context(), tagID, 1)
	if err != nil {
		t.Fatalf("the re-release bonus was not escrowed: %v", err)
	}
	if escrow.Beneficiary != r1Hex {
		t.Fatalf("the bonus is owed to %s, not to reporter one", escrow.Beneficiary)
	}
	if escrow.Satoshis != cfg.BonusSatoshis {
		t.Fatalf("escrowed %d, want %d", escrow.Satoshis, cfg.BonusSatoshis)
	}

	// --- the cooldown, then back in service ---
	if _, err := h.svc.PrepareRedeem(t.Context(), RedeemInput{TagID: tagkey.ID(tagID)}); !errors.Is(err, ErrCooldown) {
		t.Fatalf("a cooled-down tag was claimable: %v", err)
	}
	h.svc.now = func() time.Time { return time.Now().UTC().Add(cfg.CooldownFor + time.Hour) }
	if err := h.svc.Rearm(t.Context(), tagID, "02biologist"); err != nil {
		t.Fatalf("rearm: %v", err)
	}

	// --- second recapture: this is what corroborates the release ---
	receipt2 := h.report(tagID, reporter2, species.Harvested, 32.83, -79.84)
	if receipt2.PayoutSats != cfg.BaseSatoshis {
		t.Fatalf("reporter two was paid %d, want %d", receipt2.PayoutSats, cfg.BaseSatoshis)
	}
	if !receipt2.Retired {
		t.Fatal("keeping the crab did not retire the tag")
	}

	if _, err := h.store.PendingEscrow(t.Context(), tagID, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reporter one's bonus was not released by the second recapture: %v", err)
	}
	unpaid, err := h.store.UnpaidEscrows(t.Context(), 10)
	if err != nil {
		t.Fatalf("unpaid escrows: %v", err)
	}
	if len(unpaid) != 0 {
		t.Fatalf("%d bonuses are still owed after the tag was reported again: %+v", len(unpaid), unpaid)
	}

	final, _ := h.store.GetTag(t.Context(), tagID)
	if final.Status != store.StatusRetired {
		t.Fatalf("the tag is %s, want retired", final.Status)
	}
}

// TestKeepingTheCrabForfeitsTheBonus is the other half of the incentive: a
// crabber who keeps the animal is paid, and the bonus they could have earned
// simply never comes into being.
func TestKeepingTheAnimalForfeitsTheBonus(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	receipt := h.report(tagID, key(t), species.Harvested, 32.79, -79.90)
	if !receipt.Retired {
		t.Fatal("the tag was not retired")
	}
	if unpaid, _ := h.store.UnpaidEscrows(t.Context(), 10); len(unpaid) != 0 {
		t.Fatalf("keeping the animal still created a bonus obligation: %+v", unpaid)
	}
}

// TestAFailureBeforeBroadcastReturnsTheTagToService is the ordering discipline
// this package exists to enforce. Nothing reached the network, so the crabber
// must simply be able to try again.
func TestAFailureBeforeBroadcastReturnsTheTagToService(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	payee := key(t)
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())
	details := RecaptureDetails{
		Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
	}
	quote, err := h.svc.QuoteRecapture(t.Context(), tagID, details)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	observation, _ := hex.DecodeString(quote.ObservationHex)
	sig, _ := attest(t, payee, tagID, observation)

	draft, err := h.svc.PrepareRedeem(t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
		Observation: observation, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// A pre-broadcast failure: nothing reached the network, so everything must
	// unwind and the crabber must be able to simply try again.
	h.ledger.failCompleteWith = errors.Join(chain.ErrNotBroadcast, errors.New("fake wallet refused"))

	if _, err := h.svc.CompleteRedeem(t.Context(), draft.Reference, []byte{0xaa}); err == nil {
		t.Fatal("a failing redemption reported success")
	}

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusActive {
		t.Fatalf("after a pre-broadcast failure the tag is %s; its reward is now unclaimable", tag.Status)
	}
	// The provisional record must be gone, so the crabber can report again
	// without colliding with the unique index on (tag, generation, kind).
	events, _ := h.store.EventsForTag(t.Context(), tagID)
	for _, ev := range events {
		if ev.Kind == "REC" {
			t.Fatalf("a retracted redemption left a record behind: %+v", ev)
		}
	}
}

// TestAnAbandonedRedemptionIsReleased covers the crabber who loses signal.
// Without this the tag stays claimed and its reward is unclaimable forever.
func TestAnAbandonedRedemptionIsReleased(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	payee := key(t)
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())
	details := RecaptureDetails{
		Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
	}
	quote, _ := h.svc.QuoteRecapture(t.Context(), tagID, details)
	observation, _ := hex.DecodeString(quote.ObservationHex)
	sig, _ := attest(t, payee, tagID, observation)

	if _, err := h.svc.PrepareRedeem(t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
		Observation: observation, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	}); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusRedeeming {
		t.Fatalf("the tag is %s, want redeeming", tag.Status)
	}

	// The crabber walks away.
	h.svc.now = func() time.Time { return time.Now().UTC().Add(time.Hour) }
	if n := h.svc.ExpireDrafts(t.Context()); n != 1 {
		t.Fatalf("released %d abandoned redemptions, want 1", n)
	}
	tag, _ = h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusActive {
		t.Fatalf("the tag is %s after its draft expired; its reward is unclaimable", tag.Status)
	}
}

// TestASignedRecordMustMatchTheReportedCatch stops a wallet signing one thing
// while the database stores another. Without it the on-chain attestation would
// be evidence for a claim nobody made.
func TestASignedRecordMustMatchTheReportedCatch(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	payee := key(t)
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())
	details := RecaptureDetails{
		Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
	}
	quote, _ := h.svc.QuoteRecapture(t.Context(), tagID, details)
	observation, _ := hex.DecodeString(quote.ObservationHex)
	sig, _ := attest(t, payee, tagID, observation)

	// Submit the signed record but claim a different position.
	_, err := h.svc.PrepareRedeem(t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: 40.0, Lon: -74.0, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
		Observation: observation, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	})
	if !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("got %v, want ErrPayloadMismatch", err)
	}

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusActive {
		t.Fatalf("a rejected report left the tag %s", tag.Status)
	}
}

// TestProvenanceTellsTheAnimalsStory covers the feature SCDNR's own writeup says
// reporters actually come back for.
func TestProvenanceTellsTheAnimalsStory(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	// Move the clock forward so days-at-large is a real number.
	h.svc.now = func() time.Time { return time.Now().UTC().Add(97 * 24 * time.Hour) }
	h.report(tagID, key(t), species.Released, 32.83, -79.84)

	prov, err := h.svc.Provenance(t.Context(), tagID)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if prov.TaggedAt == nil {
		t.Fatal("the provenance has no tagging event")
	}
	if prov.PrimaryAt != 142 {
		t.Errorf("tagged size is %d, want 142", prov.PrimaryAt)
	}
	if prov.PrimaryLabel == "" || prov.PrimaryUnit == "" {
		t.Error("the receipt cannot say what the size it is showing means")
	}
	if len(prov.Facts) == 0 {
		t.Fatal("the receipt has nothing to show about the animal")
	}
	for _, f := range prov.Facts {
		if f.Label == "" || f.Label == f.Key || f.Value == "" {
			t.Errorf("the receipt would show a raw code to a finder: %+v", f)
		}
	}
	if prov.Common == "" || prov.Scientific == "" {
		t.Error("the receipt does not name the animal")
	}
	if len(prov.Recaptures) != 1 {
		t.Fatalf("got %d recaptures, want 1", len(prov.Recaptures))
	}
	if prov.DaysAtLarge < 90 {
		t.Errorf("days at large is %d, want about 97", prov.DaysAtLarge)
	}
	// Charleston Harbor to a little north-east: several kilometres.
	if prov.DistanceM < 1000 {
		t.Errorf("distance is %d m, which is too small for the two fixes used", prov.DistanceM)
	}
}

// TestMintingIsFreeAndDeterministic: a batch spends nothing, and its tags are
// exactly the ones the master seed produces for those ordinals.
func TestMintingIsFreeAndDeterministic(t *testing.T) {
	h := newHarness(t)
	batch, tags, err := h.svc.MintBatch(t.Context(), 5, "", "02biologist")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if batch.TagCount != 5 || len(tags) != 5 {
		t.Fatalf("minted %d tags", len(tags))
	}
	if batch.ManifestHash == "" {
		t.Error("the batch has no manifest hash, so its tag set cannot be shown to predate any activation")
	}

	seed, _ := h.ledger.TagSeed()
	for i, tag := range tags {
		want := seed.SecretFor(uint64(i)).ID()
		if tag.TagID != string(want) {
			t.Fatalf("tag %d is %s, want %s", i, tag.TagID, want)
		}
		if tag.Status != store.StatusMinted {
			t.Errorf("a fresh tag is %s, want minted", tag.Status)
		}
	}
}

func TestABatchIsBoundedInSize(t *testing.T) {
	h := newHarness(t)
	for _, n := range []int{0, -1, 5001} {
		if _, _, err := h.svc.MintBatch(t.Context(), n, "", "02biologist"); err == nil {
			t.Errorf("a batch of %d was accepted", n)
		}
	}
}

// TestBatchesCreatedInTheSameSecondDoNotCollide covers the mistake an admin
// makes by double-clicking. Batch ids were second-resolution once, and the
// second mint failed on a primary key collision with an error that explained
// nothing.
func TestBatchesCreatedInTheSameSecondDoNotCollide(t *testing.T) {
	h := newHarness(t)
	frozen := time.Now().UTC()
	h.svc.now = func() time.Time { return frozen }

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		batch, _, err := h.svc.MintBatch(t.Context(), 2, "", "02biologist")
		if err != nil {
			t.Fatalf("mint %d in the same second: %v", i, err)
		}
		if seen[batch.ID] {
			t.Fatalf("batch id %s was reused", batch.ID)
		}
		seen[batch.ID] = true
	}
}

// TestOrdinalsKeepAdvancingAcrossBatches: two tags sharing an ordinal share a
// spending key, and the second one printed could spend the first one's reward.
func TestOrdinalsKeepAdvancingAcrossBatches(t *testing.T) {
	h := newHarness(t)
	seen := map[uint64]bool{}
	for i := 0; i < 4; i++ {
		_, tags, err := h.svc.MintBatch(t.Context(), 3, "", "02biologist")
		if err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
		for _, tag := range tags {
			if seen[tag.Ordinal] {
				t.Fatalf("ordinal %d was reused", tag.Ordinal)
			}
			seen[tag.Ordinal] = true
		}
	}
	if len(seen) != 12 {
		t.Fatalf("got %d distinct ordinals across four batches of three", len(seen))
	}
}

// attest signs the way a BRC-100 wallet does: with the type-42 child derived
// for (attestation protocol, tag id, anyone), never with the identity key.
func attest(t *testing.T, identity *ec.PrivateKey, tagID string, payload []byte) (*ec.Signature, error) {
	t.Helper()
	signer, err := record.AttestationPrivateKey(identity, tagID)
	if err != nil {
		return nil, err
	}
	return signer.Sign(record.Digest(payload))
}

// TestARestartReleasesAnOrphanedRedemption covers a failure that reached a live
// deployment: a crabber's redemption failed, the tag stayed claimed, and the
// server was restarted before the draft expired. Drafts live only in memory, so
// nothing was left that could ever release it and the reward became
// permanently unclaimable.
func TestARestartReleasesAnOrphanedRedemption(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	// A redemption starts and the process dies before it completes.
	if err := h.store.Transition(t.Context(), tagID, store.StatusActive, store.StatusRedeeming); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// A fresh Service, as after a restart: no drafts in memory at all.
	restarted := New(h.ledger, h.store, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// The in-flight expiry loop cannot help; it only knows its own drafts.
	if n := restarted.ExpireDrafts(t.Context()); n != 0 {
		t.Fatalf("the expiry loop claimed to release %d drafts it never had", n)
	}
	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusRedeeming {
		t.Fatalf("setup: tag is %s", tag.Status)
	}

	n, err := restarted.ReleaseOrphanedRedemptions(t.Context())
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if n != 1 {
		t.Fatalf("released %d tags, want 1", n)
	}
	tag, _ = h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusActive {
		t.Fatalf("tag is %s after a restart; its reward is unclaimable", tag.Status)
	}
}

// TestReleasingOrphansLeavesHealthyTagsAlone: startup recovery must not disturb
// tags that are simply live, retired or cooling down.
func TestReleasingOrphansLeavesHealthyTagsAlone(t *testing.T) {
	h := newHarness(t)
	live := h.mintOne()
	h.arm(live)
	retired := h.mintOne()
	h.arm(retired)
	h.report(retired, key(t), species.Harvested, 32.79, -79.90)

	n, err := h.svc.ReleaseOrphanedRedemptions(t.Context())
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if n != 0 {
		t.Fatalf("released %d tags when none were orphaned", n)
	}
	if tag, _ := h.store.GetTag(t.Context(), live); tag.Status != store.StatusActive {
		t.Errorf("a live tag became %s", tag.Status)
	}
	if tag, _ := h.store.GetTag(t.Context(), retired); tag.Status != store.StatusRetired {
		t.Errorf("a retired tag became %s", tag.Status)
	}
}

// TestTheFinderNamesAnUnnamedAnimal is the feature working as intended: a
// biologist who leaves the name blank hands it to whoever finds the animal.
func TestTheFinderNamesAnUnnamedAnimal(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID) // the harness leaves the animal unnamed

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.AnimalName != "" {
		t.Fatalf("a freshly armed tag is already named %q", tag.AnimalName)
	}

	finder := key(t)
	h.reportNamed(tagID, finder, species.Released, 32.79, -79.90, "Old Bertha")

	tag, _ = h.store.GetTag(t.Context(), tagID)
	if tag.AnimalName != "Old Bertha" {
		t.Fatalf("animal is named %q, want %q", tag.AnimalName, "Old Bertha")
	}
	if tag.NamedBy != hex.EncodeToString(finder.PubKey().Compressed()) {
		t.Errorf("the name is credited to %q, not to the finder", tag.NamedBy)
	}

	prov, err := h.svc.Provenance(t.Context(), tagID)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if prov.Name != "Old Bertha" {
		t.Errorf("the animal's story calls it %q", prov.Name)
	}
	if prov.NamedAt == nil {
		t.Error("the story does not say when it was named")
	}
}

// TestAnAnimalIsNamedOnceAndNeverRenamed is the rule that makes a name mean
// something. It is written into a signed, permanent record, so a later rename
// would leave the database disagreeing with the chain.
func TestAnAnimalIsNamedOnceAndNeverRenamed(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	h.reportNamed(tagID, key(t), species.Released, 32.79, -79.90, "Old Bertha")

	// Straight at the store, which is where the rule actually lives.
	err := h.store.NameAnimal(t.Context(), tagID, "Something Else", "02someone")
	if !errors.Is(err, store.ErrTagNotAvailable) {
		t.Fatalf("a named animal was renamed: %v", err)
	}
	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.AnimalName != "Old Bertha" {
		t.Fatalf("the animal is now called %q", tag.AnimalName)
	}
}

// TestASecondFinderIsNotOfferedTheNaming: once an animal has a name, later reports
// carry none, so the record says who named it and when rather than logging a
// name on every sighting.
func TestASecondFinderIsNotOfferedTheNaming(t *testing.T) {
	h := newHarness(t)
	cfg := h.svc.Config()
	tagID := h.mintOne()
	h.arm(tagID)

	h.reportNamed(tagID, key(t), species.Released, 32.79, -79.90, "Old Bertha")
	h.svc.now = func() time.Time { return time.Now().UTC().Add(cfg.CooldownFor + time.Hour) }
	if err := h.svc.Rearm(t.Context(), tagID, "02biologist"); err != nil {
		t.Fatalf("rearm: %v", err)
	}

	quote, err := h.svc.QuoteRecapture(t.Context(), tagID, RecaptureDetails{
		Lat: 32.83, Lon: -79.84, AccuracyM: 6,
		Meas: map[string]int{"cw": 150}, Attr: sampleAttr(species.Released),
		PayeePubHex: hex.EncodeToString(key(t).PubKey().Compressed()),
		Name:        "Renamed By Me",
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.CanName {
		t.Error("a second finder was offered the naming")
	}
	if quote.AnimalName != "Old Bertha" {
		t.Errorf("the quote calls the animal %q", quote.AnimalName)
	}
	// And the attempted name must not have reached the record.
	var rec record.Observation
	observation, _ := hex.DecodeString(quote.ObservationHex)
	if err := json.Unmarshal(observation, &rec); err != nil {
		t.Fatalf("observation: %v", err)
	}
	if rec.Name != "" {
		t.Errorf("a later report carries the name %q; only the naming report should", rec.Name)
	}
}

// TestATaggerCanNameAtTagging covers the other route.
func TestATaggerCanNameAtTagging(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.armNamed(tagID, "Clawdia")

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.AnimalName != "Clawdia" {
		t.Fatalf("animal is named %q", tag.AnimalName)
	}

	quote, err := h.svc.QuoteRecapture(t.Context(), tagID, RecaptureDetails{
		Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released),
		PayeePubHex: hex.EncodeToString(key(t).PubKey().Compressed()),
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if quote.CanName {
		t.Error("a finder was offered the naming of an already-named animal")
	}
}

// TestTheJourneyAddsUpAcrossSightings: the straight line from the start is not
// the same as the distance actually covered once an animal has been found twice.
func TestTheJourneyAddsUpAcrossSightings(t *testing.T) {
	h := newHarness(t)
	cfg := h.svc.Config()
	tagID := h.mintOne()
	h.arm(tagID) // tagged at 32.7765, -79.9311

	// Out, then back most of the way: the total path is far longer than the
	// straight line from where it started.
	h.report(tagID, key(t), species.Released, 32.90, -79.90)
	h.svc.now = func() time.Time { return time.Now().UTC().Add(cfg.CooldownFor + time.Hour) }
	if err := h.svc.Rearm(t.Context(), tagID, "02biologist"); err != nil {
		t.Fatalf("rearm: %v", err)
	}
	h.report(tagID, key(t), species.Released, 32.78, -79.92)

	prov, err := h.svc.Provenance(t.Context(), tagID)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if len(prov.Recaptures) != 2 {
		t.Fatalf("got %d sightings, want 2", len(prov.Recaptures))
	}
	if prov.TotalPathM <= prov.DistanceM {
		t.Fatalf("total path %d m is not longer than the straight line %d m, but the animal went out and came back",
			prov.TotalPathM, prov.DistanceM)
	}
}

// TestAbandoningARedemptionReleasesTheWalletInputs covers the half of recovery
// that was missing and cost a live deployment its next redemption.
//
// CreateAction reserves real coins the moment it returns a signable
// transaction. Putting the tag back into service without releasing them leaves
// the tag claimable but the wallet quietly poorer than its balance reads, and
// on a small pool the next attempt fails with "not enough funds" while the
// dashboard shows plenty.
func TestAbandoningARedemptionReleasesTheWalletInputs(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	payee := key(t)
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())
	details := RecaptureDetails{
		Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
	}
	quote, err := h.svc.QuoteRecapture(t.Context(), tagID, details)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	observation, _ := hex.DecodeString(quote.ObservationHex)
	sig, _ := attest(t, payee, tagID, observation)

	draft, err := h.svc.PrepareRedeem(t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
		Observation: observation, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	// The crabber walks away and the draft times out.
	h.svc.now = func() time.Time { return time.Now().UTC().Add(time.Hour) }
	if n := h.svc.ExpireDrafts(t.Context()); n != 1 {
		t.Fatalf("released %d drafts, want 1", n)
	}

	if len(h.ledger.aborted) != 1 {
		t.Fatalf("the wallet inputs were not released: %d aborts", len(h.ledger.aborted))
	}
	if got, want := h.ledger.aborted[0], draft.Reference; hex.EncodeToString([]byte(got)) != want {
		t.Errorf("aborted reference %q, want %q", hex.EncodeToString([]byte(got)), want)
	}
	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusActive {
		t.Errorf("tag is %s", tag.Status)
	}
}

// TestAPreBroadcastFailureAlsoReleasesTheWalletInputs: same requirement on the
// other abandonment path.
func TestAPreBroadcastFailureAlsoReleasesTheWalletInputs(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	payee := key(t)
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())
	quote, err := h.svc.QuoteRecapture(t.Context(), tagID, RecaptureDetails{
		Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
	})
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	observation, _ := hex.DecodeString(quote.ObservationHex)
	sig, _ := attest(t, payee, tagID, observation)

	draft, err := h.svc.PrepareRedeem(t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
		Observation: observation, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	h.ledger.failCompleteWith = errors.Join(chain.ErrNotBroadcast, errors.New("fake wallet refused"))
	if _, err := h.svc.CompleteRedeem(t.Context(), draft.Reference, []byte{0xaa}); err == nil {
		t.Fatal("a failing redemption reported success")
	}
	if len(h.ledger.aborted) != 1 {
		t.Fatalf("a pre-broadcast failure did not release the wallet inputs: %d aborts", len(h.ledger.aborted))
	}
}

// TestSweepCanReclaimATagBeforeItsDate covers the cases where waiting for the
// sweep date is wrong: a tag reported stolen, a recalled batch, a cancelled
// programme, a deployment being decommissioned.
func TestSweepCanReclaimATagBeforeItsDate(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	// Far from its sweep date, so the scheduled path must not touch it.
	scheduled, err := h.svc.SweepExpired(t.Context(), 50, "operator")
	if err != nil {
		t.Fatalf("scheduled sweep: %v", err)
	}
	if len(scheduled) != 0 {
		t.Fatalf("the scheduled sweep reclaimed %d tags that are not due", len(scheduled))
	}

	res, err := h.svc.SweepTag(t.Context(), tagID, "02biologist")
	if err != nil {
		t.Fatalf("targeted sweep: %v", err)
	}
	cfg := h.svc.Config()
	if res.Satoshis != cfg.TotalReward() {
		t.Errorf("reclaimed %d satoshis, want %d", res.Satoshis, cfg.TotalReward())
	}
	if res.TxID == "" {
		t.Error("no transaction was recorded for the sweep")
	}

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusRetired {
		t.Fatalf("a swept tag is %s, want retired", tag.Status)
	}

	// Taking a live reward off the water must be attributable afterwards.
	trail, err := h.store.AuditTrail(t.Context(), 20)
	if err != nil {
		t.Fatalf("audit trail: %v", err)
	}
	found := false
	for _, e := range trail {
		if e.Action == "tag.sweep" && e.Actor == "02biologist" {
			found = true
		}
	}
	if !found {
		t.Error("an early sweep left no audit entry naming who did it")
	}
}

// TestSweepingATagTwiceIsRefused: once reclaimed there is nothing left to take,
// and a second attempt must say so rather than build a transaction that fails.
func TestSweepingATagTwiceIsRefused(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	if _, err := h.svc.SweepTag(t.Context(), tagID, "operator"); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if _, err := h.svc.SweepTag(t.Context(), tagID, "operator"); err == nil {
		t.Fatal("a retired tag was swept a second time")
	}
}

// TestAnUnarmedTagHasNothingToSweep guards the other end: a minted tag holds no
// money, so reclaiming it is a no-op that should be reported, not attempted.
func TestAnUnarmedTagHasNothingToSweep(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	if _, err := h.svc.SweepTag(t.Context(), tagID, "operator"); err == nil {
		t.Fatal("an unarmed tag was swept")
	}
}

// TestASweptRewardBecomesSpendableAgain is the point of sweeping, and it was
// wrong the first time.
//
// A sweep that names its own output pays the money to an address the wallet
// controls but never mints into its UTXO pool -- only change=true rows are,
// because an application is assumed to own the lifecycle of any output it names
// itself. The balance moves and the spendable pool does not, so the "recovered"
// money is recovered into a coin nothing can spend. A sweep must therefore
// declare no outputs and let the value become change.
func TestASweptRewardBecomesSpendableAgain(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	if _, err := h.svc.SweepTag(t.Context(), tagID, "operator"); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n := len(h.ledger.sweptWithOutputs); n != 0 {
		t.Fatalf("the sweep declared %d outputs; a swept reward must become change or the wallet cannot spend it", n)
	}
}

// TestAQueuedObservationIsAcceptedLate covers the offline path.
//
// A report captured in a marsh with no signal sits in the device's outbox until
// the boat comes back in. The signed timestamp is the moment of the catch, so
// by the time the server sees it the record is hours old -- and the fifteen
// minutes the first version allowed would have refused exactly the reports the
// offline path exists to collect.
//
// The delay is not smoothed away. It is recorded in the settlement, because a
// researcher has to be able to tell a fix taken at the moment of the catch from
// one submitted six hours later.
func TestAQueuedObservationIsAcceptedLate(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID)

	payee := key(t)
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())
	details := RecaptureDetails{
		Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
	}

	// The catch happens now; the quote is the bytes the device signs there.
	caughtAt := h.svc.now()
	quote, err := h.svc.QuoteRecapture(t.Context(), tagID, details)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	observation, _ := hex.DecodeString(quote.ObservationHex)
	sig, _ := attest(t, payee, tagID, observation)

	// Six hours in the outbox.
	const queued = 6 * time.Hour
	h.svc.now = func() time.Time { return caughtAt.Add(queued) }

	draft, err := h.svc.PrepareRedeem(t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
		Observation: observation, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	})
	if err != nil {
		t.Fatalf("a report queued for six hours was refused: %v", err)
	}
	if _, err := h.svc.CompleteRedeem(t.Context(), draft.Reference, []byte{0xaa}); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// And the delay must be visible in the record rather than lost.
	events, err := h.store.EventsForTag(t.Context(), tagID)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	var found bool
	for _, ev := range events {
		if ev.Kind != string(record.KindRecapture) {
			continue
		}
		found = true
		set, serr := record.SettlementFromJSON([]byte(ev.SettlementJSON), []byte(ev.PayloadJSON), record.KindRecapture)
		if serr != nil {
			t.Fatalf("settlement: %v", serr)
		}
		if want := int(queued.Seconds()); set.QueueSec < want-5 || set.QueueSec > want+5 {
			t.Errorf("the record says the report waited %d seconds, want about %d", set.QueueSec, want)
		}
	}
	if !found {
		t.Fatal("no recapture record was written")
	}

	// A day and a half is past any plausible outbox, and a fix that old
	// attached to today's report is a mistake rather than a late submission.
	h.svc.now = func() time.Time { return caughtAt.Add(36 * time.Hour) }
	_, err = h.svc.QuoteRecapture(t.Context(), tagID, details)
	if err == nil {
		t.Log("the tag is in cooldown, which is what refuses this; that is fine")
	}
}

// TestAReportMustNameTheSpeciesTheTagWasArmedFor is the check that makes the
// species field mean something.
//
// A tag armed for a blue crab and reported as a red drum is either a mistake or
// a recovered tag on a different animal. Both are findings. Accepting it would
// put an animal in the dataset that nobody tagged.
func TestAReportMustNameTheSpeciesTheTagWasArmedFor(t *testing.T) {
	h := newHarness(t)
	tagID := h.mintOne()
	h.arm(tagID) // CALSAP, by default

	payee := key(t)
	payeeHex := hex.EncodeToString(payee.PubKey().Compressed())

	// A well-formed observation, correctly signed -- but for the wrong species.
	obs := record.Observation{
		AccCM: 600,
		Attr:  sampleAttr(species.Released),
		LatE7: record.EncodeCoord(32.79),
		LonE7: record.EncodeCoord(-79.90),
		Meas:  sampleMeas(),
		Obs:   payeeHex,
		Sp:    "SCIOCE",
		TS:    h.svc.now().Format(time.RFC3339),
	}
	payload, err := record.Marshal(obs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sig, err := attest(t, payee, tagID, payload)
	if err != nil {
		t.Fatalf("attest: %v", err)
	}

	_, err = h.svc.PrepareRedeem(t.Context(), RedeemInput{
		TagID: tagkey.ID(tagID), Lat: 32.79, Lon: -79.90, AccuracyM: 6,
		Meas: sampleMeas(), Attr: sampleAttr(species.Released), PayeePubHex: payeeHex,
		Observation: payload, AttestSig: sig.Serialize(), AttestPubHex: payeeHex,
	})
	if !errors.Is(err, ErrPayloadMismatch) {
		t.Fatalf("a report naming the wrong species was accepted: %v", err)
	}

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Status != store.StatusActive {
		t.Fatalf("a rejected report left the tag %s", tag.Status)
	}
}

// TestATagCanBeArmedForAnySpeciesTheProfilesDescribe is the point of the whole
// refactor: a second species is a JSON file, not a release.
func TestATagCanBeArmedForAnySpeciesTheProfilesDescribe(t *testing.T) {
	h := newHarness(t)

	_, tags, err := h.svc.MintBatch(t.Context(), 1, "SCIOCE", "02biologist")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	tagID := tags[0].TagID

	walletKey, _ := h.ledger.WalletKey()
	pub := hex.EncodeToString(walletKey.PubKey().Compressed())
	in := ActivationInput{
		TagID:     tagkey.ID(tagID),
		Species:   "SCIOCE",
		Lat:       32.7765,
		Lon:       -79.9311,
		AccuracyM: 4.8,
		// A red drum: total length, no moult stage, a condition at release.
		Meas:         map[string]int{"tl": 520},
		Attr:         map[string]string{"sex": "U", "cond": "GOOD", "gear": "HANDLINE"},
		AttestPubHex: pub,
	}
	preview, err := h.svc.PrepareActivation(t.Context(), in)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if preview.Species != "SCIOCE" {
		t.Fatalf("the preview names species %q", preview.Species)
	}
	sig, _, err := SelfAttest(preview.Observation, walletKey, tagID)
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	in.Observation, in.AttestSig = preview.Observation, sig
	if _, err := h.svc.Activate(t.Context(), in); err != nil {
		t.Fatalf("activate: %v", err)
	}

	tag, _ := h.store.GetTag(t.Context(), tagID)
	if tag.Species != "SCIOCE" {
		t.Fatalf("the tag records species %q", tag.Species)
	}

	// The receipt must describe a fish, using the fish's own vocabulary.
	prov, err := h.svc.Provenance(t.Context(), tagID)
	if err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if prov.Common != "Red drum" || prov.PrimaryKey != "tl" || prov.PrimaryAt != 520 {
		t.Fatalf("the receipt does not describe a red drum: %+v", prov)
	}
	if !prov.GrowthExpected {
		t.Error("growth is not expected for a red drum; for a fish it is the point of the programme")
	}

	// And a red drum over the slot limit must go back, which is a rule a blue
	// crab does not have at all.
	over := map[string]int{"tl": 700}
	profile, _ := species.Get("SCIOCE")
	must, why := profile.MustReleaseNow(over, in.Attr)
	if !must {
		t.Error("a red drum over the slot maximum was not required to be released")
	}
	if why == "" {
		t.Error("the refusal has no reason to show anyone")
	}
}

// TestTheApiNeverSendsNullWhereAClientExpectsAList is a regression test for a
// crash on the most common path in the application.
//
// Go marshals a nil slice as `null` and an empty one as `[]`. A tag that has
// never been reported has no recaptures, so the field was nil, so the API sent
// `null` -- and the Android app, whose type said `RecaptureSummary[]`, called
// `.length` on it and died. Every finder scanning a freshly armed tag hit it.
//
// A typed client is no protection here: the type describes what the field is
// supposed to be, and the compiler has no way to know the wire disagreed. So
// the guarantee has to be made where the JSON is produced.
func TestTheApiNeverSendsNullWhereAClientExpectsAList(t *testing.T) {
	h := newHarness(t)

	// The exact state a finder scans: armed, never reported.
	tagID := h.mintOne()
	h.arm(tagID)

	assertNoNullCollections(t, h, tagID, "a freshly armed tag")

	// And after a report, where the nested per-recapture maps could be nil.
	h.report(tagID, key(t), species.Released, 32.79, -79.90)
	assertNoNullCollections(t, h, tagID, "a reported tag")
}

func assertNoNullCollections(t *testing.T, h *harness, tagID, what string) {
	t.Helper()

	prov, err := h.svc.Provenance(t.Context(), tagID)
	if err != nil {
		t.Fatalf("%s: provenance: %v", what, err)
	}
	body, err := json.Marshal(prov)
	if err != nil {
		t.Fatalf("%s: marshal: %v", what, err)
	}

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatalf("%s: unmarshal: %v", what, err)
	}

	// Every field a client indexes or iterates. Naming them explicitly rather
	// than scanning for nulls, because a null *scalar* is fine -- named_at is
	// legitimately absent until somebody names the animal.
	for _, field := range []string{"recaptures", "facts", "tagged_meas", "tagged_attr"} {
		raw, ok := generic[field]
		if !ok {
			t.Errorf("%s: %q is missing from the response entirely", what, field)
			continue
		}
		if string(raw) == "null" {
			t.Errorf("%s: %q is null; a client whose type says it is a list will crash on it",
				what, field)
		}
	}

	// The nested collections on each recapture, for the same reason.
	for i, r := range prov.Recaptures {
		if r.Meas == nil {
			t.Errorf("%s: recapture %d has a nil measurement map", what, i)
		}
		if r.Attr == nil {
			t.Errorf("%s: recapture %d has a nil attribute map", what, i)
		}
	}
}
