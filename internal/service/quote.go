package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
)

// RecaptureQuote is what a finder is offered, plus the exact bytes their wallet
// is asked to sign.
type RecaptureQuote struct {
	TagID   string `json:"tag_id"`
	Species string `json:"species"`

	// ObservationHex is canonical and must be signed byte for byte. The server
	// rebuilds nothing: a signature over bytes assembled differently is a
	// signature over nothing.
	ObservationHex string `json:"observation"`

	PayoutSats uint64 `json:"payout_satoshis"`
	// BonusSats is what this finder stands to earn later by putting the animal
	// back -- held until the tag is reported again, because that is the only
	// thing that can corroborate a release.
	BonusSats uint64 `json:"bonus_satoshis"`
	// EscrowReleaseSats is a bonus owed to the *previous* reporter that this
	// report will release. It is shown because it is part of what the finder's
	// report accomplishes, not part of what they are paid.
	EscrowReleaseSats uint64 `json:"escrow_release_satoshis"`

	MustRelease       bool   `json:"must_release"`
	MustReleaseReason string `json:"must_release_reason,omitempty"`

	// AnimalName is what the animal is already called, and CanName says whether
	// this finder gets to choose one. An animal is named once, by whoever gets
	// there first, and never renamed.
	AnimalName string `json:"animal_name"`
	CanName    bool   `json:"can_name"`

	Provenance *Provenance `json:"provenance"`
}

// RecaptureDetails is the finder's report before it is signed.
type RecaptureDetails struct {
	Lat, Lon    float64
	AccuracyM   float64
	Meas        map[string]int
	Attr        map[string]string
	PayeePubHex string

	// Name is only honoured when the animal has no name yet.
	Name string
}

// QuoteRecapture assembles the canonical record a finder's wallet will sign,
// alongside what they are being offered and the animal's history.
//
// The history is not decoration. SCDNR's own account of running a tagging
// programme is that many people who report a tag are more interested in the
// animal's story than the reward, and nobody currently gives it to them.
func (s *Service) QuoteRecapture(ctx context.Context, tagID string, in RecaptureDetails) (*RecaptureQuote, error) {
	tag, err := s.store.GetTag(ctx, tagID)
	if err != nil {
		return nil, err
	}
	switch tag.Status {
	case store.StatusActive, store.StatusRedeeming:
	case store.StatusCooldown:
		return nil, fmt.Errorf("%w: it can be reported again after %s", ErrCooldown, formatWhen(tag.CooldownUntil))
	default:
		return nil, fmt.Errorf("%w: it is %s", ErrTagNotActive, tag.Status)
	}

	profile, err := s.profileFor(tag)
	if err != nil {
		return nil, err
	}
	if err := profile.ValidateReport(in.Meas, in.Attr); err != nil {
		return nil, err
	}
	fix := species.Fix{Lat: in.Lat, Lon: in.Lon, AccuracyM: in.AccuracyM, At: s.now()}
	if err := fix.Validate(); err != nil {
		return nil, err
	}

	prov, err := s.Provenance(ctx, tagID)
	if err != nil {
		return nil, err
	}

	cfg := s.chain.Config()
	var escrowSats uint64
	if escrow, eerr := s.store.PendingEscrow(ctx, tag.TagID, tag.Generation); eerr == nil {
		escrowSats = escrow.Satoshis
	} else if !errors.Is(eerr, store.ErrNotFound) {
		return nil, eerr
	}

	disp, err := profile.Disposition(in.Attr)
	if err != nil {
		return nil, err
	}
	nextEscrow := uint64(0)
	if disp == species.Released {
		nextEscrow = cfg.BonusSatoshis
	}

	// Only the report that actually names the animal carries a name; every
	// later one leaves it empty, so the record says who named it and when.
	name := ""
	if tag.AnimalName == "" {
		name = species.NormalizeName(in.Name)
		if err := species.ValidateName(name); err != nil {
			return nil, err
		}
	}

	observation, err := record.Marshal(record.Observation{
		AccCM: int(in.AccuracyM * 100),
		Attr:  in.Attr,
		LatE7: record.EncodeCoord(in.Lat),
		LonE7: record.EncodeCoord(in.Lon),
		Meas:  in.Meas,
		Name:  name,
		Obs:   in.PayeePubHex,
		Sp:    profile.Code,
		TS:    s.now().Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}

	must, why := profile.MustReleaseNow(in.Meas, in.Attr)
	return &RecaptureQuote{
		TagID:             tag.TagID,
		Species:           profile.Code,
		ObservationHex:    hexOf(observation),
		PayoutSats:        cfg.BaseSatoshis,
		BonusSats:         nextEscrow,
		EscrowReleaseSats: escrowSats,
		MustRelease:       must,
		MustReleaseReason: why,
		AnimalName:        tag.AnimalName,
		CanName:           tag.AnimalName == "",
		Provenance:        prov,
	}, nil
}

// profileFor resolves the species a tag was armed for.
//
// A tag armed before the species column existed reports nothing, and the
// default profile is the honest answer there: the first deployment tagged blue
// crabs and only blue crabs.
func (s *Service) profileFor(tag *store.Tag) (*species.Profile, error) {
	code := tag.Species
	if code == "" {
		code = species.Default
	}
	return species.Get(code)
}

// settlementFor assembles the unsigned half of a recapture record: what the
// programme is paying, against which output, and the two derived numbers the
// receipt leads with.
func (s *Service) settlementFor(prov *Provenance, tag *store.Tag, fix species.Fix, payee string, escrowSats uint64, escrowFor string, queueSec int) record.Settlement {
	set := record.Settlement{
		EscrowSat: escrowSats,
		EscrowFor: escrowFor,
		PaidSat:   s.chain.Config().BaseSatoshis,
		Payee:     payee,
		Prev:      tag.LiveTxID,
		QueueSec:  queueSec,
	}
	if prov != nil && prov.TaggedAt != nil {
		set.DaysAt = species.DaysAtLarge(*prov.TaggedAt, fix.At)
		set.DistM = int(species.DistanceKM(species.Fix{Lat: prov.TaggedLat, Lon: prov.TaggedLon}, fix) * 1000)
	}
	return set
}
