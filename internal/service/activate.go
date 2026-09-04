package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/chain"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
)

// ActivationInput is a tagger's field report.
type ActivationInput struct {
	TagID tagkey.ID

	// Species selects the profile. Empty means the deployment's default, which
	// is what a single-species programme's clients will send.
	Species string

	Lat, Lon  float64
	AccuracyM float64

	// Meas and Attr are the profile's fields, as scaled integers and vocabulary
	// codes. What may appear here is the profile's business.
	Meas map[string]int
	Attr map[string]string

	// Name is optional at tagging. Leaving it empty is not an oversight -- it
	// hands the naming to whoever finds the animal, which is the part of this
	// they tend to remember.
	Name string

	// AttestPubHex is the tagger's identity key. It is required from the first
	// step, because it goes inside the bytes they are asked to sign.
	AttestPubHex string

	// Observation and AttestSig come back from the tagger's wallet on the
	// second step. The server does not rebuild the observation from the fields
	// above -- it checks that the two agree, and refuses if they do not.
	Observation []byte
	AttestSig   []byte
}

// Profile resolves the species profile this input is judged against.
func (in ActivationInput) Profile() (*species.Profile, error) {
	code := in.Species
	if code == "" {
		code = species.Default
	}
	return species.Get(code)
}

// ActivationPreview is what a client needs in order to sign.
type ActivationPreview struct {
	Observation []byte    `json:"-"`
	At          time.Time `json:"at"`
	Species     string    `json:"species"`
	BaseSats    uint64    `json:"base_satoshis"`
	BonusSats   uint64    `json:"bonus_satoshis"`
	TotalSats   uint64    `json:"total_satoshis"`
}

// PrepareActivation assembles the canonical bytes a tagger signs.
//
// Two round trips rather than one, because the record is signed by a wallet
// that will only sign bytes it has been handed. The alternative -- letting the
// server sign on the tagger's behalf -- would make every activation
// attributable to the server operator and nobody else, which is exactly the
// attribution the on-chain record exists to provide.
//
// AttestPubHex is required here and not only at submission, because the record
// names the tagger inside the bytes they are about to sign. A caller that
// supplied it only at the second step would be signing a record with an empty
// observer field and submitting one with it filled in -- two different records,
// and the second would be rejected as a mismatch.
func (s *Service) PrepareActivation(ctx context.Context, in ActivationInput) (*ActivationPreview, error) {
	if in.AttestPubHex == "" {
		return nil, fmt.Errorf("%w: the tagger's identity key is part of the record being signed", ErrPayloadMismatch)
	}
	profile, err := in.Profile()
	if err != nil {
		return nil, err
	}
	if err := species.ValidateName(in.Name); err != nil {
		return nil, err
	}
	tag, err := s.store.GetTag(ctx, string(in.TagID))
	if err != nil {
		return nil, err
	}
	if tag.Status != store.StatusMinted {
		return nil, fmt.Errorf("service: tag %s is already %s", in.TagID, tag.Status)
	}

	at := s.now()
	observation, err := s.chain.Observation(s.activateRequest(in, tag), at)
	if err != nil {
		return nil, err
	}
	cfg := s.chain.Config()
	return &ActivationPreview{
		Observation: observation,
		At:          at,
		Species:     profile.Code,
		BaseSats:    cfg.BaseSatoshis,
		BonusSats:   cfg.BonusSatoshis,
		TotalSats:   cfg.TotalReward(),
	}, nil
}

// Activate arms a tag: it locks the reward and writes the tagging record into
// the same output.
func (s *Service) Activate(ctx context.Context, in ActivationInput) (*chain.ActivateResult, error) {
	profile, err := in.Profile()
	if err != nil {
		return nil, err
	}
	tag, err := s.store.GetTag(ctx, string(in.TagID))
	if err != nil {
		return nil, err
	}
	if tag.Status != store.StatusMinted {
		return nil, fmt.Errorf("service: tag %s is already %s", in.TagID, tag.Status)
	}
	if err := s.checkObservation(in, tag); err != nil {
		return nil, err
	}

	req := s.activateRequest(in, tag)
	settlement, err := s.chain.ActivationSettlement(tag.BatchID)
	if err != nil {
		return nil, err
	}

	// Write-ahead. Signing is broadcasting: past the wallet call the
	// transaction may exist whatever happens here, so the record has to exist
	// first or a crash leaves an armed tag nobody knows about.
	eventID, err := s.store.BeginEvent(ctx, store.Event{
		TagID:          tag.TagID,
		Generation:     0,
		Kind:           string(record.KindActivate),
		PayloadJSON:    string(in.Observation),
		SettlementJSON: string(settlement),
		AttestPubKey:   in.AttestPubHex,
	})
	if err != nil {
		return nil, fmt.Errorf("service: record activation attempt: %w", err)
	}

	res, err := s.chain.Activate(ctx, req, in.Observation)
	if err != nil {
		// The activation path is single-step, so a failure here means the
		// wallet never got as far as broadcasting.
		if rerr := s.store.RetractEvent(ctx, eventID); rerr != nil {
			s.logger.Warn("could not retract a failed activation record", "tag", tag.TagID, "error", rerr)
		}
		return nil, err
	}

	if err := s.store.BroadcastEvent(ctx, eventID, res.TxID, res.Vout, res.Satoshis); err != nil {
		// The transaction is on the network. Losing the local record is bad,
		// but pretending the activation failed would be worse: the money is
		// locked either way, and the audit can find the output from the chain.
		s.logger.Error("activation broadcast but not recorded", "tag", tag.TagID, "txid", res.TxID, "error", err)
	}
	if name := species.NormalizeName(in.Name); name != "" {
		if err := s.store.NameAnimal(ctx, tag.TagID, name, in.AttestPubHex); err != nil {
			s.logger.Warn("armed a tag but could not record the animal's name", "tag", tag.TagID, "error", err)
		}
	}
	if err := s.store.ActivateTag(ctx, tag.TagID, profile.Code, res.TxID, res.Vout, res.Satoshis, res.SweepAfter); err != nil {
		s.logger.Error("activation broadcast but tag not updated", "tag", tag.TagID, "txid", res.TxID, "error", err)
		return res, fmt.Errorf("service: tag %s was armed on chain (%s) but the database was not updated: %w", tag.TagID, res.TxID, err)
	}
	if err := s.store.Audit(ctx, in.AttestPubHex, "tag.activate",
		fmt.Sprintf("%s (%s) at %s", tag.TagID, profile.Code, res.TxID)); err != nil {
		s.logger.Warn("could not record activation in the audit log", "tag", tag.TagID, "error", err)
	}
	return res, nil
}

func (s *Service) activateRequest(in ActivationInput, tag *store.Tag) chain.ActivateRequest {
	return chain.ActivateRequest{
		TagID:   tagkey.ID(tag.TagID),
		Ordinal: tag.Ordinal,
		BatchID: tag.BatchID,
		Species: in.Species,
		Fix: species.Fix{
			Lat:       in.Lat,
			Lon:       in.Lon,
			AccuracyM: in.AccuracyM,
			At:        s.now(),
		},
		Meas:         in.Meas,
		Attr:         in.Attr,
		Name:         species.NormalizeName(in.Name),
		AttestSig:    in.AttestSig,
		AttestPubHex: in.AttestPubHex,
	}
}

// checkObservation confirms the signed bytes describe the reported animal.
//
// Without this a tagger's wallet could sign one record while the database
// stored another, and the on-chain attestation would be evidence for a claim
// nobody made. The observation's own timestamp is trusted because it is inside
// the signed bytes; everything else has to match the form.
func (s *Service) checkObservation(in ActivationInput, tag *store.Tag) error {
	profile, err := in.Profile()
	if err != nil {
		return err
	}
	var got record.Observation
	if err := json.Unmarshal(in.Observation, &got); err != nil {
		return fmt.Errorf("%w: unparseable observation: %v", ErrPayloadMismatch, err)
	}

	want := record.Observation{
		AccCM: int(in.AccuracyM * 100),
		Attr:  in.Attr,
		LatE7: record.EncodeCoord(in.Lat),
		LonE7: record.EncodeCoord(in.Lon),
		Meas:  in.Meas,
		Name:  species.NormalizeName(in.Name),
		Obs:   in.AttestPubHex,
		Sp:    profile.Code,
		// TS is the server's own value, carried inside the signed bytes: the
		// tagger signed the moment the server stamped, and its freshness is
		// checked separately below.
		TS: got.TS,
	}
	if !got.Equal(want) {
		return fmt.Errorf("%w: the signed record does not match the submitted form", ErrPayloadMismatch)
	}
	_ = tag
	return s.checkFreshness(got.TS)
}

// checkFreshness bounds how far a signed record's own timestamp may be from now.
//
// The timestamp is inside the signed bytes, so it cannot be edited after
// signing -- but it can be stale. A form left open on a boat for an hour would
// anchor a position fix to the wrong moment, and the fix is the entire
// scientific value of the record.
func (s *Service) checkFreshness(ts string) error {
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return fmt.Errorf("%w: unparseable timestamp", ErrPayloadMismatch)
	}
	if age := s.now().Sub(at); age > maxRecordAge || age < -maxClockSkew {
		return fmt.Errorf("%w: the signed record is %s old; capture a fresh position fix",
			ErrPayloadMismatch, age.Round(time.Second))
	}
	return nil
}

// queueDelay is how long an observation waited before it reached the server,
// rounded to whole seconds and never negative.
func (s *Service) queueDelay(ts string) int {
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0
	}
	d := s.now().Sub(at)
	if d < 0 {
		return 0
	}
	return int(d.Seconds())
}

// maxRecordAge and maxClockSkew bound how far a signed record's own timestamp
// may be from now.
//
// A day, not the fifteen minutes the first version allowed, because a report
// captured in a marsh with no signal is queued on the device and submitted when
// the boat comes back in. Refusing those would refuse exactly the reports the
// offline path exists to collect. The delay is not smoothed away: it is
// recorded in the settlement as QueueSec, so a researcher can tell a fix taken
// at the moment of the catch from one submitted six hours later.
//
// The negative bound is generous because phone clocks drift and a finder cannot
// be asked to fix theirs.
const (
	maxRecordAge = 24 * time.Hour
	maxClockSkew = 2 * time.Minute
)

// SelfAttest signs a payload with a key this process holds.
//
// It exists for the command line and for tests. In the field the attestation
// comes from the observer's own BRC-100 wallet, which is the point -- a record
// signed by the server names the server operator and nobody else. A record
// attested this way is honestly labelled: the identity key in it is the
// wallet's, so anyone reading the dataset can see which activations were made
// by an operator rather than by a named person.
func SelfAttest(payload []byte, key *ec.PrivateKey, tagID string) ([]byte, string, error) {
	// Sign with the same BRC-42 child a wallet would use, not with the identity
	// key itself. A wallet's createSignature always derives; signing with the
	// parent here would produce a signature that verifies nowhere.
	signer, err := record.AttestationPrivateKey(key, tagID)
	if err != nil {
		return nil, "", fmt.Errorf("service: self-attest: %w", err)
	}
	sig, err := signer.Sign(record.Digest(payload))
	if err != nil {
		return nil, "", fmt.Errorf("service: self-attest: %w", err)
	}
	// The record still names the identity key. The signature is by its child,
	// which anyone can re-derive from it.
	return sig.Serialize(), hex.EncodeToString(key.PubKey().Compressed()), nil
}
