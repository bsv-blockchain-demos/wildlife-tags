// Package species holds everything about a tagged animal that is not about any
// one animal.
//
// The program began shaped like a blue crab: carapace width, moult stage,
// sponge females, South Carolina's five-inch rule. None of that belongs in
// code. SCDNR alone runs a marine game fish programme covering 46 target
// species across 20 families with the same tag-release-recapture shape, and a
// deer harvest tag is the same machinery again with a different workflow. So a
// species is a Profile -- data, loaded from JSON -- and adding one is a file,
// not a release.
//
// What stays in code is the part that really is universal: where something was,
// when, how far it moved, and what it is called.
package species

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Disposition is what became of the animal. Universal across every workflow:
// a crab is put back or kept, a deer is harvested.
type Disposition string

const (
	// Released means put back with the tag still on, which is the outcome that
	// can produce another data point.
	Released Disposition = "RELEASED"
	// Harvested means kept. The tag retires.
	Harvested Disposition = "HARVESTED"
)

// Valid reports whether a disposition code is one we recognise.
func (d Disposition) Valid() bool { return d == Released || d == Harvested }

// MaxNameLen bounds an animal's name.
//
// Names go into a Bitcoin transaction and are public and permanent, so this is
// a hard limit rather than a display preference: every byte is paid for, and
// there is no edit afterwards.
const MaxNameLen = 24

var (
	ErrBadName             = errors.New("species: unusable name")
	ErrBadCoordinate       = errors.New("species: coordinates are out of range")
	ErrImplausibleAccuracy = errors.New("species: reported gps accuracy is implausible")
)

// NormalizeName tidies a proposed name without changing what it says.
//
// Collapsing whitespace matters more than it looks: the name is written into a
// signed record, so "Big  Red" and "Big Red" would be different bytes and a
// signature over one would not verify against the other.
func NormalizeName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// ValidateName checks a proposed name.
//
// An empty name is fine and means "not named": the tagger may skip it, and the
// first finder gets the chance instead.
func ValidateName(name string) error {
	name = NormalizeName(name)
	if name == "" {
		return nil
	}
	if utf8.RuneCountInString(name) > MaxNameLen {
		return fmt.Errorf("%w: %d characters, limit is %d", ErrBadName, utf8.RuneCountInString(name), MaxNameLen)
	}
	for _, r := range name {
		// Two classes have to go. Non-printable characters are self-evident.
		// Format characters are the subtle ones: a right-to-left override
		// reorders everything after it on the line, and a zero-width space is
		// invisible but still there -- both let a name interfere with the
		// rendering of a page that will display it forever.
		//
		// Exotic whitespace is not in this category: NormalizeName has already
		// folded line and paragraph separators into ordinary spaces, which is
		// what somebody pasting from a document should get.
		if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("%w: contains a character that cannot be displayed", ErrBadName)
		}
	}
	return nil
}

// Fix is a position report from a phone or a handheld unit.
//
// AccuracyM is carried because it is the only honest quality signal available:
// the chain can prove when this record was written and that it has not changed,
// but nothing about it proves the device was where it says it was. A researcher
// filtering the dataset needs to see it.
type Fix struct {
	Lat       float64
	Lon       float64
	AccuracyM float64
	At        time.Time
}

// Validate rejects coordinates that cannot be real.
func (f Fix) Validate() error {
	if f.Lat < -90 || f.Lat > 90 || f.Lon < -180 || f.Lon > 180 {
		return fmt.Errorf("%w: %.5f, %.5f", ErrBadCoordinate, f.Lat, f.Lon)
	}
	if f.Lat == 0 && f.Lon == 0 {
		// Null Island is what a geolocation failure looks like when it is
		// serialised as two float64 zeroes.
		return fmt.Errorf("%w: 0,0 is a failed fix, not a position", ErrBadCoordinate)
	}
	if f.AccuracyM < 0 || f.AccuracyM > 100000 {
		return fmt.Errorf("%w: %.1f m", ErrImplausibleAccuracy, f.AccuracyM)
	}
	return nil
}

// earthRadiusKM is the mean radius. Tagged animals move single-digit to
// low-hundreds of kilometres, so the difference between the mean radius and a
// proper ellipsoid is far below the GPS noise already in the fix.
const earthRadiusKM = 6371.0088

// DistanceKM is the great-circle distance between two fixes.
func DistanceKM(a, b Fix) float64 {
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dLat := lat2 - lat1
	dLon := (b.Lon - a.Lon) * math.Pi / 180

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKM * math.Asin(math.Sqrt(math.Min(1, h)))
}

// DaysAtLarge is how long the animal carried the tag before it was found.
//
// It truncates rather than rounds, so "1 day" means a full day elapsed. An
// animal recaptured the same afternoon it was released reads as 0, which is the
// honest answer and a genuinely interesting record.
func DaysAtLarge(tagged, recaptured time.Time) int {
	d := recaptured.Sub(tagged)
	if d < 0 {
		return 0
	}
	return int(d.Hours() / 24)
}

// Growth is the change in a measurement between tagging and recapture.
//
// What it means depends on the animal, and the profile says so. A blue crab
// grows only by moulting and sheds the tag when it does, so a non-zero value is
// a signal that one of the measurements is wrong. A fish grows continuously
// while carrying the same tag, so a non-zero value is the point.
func Growth(before, after int) int { return after - before }

// DispositionKey is the attribute every mark-recapture report carries: what the
// finder did with the animal.
//
// It is not declared in any profile. It is universal to the workflow rather
// than to the species, it drives the payout split, and a profile that forgot to
// declare it would silently lose the one self-reported field with money
// attached -- so the registry injects it into every mark-recapture profile
// instead of trusting each one to remember.
const DispositionKey = "disp"

// dispositionVocab is that injected attribute.
func dispositionVocab() Vocab {
	return Vocab{
		Key:   DispositionKey,
		Label: "What happened to the animal",
		// Not Required, because a tagging has no disposition: the tagger is
		// releasing the animal by definition. ValidateReport requires it
		// explicitly, which is the one place it means anything.
		Required: false,
		Values: []VocabValue{
			{Code: string(Released), Label: "Released with the tag still on"},
			{Code: string(Harvested), Label: "Kept"},
		},
	}
}
