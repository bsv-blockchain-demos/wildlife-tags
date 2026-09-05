package species

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Workflow is the shape of the programme, and there are exactly two.
//
// Mark-recapture has two parties and two events: somebody tags a live animal
// and releases it, and later somebody else finds it and reports. The reward
// flows from the programme to the finder, and the tag is a bearer instrument.
//
// Harvest has one party and one event: a hunter tags an animal they killed,
// because the law requires it. There is no reward and no second finder. What
// matters is that the record is timestamped, carries a position, and cannot be
// reused -- which is exactly what a one-time UTXO spend is. South Carolina
// currently does this with a confirmation number a meat processor has to trust
// a database to validate.
type Workflow string

const (
	MarkRecapture Workflow = "mark-recapture"
	Harvest       Workflow = "harvest"
)

// Valid reports whether a workflow is one we implement.
func (w Workflow) Valid() bool { return w == MarkRecapture || w == Harvest }

// Measure is a number recorded about an animal.
//
// Scale exists because the record format bans floats: a length in millimetres
// has Scale 1, a temperature in hundredths of a degree has Scale 100. The value
// stored and signed is always the scaled integer.
type Measure struct {
	Key      string `json:"key"`   // short and stable; it goes on chain: "cw", "tl", "pts"
	Label    string `json:"label"` // "Carapace width, tip to tip"
	Unit     string `json:"unit"`  // "mm"
	Scale    int    `json:"scale"` // 1 whole units, 100 hundredths
	Min      int    `json:"min"`   // in scaled units
	Max      int    `json:"max"`
	Required bool   `json:"required"`
	// TaggingOnly marks a field a tagger records and a finder is not asked
	// for. See the note on Vocab.TaggingOnly.
	TaggingOnly bool `json:"tagging_only,omitempty"`
	// Sticky marks a value that describes the place and moment, not the
	// animal: a water temperature or salinity reading is the same for
	// every crab pulled from one trap haul. The console carries a sticky
	// value forward from one tagging to the next rather than clearing it,
	// so a biologist tagging a dozen animals off one haul types it once.
	// A per-animal measurement like carapace width is never sticky.
	Sticky bool   `json:"sticky,omitempty"`
	Help   string `json:"help,omitempty"`
}

// VocabValue is one allowed answer.
type VocabValue struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// Vocab is a categorical choice: sex, life stage, gear, disposition.
type Vocab struct {
	Key      string       `json:"key"`
	Label    string       `json:"label"`
	Values   []VocabValue `json:"values"`
	Required bool         `json:"required"`
	// TaggingOnly marks a field a tagger records and a finder is not asked for.
	//
	// Shell condition is the example: a biologist stages a crab because that is
	// what decides whether the tag will still be on it next week, and getting
	// it wrong wastes the tag. Somebody who pulls a tagged crab out of a trap
	// is not a trained observer and should not be required to guess -- and a
	// required field a member of the public cannot answer is a report that
	// never gets filed, which costs the programme the data point it exists for.
	//
	// The field is still accepted on a report if it is offered.
	TaggingOnly bool `json:"tagging_only,omitempty"`
	// Sticky: see Measure.Sticky. Gear is the usual case for a vocabulary --
	// a trap haul is pulled with one kind of gear, not a different one per
	// crab.
	Sticky bool   `json:"sticky,omitempty"`
	Help   string `json:"help,omitempty"`
}

// Label returns the human form of a code, or the code itself if unknown.
func (v Vocab) Label_(code string) string {
	for _, val := range v.Values {
		if val.Code == code {
			return val.Label
		}
	}
	return code
}

// Has reports whether a code is in the vocabulary.
func (v Vocab) Has(code string) bool {
	for _, val := range v.Values {
		if val.Code == code {
			return true
		}
	}
	return false
}

// Rule is a declarative predicate over an observation.
//
// Keeping legal and protocol constraints as data rather than code is what lets
// a new species be a JSON file. One shape covers "females carrying eggs must be
// returned", "under five inches must be returned", a fish slot limit, and a
// deer antler restriction.
//
// Exactly one of Measure or Vocab is set. A rule with a Measure fires when the
// value is below LessThan or above MoreThan; a rule with a Vocab fires when the
// recorded code is in In, or -- with NotIn -- when it is not.
type Rule struct {
	Measure  string   `json:"measure,omitempty"`
	Vocab    string   `json:"vocab,omitempty"`
	LessThan *int     `json:"less_than,omitempty"`
	MoreThan *int     `json:"more_than,omitempty"`
	In       []string `json:"in,omitempty"`
	NotIn    []string `json:"not_in,omitempty"`
	Reason   string   `json:"reason"` // shown to the user, and carried in the refusal
}

// Fires reports whether the rule matches an observation.
func (r Rule) Fires(meas map[string]int, attr map[string]string) bool {
	switch {
	case r.Measure != "":
		v, ok := meas[r.Measure]
		if !ok {
			// A rule about a measurement nobody recorded cannot fire. The
			// measurement being required is a separate check.
			return false
		}
		if r.LessThan != nil && v < *r.LessThan {
			return true
		}
		if r.MoreThan != nil && v > *r.MoreThan {
			return true
		}
		return false

	case r.Vocab != "":
		code, ok := attr[r.Vocab]
		if !ok {
			return false
		}
		for _, c := range r.In {
			if c == code {
				return true
			}
		}
		if len(r.NotIn) > 0 {
			for _, c := range r.NotIn {
				if c == code {
					return false
				}
			}
			return true
		}
		return false
	}
	return false
}

// Profile is everything species-specific, as data.
type Profile struct {
	Code       string   `json:"code"`       // "CALSAP"
	Common     string   `json:"common"`     // "Atlantic blue crab"
	Scientific string   `json:"scientific"` // "Callinectes sapidus"
	Workflow   Workflow `json:"workflow"`
	Programme  string   `json:"programme"` // the DNR programme this belongs to

	Measures []Measure `json:"measures"`
	Vocabs   []Vocab   `json:"vocabs"`

	// PrimaryMeasure is the size axis a receipt leads with, and the one growth
	// is computed against. Every animal has one obvious answer and no page
	// should have to guess.
	PrimaryMeasure string `json:"primary_measure"`

	// NotTaggable refuses a tagging outright. A blue crab peeler sheds the tag
	// at its next moult and takes the locked reward with it.
	NotTaggable []Rule `json:"not_taggable"`
	// MustRelease forbids claiming to have kept the animal. It does not refuse
	// the report -- the animal has already been caught and the data point is
	// the whole reason the programme exists.
	MustRelease []Rule `json:"must_release"`

	// SweepAfterDays is how long a reward stays claimable. A blue crab lives
	// two to three years and sheds a tag at every moult; a fish carries one for
	// far longer. Zero means never sweep.
	SweepAfterDays int `json:"sweep_after_days"`

	// QRVersionMax bounds the printed code's density, because tag stock differs:
	// a 1x2in crab tag wired through the lateral spines is not a deer ear tag.
	QRVersionMax int `json:"qr_version_max"`

	// GrowthExpected says whether a non-zero growth between sightings is normal.
	// False for anything that sheds its tag when it grows.
	GrowthExpected bool `json:"growth_expected"`
}

var (
	ErrUnknownSpecies = errors.New("species: unknown species")
	ErrBadProfile     = errors.New("species: malformed profile")
	ErrUnknownMeasure = errors.New("species: unrecognised measurement")
	ErrUnknownVocab   = errors.New("species: unrecognised attribute")
	ErrBadValue       = errors.New("species: value out of range")
	ErrMissing        = errors.New("species: required value missing")
	ErrNotTaggable    = errors.New("species: this animal should not be tagged")
	ErrMustRelease    = errors.New("species: this animal must be released")
)

// SweepAfter is the reward's claimable window as a duration.
func (p *Profile) SweepAfter() time.Duration {
	return time.Duration(p.SweepAfterDays) * 24 * time.Hour
}

// Measure looks up a measurement definition.
func (p *Profile) Measure(key string) (Measure, bool) {
	for _, m := range p.Measures {
		if m.Key == key {
			return m, true
		}
	}
	return Measure{}, false
}

// Vocab looks up an attribute definition.
func (p *Profile) Vocab(key string) (Vocab, bool) {
	for _, v := range p.Vocabs {
		if v.Key == key {
			return v, true
		}
	}
	return Vocab{}, false
}

// Label renders an attribute code for a human.
func (p *Profile) Label(vocabKey, code string) string {
	if v, ok := p.Vocab(vocabKey); ok {
		return v.Label_(code)
	}
	return code
}

// Validate checks the profile itself is coherent.
//
// A malformed profile is a species nobody can record, and the failure would
// otherwise surface as a confusing rejection in the field rather than at
// startup.
func (p *Profile) Validate() error {
	switch {
	case p.Code == "":
		return fmt.Errorf("%w: no code", ErrBadProfile)
	case p.Common == "":
		return fmt.Errorf("%w: %s has no common name", ErrBadProfile, p.Code)
	case !p.Workflow.Valid():
		return fmt.Errorf("%w: %s has workflow %q", ErrBadProfile, p.Code, p.Workflow)
	case len(p.Measures) == 0:
		return fmt.Errorf("%w: %s records no measurements", ErrBadProfile, p.Code)
	}

	seen := map[string]bool{}
	for _, m := range p.Measures {
		if m.Key == "" || m.Scale < 1 || m.Min > m.Max {
			return fmt.Errorf("%w: %s measure %q is incoherent", ErrBadProfile, p.Code, m.Key)
		}
		if seen[m.Key] {
			return fmt.Errorf("%w: %s repeats measure %q", ErrBadProfile, p.Code, m.Key)
		}
		seen[m.Key] = true
	}
	for _, v := range p.Vocabs {
		if v.Key == "" || len(v.Values) == 0 {
			return fmt.Errorf("%w: %s vocabulary %q is empty", ErrBadProfile, p.Code, v.Key)
		}
		if seen[v.Key] {
			return fmt.Errorf("%w: %s reuses key %q for a measure and a vocabulary", ErrBadProfile, p.Code, v.Key)
		}
		seen[v.Key] = true
	}
	if _, ok := p.Measure(p.PrimaryMeasure); !ok {
		return fmt.Errorf("%w: %s names primary measure %q which it does not define",
			ErrBadProfile, p.Code, p.PrimaryMeasure)
	}

	// A rule naming a field that does not exist can never fire, so it is a
	// legal constraint silently doing nothing -- the worst possible outcome.
	for _, set := range [][]Rule{p.NotTaggable, p.MustRelease} {
		for _, r := range set {
			if err := p.checkRule(r); err != nil {
				return err
			}
		}
	}
	if p.QRVersionMax < 1 || p.QRVersionMax > 40 {
		return fmt.Errorf("%w: %s has QR version cap %d", ErrBadProfile, p.Code, p.QRVersionMax)
	}
	return nil
}

func (p *Profile) checkRule(r Rule) error {
	switch {
	case r.Measure != "" && r.Vocab != "":
		return fmt.Errorf("%w: %s rule sets both a measure and a vocabulary", ErrBadProfile, p.Code)
	case r.Measure == "" && r.Vocab == "":
		return fmt.Errorf("%w: %s rule sets neither a measure nor a vocabulary", ErrBadProfile, p.Code)
	case r.Reason == "":
		return fmt.Errorf("%w: %s rule has no reason to show anyone", ErrBadProfile, p.Code)
	}
	if r.Measure != "" {
		if _, ok := p.Measure(r.Measure); !ok {
			return fmt.Errorf("%w: %s rule names measure %q which it does not define",
				ErrBadProfile, p.Code, r.Measure)
		}
		if r.LessThan == nil && r.MoreThan == nil {
			return fmt.Errorf("%w: %s rule on %q sets no threshold", ErrBadProfile, p.Code, r.Measure)
		}
		return nil
	}
	v, ok := p.Vocab(r.Vocab)
	if !ok {
		return fmt.Errorf("%w: %s rule names vocabulary %q which it does not define",
			ErrBadProfile, p.Code, r.Vocab)
	}
	if len(r.In) == 0 && len(r.NotIn) == 0 {
		return fmt.Errorf("%w: %s rule on %q lists no codes", ErrBadProfile, p.Code, r.Vocab)
	}
	for _, code := range append(append([]string{}, r.In...), r.NotIn...) {
		if !v.Has(code) {
			return fmt.Errorf("%w: %s rule names code %q which %q does not allow",
				ErrBadProfile, p.Code, code, r.Vocab)
		}
	}
	return nil
}

// ValidateObservation checks recorded values against the profile.
//
// Unknown keys are refused rather than ignored: a measurement the profile does
// not define is a typo or a client from a different version, and silently
// dropping it would put a hole in the dataset that nobody notices.
//
// tagging says whether the observer is a tagger, which decides whether the
// TaggingOnly fields are required.
func (p *Profile) ValidateObservation(meas map[string]int, attr map[string]string) error {
	return p.validate(meas, attr, true)
}

func (p *Profile) validate(meas map[string]int, attr map[string]string, tagging bool) error {
	for key, v := range meas {
		m, ok := p.Measure(key)
		if !ok {
			return fmt.Errorf("%w: %s does not record %q", ErrUnknownMeasure, p.Code, key)
		}
		if v < m.Min || v > m.Max {
			return fmt.Errorf("%w: %s is %s, outside %s..%s",
				ErrBadValue, m.Label, scaled(v, m), scaled(m.Min, m), scaled(m.Max, m))
		}
	}
	for key, code := range attr {
		v, ok := p.Vocab(key)
		if !ok {
			return fmt.Errorf("%w: %s does not record %q", ErrUnknownVocab, p.Code, key)
		}
		if !v.Has(code) {
			return fmt.Errorf("%w: %q is not a %s", ErrBadValue, code, v.Label)
		}
	}
	for _, m := range p.Measures {
		if m.Required && (tagging || !m.TaggingOnly) {
			if _, ok := meas[m.Key]; !ok {
				return fmt.Errorf("%w: %s", ErrMissing, m.Label)
			}
		}
	}
	for _, v := range p.Vocabs {
		if v.Required && (tagging || !v.TaggingOnly) {
			if _, ok := attr[v.Key]; !ok {
				return fmt.Errorf("%w: %s", ErrMissing, v.Label)
			}
		}
	}
	return nil
}

// ValidateTagging additionally refuses animals that should not carry a tag.
//
// A tag costs a printed label, a boat trip and a locked reward, so spending one
// on an animal that will shed it within the week is the expensive mistake this
// guards against.
func (p *Profile) ValidateTagging(meas map[string]int, attr map[string]string) error {
	if err := p.validate(meas, attr, true); err != nil {
		return err
	}
	for _, r := range p.NotTaggable {
		if r.Fires(meas, attr) {
			return fmt.Errorf("%w: %s", ErrNotTaggable, r.Reason)
		}
	}
	return nil
}

// MustReleaseNow reports whether the law requires this animal to go back, and
// why.
//
// This is the one place where the law and the incentive point the same way: a
// released animal is worth more than a kept one under the escrow, so somebody
// holding a protected animal is being paid to do what they must do anyway.
func (p *Profile) MustReleaseNow(meas map[string]int, attr map[string]string) (bool, string) {
	for _, r := range p.MustRelease {
		if r.Fires(meas, attr) {
			return true, r.Reason
		}
	}
	return false, ""
}

// Disposition reads what the reporter did with the animal.
func (p *Profile) Disposition(attr map[string]string) (Disposition, error) {
	d := Disposition(attr[DispositionKey])
	if !d.Valid() {
		return "", fmt.Errorf("%w: %q is not something to have done with an animal", ErrBadValue, d)
	}
	return d, nil
}

// ValidateReport checks a recapture. It deliberately does not refuse a
// protected animal -- it has already been caught, and refusing the report would
// destroy the very data the programme exists to collect. It refuses only the
// claim to have kept one.
func (p *Profile) ValidateReport(meas map[string]int, attr map[string]string) error {
	disp, err := p.Disposition(attr)
	if err != nil {
		return err
	}
	if err := p.validate(meas, attr, false); err != nil {
		return err
	}
	if must, why := p.MustReleaseNow(meas, attr); must && disp == Harvested {
		return fmt.Errorf("%w: %s", ErrMustRelease, why)
	}
	return nil
}

// MeasureKeys returns the defined measurement keys in sorted order, which is
// the order the canonical record encodes them in.
func (p *Profile) MeasureKeys() []string {
	out := make([]string, 0, len(p.Measures))
	for _, m := range p.Measures {
		out = append(out, m.Key)
	}
	sort.Strings(out)
	return out
}

// scaled renders a stored integer in its human unit, for error messages.
func scaled(v int, m Measure) string {
	if m.Scale <= 1 {
		return fmt.Sprintf("%d %s", v, m.Unit)
	}
	return fmt.Sprintf("%.2f %s", float64(v)/float64(m.Scale), m.Unit)
}
