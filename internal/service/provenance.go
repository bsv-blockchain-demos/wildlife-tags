package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/store"
)

// Provenance is an animal's whole story, assembled for the receipt a finder
// sees the moment they are paid.
//
// This is the feature SCDNR's own writeup on running a tagging programme points
// at: many people who report a tag turn out to be more interested in where the
// animal came from and how far it moved than in the reward, and one angler's
// explanation was simply that understanding his prey made him better at
// catching it. Nobody currently gives them that, and the on-chain record makes
// it free to give.
type Provenance struct {
	TagID string `json:"tag_id"`

	// Species, Common and Scientific identify the animal for a reader who has
	// never heard of a species code.
	Species    string `json:"species"`
	Common     string `json:"common_name"`
	Scientific string `json:"scientific_name"`

	// Name is what the animal is called, and NamedBy/NamedAt say who chose it
	// and when. Empty means nobody has yet, which is an invitation.
	Name    string     `json:"name"`
	NamedBy string     `json:"named_by,omitempty"`
	NamedAt *time.Time `json:"named_at,omitempty"`

	TaggedAt  *time.Time `json:"tagged_at"`
	TaggedLat float64    `json:"tagged_lat"`
	TaggedLon float64    `json:"tagged_lon"`
	TaggedTx  string     `json:"tagged_txid"`

	// TaggedMeas and TaggedAttr are the profile's fields as recorded at
	// tagging, and Facts is the same thing rendered for a human.
	TaggedMeas map[string]int    `json:"tagged_meas"`
	TaggedAttr map[string]string `json:"tagged_attr"`
	Facts      []Fact            `json:"facts"`

	// PrimarySize is the measurement the receipt leads with -- carapace width
	// for a crab, total length for a fish -- with its unit, so a page does not
	// have to guess which of several numbers is the size.
	PrimaryKey   string `json:"primary_key"`
	PrimaryLabel string `json:"primary_label"`
	PrimaryUnit  string `json:"primary_unit"`
	PrimaryScale int    `json:"primary_scale"`
	PrimaryAt    int    `json:"primary_at_tagging"`

	TaggerKey string `json:"tagger_key"`
	BatchID   string `json:"batch_id"`

	// Recaptures is every previous report, oldest first. A tag on its third
	// journey has a genuinely interesting track.
	Recaptures []RecaptureSummary `json:"recaptures"`

	// DaysAtLarge and DistanceM are measured to the most recent known point.
	DaysAtLarge int `json:"days_at_large"`
	DistanceM   int `json:"distance_m"`

	// TotalPathM is the sum of every leg, which for an animal caught more than
	// once is further than the straight line from where it started. It is still
	// a floor on how far it actually travelled: between two sightings it may
	// have gone anywhere.
	TotalPathM int `json:"total_path_m"`
	// Growth is the change in the primary measurement since tagging. For a
	// crab it is normally zero -- a blue crab grows only by moulting, and it
	// sheds the tag when it does, so a non-zero value is a measurement worth
	// looking at rather than a fact about the animal. For a fish it is the
	// point of the programme, which is what GrowthExpected on the profile says.
	Growth         int  `json:"growth"`
	GrowthExpected bool `json:"growth_expected"`

	Generation uint32 `json:"generation"`
	Status     string `json:"status"`
}

// Fact is one recorded field, rendered for a human.
type Fact struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// RecaptureSummary is one previous report.
type RecaptureSummary struct {
	At          time.Time         `json:"at"`
	Lat         float64           `json:"lat"`
	Lon         float64           `json:"lon"`
	Meas        map[string]int    `json:"meas"`
	Attr        map[string]string `json:"attr"`
	PrimaryAt   int               `json:"primary"`
	Disposition string            `json:"disposition"`
	DaysAtLarge int               `json:"days_at_large"`
	DistanceM   int               `json:"distance_m"`
	TxID        string            `json:"txid"`
	Proven      bool              `json:"proven"`
	PaidSats    uint64            `json:"paid_satoshis"`
}

// Provenance assembles a tag's history from its recorded events.
func (s *Service) Provenance(ctx context.Context, tagID string) (*Provenance, error) {
	tag, err := s.store.GetTag(ctx, tagID)
	if err != nil {
		return nil, err
	}
	events, err := s.store.EventsForTag(ctx, tagID)
	if err != nil {
		return nil, err
	}
	profile, err := s.profileFor(tag)
	if err != nil {
		return nil, err
	}
	primary, _ := profile.Measure(profile.PrimaryMeasure)

	prov := &Provenance{
		TagID:          tag.TagID,
		Species:        profile.Code,
		Common:         profile.Common,
		Scientific:     profile.Scientific,
		BatchID:        tag.BatchID,
		Generation:     tag.Generation,
		Status:         string(tag.Status),
		PrimaryKey:     primary.Key,
		PrimaryLabel:   primary.Label,
		PrimaryUnit:    primary.Unit,
		PrimaryScale:   primary.Scale,
		GrowthExpected: profile.GrowthExpected,
	}

	var taggedFix species.Fix
	for _, ev := range events {
		if ev.Status == store.EventFailed || ev.PayloadJSON == "" {
			continue
		}
		obs, err := observationFromEvent(ev)
		if err != nil {
			return nil, fmt.Errorf("service: decode %s record for %s: %w", ev.Kind, tagID, err)
		}
		fix, err := obs.Fix()
		if err != nil {
			return nil, fmt.Errorf("service: %s fix for %s: %w", ev.Kind, tagID, err)
		}

		// The first record carrying a name is the one that named the animal,
		// whatever the database cache happens to say.
		if obs.Name != "" && prov.Name == "" {
			at := fix.At
			prov.Name, prov.NamedBy, prov.NamedAt = obs.Name, obs.Obs, &at
		}

		switch ev.Kind {
		case string(record.KindActivate):
			taggedFix = fix
			at := fix.At
			prov.TaggedAt = &at
			prov.TaggedLat, prov.TaggedLon = fix.Lat, fix.Lon
			prov.TaggedTx = ev.TxID
			prov.TaggedMeas, prov.TaggedAttr = obs.Meas, obs.Attr
			prov.TaggerKey = obs.Obs
			prov.PrimaryAt = obs.Meas[profile.PrimaryMeasure]
			prov.Facts = facts(profile, obs.Meas, obs.Attr)
			if batch := batchFromEvent(ev); batch != "" {
				prov.BatchID = batch
			}

		case string(record.KindRecapture):
			set := settlementFromEvent(ev)
			prov.Recaptures = append(prov.Recaptures, RecaptureSummary{
				At:          fix.At,
				Lat:         fix.Lat,
				Lon:         fix.Lon,
				Meas:        obs.Meas,
				Attr:        obs.Attr,
				PrimaryAt:   obs.Meas[profile.PrimaryMeasure],
				Disposition: string(obs.Disposition()),
				DaysAtLarge: set.DaysAt,
				DistanceM:   set.DistM,
				TxID:        ev.TxID,
				// "Proven" means a merkle proof has been verified against
				// locally held headers -- not merely that the transaction was
				// accepted. The distinction is the whole timestamp claim.
				Proven:   ev.Status == store.EventMined,
				PaidSats: ev.Satoshis,
			})
		}
	}

	// Fall back to the cache only if no record carried a name, which happens
	// while a naming report is still in flight.
	if prov.Name == "" {
		prov.Name, prov.NamedBy = tag.AnimalName, tag.NamedBy
	}

	// Total path: every leg, tagging to first sighting to second and so on.
	prev := species.Fix{Lat: prov.TaggedLat, Lon: prov.TaggedLon}
	for _, r := range prov.Recaptures {
		here := species.Fix{Lat: r.Lat, Lon: r.Lon}
		prov.TotalPathM += int(species.DistanceKM(prev, here) * 1000)
		prev = here
	}
	if n := len(prov.Recaptures); n > 0 && prov.PrimaryAt > 0 {
		prov.Growth = species.Growth(prov.PrimaryAt, prov.Recaptures[n-1].PrimaryAt)
	}

	// Measure to the most recent known point, which is the last recapture if
	// there is one and "now" if the animal is still out there.
	if prov.TaggedAt != nil {
		last := species.Fix{Lat: prov.TaggedLat, Lon: prov.TaggedLon}
		at := s.now()
		if n := len(prov.Recaptures); n > 0 {
			r := prov.Recaptures[n-1]
			last = species.Fix{Lat: r.Lat, Lon: r.Lon}
			at = r.At
		}
		prov.DaysAtLarge = species.DaysAtLarge(*prov.TaggedAt, at)
		prov.DistanceM = int(species.DistanceKM(taggedFix, last) * 1000)
	}

	prov.normalise()
	return prov, nil
}

// normalise makes every collection non-nil.
//
// Go marshals a nil slice as `null` and an empty one as `[]`, and a field that
// is sometimes one and sometimes the other is a trap for every client that ever
// consumes it. A typed client is not protected: its type says the field is an
// array, the compiler agrees, and the first tag anybody scans -- one that has
// never been reported, so `recaptures` is nil -- crashes on `.length`.
//
// That is not hypothetical. It crashed the Android app on the single most
// common path in the whole application: a finder scanning a freshly armed tag.
//
// The honest fix is at the source. "No recaptures" is an empty list, not the
// absence of a list, and saying so once here is worth more than a guard in
// every consumer that will ever exist.
func (p *Provenance) normalise() {
	if p.Recaptures == nil {
		p.Recaptures = []RecaptureSummary{}
	}
	if p.Facts == nil {
		p.Facts = []Fact{}
	}
	if p.TaggedMeas == nil {
		p.TaggedMeas = map[string]int{}
	}
	if p.TaggedAttr == nil {
		p.TaggedAttr = map[string]string{}
	}
	for i := range p.Recaptures {
		if p.Recaptures[i].Meas == nil {
			p.Recaptures[i].Meas = map[string]int{}
		}
		if p.Recaptures[i].Attr == nil {
			p.Recaptures[i].Attr = map[string]string{}
		}
	}
}

// facts renders an observation's fields for a human, in a stable order.
func facts(p *species.Profile, meas map[string]int, attr map[string]string) []Fact {
	out := make([]Fact, 0, len(meas)+len(attr))
	for _, m := range p.Measures {
		v, ok := meas[m.Key]
		if !ok {
			continue
		}
		value := fmt.Sprintf("%d %s", v, m.Unit)
		if m.Scale > 1 {
			value = fmt.Sprintf("%.2f %s", float64(v)/float64(m.Scale), m.Unit)
		}
		out = append(out, Fact{Key: m.Key, Label: m.Label, Value: value})
	}
	for _, v := range p.Vocabs {
		code, ok := attr[v.Key]
		if !ok {
			continue
		}
		out = append(out, Fact{Key: v.Key, Label: v.Label, Value: v.Label_(code)})
	}

	// Anything the profile does not describe still gets shown rather than
	// dropped: it is in the signed record either way, and a page that quietly
	// hides part of what was attested is worse than one that shows a raw key.
	var extra []string
	for k := range meas {
		if _, known := p.Measure(k); !known {
			extra = append(extra, k)
		}
	}
	for k := range attr {
		if _, known := p.Vocab(k); !known {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		if v, ok := meas[k]; ok {
			out = append(out, Fact{Key: k, Label: k, Value: fmt.Sprintf("%d", v)})
			continue
		}
		out = append(out, Fact{Key: k, Label: k, Value: attr[k]})
	}
	return out
}

// observationFromEvent reads the signed half of a stored record.
//
// The database keeps only the payloads, not the whole script, so version 1
// events are distinguished by shape rather than by a version field. See
// record.ObservationFromJSON.
func observationFromEvent(ev store.Event) (*record.Observation, error) {
	return record.ObservationFromJSON([]byte(ev.PayloadJSON), record.Kind(ev.Kind))
}

// settlementFromEvent reads the unsigned half, which version 1 records carried
// inside their signed payload and current ones store beside it.
//
// A settlement that will not parse is treated as absent rather than fatal: this
// is a receipt being rendered, and the signed half -- the part that is actually
// evidence of anything -- has already been read.
func settlementFromEvent(ev store.Event) record.Settlement {
	set, err := record.SettlementFromJSON([]byte(ev.SettlementJSON), []byte(ev.PayloadJSON), record.Kind(ev.Kind))
	if err != nil {
		return record.Settlement{}
	}
	return set
}

// batchFromEvent reads the print run an activation recorded.
func batchFromEvent(ev store.Event) string {
	return settlementFromEvent(ev).Batch
}
