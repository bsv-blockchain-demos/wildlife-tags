package tagscript

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"
	"github.com/bsv-blockchain/go-sdk/transaction/template/pushdrop"
)

func mustKey(t *testing.T) *ec.PrivateKey {
	t.Helper()
	k, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key: %v", err)
	}
	return k
}

func sampleFields() [][]byte {
	return [][]byte{
		[]byte("WILDTAG"),
		[]byte("1"),
		[]byte("SC7K2M9Q"),
		[]byte("ACT"),
		[]byte("0"),
		[]byte(`{"t":"act","lat":32.7765,"lon":-79.9311}`),
	}
}

// spendFixture builds a transaction shaped the way the toolbox builds one: the
// tag input first, then a P2PKH fuel input, with the payout and change after
// it. Signing and verification are exercised against that real shape rather
// than a one-input toy, because the sighash commits to all of it.
func spendFixture(t *testing.T, lock *script.Script, lockSats uint64) (*transaction.Transaction, *ec.PrivateKey) {
	t.Helper()

	fuelKey := mustKey(t)
	fuelAddr, err := script.NewAddressFromPublicKey(fuelKey.PubKey(), false)
	if err != nil {
		t.Fatalf("fuel address: %v", err)
	}
	fuelLock, err := p2pkh.Lock(fuelAddr)
	if err != nil {
		t.Fatalf("fuel lock: %v", err)
	}

	// The parent holding the tag output, plus a separate parent for the fuel.
	tagParent := transaction.NewTransaction()
	tagParent.AddOutput(&transaction.TransactionOutput{LockingScript: lock, Satoshis: lockSats})

	fuelParent := transaction.NewTransaction()
	fuelParent.AddOutput(&transaction.TransactionOutput{LockingScript: fuelLock, Satoshis: 100000})

	tx := transaction.NewTransaction()
	tx.AddInputFromTx(tagParent, 0, nil)
	tx.AddInputFromTx(fuelParent, 0, nil)

	payoutKey := mustKey(t)
	payoutAddr, err := script.NewAddressFromPublicKey(payoutKey.PubKey(), false)
	if err != nil {
		t.Fatalf("payout address: %v", err)
	}
	payoutLock, err := p2pkh.Lock(payoutAddr)
	if err != nil {
		t.Fatalf("payout lock: %v", err)
	}
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: payoutLock, Satoshis: 5000})
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: fuelLock, Satoshis: 94000})

	return tx, fuelKey
}

// signBoth completes the two-of-two on input 0 and leaves the fuel input
// unsigned; VerifyInput only ever looks at one input at a time.
func signBoth(t *testing.T, tx *transaction.Transaction, tagKey, dnrKey *ec.PrivateKey) {
	t.Helper()
	tagSig, err := SignWith(tagKey, tx, 0)
	if err != nil {
		t.Fatalf("tag signature: %v", err)
	}
	dnrSig, err := SignWith(dnrKey, tx, 0)
	if err != nil {
		t.Fatalf("dnr signature: %v", err)
	}
	unlock, err := Unlock(dnrSig, tagSig)
	if err != nil {
		t.Fatalf("assemble unlocking script: %v", err)
	}
	tx.Inputs[0].UnlockingScript = unlock
}

func TestBothSignaturesTogetherUnlockTheOutput(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	tx, _ := spendFixture(t, lock, 20000)
	signBoth(t, tx, tagKey, dnrKey)

	if err := VerifyInput(tx, 0); err != nil {
		t.Fatalf("a correctly signed spend was rejected: %v", err)
	}
}

func TestTheTagSignatureAloneIsNotEnough(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	tx, _ := spendFixture(t, lock, 20000)

	// A finder who holds the tag secret but no DNR co-signature. This is the
	// exact attack the second key exists to stop: reporter #1 coming back for
	// the escrow that holds the next finder's reward.
	tagSig, err := SignWith(tagKey, tx, 0)
	if err != nil {
		t.Fatalf("tag signature: %v", err)
	}
	unlock, err := Unlock(tagSig, tagSig)
	if err != nil {
		t.Fatalf("assemble unlocking script: %v", err)
	}
	tx.Inputs[0].UnlockingScript = unlock

	if err := VerifyInput(tx, 0); err == nil {
		t.Fatal("a spend carrying only the tag key was accepted")
	}
}

func TestTheDNRSignatureAloneIsNotEnough(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	tx, _ := spendFixture(t, lock, 20000)

	dnrSig, err := SignWith(dnrKey, tx, 0)
	if err != nil {
		t.Fatalf("dnr signature: %v", err)
	}
	unlock, err := Unlock(dnrSig, dnrSig)
	if err != nil {
		t.Fatalf("assemble unlocking script: %v", err)
	}
	tx.Inputs[0].UnlockingScript = unlock

	if err := VerifyInput(tx, 0); err == nil {
		t.Fatal("a spend carrying only the DNR key was accepted")
	}
}

func TestSignatureOrderIsNotInterchangeable(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	tx, _ := spendFixture(t, lock, 20000)

	tagSig, err := SignWith(tagKey, tx, 0)
	if err != nil {
		t.Fatalf("tag signature: %v", err)
	}
	dnrSig, err := SignWith(dnrKey, tx, 0)
	if err != nil {
		t.Fatalf("dnr signature: %v", err)
	}
	// Swapped: tagSig pushed first, so dnrSig ends up on top where
	// OP_CHECKSIGVERIFY expects the tag signature.
	unlock, err := Unlock(tagSig, dnrSig)
	if err != nil {
		t.Fatalf("assemble unlocking script: %v", err)
	}
	tx.Inputs[0].UnlockingScript = unlock

	if err := VerifyInput(tx, 0); err == nil {
		t.Fatal("signatures pushed in the wrong order were accepted")
	}
}

func TestChangingAnOutputInvalidatesTheSpend(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	tx, _ := spendFixture(t, lock, 20000)
	signBoth(t, tx, tagKey, dnrKey)
	if err := VerifyInput(tx, 0); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// This is the property the whole design leans on: the recapture record and
	// the payout live in the same transaction the tag key signed, so neither
	// can be edited after the fact.
	tx.Outputs[0].Satoshis = 6000
	if err := VerifyInput(tx, 0); err == nil {
		t.Fatal("a spend remained valid after its payout was altered")
	}
}

func TestDecodeRoundTripsEveryField(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	fields := sampleFields()
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), fields)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	got, err := Decode(lock)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.TagPub.IsEqual(tagKey.PubKey()) {
		t.Error("tag public key did not round-trip")
	}
	if !got.DNRPub.IsEqual(dnrKey.PubKey()) {
		t.Error("dnr public key did not round-trip")
	}
	if len(got.Fields) != len(fields) {
		t.Fatalf("field count: got %d, want %d", len(got.Fields), len(fields))
	}
	for i := range fields {
		if !bytes.Equal(got.Fields[i], fields[i]) {
			t.Errorf("field %d: got %q, want %q", i, got.Fields[i], fields[i])
		}
	}
}

func TestDecodeHandlesAnOddFieldCount(t *testing.T) {
	// An odd count ends in OP_DROP rather than OP_2DROP; the boundary is easy
	// to get wrong in either direction.
	tagKey, dnrKey := mustKey(t), mustKey(t)
	for n := 1; n <= 9; n++ {
		fields := make([][]byte, n)
		for i := range fields {
			fields[i] = []byte{byte('a' + i)}
		}
		lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), fields)
		if err != nil {
			t.Fatalf("%d fields: lock: %v", n, err)
		}
		got, err := Decode(lock)
		if err != nil {
			t.Fatalf("%d fields: decode: %v", n, err)
		}
		if len(got.Fields) != n {
			t.Errorf("%d fields: decoded %d", n, len(got.Fields))
		}
	}
}

func TestDecodeRefusesAScriptThatDoesNotRebuild(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Append a trailing opcode. The fields still parse, but the script is no
	// longer the one those fields produce, so Decode must refuse rather than
	// report fields from a script it does not fully understand.
	tampered := append(script.Script{}, *lock...)
	tampered = append(tampered, script.OpNOP)

	if _, err := Decode(&tampered); !errors.Is(err, ErrNotTagScript) {
		t.Fatalf("got %v, want ErrNotTagScript", err)
	}
}

func TestDecodeRejectsAPlainPushDropScript(t *testing.T) {
	// A single-OP_CHECKSIG PushDrop token is the thing our script is most
	// likely to be confused with. Decode must not claim it.
	tagKey := mustKey(t)
	chunks := []*script.ScriptChunk{
		{Op: byte(len(tagKey.PubKey().Compressed())), Data: tagKey.PubKey().Compressed()},
		{Op: script.OpCHECKSIG},
		{Op: 3, Data: []byte("abc")},
		{Op: script.OpDROP},
	}
	s, err := script.NewScriptFromScriptOps(chunks)
	if err != nil {
		t.Fatalf("build pushdrop: %v", err)
	}
	if _, err := Decode(s); !errors.Is(err, ErrNotTagScript) {
		t.Fatalf("got %v, want ErrNotTagScript", err)
	}
}

func TestLockRefusesAnEmptyField(t *testing.T) {
	// An empty push is indistinguishable from "the fields ended", so a record
	// containing one silently loses everything after it. Refuse at encode time
	// rather than write a record that cannot be read back.
	tagKey, dnrKey := mustKey(t), mustKey(t)
	fields := [][]byte{[]byte("WILDTAG"), {}, []byte("SC7K2M9Q")}
	if _, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), fields); !errors.Is(err, ErrEmptyField) {
		t.Fatalf("got %v, want ErrEmptyField", err)
	}
}

func TestLockRefusesNoFields(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	if _, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), nil); !errors.Is(err, ErrNoFields) {
		t.Fatalf("got %v, want ErrNoFields", err)
	}
}

func TestLockCarriesAPayloadOfRealisticSize(t *testing.T) {
	// A recapture payload plus a DER signature is the largest thing the script
	// carries; a push over 75 bytes changes encoding to OP_PUSHDATA1, which the
	// rebuild check would catch if pushChunk got it wrong.
	tagKey, dnrKey := mustKey(t), mustKey(t)
	payload := []byte(strings.Repeat("x", 400))
	sig := bytes.Repeat([]byte{0xab}, 71)
	fields := [][]byte{[]byte("WILDTAG"), []byte("1"), []byte("SC7K2M9Q"), []byte("REC"), []byte("1"), payload, sig, bytes.Repeat([]byte{0x02}, 33)}

	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), fields)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	got, err := Decode(lock)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got.Fields[5], payload) {
		t.Error("the 400-byte payload did not round-trip")
	}
	if !bytes.Equal(got.Fields[6], sig) {
		t.Error("the signature field did not round-trip")
	}
}

// TestTheGoSDKPushDropDecoderDoesNotClaimOurScript pins the reason this package
// ships its own decoder. If a future go-sdk teaches pushdrop.Decode to handle a
// two-of-two head, this test fails and Decode can be reconsidered.
func TestTheGoSDKPushDropDecoderDoesNotClaimOurScript(t *testing.T) {
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	if got := pushdrop.Decode(lock); got != nil {
		t.Fatalf("pushdrop.Decode now parses our script (%d fields); revisit tagscript.Decode", len(got.Fields))
	}
}

func TestUnlockingScriptEstimateCoversRealSignatures(t *testing.T) {
	// Under-declaring UnlockingScriptLength underpays the fee, and arcade
	// rejects that with a permanent 4xx. Sign a batch and confirm the real
	// script never exceeds what we tell the fee estimator.
	tagKey, dnrKey := mustKey(t), mustKey(t)
	lock, err := Lock(tagKey.PubKey(), dnrKey.PubKey(), sampleFields())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	for i := 0; i < 200; i++ {
		tx, _ := spendFixture(t, lock, 20000)
		signBoth(t, tx, tagKey, dnrKey)
		if got := uint32(len(*tx.Inputs[0].UnlockingScript)); got > UnlockingScriptEstimate {
			t.Fatalf("unlocking script was %d bytes, over the %d-byte estimate", got, UnlockingScriptEstimate)
		}
	}
}
