package web

import "testing"

// TestArtForFallsBackForAnUnknownSpecies guards the reason speciesArtByCode
// exists as a map rather than a required field: a species profile this
// deployment adds later (or one from another deployment, if a tag ID were
// ever guessed) must still render a card, not panic on a missing icon key.
func TestArtForFallsBackForAnUnknownSpecies(t *testing.T) {
	if got := artFor("CALSAP"); got.Icon != "blue-crab" {
		t.Errorf("CALSAP -> icon %q, want blue-crab", got.Icon)
	}
	if got := artFor("SCIOCE"); got.Icon != "red-drum" {
		t.Errorf("SCIOCE -> icon %q, want red-drum", got.Icon)
	}
	if got := artFor("SOMETHING-NEW"); got != fallbackArt {
		t.Errorf("unknown species -> %+v, want the fallback %+v", got, fallbackArt)
	}
	if got := artFor(""); got != fallbackArt {
		t.Errorf("empty species (never armed) -> %+v, want the fallback %+v", got, fallbackArt)
	}
}

func TestDistanceLabelMatchesTheClientsUnitSwitch(t *testing.T) {
	cases := map[int]string{
		0:     "0 m",
		1:     "1 m",
		999:   "999 m",
		1000:  "1.0 km",
		6400:  "6.4 km",
		12000: "12.0 km",
	}
	for m, want := range cases {
		if got := distanceLabel(m); got != want {
			t.Errorf("distanceLabel(%d) = %q, want %q", m, got, want)
		}
	}
}

func TestDayAndSightingWordsPluraliseCorrectly(t *testing.T) {
	if dayWord(1) != "day" || dayWord(0) != "days" || dayWord(2) != "days" {
		t.Error("dayWord does not singularise exactly at 1")
	}
	if sightingWord(1) != "sighting" || sightingWord(0) != "sightings" || sightingWord(3) != "sightings" {
		t.Error("sightingWord does not singularise exactly at 1")
	}
}

// TestOGIconsMatchTheSpeciesArtTable is what would have caught a species
// added to speciesArtByCode without adding its icon to newOGRenderer's load
// list: a card asking for an icon key the renderer never loaded falls back
// to no icon at all rather than erroring, silently.
func TestOGIconsMatchTheSpeciesArtTable(t *testing.T) {
	loaded := make(map[string]bool, len(ogIcons))
	for _, name := range ogIcons {
		loaded[name] = true
	}
	for code, art := range speciesArtByCode {
		if !loaded[art.Icon] {
			t.Errorf("species %s wants icon %q, which newOGRenderer never loads", code, art.Icon)
		}
	}
	if !loaded[fallbackArt.Icon] {
		t.Errorf("the fallback icon %q is not in the load list", fallbackArt.Icon)
	}
}
