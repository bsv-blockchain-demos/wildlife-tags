package record

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
)

// This file is the version 1 format, kept so nothing already on chain becomes
// unreadable. Nothing writes it any more.
//
// Version 1 put the whole record in one signed blob, including amounts the
// signer did not decide and an outpoint they could not know. Version 2 splits
// those out; see the package comment. The structs stay because the audit and
// the provenance page still have to read the first deployment's records, and a
// format that becomes unreadable the moment it is superseded is not much of a
// timestamp.
//
// It was also blue-crab-shaped: carapace width and moult stage were columns
// rather than profile keys. LegacyObservation lifts a v1 payload into the
// species-agnostic form so every reader downstream has one shape to handle.

// LegacyActivation is the version 1 payload written when a tag was armed.
type LegacyActivation struct {
	AccCM   int    `json:"acc"`
	BaseSat uint64 `json:"base"`
	Batch   string `json:"batch"`
	Bio     string `json:"bio"`
	BonSat  uint64 `json:"bonus"`
	WidthMM int    `json:"cw"`
	Gear    string `json:"gear"`
	LatE7   int32  `json:"lat"`
	LonE7   int32  `json:"lon"`
	Molt    string `json:"molt"`
	Name    string `json:"name"`
	SalPPT  int    `json:"sal"`
	Sex     string `json:"sex"`
	Species string `json:"sp"`
	TS      string `json:"ts"`
	TempC   int    `json:"wt"`
}

// LegacyRecapture is the version 1 payload written when a tag was reported.
type LegacyRecapture struct {
	AccCM     int    `json:"acc"`
	WidthMM   int    `json:"cw"`
	DaysAt    int    `json:"dal"`
	Disp      string `json:"disp"`
	EscrowSat uint64 `json:"escrow"`
	EscrowFor string `json:"escrowFor"`
	Gear      string `json:"gear"`
	LatE7     int32  `json:"lat"`
	LonE7     int32  `json:"lon"`
	DistM     int    `json:"m"`
	Name      string `json:"name"`
	PaidSat   uint64 `json:"paid"`
	Payee     string `json:"payee"`
	Prev      string `json:"prev"`
	Sex       string `json:"sex"`
	Sponge    bool   `json:"sponge"`
	TS        string `json:"ts"`
}

// Observation lifts a version 1 activation into the current shape.
func (a LegacyActivation) Observation() Observation {
	sp := a.Species
	if sp == "" {
		sp = species.Default
	}
	meas := map[string]int{"cw": a.WidthMM}
	if a.TempC != 0 {
		meas["wt"] = a.TempC
	}
	if a.SalPPT != 0 {
		meas["sal"] = a.SalPPT
	}
	return Observation{
		AccCM: a.AccCM,
		Attr:  map[string]string{"sex": a.Sex, "stage": a.Molt, "gear": a.Gear},
		LatE7: a.LatE7,
		LonE7: a.LonE7,
		Meas:  meas,
		Name:  a.Name,
		Obs:   a.Bio,
		Sp:    sp,
		TS:    a.TS,
	}
}

// Settlement lifts a version 1 activation's amounts into the current shape.
func (a LegacyActivation) Settlement() Settlement {
	return Settlement{BaseSat: a.BaseSat, Batch: a.Batch, BonSat: a.BonSat}
}

// Observation lifts a version 1 recapture into the current shape.
//
// The species is assumed rather than read: version 1 recaptures did not carry
// one, which is exactly the gap version 2 closes.
func (r LegacyRecapture) Observation() Observation {
	return Observation{
		AccCM: r.AccCM,
		Attr: map[string]string{
			"sex":                  r.Sex,
			"gear":                 r.Gear,
			species.DispositionKey: r.Disp,
		},
		LatE7: r.LatE7,
		LonE7: r.LonE7,
		Meas:  map[string]int{"cw": r.WidthMM},
		Name:  r.Name,
		Obs:   r.Payee,
		Sp:    species.Default,
		TS:    r.TS,
	}
}

// Settlement lifts a version 1 recapture's amounts into the current shape.
func (r LegacyRecapture) Settlement() Settlement {
	return Settlement{
		DaysAt:    r.DaysAt,
		EscrowSat: r.EscrowSat,
		EscrowFor: r.EscrowFor,
		DistM:     r.DistM,
		PaidSat:   r.PaidSat,
		Payee:     r.Payee,
		Prev:      r.Prev,
	}
}

// Halves returns any record's payload in the current two-part shape, lifting a
// version 1 record on the way through.
//
// Every reader downstream goes through this, so nothing but this file has to
// know that version 1 existed.
func (r *Record) Halves() (*Observation, *Settlement, error) {
	if r.Version == Version {
		obs, err := r.Observation()
		if err != nil {
			return nil, nil, err
		}
		set, err := r.Settlement()
		if err != nil {
			return nil, nil, err
		}
		return obs, set, nil
	}

	switch r.Kind {
	case KindActivate:
		var a LegacyActivation
		if err := json.Unmarshal(r.Payload, &a); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		obs, set := a.Observation(), a.Settlement()
		return &obs, &set, nil
	case KindRecapture:
		var c LegacyRecapture
		if err := json.Unmarshal(r.Payload, &c); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		obs, set := c.Observation(), c.Settlement()
		return &obs, &set, nil
	}
	return nil, nil, fmt.Errorf("%w: %q", ErrBadKind, r.Kind)
}

// LegacyFix converts a version 1 timestamp and position into a domain fix.
func legacyFix(latE7, lonE7 int32, accCM int, ts string) (species.Fix, error) {
	at, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return species.Fix{}, fmt.Errorf("%w: timestamp: %v", ErrBadPayload, err)
	}
	return species.Fix{
		Lat:       DecodeCoord(latE7),
		Lon:       DecodeCoord(lonE7),
		AccuracyM: float64(accCM) / 100,
		At:        at.UTC(),
	}, nil
}

// Fix converts a version 1 activation's position into a domain fix.
func (a LegacyActivation) Fix() (species.Fix, error) {
	return legacyFix(a.LatE7, a.LonE7, a.AccCM, a.TS)
}

// Fix converts a version 1 recapture's position into a domain fix.
func (r LegacyRecapture) Fix() (species.Fix, error) {
	return legacyFix(r.LatE7, r.LonE7, r.AccCM, r.TS)
}

// ObservationFromJSON reads a stored payload in either format version.
//
// The application database keeps the payload without the surrounding script, so
// there is no version field to dispatch on. Version 2 observations are the ones
// carrying "obs"; nothing in version 1 had that key, which is what makes the
// probe safe rather than a guess.
func ObservationFromJSON(payload []byte, kind Kind) (*Observation, error) {
	if len(payload) == 0 {
		return nil, fmt.Errorf("%w: empty payload", ErrBadPayload)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
	}
	if _, current := probe["obs"]; current {
		var o Observation
		if err := json.Unmarshal(payload, &o); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		return &o, nil
	}

	switch kind {
	case KindActivate:
		var a LegacyActivation
		if err := json.Unmarshal(payload, &a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		o := a.Observation()
		return &o, nil
	case KindRecapture:
		var c LegacyRecapture
		if err := json.Unmarshal(payload, &c); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		o := c.Observation()
		return &o, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrBadKind, kind)
}

// SettlementFromJSON reads the unsigned half.
//
// settlement is what the current format stores separately; payload is the
// version 1 blob that carried both halves together. Passing both lets a caller
// hand over whatever the database has without knowing which era it came from.
func SettlementFromJSON(settlement, payload []byte, kind Kind) (Settlement, error) {
	if len(settlement) > 0 {
		var s Settlement
		if err := json.Unmarshal(settlement, &s); err != nil {
			return Settlement{}, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		return s, nil
	}
	if len(payload) == 0 {
		return Settlement{}, nil
	}

	switch kind {
	case KindActivate:
		var a LegacyActivation
		if err := json.Unmarshal(payload, &a); err != nil {
			return Settlement{}, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		return a.Settlement(), nil
	case KindRecapture:
		var c LegacyRecapture
		if err := json.Unmarshal(payload, &c); err != nil {
			return Settlement{}, fmt.Errorf("%w: %v", ErrBadPayload, err)
		}
		return c.Settlement(), nil
	}
	return Settlement{}, fmt.Errorf("%w: %q", ErrBadKind, kind)
}
