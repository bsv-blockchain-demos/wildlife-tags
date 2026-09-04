package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
)

const tagID = "K2M9Q7C"

func mustKey(t *testing.T) *ec.PrivateKey {
	t.Helper()
	k, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return k
}

func sampleObservation() Observation {
	return Observation{
		AccCM: 480,
		Attr:  map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"},
		LatE7: EncodeCoord(32.7765),
		LonE7: EncodeCoord(-79.9311),
		Meas:  map[string]int{"cw": 142, "wt": 2840, "sal": 3120},
		Obs:   "02abcd",
		Sp:    "CALSAP",
		TS:    "2026-08-26T14:03:11Z",
	}
}

func sampleRecapture() Observation {
	return Observation{
		AccCM: 620,
		Attr: map[string]string{
			"sex": "M", "gear": "TROTLINE", species.DispositionKey: string(species.Released),
		},
		LatE7: EncodeCoord(32.81),
		LonE7: EncodeCoord(-79.85),
		Meas:  map[string]int{"cw": 149},
		Obs:   "02cd",
		Sp:    "CALSAP",
		TS:    "2026-12-01T09:00:00Z",
	}
}

func activationSettlement() Settlement {
	return Settlement{BaseSat: 5000, Batch: "B7", BonSat: 15000}
}

func recaptureSettlement() Settlement {
	return Settlement{
		DaysAt: 97, EscrowSat: 15000, EscrowFor: "02cd", DistM: 14200,
		PaidSat: 5000, Payee: "02cd", Prev: "ab12",
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// signed attests exactly as a BRC-100 wallet would: with the derived child,
// not with the identity key.
func signed(t *testing.T, key *ec.PrivateKey, payload []byte) []byte {
	t.Helper()
	signer, err := AttestationPrivateKey(key, tagID)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	sig, err := signer.Sign(Digest(payload))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return sig.Serialize()
}

// encodeSample builds a well-formed activation record signed by key.
func encodeSample(t *testing.T, key *ec.PrivateKey) ([][]byte, []byte, []byte) {
	t.Helper()
	obs := mustMarshal(t, sampleObservation())
	set := mustMarshal(t, activationSettlement())
	fields, err := Encode(tagID, KindActivate, 0, obs, signed(t, key, obs), key.PubKey(), set)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return fields, obs, set
}

func TestAnActivationRoundTrips(t *testing.T) {
	key := mustKey(t)
	wantObs, wantSet := sampleObservation(), activationSettlement()
	fields, _, _ := encodeSample(t, key)

	rec, err := Decode(fields)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rec.Version != Version || rec.TagID != tagID || rec.Kind != KindActivate || rec.Generation != 0 {
		t.Fatalf("header did not round-trip: %+v", rec)
	}
	gotObs, gotSet, err := rec.Halves()
	if err != nil {
		t.Fatalf("halves: %v", err)
	}
	if !reflect.DeepEqual(*gotObs, wantObs.Canonical()) {
		t.Fatalf("observation did not round-trip:\n got %+v\nwant %+v", *gotObs, wantObs)
	}
	if !reflect.DeepEqual(*gotSet, wantSet) {
		t.Fatalf("settlement did not round-trip:\n got %+v\nwant %+v", *gotSet, wantSet)
	}
}

func TestARecaptureRoundTrips(t *testing.T) {
	key := mustKey(t)
	wantObs, wantSet := sampleRecapture(), recaptureSettlement()
	obs, set := mustMarshal(t, wantObs), mustMarshal(t, wantSet)

	fields, err := Encode(tagID, KindRecapture, 1, obs, signed(t, key, obs), key.PubKey(), set)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec, err := Decode(fields)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	gotObs, gotSet, err := rec.Halves()
	if err != nil {
		t.Fatalf("halves: %v", err)
	}
	if !reflect.DeepEqual(*gotObs, wantObs.Canonical()) {
		t.Fatalf("observation did not round-trip:\n got %+v\nwant %+v", *gotObs, wantObs)
	}
	if !reflect.DeepEqual(*gotSet, wantSet) {
		t.Fatalf("settlement did not round-trip:\n got %+v\nwant %+v", *gotSet, wantSet)
	}
	if got := gotObs.Disposition(); got != species.Released {
		t.Errorf("disposition read back as %q", got)
	}
}

// TestThePayloadIsCanonicalJSON is the property every client depends on: a
// wallet signs a digest computed over bytes the client produced, and the server
// must be able to reproduce those exact bytes to store a verifiable record. If
// Go's field order ever stops matching sorted-key JSON.stringify, every
// attestation made off-server stops verifying on it.
func TestThePayloadIsCanonicalJSON(t *testing.T) {
	for _, payload := range []any{sampleObservation(), sampleRecapture(), activationSettlement(), recaptureSettlement()} {
		b, err := Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		assertSortedKeys(t, b)
	}
}

// assertSortedKeys walks an encoded object and every nested one, because the
// observation's two maps are nested objects and their keys are just as much
// part of the signed bytes as the top-level ones.
func assertSortedKeys(t *testing.T, b []byte) {
	t.Helper()

	var emitted []string
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil { // consume '{'
		t.Fatalf("token: %v", err)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		emitted = append(emitted, tok.(string))
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			t.Fatalf("decode value: %v", err)
		}
		if len(value) > 0 && value[0] == '{' {
			assertSortedKeys(t, value)
		}
	}

	sorted := append([]string(nil), emitted...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(emitted, sorted) {
		t.Fatalf("keys are not emitted in sorted order:\n got %v\nwant %v", emitted, sorted)
	}
}

// TestCanonicalEncodingIsStableAcrossMapOrder is the guard the map-valued
// fields make necessary.
//
// Go's encoding/json sorts map keys, so two observations built by inserting the
// same pairs in different orders must produce identical bytes. If that ever
// stops being true, two clients recording the same catch would sign different
// messages and neither would be able to check the other's work.
func TestCanonicalEncodingIsStableAcrossMapOrder(t *testing.T) {
	forward := Observation{
		Attr: map[string]string{},
		Meas: map[string]int{},
		TS:   "2026-01-01T00:00:00Z",
	}
	backward := Observation{
		Attr: map[string]string{},
		Meas: map[string]int{},
		TS:   "2026-01-01T00:00:00Z",
	}
	attrs := []struct {
		k, v string
	}{{"sex", "M"}, {"stage", "HARD"}, {"gear", "TRAP"}, {"disp", "RELEASED"}}
	meas := []struct {
		k string
		v int
	}{{"cw", 142}, {"wt", 2840}, {"sal", 3120}}

	for i := range attrs {
		forward.Attr[attrs[i].k] = attrs[i].v
		backward.Attr[attrs[len(attrs)-1-i].k] = attrs[len(attrs)-1-i].v
	}
	for i := range meas {
		forward.Meas[meas[i].k] = meas[i].v
		backward.Meas[meas[len(meas)-1-i].k] = meas[len(meas)-1-i].v
	}

	a, b := mustMarshal(t, forward), mustMarshal(t, backward)
	if !bytes.Equal(a, b) {
		t.Fatalf("insertion order changed the signed bytes:\n %s\n %s", a, b)
	}
	if !forward.Equal(backward) {
		t.Fatal("Equal disagrees with the canonical bytes")
	}
}

// A nil map and an empty one are the same report, and a blank answer is the
// same as no answer. Both sides of the wire implement these two rules, so a
// client that omits an empty map still signs bytes the server can rebuild.
func TestCanonicalisationNormalisesAbsence(t *testing.T) {
	empty := Observation{TS: "2026-01-01T00:00:00Z"}
	explicit := Observation{Attr: map[string]string{}, Meas: map[string]int{}, TS: "2026-01-01T00:00:00Z"}
	if !bytes.Equal(mustMarshal(t, empty), mustMarshal(t, explicit)) {
		t.Error("a nil map and an empty map produced different bytes")
	}
	if got := string(mustMarshal(t, empty)); !bytes.Contains([]byte(got), []byte(`"attr":{}`)) {
		t.Errorf("a nil map did not encode as {}: %s", got)
	}

	blank := Observation{Attr: map[string]string{"sex": ""}, TS: "2026-01-01T00:00:00Z"}
	if !bytes.Equal(mustMarshal(t, blank), mustMarshal(t, empty)) {
		t.Error("a blank answer and no answer produced different bytes")
	}
}

// TestNoPayloadFieldIsAFloat guards the other half of the canonical-encoding
// promise. A float in a signed payload is a cross-language signature break
// waiting to happen: Go and JavaScript agree on shortest-round-trip formatting
// today, and depending on that agreement forever is not a good trade against
// storing integers.
func TestNoPayloadFieldIsAFloat(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Observation{}), reflect.TypeOf(Settlement{}),
		reflect.TypeOf(LegacyActivation{}), reflect.TypeOf(LegacyRecapture{}),
	}
	for _, typ := range types {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			kind := f.Type.Kind()
			if kind == reflect.Map {
				kind = f.Type.Elem().Kind()
			}
			switch kind {
			case reflect.Float32, reflect.Float64:
				t.Errorf("%s.%s carries a %s; store a scaled integer instead", typ.Name(), f.Name, kind)
			}
		}
	}
}

func TestAttestationVerifies(t *testing.T) {
	key := mustKey(t)
	fields, _, _ := encodeSample(t, key)
	rec, err := Decode(fields)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := rec.Verify(); err != nil {
		t.Fatalf("a genuine attestation failed to verify: %v", err)
	}
}

func TestAnAttestationFromTheWrongKeyIsRejected(t *testing.T) {
	// This is what makes an activation attributable to a named person rather
	// than to whoever runs the server.
	signer, impostor := mustKey(t), mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())
	fields, err := Encode(tagID, KindActivate, 0, obs, signed(t, signer, obs), impostor.PubKey(), set)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec, _ := Decode(fields)
	if err := rec.Verify(); !errors.Is(err, ErrBadAttestation) {
		t.Fatalf("got %v, want ErrBadAttestation", err)
	}
}

func TestATamperedPayloadBreaksTheAttestation(t *testing.T) {
	key := mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())
	sig := signed(t, key, obs)

	moved := sampleObservation()
	moved.LatE7 = EncodeCoord(40.0)
	fields, err := Encode(tagID, KindActivate, 0, mustMarshal(t, moved), sig, key.PubKey(), set)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec, _ := Decode(fields)
	if err := rec.Verify(); !errors.Is(err, ErrBadAttestation) {
		t.Fatalf("moving the tagging position past the attestation was not detected: %v", err)
	}
}

func TestDecodeRejectsMalformedRecords(t *testing.T) {
	key := mustKey(t)
	good, _, _ := encodeSample(t, key)

	for _, tc := range []struct {
		name   string
		mutate func([][]byte) [][]byte
		want   error
	}{
		{"too few fields", func(f [][]byte) [][]byte { return f[:5] }, ErrNotARecord},
		{"wrong magic", func(f [][]byte) [][]byte { c := clone(f); c[0] = []byte("OTHER"); return c }, ErrNotARecord},
		{"future version", func(f [][]byte) [][]byte { c := clone(f); c[1] = []byte("3"); return c }, ErrBadVersion},
		{"version 1 field count", func(f [][]byte) [][]byte { c := clone(f); c[1] = []byte("1"); return c }, ErrNotARecord},
		{"unknown kind", func(f [][]byte) [][]byte { c := clone(f); c[3] = []byte("XYZ"); return c }, ErrBadKind},
		{"bad generation", func(f [][]byte) [][]byte { c := clone(f); c[4] = []byte("nope"); return c }, ErrNotARecord},
		{"bad public key", func(f [][]byte) [][]byte { c := clone(f); c[7] = []byte{0x01, 0x02}; return c }, ErrBadAttestation},
	} {
		if _, err := Decode(tc.mutate(good)); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestEncodeRefusesIncompleteRecords(t *testing.T) {
	key := mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())
	sig := signed(t, key, obs)
	pub := key.PubKey()

	if _, err := Encode("", KindActivate, 0, obs, sig, pub, set); err == nil {
		t.Error("an empty tag id was accepted")
	}
	if _, err := Encode(tagID, Kind("WAT"), 0, obs, sig, pub, set); !errors.Is(err, ErrBadKind) {
		t.Error("an unknown kind was accepted")
	}
	if _, err := Encode(tagID, KindActivate, 0, nil, sig, pub, set); !errors.Is(err, ErrBadPayload) {
		t.Error("an empty observation was accepted")
	}
	if _, err := Encode(tagID, KindActivate, 0, obs, sig, pub, nil); !errors.Is(err, ErrBadPayload) {
		t.Error("an empty settlement was accepted")
	}
	if _, err := Encode(tagID, KindActivate, 0, obs, nil, pub, set); !errors.Is(err, ErrBadAttestation) {
		t.Error("a missing attestation was accepted")
	}
	if _, err := Encode(tagID, KindActivate, 0, obs, sig, nil, set); !errors.Is(err, ErrBadAttestation) {
		t.Error("a missing attestation key was accepted")
	}
}

// TestV1RecordsStillDecode is the promise that nothing already on chain becomes
// unreadable.
//
// The first deployment wrote eight-field records with one signed blob carrying
// both halves. Those records stay decodable and their attestations stay
// verifiable; only their protocol string moves, which is a separate decision.
// A timestamp format that becomes unreadable the moment it is superseded is not
// much of a timestamp.
func TestV1RecordsStillDecode(t *testing.T) {
	key := mustKey(t)
	legacy := LegacyActivation{
		AccCM: 480, BaseSat: 5000, Batch: "B7", Bio: "02abcd", BonSat: 15000,
		WidthMM: 142, Gear: "TRAP", LatE7: EncodeCoord(32.7765), LonE7: EncodeCoord(-79.9311),
		Molt: "HARD", SalPPT: 3120, Sex: "M", Species: "CALSAP",
		TS: "2026-08-26T14:03:11Z", TempC: 2840,
	}
	payload := mustMarshal(t, legacy)

	// Assemble the eight-field form by hand: nothing writes it any more.
	fields := [][]byte{
		[]byte(Magic), []byte(VersionLegacy), []byte(tagID), []byte(KindActivate), []byte("0"),
		payload, signed(t, key, payload), key.PubKey().Compressed(),
	}
	rec, err := Decode(fields)
	if err != nil {
		t.Fatalf("a version 1 record did not decode: %v", err)
	}
	if rec.Version != VersionLegacy {
		t.Fatalf("version read back as %q", rec.Version)
	}
	if err := rec.Verify(); err != nil {
		t.Fatalf("a version 1 attestation no longer verifies: %v", err)
	}

	obs, set, err := rec.Halves()
	if err != nil {
		t.Fatalf("lift: %v", err)
	}
	if obs.Meas["cw"] != 142 || obs.Attr["stage"] != "HARD" || obs.Sp != "CALSAP" || obs.Obs != "02abcd" {
		t.Errorf("a version 1 activation did not lift correctly: %+v", obs)
	}
	if set.BaseSat != 5000 || set.Batch != "B7" || set.BonSat != 15000 {
		t.Errorf("a version 1 settlement did not lift correctly: %+v", set)
	}

	// A version 1 recapture had no species field at all, which is the gap
	// version 2 closes; lifting one assumes the deployment's default.
	legacyRec := LegacyRecapture{
		AccCM: 620, WidthMM: 149, DaysAt: 97, Disp: "RELEASED", Gear: "TROTLINE",
		LatE7: EncodeCoord(32.81), LonE7: EncodeCoord(-79.85), DistM: 14200,
		PaidSat: 5000, Payee: "02cd", Prev: "ab12", Sex: "M", TS: "2026-12-01T09:00:00Z",
	}
	lifted, err := ObservationFromJSON(mustMarshal(t, legacyRec), KindRecapture)
	if err != nil {
		t.Fatalf("lift recapture: %v", err)
	}
	if lifted.Sp != species.Default || lifted.Disposition() != species.Released {
		t.Errorf("a version 1 recapture did not lift correctly: %+v", lifted)
	}
}

// A stored payload has no version field around it, so the reader distinguishes
// the two eras by shape. This pins that both are read correctly.
func TestStoredPayloadsAreReadInEitherFormat(t *testing.T) {
	current := mustMarshal(t, sampleObservation())
	got, err := ObservationFromJSON(current, KindActivate)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if got.Obs != "02abcd" || got.Meas["cw"] != 142 {
		t.Errorf("a current observation was misread: %+v", got)
	}

	legacy := mustMarshal(t, LegacyActivation{WidthMM: 142, Sex: "M", Molt: "HARD", TS: "2026-01-01T00:00:00Z"})
	got, err = ObservationFromJSON(legacy, KindActivate)
	if err != nil {
		t.Fatalf("legacy: %v", err)
	}
	if got.Meas["cw"] != 142 || got.Attr["sex"] != "M" {
		t.Errorf("a version 1 observation was misread: %+v", got)
	}
}

// TestEveryFieldSurvivesALockingScript is the seam test between this package
// and tagscript: a record has to go into a real script and come back out
// unchanged, including both payloads and the DER signature.
func TestEveryFieldSurvivesALockingScript(t *testing.T) {
	attest, tagKey, dnrKey := mustKey(t), mustKey(t), mustKey(t)
	fields, _, _ := encodeSample(t, attest)

	lock, err := tagscript.Lock(tagKey.PubKey(), dnrKey.PubKey(), fields)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	decoded, err := tagscript.Decode(lock)
	if err != nil {
		t.Fatalf("decode script: %v", err)
	}
	rec, err := Decode(decoded.Fields)
	if err != nil {
		t.Fatalf("decode record: %v", err)
	}
	if err := rec.Verify(); err != nil {
		t.Fatalf("attestation did not survive the script: %v", err)
	}
	obs, set, err := rec.Halves()
	if err != nil {
		t.Fatalf("halves: %v", err)
	}
	if !reflect.DeepEqual(*obs, sampleObservation().Canonical()) {
		t.Fatal("the observation did not survive the script")
	}
	if !reflect.DeepEqual(*set, activationSettlement()) {
		t.Fatal("the settlement did not survive the script")
	}
}

func TestCoordinatesSurviveTheIntegerEncoding(t *testing.T) {
	// A centimetre of error is irrelevant next to a phone's metres of GPS
	// noise, but a systematic drift would not be.
	for _, deg := range []float64{32.7765, -79.9311, 0.0000001, -0.0000001, 89.9999999, -179.9999999} {
		got := DecodeCoord(EncodeCoord(deg))
		if diff := got - deg; diff > 1e-7 || diff < -1e-7 {
			t.Errorf("%.9f round-tripped to %.9f (off by %.2e)", deg, got, diff)
		}
	}
}

func TestAFixSurvivesTheRecord(t *testing.T) {
	o := sampleObservation()
	fix, err := o.Fix()
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	if err := fix.Validate(); err != nil {
		t.Fatalf("the reconstructed fix is invalid: %v", err)
	}
	if got := fix.AccuracyM; got != 4.8 {
		t.Errorf("accuracy round-tripped to %v m, want 4.8", got)
	}
	want, _ := time.Parse(time.RFC3339, o.TS)
	if !fix.At.Equal(want) {
		t.Errorf("timestamp round-tripped to %v, want %v", fix.At, want)
	}
}

func clone(f [][]byte) [][]byte {
	c := make([][]byte, len(f))
	copy(c, f)
	return c
}

// TestAWalletSignatureVerifies is the regression test for a bug that reached a
// live deployment and surfaced as "attestation signature does not verify" the
// first time a finder tried to redeem a tag.
//
// A BRC-100 wallet's createSignature never signs with the identity key. It
// derives a BRC-42 child from (protocol, keyID, counterparty) and signs with
// that. The record carries the identity key, so verifying against it directly
// fails every time -- and nothing catches it in isolation, because both halves
// look correct on their own.
//
// This test signs the way a wallet does and verifies the way the server does.
func TestAWalletSignatureVerifies(t *testing.T) {
	identity := mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())

	// What the wallet does.
	signer, err := AttestationPrivateKey(identity, tagID)
	if err != nil {
		t.Fatalf("derive signing key: %v", err)
	}
	sig, err := signer.Sign(Digest(obs))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// The record names the identity, not the child.
	fields, err := Encode(tagID, KindActivate, 0, obs, sig.Serialize(), identity.PubKey(), set)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec, err := Decode(fields)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := rec.Verify(); err != nil {
		t.Fatalf("a genuine wallet attestation failed to verify: %v", err)
	}
}

// TestSigningWithTheIdentityKeyIsRejected pins the mistake itself, so a future
// "simplification" back to signing with the parent fails loudly here rather
// than in the field.
func TestSigningWithTheIdentityKeyIsRejected(t *testing.T) {
	identity := mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())

	sig, err := identity.Sign(Digest(obs)) // the wrong key
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	fields, _ := Encode(tagID, KindActivate, 0, obs, sig.Serialize(), identity.PubKey(), set)
	rec, _ := Decode(fields)
	if err := rec.Verify(); !errors.Is(err, ErrBadAttestation) {
		t.Fatal("a signature made with the identity key was accepted; the derivation is not being applied")
	}
}

// TestAnyoneCanReDeriveTheAttestationKey is why the counterparty is "anyone"
// and not "self".
//
// The record's whole purpose is to say which tagger or finder stands behind it.
// That is only worth anything if a third party -- a reviewer, an auditor,
// somebody reading the open dataset years later -- can check it with nothing
// but the published record. Anyone's private key is the well-known value 1, so
// the derivation is reproducible from public information alone. Under "self" it
// would depend on a secret only the signer holds, and the attestation would be
// verifiable by nobody but its author.
func TestAnyoneCanReDeriveTheAttestationKey(t *testing.T) {
	identity := mustKey(t)

	// The signer's side: derived from their own private key.
	signer, err := AttestationPrivateKey(identity, tagID)
	if err != nil {
		t.Fatalf("signer derive: %v", err)
	}

	// A stranger's side: derived from the published identity key alone.
	public, err := AttestationKey(identity.PubKey(), tagID)
	if err != nil {
		t.Fatalf("public derive: %v", err)
	}

	if !signer.PubKey().IsEqual(public) {
		t.Fatal("a third party cannot re-derive the attestation key; the record's attribution would be unverifiable")
	}
}

// TestAnAttestationIsBoundToItsTag stops a signature attesting to one animal
// being lifted onto another, which the tag id in the keyID is what prevents.
func TestAnAttestationIsBoundToItsTag(t *testing.T) {
	identity := mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())

	signer, err := AttestationPrivateKey(identity, tagID)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	sig, _ := signer.Sign(Digest(obs))

	// Same signature, same payload, different tag.
	fields, _ := Encode("OTHER99", KindActivate, 0, obs, sig.Serialize(), identity.PubKey(), set)
	rec, _ := Decode(fields)
	if err := rec.Verify(); !errors.Is(err, ErrBadAttestation) {
		t.Fatal("an attestation for one tag verified against another")
	}
}

// TestTheKeyIDMustBeTheCanonicalTagID is the second half of a bug that reached
// a live deployment.
//
// A tag id is displayed grouped -- "JTZ-DQT3" -- and a biologist types it that
// way. ParseID strips the dash, so the record and the server both use the
// canonical "JTZDQT3". The admin console was signing under the string that had
// been typed, which derives a different BRC-42 child, and arming failed with
// "attestation signature does not verify" and nothing on screen to explain it.
//
// The fix is that the server hands the canonical id back and the client signs
// under exactly that. This pins the failure so the shortcut cannot return.
func TestTheKeyIDMustBeTheCanonicalTagID(t *testing.T) {
	identity := mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())

	// Signed under the display form, as a human types it.
	signer, err := AttestationPrivateKey(identity, "K2M-9Q7C")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	sig, _ := signer.Sign(Digest(obs))

	// The record carries the canonical form, which is what the server verifies.
	fields, _ := Encode(tagID, KindActivate, 0, obs, sig.Serialize(), identity.PubKey(), set)
	rec, _ := Decode(fields)
	if err := rec.Verify(); !errors.Is(err, ErrBadAttestation) {
		t.Fatal("an attestation signed under the display form of the tag id was accepted")
	}

	// And the canonical form works.
	signer, _ = AttestationPrivateKey(identity, tagID)
	sig, _ = signer.Sign(Digest(obs))
	fields, _ = Encode(tagID, KindActivate, 0, obs, sig.Serialize(), identity.PubKey(), set)
	rec, _ = Decode(fields)
	if err := rec.Verify(); err != nil {
		t.Fatalf("a correctly signed attestation was refused: %v", err)
	}
}

// TestTheAttestationIsOverTheDigestNotTheDoubleDigest pins the third and
// subtlest of the bugs that broke live redemption and arming.
//
// BRC-100's createSignature applies SHA-256 to whatever is passed as `data`
// before signing. The pages were passing sha256(payload), so the wallet signed
// sha256(sha256(payload)) while the server verified against sha256(payload).
//
// Nothing about either side looked wrong: the wallet made a valid signature,
// the server did a valid verification, and the derived key matched exactly.
// Only the message differed. The pages now pass the payload itself and let the
// wallet hash it once.
func TestTheAttestationIsOverTheDigestNotTheDoubleDigest(t *testing.T) {
	identity := mustKey(t)
	obs, set := mustMarshal(t, sampleObservation()), mustMarshal(t, activationSettlement())

	signer, err := AttestationPrivateKey(identity, tagID)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	// What a wallet handed the payload produces: one hash, then sign.
	good, err := signer.Sign(Digest(obs))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	fields, _ := Encode(tagID, KindActivate, 0, obs, good.Serialize(), identity.PubKey(), set)
	rec, _ := Decode(fields)
	if err := rec.Verify(); err != nil {
		t.Fatalf("a correctly hashed attestation was refused: %v", err)
	}

	// What a wallet handed a digest produces: hashed twice.
	doubled, err := signer.Sign(Digest(Digest(obs)))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	fields, _ = Encode(tagID, KindActivate, 0, obs, doubled.Serialize(), identity.PubKey(), set)
	rec, _ = Decode(fields)
	if err := rec.Verify(); !errors.Is(err, ErrBadAttestation) {
		t.Fatal("a double-hashed attestation was accepted; the signing contract is ambiguous")
	}
}
