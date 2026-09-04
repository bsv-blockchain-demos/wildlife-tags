package audit

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/transaction"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
)

// fakeTxs stands in for the wallet's storage. The audit's whole job is
// comparing what the database says against what the chain actually holds, so
// the interesting tests are the ones where those two disagree.
type fakeTxs map[string][]byte

func (f fakeTxs) RawTxs(_ context.Context, txids []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, id := range txids {
		if raw, ok := f[id]; ok {
			out[id] = raw
		}
	}
	return out, nil
}

type fixture struct {
	store  *store.Store
	txs    fakeTxs
	tagKey *ec.PrivateKey
	dnrKey *ec.PrivateKey
	attest *ec.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tags.db"), "")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	f := &fixture{store: s, txs: fakeTxs{}}
	f.tagKey, _ = ec.NewPrivateKey()
	f.dnrKey, _ = ec.NewPrivateKey()
	f.attest, _ = ec.NewPrivateKey()
	return f
}

func (f *fixture) dnrPubHex() string {
	return hex.EncodeToString(f.dnrKey.PubKey().Compressed())
}

// activate seeds a tag armed with a real locking script carrying a real record.
func (f *fixture) activate(t *testing.T, tagID string) string {
	t.Helper()
	now := time.Now().UTC()

	if err := f.store.CreateBatch(t.Context(), store.Batch{
		ID: "B1", CreatedAt: now, CreatedBy: "02bio", TagCount: 1, ManifestHash: "abc",
	}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	if err := f.store.InsertTags(t.Context(), []store.Tag{{
		TagID: tagID, BatchID: "B1", Ordinal: 0,
		PubKeyHex: hex.EncodeToString(f.tagKey.PubKey().Compressed()), CreatedAt: now,
	}}); err != nil {
		t.Fatalf("tags: %v", err)
	}

	payload, _ := record.Marshal(record.Observation{
		AccCM: 480,
		Attr:  map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"},
		LatE7: record.EncodeCoord(32.7765), LonE7: record.EncodeCoord(-79.9311),
		Meas: map[string]int{"cw": 142},
		Obs:  hex.EncodeToString(f.attest.PubKey().Compressed()),
		Sp:   "CALSAP", TS: now.Format(time.RFC3339),
	})
	settlement, _ := record.Marshal(record.Settlement{BaseSat: 5000, Batch: "B1", BonSat: 15000})
	attestKey, err := record.AttestationPrivateKey(f.attest, tagID)
	if err != nil {
		t.Fatalf("derive attestation key: %v", err)
	}
	sig, _ := attestKey.Sign(record.Digest(payload))
	fields, err := record.Encode(tagID, record.KindActivate, 0, payload, sig.Serialize(), f.attest.PubKey(), settlement)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	lock, err := tagscript.Lock(f.tagKey.PubKey(), f.dnrKey.PubKey(), fields)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	tx := transaction.NewTransaction()
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: lock, Satoshis: 20000})
	txid := tx.TxID().String()
	f.txs[txid] = tx.Bytes()

	id, _ := f.store.BeginEvent(t.Context(), store.Event{
		TagID: tagID, Generation: 0, Kind: "ACT",
		PayloadJSON: string(payload), SettlementJSON: string(settlement),
		AttestPubKey: hex.EncodeToString(f.attest.PubKey().Compressed()),
	})
	_ = f.store.BroadcastEvent(t.Context(), id, txid, 0, 20000)
	_ = f.store.MarkEventMined(t.Context(), txid)
	_ = f.store.ActivateTag(t.Context(), tagID, "CALSAP", txid, 0, 20000, now.Add(540*24*time.Hour))
	return txid
}

func TestAHealthyProgramHasNoFindings(t *testing.T) {
	f := newFixture(t)
	f.activate(t, "K2M9Q7C")

	rep, err := Run(t.Context(), f.store, f.txs, f.dnrPubHex())
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if rep.Criticals() != 0 {
		t.Fatalf("a healthy program produced criticals: %+v", rep.Findings)
	}
	if rep.TagsChecked != 1 || rep.TxsChecked != 1 {
		t.Fatalf("checked %d tags and %d transactions", rep.TagsChecked, rep.TxsChecked)
	}
}

// TestATamperedDatabaseIsCaught is what the audit is for: the chain is the
// authority, and a database edited to say something else must not pass quietly.
func TestATamperedDatabaseIsCaught(t *testing.T) {
	f := newFixture(t)
	txid := f.activate(t, "K2M9Q7C")

	// Someone edits the recorded position after the fact. The chain still
	// carries the original.
	if _, err := f.store.DB().Exec(
		`UPDATE tag_events SET payload_json = ? WHERE txid = ?`,
		`{"acc":480,"lat":400000000,"lon":-740000000}`, txid); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	rep, err := Run(t.Context(), f.store, f.txs, f.dnrPubHex())
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !hasCheck(rep, "record.payload") {
		t.Fatalf("an edited payload was not caught: %+v", rep.Findings)
	}
}

// TestAnOutputLockedToTheWrongKeyIsCritical catches a tag whose reward is not
// actually spendable by the tag it claims to belong to.
func TestAnOutputLockedToTheWrongKeyIsCritical(t *testing.T) {
	f := newFixture(t)
	f.activate(t, "K2M9Q7C")

	if _, err := f.store.DB().Exec(
		`UPDATE tags SET pubkey_hex = '02' || substr(pubkey_hex, 3)`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	// Force a definite mismatch rather than relying on the prefix flip.
	if _, err := f.store.DB().Exec(
		`UPDATE tags SET pubkey_hex = '020000000000000000000000000000000000000000000000000000000000000001'`); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	rep, err := Run(t.Context(), f.store, f.txs, f.dnrPubHex())
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !hasCheck(rep, "script.tagkey") {
		t.Fatalf("a mismatched tag key was not caught: %+v", rep.Findings)
	}
}

// TestAnOutputNamingAnotherDeploymentsKeyIsCritical catches a record that was
// written by a different program against the same database.
func TestAnOutputNamingAnotherDeploymentsKeyIsCritical(t *testing.T) {
	f := newFixture(t)
	f.activate(t, "K2M9Q7C")

	other, _ := ec.NewPrivateKey()
	rep, err := Run(t.Context(), f.store, f.txs, hex.EncodeToString(other.PubKey().Compressed()))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !hasCheck(rep, "script.cosignkey") {
		t.Fatalf("a foreign co-signing key was not caught: %+v", rep.Findings)
	}
}

// TestATransactionTheWalletHasLostIsReported: the database names a transaction
// the wallet store cannot produce, which means the audit cannot verify it.
func TestATransactionTheWalletHasLostIsReported(t *testing.T) {
	f := newFixture(t)
	txid := f.activate(t, "K2M9Q7C")
	delete(f.txs, txid)

	rep, err := Run(t.Context(), f.store, f.txs, f.dnrPubHex())
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !hasCheck(rep, "tx.missing") {
		t.Fatalf("a missing transaction was not reported: %+v", rep.Findings)
	}
}

// TestAStrandedBonusIsReported: a retired tag can never be reported again, so
// a bonus still owed against it has no path to ever being paid. Somebody has to
// decide what to do about that, and they can only decide if they are told.
func TestAStrandedBonusIsReported(t *testing.T) {
	f := newFixture(t)
	f.activate(t, "K2M9Q7C")

	if err := f.store.CreateEscrow(t.Context(), store.Escrow{
		TagID: "K2M9Q7C", Generation: 1, Beneficiary: "02reporter", Satoshis: 15000,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("escrow: %v", err)
	}
	if err := f.store.RetireTag(t.Context(), "K2M9Q7C"); err != nil {
		t.Fatalf("retire: %v", err)
	}

	rep, err := Run(t.Context(), f.store, f.txs, f.dnrPubHex())
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !hasCheck(rep, "escrow.stranded") {
		t.Fatalf("a bonus that can never be paid was not reported: %+v", rep.Findings)
	}
}

func hasCheck(rep *Report, check string) bool {
	for _, f := range rep.Findings {
		if f.Check == check {
			return true
		}
	}
	return false
}
