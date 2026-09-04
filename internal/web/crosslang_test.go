package web

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-sdk/transaction/template/p2pkh"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
)

// This file is the guard that three production bugs got past.
//
// A client builds a record's canonical bytes, attests to them with its wallet,
// and signs a transaction the server built; the Go side then rebuilds the same
// bytes, verifies the attestation, co-signs, and asks the script interpreter to
// accept the pair. Every part of that is easy to get subtly wrong in a way that
// looks fine on its own -- a key derived from the wrong material, a key id in
// the wrong form, a message hashed once instead of twice, a JSON object whose
// keys came out in insertion order -- and the only honest check is to run both
// languages against each other and compare the bytes.
//
// Both tests skip without node rather than failing, matching rule-110-arcade.

// The values a form actually collects, before any encoding. Both sides build
// the record from these, so the encoder's own scaling and rounding are under
// test rather than assumed.
const (
	fixLat       = 32.7765
	fixLon       = -79.9311
	fixAccuracyM = 4.8
	fixAt        = "2026-08-26T14:03:11Z"
	fixName      = "Old Bertha"
	fixSpecies   = "CALSAP"
)

func fixMeas() map[string]int { return map[string]int{"cw": 142, "wt": 2840, "sal": 3120} }
func fixAttr() map[string]string {
	return map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"}
}

// crossLang is everything the client produced, plus what the server built to
// compare it against.
type crossLang struct {
	Observation  string `json:"observation"`
	AttestSigHex string `json:"attestSigHex"`
	AttestPubKey string `json:"attestPubKey"`
	TagPubKey    string `json:"tagPubKey"`
	SigHex       string `json:"sigHex"`
	SighashHex   string `json:"sighashHex"`

	// The server's side, so a test can compare without rebuilding it.
	goObservation []byte
	goSettlement  []byte
	attest        *ec.PrivateKey
	attestPubHex  string
	dnrKey        *ec.PrivateKey
	tagKey        *ec.PrivateKey
	secret        tagkey.Secret
	tx            *transaction.Transaction
}

// runClient builds a realistic redemption, runs the client half under node, and
// returns both sides.
func runClient(t *testing.T) *crossLang {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found; skipping the cross-language test")
	}

	// A fixed secret, so a failure is reproducible.
	secret, err := tagkey.DecodeSecret("rRr2cgtt-OsSKlo3k6goPQ")
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	tagKey := secret.PrivateKey()

	dnrKey, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatalf("dnr key: %v", err)
	}
	attest, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatalf("attest key: %v", err)
	}
	attestPubHex := hex.EncodeToString(attest.PubKey().Compressed())

	payload, err := record.Marshal(record.Observation{
		AccCM: int(fixAccuracyM * 100),
		Attr:  fixAttr(),
		LatE7: record.EncodeCoord(fixLat),
		LonE7: record.EncodeCoord(fixLon),
		Meas:  fixMeas(),
		Name:  fixName,
		Obs:   attestPubHex,
		Sp:    fixSpecies,
		TS:    fixAt,
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	settlement, err := record.Marshal(record.Settlement{BaseSat: 5000, Batch: "B1", BonSat: 15000})
	if err != nil {
		t.Fatalf("settlement: %v", err)
	}

	// A realistic tag output: the record lives in the locking script.
	signer, err := record.AttestationPrivateKey(attest, string(secret.ID()))
	if err != nil {
		t.Fatalf("derive attestation key: %v", err)
	}
	asig, err := signer.Sign(record.Digest(payload))
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	fields, err := record.Encode(string(secret.ID()), record.KindActivate, 0,
		payload, asig.Serialize(), attest.PubKey(), settlement)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	lock, err := tagscript.Lock(tagKey.PubKey(), dnrKey.PubKey(), fields)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}

	// The transaction the client is asked to sign, shaped like a real
	// redemption: the tag input first, then a fuel input, with a payout and
	// change.
	parent := transaction.NewTransaction()
	parent.AddOutput(&transaction.TransactionOutput{LockingScript: lock, Satoshis: 20000})

	fuelKey, _ := ec.NewPrivateKey()
	fuelAddr, _ := script.NewAddressFromPublicKey(fuelKey.PubKey(), false)
	fuelLock, _ := p2pkh.Lock(fuelAddr)
	fuelParent := transaction.NewTransaction()
	fuelParent.AddOutput(&transaction.TransactionOutput{LockingScript: fuelLock, Satoshis: 50000})

	tx := transaction.NewTransaction()
	tx.AddInputFromTx(parent, 0, nil)
	tx.AddInputFromTx(fuelParent, 0, nil)
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: fuelLock, Satoshis: 5000})
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: lock, Satoshis: 20000})
	tx.AddOutput(&transaction.TransactionOutput{LockingScript: fuelLock, Satoshis: 44000})

	beef, err := tx.BEEF()
	if err != nil {
		t.Fatalf("beef: %v", err)
	}

	fixture := filepath.Join(t.TempDir(), "fixture.json")
	body, _ := json.Marshal(map[string]any{
		"beefHex":       hex.EncodeToString(beef),
		"secretHex":     hex.EncodeToString(secret[:]),
		"inputIndex":    0,
		"tagID":         string(secret.ID()),
		"attestPrivHex": hex.EncodeToString(attest.Serialize()),
		"observation":   json.RawMessage(unsortedObservation(attestPubHex)),
	})
	if err := os.WriteFile(fixture, body, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	out, err := exec.CommandContext(t.Context(), node,
		filepath.Join("testdata", "sign_test.js"), fixture).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			// The vendored SDK is one enormous line, so echoing all of stderr
			// buries the actual message. The last few lines are the stack.
			t.Fatalf("node failed: %v\n%s", err, tail(ee.Stderr, 800))
		}
		t.Fatalf("node failed: %v", err)
	}

	var got crossLang
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode node output: %v\n%s", err, out)
	}
	got.goObservation = payload
	got.goSettlement = settlement
	got.attest, got.attestPubHex = attest, attestPubHex
	got.dnrKey, got.tagKey, got.secret, got.tx = dnrKey, tagKey, secret, tx
	return &got
}

// unsortedObservation writes the fixture's observation with every object's keys
// in *reverse* alphabetical order.
//
// This is what gives the byte comparison teeth. Go's json.Marshal sorts map
// keys, so a fixture built the ordinary way arrives in JavaScript already
// sorted, JSON.parse preserves that order, and JSON.stringify reproduces it --
// so the test would pass even with the client's sorting removed entirely, which
// is exactly the drift it exists to catch. Handing the client the worst
// possible order means only real sorting can produce the right bytes.
//
// It is also the honest input: attr and meas come from a form, in whatever
// order the fields happen to be read.
func unsortedObservation(observer string) string {
	return `{
		"species": "` + fixSpecies + `",
		"observer": "` + observer + `",
		"name": "` + fixName + `",
		"meas": {"wt": 2840, "sal": 3120, "cw": 142},
		"lon": ` + strconv.FormatFloat(fixLon, 'f', -1, 64) + `,
		"lat": ` + strconv.FormatFloat(fixLat, 'f', -1, 64) + `,
		"attr": {"stage": "HARD", "sex": "M", "gear": "TRAP"},
		"at": "` + fixAt + `",
		"accuracyM": ` + strconv.FormatFloat(fixAccuracyM, 'f', -1, 64) + `
	}`
}

func tail(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[len(b)-n:]
}

// TestTheAppAndTheServerAgreeOnCanonicalBytes is the most important test in the
// repository.
//
// Every signature in this system is over the bytes an observation becomes, and
// the two sides build those bytes with different code in different languages.
// Go's encoding/json sorts map keys; JavaScript's JSON.stringify uses insertion
// order. If the shared encoder ever stops correcting for that, every record made
// on a phone fails to verify on the server -- and the error, "attestation
// signature does not verify", names neither side.
//
// So: build the same observation from the same human values on both sides, and
// compare the bytes exactly.
func TestTheAppAndTheServerAgreeOnCanonicalBytes(t *testing.T) {
	got := runClient(t)

	if got.Observation != string(got.goObservation) {
		t.Fatalf("the client and the server disagree about the canonical bytes:\n  client: %s\n  server: %s",
			got.Observation, got.goObservation)
	}

	// And the attestation the client made over them must verify on the server,
	// through the same path a real record takes.
	if got.AttestPubKey != got.attestPubHex {
		t.Fatalf("the client attested as %s, not %s", got.AttestPubKey, got.attestPubHex)
	}
	clientSig, err := hex.DecodeString(got.AttestSigHex)
	if err != nil {
		t.Fatalf("attestation hex: %v", err)
	}
	fields, err := record.Encode(string(got.secret.ID()), record.KindActivate, 0,
		[]byte(got.Observation), clientSig, got.attest.PubKey(), got.goSettlement)
	if err != nil {
		t.Fatalf("encode the client's record: %v", err)
	}
	rec, err := record.Decode(fields)
	if err != nil {
		t.Fatalf("decode the client's record: %v", err)
	}
	if err := rec.Verify(); err != nil {
		t.Fatalf("the server rejected an attestation the client made: %v", err)
	}
}

// TestTheAppProducesASignatureTheInterpreterAccepts is the other half: the tag
// key signs the transaction, and the real script interpreter judges the result.
func TestTheAppProducesASignatureTheInterpreterAccepts(t *testing.T) {
	got := runClient(t)

	// The client must derive the same tag key from the bearer secret. If this
	// drifts, every redemption fails and the client has no way to tell why.
	if want := hex.EncodeToString(got.tagKey.PubKey().Compressed()); got.TagPubKey != want {
		t.Fatalf("the client derived tag key %s, Go derives %s", got.TagPubKey, want)
	}

	// And the same sighash, which is what the signature is actually over.
	wantHash, err := tagscript.SigHash(got.tx, 0)
	if err != nil {
		t.Fatalf("sighash: %v", err)
	}
	if got.SighashHex != hex.EncodeToString(wantHash) {
		t.Fatalf("preimage mismatch:\n  client: %s\n  Go:     %s", got.SighashHex, hex.EncodeToString(wantHash))
	}

	// Finally: does the interpreter accept what the client produced?
	tagSig, err := hex.DecodeString(got.SigHex)
	if err != nil {
		t.Fatalf("signature hex: %v", err)
	}
	dnrSig, err := tagscript.SignWith(got.dnrKey, got.tx, 0)
	if err != nil {
		t.Fatalf("co-sign: %v", err)
	}
	unlock, err := tagscript.Unlock(dnrSig, tagSig)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	got.tx.Inputs[0].UnlockingScript = unlock

	if err := tagscript.VerifyInput(got.tx, 0); err != nil {
		t.Fatalf("the interpreter rejected a signature made by the client: %v", err)
	}
}
