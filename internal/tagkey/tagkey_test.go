package tagkey

import (
	"errors"
	"strings"
	"testing"

	"rsc.io/qr"
)

func testSeed(t *testing.T) Seed {
	t.Helper()
	s, err := SeedFromBytes([]byte("wildtag test seed, 32 bytes long"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s
}

func TestDerivationIsDeterministic(t *testing.T) {
	// The whole recovery story depends on this: DNR must be able to reprint a
	// lost batch and sweep unrecaptured rewards years later from the seed alone.
	a, b := testSeed(t), testSeed(t)
	for _, ord := range []uint64{0, 1, 42, 999999} {
		s1, s2 := a.SecretFor(ord), b.SecretFor(ord)
		if s1 != s2 {
			t.Fatalf("ordinal %d: secret is not deterministic", ord)
		}
		if s1.ID() != s2.ID() {
			t.Fatalf("ordinal %d: id is not deterministic", ord)
		}
		if !s1.PrivateKey().PubKey().IsEqual(s2.PrivateKey().PubKey()) {
			t.Fatalf("ordinal %d: key is not deterministic", ord)
		}
	}
}

func TestDifferentOrdinalsGiveDifferentTags(t *testing.T) {
	seed := testSeed(t)
	seen := map[string]uint64{}
	for ord := uint64(0); ord < 5000; ord++ {
		sec := seed.SecretFor(ord)
		id := string(sec.ID())
		if prev, dup := seen[id]; dup {
			t.Fatalf("ordinals %d and %d both produced tag id %s", prev, ord, id)
		}
		seen[id] = ord
	}
}

func TestADifferentSeedGivesDifferentTags(t *testing.T) {
	a := testSeed(t)
	b, err := SeedFromBytes([]byte("a different seed, also 32 bytes!"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if a.SecretFor(7) == b.SecretFor(7) {
		t.Fatal("two seeds produced the same secret")
	}
}

// TestTheBrowserCanDeriveEverythingFromTheFragment is the property that lets
// the redemption page work without trusting the server: the secret alone yields
// the spending key and the tag id.
func TestTheBrowserCanDeriveEverythingFromTheFragment(t *testing.T) {
	seed := testSeed(t)
	sec := seed.SecretFor(1234)
	payload := QRPayload("https://bcrab.sc.gov", sec.ID(), sec)

	gotID, gotSec, err := ParsePayload(payload)
	if err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	if gotSec != sec {
		t.Fatal("secret did not survive the payload round trip")
	}
	if gotID != sec.ID() {
		t.Fatalf("id in the path (%s) disagrees with the id derived from the secret (%s)", gotID, sec.ID())
	}
	if !gotSec.PrivateKey().PubKey().IsEqual(sec.PrivateKey().PubKey()) {
		t.Fatal("key did not survive the payload round trip")
	}
}

func TestTheSecretIsInTheFragmentNotThePath(t *testing.T) {
	// If this ever regresses, every tag secret lands in the server's access
	// logs and anyone who can read them can redeem tags.
	seed := testSeed(t)
	sec := seed.SecretFor(3)
	payload := QRPayload("https://bcrab.sc.gov", sec.ID(), sec)

	hash := strings.Index(payload, "#")
	if hash < 0 {
		t.Fatal("payload has no fragment")
	}
	if strings.Contains(payload[:hash], sec.Encode()) {
		t.Fatal("the secret appears before the fragment marker")
	}
}

func TestParseIDAcceptsWhatAHumanWouldActuallyType(t *testing.T) {
	seed := testSeed(t)
	id := seed.SecretFor(77).ID()

	for _, form := range []string{
		string(id),
		strings.ToLower(string(id)),
		id.Display(),
		" " + id.Display() + " ",
		strings.ReplaceAll(string(id), "-", " "),
	} {
		got, err := ParseID(form)
		if err != nil {
			t.Errorf("ParseID(%q): %v", form, err)
			continue
		}
		if got != id {
			t.Errorf("ParseID(%q) = %s, want %s", form, got, id)
		}
	}
}

func TestParseIDAppliesTheCrockfordSubstitutions(t *testing.T) {
	// Someone reading a fouled tag aloud will say "oh" for zero and "eye" or
	// "ell" for one. The alphabet excludes I, L and O precisely so those can be
	// mapped back without ambiguity.
	for _, sub := range []struct{ typed, means string }{
		{"I", "1"}, {"L", "1"}, {"O", "0"},
		{"i", "1"}, {"l", "1"}, {"o", "0"},
	} {
		canonical := "1" + strings.Repeat("2", idDataLen-1)
		canonical += string(checkChar([]byte(canonical)))
		typed := sub.typed + canonical[1:]
		if sub.means != "1" {
			continue
		}
		got, err := ParseID(typed)
		if err != nil {
			t.Errorf("ParseID(%q): %v", typed, err)
			continue
		}
		if string(got) != canonical {
			t.Errorf("ParseID(%q) = %s, want %s", typed, got, canonical)
		}
	}
}

func TestTheCheckCharacterCatchesASingleWrongCharacter(t *testing.T) {
	seed := testSeed(t)
	id := string(seed.SecretFor(500).ID())

	caught, total := 0, 0
	for pos := 0; pos < idDataLen; pos++ {
		for _, c := range crockford {
			if byte(c) == id[pos] {
				continue
			}
			total++
			corrupted := id[:pos] + string(c) + id[pos+1:]
			if _, err := ParseID(corrupted); err != nil {
				caught++
			}
		}
	}
	if caught != total {
		t.Fatalf("caught %d of %d single-character errors", caught, total)
	}
}

// TestTheCheckCharacterCatchesMostTranspositions measures the gap that odd
// weights buy in exchange for perfect single-character detection. It is a
// measurement pinned as a test, not an aspiration: one base-32 check character
// provably cannot catch both classes completely, and this records which trade
// the code makes so a future change cannot quietly reverse it.
func TestTheCheckCharacterCatchesMostTranspositions(t *testing.T) {
	seed := testSeed(t)
	caught, total := 0, 0
	for ord := uint64(0); ord < 2000; ord++ {
		id := string(seed.SecretFor(ord).ID())
		for pos := 0; pos+1 < idDataLen; pos++ {
			if id[pos] == id[pos+1] {
				continue
			}
			total++
			swapped := id[:pos] + string(id[pos+1]) + string(id[pos]) + id[pos+2:]
			if _, err := ParseID(swapped); err != nil {
				caught++
			}
		}
	}
	rate := float64(caught) / float64(total)
	t.Logf("caught %d of %d adjacent transpositions (%.1f%%)", caught, total, rate*100)
	// The algebra puts the blind spot at exactly 160/4960 = 3.2% of character
	// pairs, so anything under 95% means the weighting scheme changed.
	if rate < 0.95 {
		t.Errorf("transposition detection fell to %.1f%%, below the 95%% the odd-weight scheme should give", rate*100)
	}
}

func TestParseIDRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "SHORT", "WAYTOOLONGFORATAG", "ABC!23X", "UUUUUUU"} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("ParseID(%q) was accepted", bad)
		}
	}
}

func TestDecodeSecretRejectsTheWrongLength(t *testing.T) {
	if _, err := DecodeSecret("AAAA"); !errors.Is(err, ErrBadSecret) {
		t.Fatalf("got %v, want ErrBadSecret", err)
	}
}

// TestTheQRCodeFitsOnACrabTag is the constraint that most easily regresses,
// because it regresses by someone choosing a longer hostname rather than by
// anyone touching this package.
//
// SCDNR's blue crab tags are roughly 1x2in plastic rectangles wired through the
// lateral spines, which leaves about a 0.75in square for a QR code. Version 6
// (41x41) puts the module pitch under 0.019in, which is where a wet, fouled,
// abraded tag starts failing to scan. Version 5 is the ceiling this test
// enforces.
func TestTheQRCodeFitsOnACrabTag(t *testing.T) {
	const maxVersion = 5
	const eccLevel = qr.M // 38% redundancy, for a tag that lives in seawater

	seed := testSeed(t)

	// The longest realistic deployment host. Anything longer than this needs a
	// physical scan test before it ships.
	for _, host := range []string{
		"https://bcrab.sc.gov",
		"https://wildtag.sc.gov",
		"https://wildtag.dnr.sc.gov",
	} {
		worst := 0
		for ord := uint64(0); ord < 200; ord++ {
			sec := seed.SecretFor(ord)
			payload := QRPayload(host, sec.ID(), sec)
			code, err := qr.Encode(payload, eccLevel)
			if err != nil {
				t.Fatalf("%s: encode %q: %v", host, payload, err)
			}
			if v := (code.Size - 17) / 4; v > worst {
				worst = v
			}
		}
		t.Logf("%-28s -> QR version %d (%dx%d modules), %.4f in/module on a 0.75in square",
			host, worst, 17+4*worst, 17+4*worst, 0.75/float64(17+4*worst))
		if worst > maxVersion {
			t.Errorf("%s produces QR version %d, over the version-%d budget: "+
				"either shorten the host or field-test the denser code first", host, worst, maxVersion)
		}
	}
}

func TestSecretEncodingIsTwentyTwoCharacters(t *testing.T) {
	// Pinned because it is the term in the QR budget this package controls.
	seed := testSeed(t)
	for ord := uint64(0); ord < 100; ord++ {
		if got := len(seed.SecretFor(ord).Encode()); got != 22 {
			t.Fatalf("ordinal %d: encoded secret is %d characters, want 22", ord, got)
		}
	}
}

func TestSecretEncodingIsURLSafe(t *testing.T) {
	// A '+' or '/' in the fragment would be re-encoded by some scanners and
	// silently corrupt the secret.
	const safe = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	seed := testSeed(t)
	for ord := uint64(0); ord < 2000; ord++ {
		for _, c := range seed.SecretFor(ord).Encode() {
			if !strings.ContainsRune(safe, c) {
				t.Fatalf("ordinal %d: encoded secret contains %q", ord, c)
			}
		}
	}
}

func TestPrivateKeysAreDistinctPerTag(t *testing.T) {
	seed := testSeed(t)
	seen := map[string]bool{}
	for ord := uint64(0); ord < 2000; ord++ {
		k := seed.SecretFor(ord).PrivateKey().PubKey().ToDERHex()
		if seen[k] {
			t.Fatalf("ordinal %d reused a tag key", ord)
		}
		seen[k] = true
	}
}
