// Package record defines what a tag writes into a Bitcoin locking script.
//
// A version 2 record is nine pushed fields inside a tag output's script:
//
//	f0 "WILDTAG"                 magic
//	f1 "2"                      format version
//	f2 tag id                   e.g. "K2M9Q7C"
//	f3 "ACT" | "REC"            what happened
//	f4 generation               "0" for the activation, then one per recapture
//	f5 observation              canonical JSON, see Observation
//	f6 observation signature    DER, over SHA-256 of f5
//	f7 observer identity key    33-byte compressed
//	f8 settlement               canonical JSON, see Settlement
//
// # Why the payload is in two halves
//
// Version 1 put everything in one signed blob, including the amounts paid and
// the outpoint being spent. That was wrong twice over. A finder standing in a
// marsh cannot know what the escrow balance is or which output their report
// will spend, and asking their wallet to sign those numbers means the record
// they attest to is mostly claims made on their behalf. It also makes offline
// capture impossible: none of those values exist until the server is reachable.
//
// So f5 is what a person saw -- position, measurements, what they did with the
// animal -- and it is signed by them and buildable on a phone with no signal.
// f8 is what the programme paid and against which output, added at submission.
//
// f8 carries no signature of its own and does not need one. The transaction is
// already signed by both the tag key and DNR's, and every settlement value is
// independently checkable against the transaction itself: prev is the input,
// paid is output zero. A settlement that disagrees with its own transaction is
// caught by the audit, not by a signature.
//
// The attestation in f6/f7 is separate from the two signatures that unlock the
// output. Those prove the spend was authorised; this proves *who said so* -- the
// biologist's identity key at tagging, the finder's at recapture. Without it a
// record would be attributable only to whoever ran the server.
//
// # Canonical encoding
//
// The observation is signed, so its bytes have to be reproducible by anything
// that wants to check the signature -- including a browser and a phone. Three
// decisions follow:
//
// Struct fields are declared in alphabetical order by JSON tag, so Go's encoder
// emits exactly what a JSON.stringify over sorted keys emits.
//
// The two maps are sorted. Go's encoding/json sorts map keys for us;
// JavaScript's JSON.stringify uses insertion order, so the TypeScript encoder
// in internal/web/static/canonical.js has to sort explicitly. That asymmetry is
// the single most dangerous thing in this format, and web.TestTheAppAndTheServerAgreeOnCanonicalBytes
// exists to catch it.
//
// No field is a float. Coordinates are integer degrees times 1e7, distances are
// whole metres, temperature and salinity are hundredths. Float formatting is
// the classic cross-language signature break: 32.7765 has more than one
// defensible shortest representation, and a signature over the wrong one fails
// in a way that is very hard to debug from a boat.
package record

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/wallet"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
)

// Magic identifies a tag record among everything else on chain.
const Magic = "WILDTAG"

// Version is the format version written today. It is a field rather than an
// assumption because these records are meant to be read years after they were
// written.
const Version = "2"

// VersionLegacy is the single-payload format the first deployment wrote.
// Records in it stay readable; see Decode.
const VersionLegacy = "1"

// FieldCount is how many pushes a well-formed record of each version has.
const (
	FieldCount       = 9
	FieldCountLegacy = 8
)

// Kind is what the record describes.
type Kind string

const (
	// KindActivate is written when a tagger arms a tag and releases the animal.
	KindActivate Kind = "ACT"
	// KindRecapture is written when somebody reports finding one.
	KindRecapture Kind = "REC"
)

// CoordScale converts degrees to the integer form stored on chain. 1e-7 degrees
// is a little over a centimetre, far finer than any phone's fix.
const CoordScale = 1e7

var (
	ErrNotARecord     = errors.New("record: fields are not a tag record")
	ErrBadVersion     = errors.New("record: unsupported format version")
	ErrBadKind        = errors.New("record: unrecognised record kind")
	ErrBadAttestation = errors.New("record: attestation signature does not verify")
	ErrBadPayload     = errors.New("record: malformed payload")
)

// Observation is what a person saw, signed by them.
//
// Everything here is knowable at the moment of the catch, with no server in
// reach: that is the property that makes offline capture possible, and it is
// why the amounts live in Settlement instead.
//
// Field order is alphabetical by JSON tag; see the package comment.
type Observation struct {
	// AccCM is the device's own reported accuracy, in centimetres. It is
	// carried because it is the only honest quality signal available: the chain
	// can prove when this record was written and that it has not changed, but
	// nothing about it proves the device was where it says it was.
	AccCM int `json:"acc"`
	// Attr holds the profile's categorical answers -- sex, stage, gear,
	// disposition. Keys are validated against the species profile, so an
	// unknown one is a rejected report rather than a silent hole in the data.
	Attr  map[string]string `json:"attr"`
	LatE7 int32             `json:"lat"`
	LonE7 int32             `json:"lon"`
	// Meas holds the profile's numbers as scaled integers -- never floats.
	Meas map[string]int `json:"meas"`
	// Name is what the animal is called, or empty. At tagging an empty name
	// hands the naming to whoever finds it; on a report it is set only when
	// this report is the one that names the animal. It lives in the record
	// rather than only in a database because it is the one field a finder will
	// care about years later, and a name that can be quietly edited afterwards
	// is not really the animal's name.
	Name string `json:"name"`
	// Obs is the observer's identity key, compressed hex. It is inside the
	// signed bytes so the record cannot be re-attributed after the fact.
	Obs string `json:"obs"`
	// Sp is the species code. It is on recaptures as well as activations,
	// because a finder reporting a different species from the one tagged is a
	// finding rather than something to reconcile silently.
	Sp string `json:"sp"`
	TS string `json:"ts"` // RFC3339, UTC
}

// Settlement is what the programme paid and against which output.
//
// Added by the server at submission and unsigned; see the package comment for
// why it needs no signature of its own. Zero values mean "not applicable":
// an activation has no payee, a recapture has no batch.
type Settlement struct {
	BaseSat uint64 `json:"base"`  // ACT: satoshis payable on the first report
	Batch   string `json:"batch"` // ACT: print batch, for tracing a bad run of tags
	BonSat  uint64 `json:"bonus"` // ACT: satoshis escrowed for the re-release bonus

	DaysAt    int    `json:"dal"`       // REC: days at large since activation
	EscrowSat uint64 `json:"escrow"`    // REC: satoshis released to the previous reporter
	EscrowFor string `json:"escrowFor"` // REC: identity key that escrow is owed to
	DistM     int    `json:"m"`         // REC: straight-line metres from the tagging fix
	PaidSat   uint64 `json:"paid"`      // REC: satoshis paid to this reporter now
	Payee     string `json:"payee"`     // REC: this reporter's identity key
	Prev      string `json:"prev"`      // REC: txid of the output this spends

	// QueueSec is how long the observation sat in a device's outbox before it
	// reached the server, in seconds. Zero for a report made with signal.
	//
	// It is recorded rather than smoothed away because it is the difference
	// between a fix taken at the moment of the catch and one taken six hours
	// later, and a researcher filtering the dataset has to be able to see which
	// they are looking at. The signed timestamp is still the moment of the
	// catch; this says when the programme heard about it.
	QueueSec int `json:"q"`
}

// Record is a decoded record with its payloads still in bytes, so a caller can
// verify the attestation against exactly what was signed.
type Record struct {
	Version    string
	TagID      string
	Kind       Kind
	Generation uint32
	// Payload is the signed half: an Observation on a version 2 record, and the
	// whole legacy payload on a version 1 one.
	Payload   []byte
	AttestSig []byte
	AttestPub *ec.PublicKey
	// Settled is the unsigned half. Nil on a version 1 record.
	Settled []byte
}

// EncodeCoord converts degrees to the on-chain integer form.
func EncodeCoord(deg float64) int32 {
	// Round half away from zero rather than truncating, so a fix does not drift
	// systematically toward the equator.
	if deg < 0 {
		return int32(deg*CoordScale - 0.5)
	}
	return int32(deg*CoordScale + 0.5)
}

// DecodeCoord converts the on-chain integer form back to degrees.
func DecodeCoord(e7 int32) float64 { return float64(e7) / CoordScale }

// Fix converts an observation's position back into a domain fix.
func (o Observation) Fix() (species.Fix, error) {
	ts, err := time.Parse(time.RFC3339, o.TS)
	if err != nil {
		return species.Fix{}, fmt.Errorf("%w: timestamp: %v", ErrBadPayload, err)
	}
	return species.Fix{
		Lat:       DecodeCoord(o.LatE7),
		Lon:       DecodeCoord(o.LonE7),
		AccuracyM: float64(o.AccCM) / 100,
		At:        ts.UTC(),
	}, nil
}

// Disposition reads what the reporter did with the animal.
func (o Observation) Disposition() species.Disposition {
	return species.Disposition(o.Attr[species.DispositionKey])
}

// Canonical returns the observation with its maps normalised.
//
// Two rules, and both sides of the wire implement them. A nil map encodes as
// {} rather than null, so a client that simply omitted an empty map produces
// the same bytes. An entry whose value is empty is dropped, so "recorded as
// blank" and "not recorded" cannot be two different signatures over the same
// report.
func (o Observation) Canonical() Observation {
	attr := make(map[string]string, len(o.Attr))
	for k, v := range o.Attr {
		if k != "" && v != "" {
			attr[k] = v
		}
	}
	meas := make(map[string]int, len(o.Meas))
	for k, v := range o.Meas {
		if k != "" {
			meas[k] = v
		}
	}
	o.Attr, o.Meas = attr, meas
	return o
}

// Equal compares two observations by their canonical bytes, which is the only
// comparison that matters: two observations are the same report exactly when
// they would produce the same signature.
func (o Observation) Equal(other Observation) bool {
	a, err1 := Marshal(o)
	b, err2 := Marshal(other)
	return err1 == nil && err2 == nil && string(a) == string(b)
}

// MeasureKeys returns the recorded measurement keys in sorted order.
func (o Observation) MeasureKeys() []string {
	out := make([]string, 0, len(o.Meas))
	for k := range o.Meas {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Marshal renders a payload in the canonical form that gets signed.
//
// json.Marshal on a struct emits fields in declaration order, and the payload
// structs declare theirs alphabetically by tag; map keys it sorts itself. So
// this is byte-identical to what a JSON.stringify over recursively sorted keys
// produces on a phone.
func Marshal(payload any) ([]byte, error) {
	if obs, ok := payload.(Observation); ok {
		payload = obs.Canonical()
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("record: marshal payload: %w", err)
	}
	return b, nil
}

// Digest is what an attester signs: SHA-256 of the canonical payload bytes.
func Digest(payload []byte) []byte {
	sum := sha256.Sum256(payload)
	return sum[:]
}

// Encode assembles the nine script fields.
//
// Every field must be non-empty, because an empty push is indistinguishable
// from the end of the record when it is read back -- see tagscript.Lock. Fields
// that can legitimately be absent are carried inside the JSON payloads, where
// absence has a representation.
func Encode(tagID string, kind Kind, generation uint32, observation, attestSig []byte, attestPub *ec.PublicKey, settlement []byte) ([][]byte, error) {
	switch {
	case tagID == "":
		return nil, fmt.Errorf("%w: tag id is required", ErrNotARecord)
	case kind != KindActivate && kind != KindRecapture:
		return nil, fmt.Errorf("%w: %q", ErrBadKind, kind)
	case len(observation) == 0:
		return nil, fmt.Errorf("%w: observation is required", ErrBadPayload)
	case len(settlement) == 0:
		return nil, fmt.Errorf("%w: settlement is required", ErrBadPayload)
	case len(attestSig) == 0:
		return nil, fmt.Errorf("%w: attestation signature is required", ErrBadAttestation)
	case attestPub == nil:
		return nil, fmt.Errorf("%w: attestation public key is required", ErrBadAttestation)
	}
	return [][]byte{
		[]byte(Magic),
		[]byte(Version),
		[]byte(tagID),
		[]byte(kind),
		[]byte(strconv.FormatUint(uint64(generation), 10)),
		observation,
		attestSig,
		attestPub.Compressed(),
		settlement,
	}, nil
}

// Decode reads a record's fields back, in either format version.
//
// It does not verify the attestation; callers that care do that explicitly with
// Verify, because reading a record and trusting it are different decisions and
// the audit command wants to do the first without the second.
func Decode(fields [][]byte) (*Record, error) {
	if len(fields) < 2 {
		return nil, fmt.Errorf("%w: got %d fields", ErrNotARecord, len(fields))
	}
	if string(fields[0]) != Magic {
		return nil, fmt.Errorf("%w: magic is %q", ErrNotARecord, fields[0])
	}

	version := string(fields[1])
	want := FieldCount
	switch version {
	case Version:
	case VersionLegacy:
		want = FieldCountLegacy
	default:
		return nil, fmt.Errorf("%w: %q", ErrBadVersion, version)
	}
	if len(fields) != want {
		return nil, fmt.Errorf("%w: version %s record has %d fields, want %d", ErrNotARecord, version, len(fields), want)
	}

	kind := Kind(fields[3])
	if kind != KindActivate && kind != KindRecapture {
		return nil, fmt.Errorf("%w: %q", ErrBadKind, kind)
	}
	gen, err := strconv.ParseUint(string(fields[4]), 10, 32)
	if err != nil {
		return nil, fmt.Errorf("%w: generation: %v", ErrNotARecord, err)
	}
	pub, err := ec.PublicKeyFromBytes(fields[7])
	if err != nil {
		return nil, fmt.Errorf("%w: attestation public key: %v", ErrBadAttestation, err)
	}

	rec := &Record{
		Version:    version,
		TagID:      string(fields[2]),
		Kind:       kind,
		Generation: uint32(gen),
		Payload:    fields[5],
		AttestSig:  fields[6],
		AttestPub:  pub,
	}
	if version == Version {
		rec.Settled = fields[8]
	}
	return rec, nil
}

// AttestProtocol is the BRC-100 protocol an attestation is signed under.
//
// Security level 2 means the signer's wallet prompts per counterparty, which is
// the right friction for "put my name on this record permanently".
var AttestProtocol = wallet.Protocol{
	SecurityLevel: wallet.SecurityLevelEveryAppAndCounterparty,
	Protocol:      "wildtag observation",
}

// AttestationKey derives the public key that actually signs a record.
//
// This indirection is not optional and getting it wrong is silent. A BRC-100
// wallet's createSignature never signs with the identity key itself: it always
// derives a BRC-42 child from (protocol, keyID, counterparty) and signs with
// that. So a record carrying an identity key in field 7 cannot be verified
// against that key directly -- the signature was made by a descendant of it.
//
// The counterparty is deliberately "anyone" rather than "self". Anyone's
// private key is the publicly known value 1, so any third party holding the
// identity key, the protocol and the tag id can re-derive this exact public key
// and check the signature themselves. Under "self" the derivation depends on a
// secret only the signer holds, and the attestation would be verifiable by
// nobody but its author -- which would defeat the whole reason the record
// carries an identity at all.
//
// The keyID is the tag id, so a signature attesting to one animal cannot be
// lifted onto another.
func AttestationKey(identity *ec.PublicKey, tagID string) (*ec.PublicKey, error) {
	anyonePriv, _ := wallet.AnyoneKey()
	pub, err := wallet.NewKeyDeriver(anyonePriv).DerivePublicKey(
		AttestProtocol, tagID,
		wallet.Counterparty{Type: wallet.CounterpartyTypeOther, Counterparty: identity},
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: derive attestation key: %v", ErrBadAttestation, err)
	}
	return pub, nil
}

// AttestationPrivateKey is the other side of the same derivation, for a signer
// that holds a private key directly rather than going through a wallet.
func AttestationPrivateKey(identity *ec.PrivateKey, tagID string) (*ec.PrivateKey, error) {
	priv, err := wallet.NewKeyDeriver(identity).DerivePrivateKey(
		AttestProtocol, tagID,
		wallet.Counterparty{Type: wallet.CounterpartyTypeAnyone},
	)
	if err != nil {
		return nil, fmt.Errorf("%w: derive attestation key: %v", ErrBadAttestation, err)
	}
	return priv, nil
}

// Verify checks that the attestation signature really was made by the identity
// named in the record, over this payload.
//
// A record that fails this is not evidence of anything: it names a tagger or a
// finder who did not sign it. The audit command treats a failure here as a
// finding, not a parse error.
func (r *Record) Verify() error {
	sig, err := ec.ParseDERSignature(r.AttestSig)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadAttestation, err)
	}
	signer, err := AttestationKey(r.AttestPub, r.TagID)
	if err != nil {
		return err
	}
	if !sig.Verify(Digest(r.Payload), signer) {
		return ErrBadAttestation
	}
	return nil
}

// AttestPubHex is the attesting identity key in the form the rest of the
// application passes identity keys around in.
func (r *Record) AttestPubHex() string { return hex.EncodeToString(r.AttestPub.Compressed()) }

// Observation parses the signed half.
func (r *Record) Observation() (*Observation, error) {
	if r.Version != Version {
		return nil, fmt.Errorf("%w: version %s records carry no observation", ErrBadVersion, r.Version)
	}
	var o Observation
	if err := json.Unmarshal(r.Payload, &o); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	return &o, nil
}

// Settlement parses the unsigned half.
func (r *Record) Settlement() (*Settlement, error) {
	if r.Version != Version {
		return nil, fmt.Errorf("%w: version %s records carry no settlement", ErrBadVersion, r.Version)
	}
	var s Settlement
	if err := json.Unmarshal(r.Settled, &s); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	return &s, nil
}
