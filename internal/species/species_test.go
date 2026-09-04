package species_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/species"
)

func crabProfile(t *testing.T) *species.Profile {
	t.Helper()
	p, err := species.Get("CALSAP")
	if err != nil {
		t.Fatalf("blue crab profile: %v", err)
	}
	return p
}

func TestEveryProfileValidates(t *testing.T) {
	all, err := species.All()
	if err != nil {
		t.Fatalf("load profiles: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("want at least two shipped profiles, got %d", len(all))
	}
	for _, p := range all {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", p.Code, err)
		}
		if p.Workflow == species.MarkRecapture {
			if _, ok := p.Vocab(species.DispositionKey); !ok {
				t.Errorf("%s is mark-recapture but has no %q attribute", p.Code, species.DispositionKey)
			}
		}
	}
}

// TestRulesRefuseWhatTheLawRefuses is the test the move from code to data
// exists to survive. These four outcomes were hardcoded in internal/crab; if a
// JSON edit ever loses one, this is what says so.
func TestRulesRefuseWhatTheLawRefuses(t *testing.T) {
	p := crabProfile(t)

	cases := []struct {
		name    string
		meas    map[string]int
		attr    map[string]string
		release bool
		reason  string
	}{
		{
			name:    "a sponge female must go back whatever her size",
			meas:    map[string]int{"cw": 160},
			attr:    map[string]string{"sex": "FS", "stage": "HARD", "gear": "TRAP"},
			release: true,
			reason:  "eggs",
		},
		{
			name:    "an undersized crab must go back",
			meas:    map[string]int{"cw": 120},
			attr:    map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"},
			release: true,
			reason:  "five-inch",
		},
		{
			name:    "exactly five inches is legal",
			meas:    map[string]int{"cw": 127},
			attr:    map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"},
			release: false,
		},
		{
			name:    "a legal male may be kept",
			meas:    map[string]int{"cw": 150},
			attr:    map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"},
			release: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			must, why := p.MustReleaseNow(tc.meas, tc.attr)
			if must != tc.release {
				t.Fatalf("MustReleaseNow = %v (%q), want %v", must, why, tc.release)
			}
			if tc.reason != "" && !strings.Contains(why, tc.reason) {
				t.Errorf("reason %q does not mention %q", why, tc.reason)
			}

			tc.attr[species.DispositionKey] = string(species.Harvested)
			err := p.ValidateReport(tc.meas, tc.attr)
			if tc.release && !errors.Is(err, species.ErrMustRelease) {
				t.Errorf("keeping it was accepted: %v", err)
			}
			if !tc.release && err != nil {
				t.Errorf("keeping a legal crab was refused: %v", err)
			}
		})
	}
}

// A report of a protected animal is still a report. Refusing it would destroy
// the data point the programme exists to collect.
func TestAProtectedAnimalMayStillBeReported(t *testing.T) {
	p := crabProfile(t)
	meas := map[string]int{"cw": 100}
	attr := map[string]string{"sex": "FS", "stage": "HARD", "gear": "TRAP", species.DispositionKey: string(species.Released)}

	if err := p.ValidateReport(meas, attr); err != nil {
		t.Fatalf("releasing an undersized sponge female was refused: %v", err)
	}
}

func TestOnlyHardShellCrabsAreTaggable(t *testing.T) {
	p := crabProfile(t)
	base := map[string]string{"sex": "M", "gear": "TRAP"}
	meas := map[string]int{"cw": 150}

	for _, stage := range []string{"PEELER_WHITE", "PEELER_PINK", "PEELER_RED", "SOFT", "PAPER"} {
		attr := map[string]string{"sex": base["sex"], "gear": base["gear"], "stage": stage}
		if err := p.ValidateTagging(meas, attr); !errors.Is(err, species.ErrNotTaggable) {
			t.Errorf("tagging a %s crab was accepted: %v", stage, err)
		}
	}
	hard := map[string]string{"sex": "M", "gear": "TRAP", "stage": "HARD"}
	if err := p.ValidateTagging(meas, hard); err != nil {
		t.Errorf("tagging a hard-shell crab was refused: %v", err)
	}
}

// Red drum is the whole point of the abstraction: a slot limit rather than a
// simple minimum, and no moult stage at all.
func TestRedDrumHasASlotLimit(t *testing.T) {
	p, err := species.Get("SCIOCE")
	if err != nil {
		t.Fatalf("red drum profile: %v", err)
	}
	attr := map[string]string{"sex": "U", "cond": "GOOD", "gear": "HANDLINE"}

	for _, tc := range []struct {
		mm      int
		release bool
	}{{300, true}, {381, false}, {500, false}, {584, false}, {700, true}} {
		must, why := p.MustReleaseNow(map[string]int{"tl": tc.mm}, attr)
		if must != tc.release {
			t.Errorf("%d mm: MustReleaseNow = %v (%q), want %v", tc.mm, must, why, tc.release)
		}
	}
	if _, ok := p.Vocab("stage"); ok {
		t.Error("red drum should not carry a moult stage")
	}
}

func TestUnknownFieldsAreRefusedRatherThanDropped(t *testing.T) {
	p := crabProfile(t)
	attr := map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP"}

	if err := p.ValidateObservation(map[string]int{"cw": 150, "tl": 400}, attr); !errors.Is(err, species.ErrUnknownMeasure) {
		t.Errorf("a measurement the crab profile does not define was accepted: %v", err)
	}
	withJunk := map[string]string{"sex": "M", "stage": "HARD", "gear": "TRAP", "antlers": "8"}
	if err := p.ValidateObservation(map[string]int{"cw": 150}, withJunk); !errors.Is(err, species.ErrUnknownVocab) {
		t.Errorf("an attribute the crab profile does not define was accepted: %v", err)
	}
	if err := p.ValidateObservation(map[string]int{"cw": 150}, map[string]string{"sex": "M", "stage": "HARD"}); !errors.Is(err, species.ErrMissing) {
		t.Errorf("a missing required attribute was accepted: %v", err)
	}
	if err := p.ValidateObservation(map[string]int{"cw": 20}, attr); !errors.Is(err, species.ErrBadValue) {
		t.Errorf("a 20 mm crab was accepted: %v", err)
	}
	if err := p.ValidateObservation(map[string]int{"cw": 150}, map[string]string{"sex": "Q", "stage": "HARD", "gear": "TRAP"}); !errors.Is(err, species.ErrBadValue) {
		t.Errorf("an unrecognised sex code was accepted: %v", err)
	}
}

func TestAProfileThatCannotFireItsOwnRulesIsRefused(t *testing.T) {
	base := func() species.Profile {
		return species.Profile{
			Code: "TEST", Common: "Test animal", Workflow: species.MarkRecapture,
			PrimaryMeasure: "len", QRVersionMax: 5,
			Measures: []species.Measure{{Key: "len", Label: "Length", Unit: "mm", Scale: 1, Min: 1, Max: 100, Required: true}},
			Vocabs:   []species.Vocab{{Key: "sex", Label: "Sex", Values: []species.VocabValue{{Code: "M", Label: "Male"}}}},
		}
	}
	ok := base()
	if err := ok.Validate(); err != nil {
		t.Fatalf("a coherent profile was refused: %v", err)
	}

	limit := 50
	for name, mutate := range map[string]func(*species.Profile){
		"a rule on a measure nobody records": func(p *species.Profile) {
			p.MustRelease = []species.Rule{{Measure: "girth", LessThan: &limit, Reason: "too thin"}}
		},
		"a rule on a code nobody allows": func(p *species.Profile) {
			p.MustRelease = []species.Rule{{Vocab: "sex", In: []string{"FS"}, Reason: "eggs"}}
		},
		"a rule with no threshold": func(p *species.Profile) {
			p.MustRelease = []species.Rule{{Measure: "len", Reason: "too short"}}
		},
		"a rule with nothing to show anyone": func(p *species.Profile) {
			p.MustRelease = []species.Rule{{Measure: "len", LessThan: &limit}}
		},
		"a primary measure it does not define": func(p *species.Profile) { p.PrimaryMeasure = "girth" },
		"a workflow nobody implements":         func(p *species.Profile) { p.Workflow = "vibes" },
	} {
		t.Run(name, func(t *testing.T) {
			p := base()
			mutate(&p)
			if err := p.Validate(); !errors.Is(err, species.ErrBadProfile) {
				t.Errorf("accepted: %v", err)
			}
		})
	}
}

func TestDistanceAndDaysAtLarge(t *testing.T) {
	charleston := species.Fix{Lat: 32.7765, Lon: -79.9311}
	beaufort := species.Fix{Lat: 32.4316, Lon: -80.6698}

	// Roughly 78 km by great circle; the tolerance is generous because the
	// point is that the formula is right, not that the ports are.
	if d := species.DistanceKM(charleston, beaufort); math.Abs(d-78) > 3 {
		t.Errorf("Charleston to Beaufort is %.1f km, want about 78", d)
	}
	if d := species.DistanceKM(charleston, charleston); d != 0 {
		t.Errorf("a fix is %.6f km from itself", d)
	}

	tagged := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want int
	}{
		{tagged.Add(23 * time.Hour), 0},
		{tagged.Add(25 * time.Hour), 1},
		{tagged.Add(-time.Hour), 0},
	} {
		if got := species.DaysAtLarge(tagged, tc.at); got != tc.want {
			t.Errorf("DaysAtLarge to %s = %d, want %d", tc.at, got, tc.want)
		}
	}
}

func TestFixValidation(t *testing.T) {
	for name, f := range map[string]species.Fix{
		"null island":       {Lat: 0, Lon: 0, AccuracyM: 5},
		"off the globe":     {Lat: 91, Lon: 0, AccuracyM: 5},
		"past the antimer":  {Lat: 10, Lon: 181, AccuracyM: 5},
		"absurd accuracy":   {Lat: 32.7, Lon: -79.9, AccuracyM: 1e6},
		"negative accuracy": {Lat: 32.7, Lon: -79.9, AccuracyM: -1},
	} {
		if err := f.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	good := species.Fix{Lat: 32.7765, Lon: -79.9311, AccuracyM: 8}
	if err := good.Validate(); err != nil {
		t.Errorf("a real fix was refused: %v", err)
	}
}

func TestNames(t *testing.T) {
	if got := species.NormalizeName("  Big   Red \n"); got != "Big Red" {
		t.Errorf("NormalizeName = %q", got)
	}
	if err := species.ValidateName(""); err != nil {
		t.Errorf("an unnamed animal was refused: %v", err)
	}
	if err := species.ValidateName(strings.Repeat("a", species.MaxNameLen+1)); !errors.Is(err, species.ErrBadName) {
		t.Errorf("an over-long name was accepted: %v", err)
	}
	// A right-to-left override reorders everything after it on a page that
	// will display this name forever.
	if err := species.ValidateName("Big ‮Red"); !errors.Is(err, species.ErrBadName) {
		t.Errorf("a bidi override was accepted: %v", err)
	}
	// Exotic whitespace is folded, not refused: somebody pasting from a
	// document should get their name, not a rejection.
	if err := species.ValidateName("Big Red"); err != nil {
		t.Errorf("a pasted line separator was refused: %v", err)
	}
}
