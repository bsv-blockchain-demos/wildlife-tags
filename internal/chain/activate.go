package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sdk "github.com/bsv-blockchain/go-sdk/wallet"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagkey"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
)

// ActivateRequest is everything a tagger supplies when arming a tag.
type ActivateRequest struct {
	TagID   tagkey.ID
	Ordinal uint64
	BatchID string

	// Species names the profile every measurement and attribute is checked
	// against. It is recorded explicitly even when a programme runs one
	// species, because a programme that outgrows its first species and finds
	// the field missing has to guess retroactively about every record already
	// written.
	Species string

	Fix species.Fix
	// Meas and Attr are the profile's fields. What may appear here is the
	// species profile's business, not this package's.
	Meas map[string]int
	Attr map[string]string

	// Name is optional. A tagger who skips it leaves the naming to whoever
	// finds the animal, which is usually the better story anyway.
	Name string

	// Attestation is the tagger's signature over the canonical observation,
	// made by their BRC-100 wallet. It is what makes the record attributable to
	// a person rather than to whoever runs the server.
	AttestSig    []byte
	AttestPubHex string
}

// Profile resolves the species profile this request is judged against.
func (r ActivateRequest) Profile() (*species.Profile, error) {
	code := r.Species
	if code == "" {
		code = species.Default
	}
	return species.Get(code)
}

// ActivateResult is what arming a tag produced.
type ActivateResult struct {
	TxID        string
	Vout        uint32
	Satoshis    uint64
	Observation []byte
	Settlement  []byte
	SweepAfter  time.Time
}

// Observation assembles the canonical bytes a tagger signs.
//
// It is separate from Activate because the tagger's device needs exactly these
// bytes to sign before the server is asked to do anything, and a signature over
// bytes the server assembled differently is a signature over nothing.
func (c *Chain) Observation(req ActivateRequest, at time.Time) ([]byte, error) {
	profile, err := req.Profile()
	if err != nil {
		return nil, err
	}
	if err := profile.ValidateTagging(req.Meas, req.Attr); err != nil {
		return nil, err
	}
	if err := req.Fix.Validate(); err != nil {
		return nil, err
	}
	return record.Marshal(record.Observation{
		AccCM: int(req.Fix.AccuracyM * 100),
		Attr:  req.Attr,
		LatE7: record.EncodeCoord(req.Fix.Lat),
		LonE7: record.EncodeCoord(req.Fix.Lon),
		Meas:  req.Meas,
		Name:  req.Name,
		Obs:   req.AttestPubHex,
		Sp:    profile.Code,
		TS:    at.UTC().Format(time.RFC3339),
	})
}

// ActivationSettlement is the unsigned half of an activation record: what the
// programme locked up, and which print run the tag came from.
func (c *Chain) ActivationSettlement(batchID string) ([]byte, error) {
	return record.Marshal(record.Settlement{
		BaseSat: c.cfg.BaseSatoshis,
		Batch:   batchID,
		BonSat:  c.cfg.BonusSatoshis,
	})
}

// Activate arms a tag: it locks the reward and writes the tagging record into
// the same output, so the two cannot be separated afterwards.
//
// This is the simple direction. Nothing custom is being *spent* here, only
// created, so the wallet can sign and process in one step.
func (c *Chain) Activate(ctx context.Context, req ActivateRequest, observation []byte) (*ActivateResult, error) {
	settlement, err := c.ActivationSettlement(req.BatchID)
	if err != nil {
		return nil, err
	}
	rec, err := c.buildActivationRecord(req, observation, settlement)
	if err != nil {
		return nil, err
	}

	tagPub, err := c.tagPubKey(req.Ordinal)
	if err != nil {
		return nil, err
	}
	lock, err := tagscript.Lock(tagPub, c.CoSignPubKey(), rec)
	if err != nil {
		return nil, fmt.Errorf("chain: build tag lock: %w", err)
	}

	no := false
	yes := true
	instructions, err := json.Marshal(tagInstructions{TagID: string(req.TagID), Ordinal: req.Ordinal, Generation: 0})
	if err != nil {
		return nil, fmt.Errorf("chain: encode custom instructions: %w", err)
	}

	res, err := c.Wallet.CreateAction(ctx, sdk.CreateActionArgs{
		Description: fmt.Sprintf("activate tag %s", req.TagID),
		Outputs: []sdk.CreateActionOutput{{
			LockingScript:      lock.Bytes(),
			Satoshis:           c.cfg.TotalReward(),
			OutputDescription:  "tag reward",
			Basket:             TagBasket,
			CustomInstructions: string(instructions),
			Tags:               []string{AppLabel, "act", string(req.TagID)},
		}},
		Labels: []string{AppLabel, "tag:" + string(req.TagID)},
		Options: &sdk.CreateActionOptions{
			SignAndProcess: &yes,
			// The tag output must be at index 0. Randomised outputs would move
			// it, and every later generation locates its parent by index.
			RandomizeOutputs:       &no,
			AcceptDelayedBroadcast: &no,
		},
	}, c.cfg.Originator)
	if err != nil {
		return nil, fmt.Errorf("chain: activate tag %s: %w", req.TagID, err)
	}

	profile, err := req.Profile()
	if err != nil {
		return nil, err
	}
	return &ActivateResult{
		TxID:        res.Txid.String(),
		Vout:        0,
		Satoshis:    c.cfg.TotalReward(),
		Observation: observation,
		Settlement:  settlement,
		SweepAfter:  time.Now().UTC().Add(c.sweepAfter(profile)),
	}, nil
}

// sweepAfter is how long this species' reward stays claimable.
//
// The profile decides, because the answer is a fact about the animal: a blue
// crab sheds its tag at every moult and is gone within eighteen months, while a
// red drum carries one for years. The configured value is the floor, so an
// operator can still hold everything open longer than any profile asks.
func (c *Chain) sweepAfter(p *species.Profile) time.Duration {
	if d := p.SweepAfter(); d > 0 {
		return d
	}
	return c.cfg.SweepAfter
}

// buildActivationRecord validates the request and assembles the script fields.
func (c *Chain) buildActivationRecord(req ActivateRequest, observation, settlement []byte) ([][]byte, error) {
	profile, err := req.Profile()
	if err != nil {
		return nil, err
	}
	if err := profile.ValidateTagging(req.Meas, req.Attr); err != nil {
		return nil, err
	}
	if err := req.Fix.Validate(); err != nil {
		return nil, err
	}
	pub, err := parsePubHex(req.AttestPubHex)
	if err != nil {
		return nil, fmt.Errorf("chain: tagger identity key: %w", err)
	}

	fields, err := record.Encode(string(req.TagID), record.KindActivate, 0, observation, req.AttestSig, pub, settlement)
	if err != nil {
		return nil, fmt.Errorf("chain: encode activation: %w", err)
	}

	// Verify the tagger's attestation before it goes on chain. A record that
	// names a signer who did not sign it is worse than no record: it is
	// evidence pointing at the wrong person, and it would be permanent.
	decoded, err := record.Decode(fields)
	if err != nil {
		return nil, fmt.Errorf("chain: re-read activation: %w", err)
	}
	if err := decoded.Verify(); err != nil {
		return nil, fmt.Errorf("chain: activation attestation: %w", err)
	}
	return fields, nil
}

// tagInstructions travels in the output's CustomInstructions so a tag output
// found in the wallet can be traced back to its tag without consulting the
// application database. The audit uses it to find outputs the database has
// forgotten about.
type tagInstructions struct {
	TagID      string `json:"tag_id"`
	Ordinal    uint64 `json:"ordinal"`
	Generation uint32 `json:"generation"`
}
