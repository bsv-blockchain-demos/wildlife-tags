package web

import (
	"github.com/bsv-blockchain-demos/wildlife-tags/internal/tagscript"
	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/bsv-blockchain/go-sdk/script"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/bsv-blockchain-demos/wildlife-tags/internal/record"
)

// TestPagesDoNotClaimTheChainProvesLocation is a guard on the copy, not the
// code, and it is here because this is the claim the whole application is most
// tempted to overstate.
//
// The chain proves when a record was written, that it has not been altered
// since, and that whoever wrote it held the physical tag. It proves nothing
// about where the phone was standing: browser geolocation is self-reported and
// trivially spoofed. A page that blurred those together would be selling a
// fishery-management dataset on a guarantee it does not have.
//
// rule-110-arcade guards its explainer the same way, for the same reason.
func TestPagesDoNotClaimTheChainProvesLocation(t *testing.T) {
	forbidden := []struct {
		pattern *regexp.Regexp
		why     string
	}{
		{regexp.MustCompile(`(?i)prov(es|en|able)[^.]{0,40}\b(location|position|where)\b`),
			"the chain cannot prove a position; it is an attestation"},
		{regexp.MustCompile(`(?i)\bverified\s+(gps|location|position|coordinates)\b`),
			"a GPS fix is not verified by anything here"},
		{regexp.MustCompile(`(?i)\bproof\s+of\s+(location|position)\b`),
			"there is no proof of location"},
		{regexp.MustCompile(`(?i)\btamper[- ]?proof\b`),
			"tamper-evident, not tamper-proof"},
		{regexp.MustCompile(`(?i)\bcannot\s+be\s+faked\b`),
			"a self-reported fix can be faked"},
	}

	pages := htmlPages(t)
	if len(pages) == 0 {
		t.Fatal("no HTML pages were scanned; the guard is not actually guarding anything")
	}

	for name, body := range pages {
		text := strings.Join(strings.Fields(body), " ")
		for _, f := range forbidden {
			if m := f.pattern.FindString(text); m != "" {
				t.Errorf("%s claims more than the chain supports (%q): %s", name, m, f.why)
			}
		}
	}
}

// TestEveryPublicPageCarriesTheHonestyPanel makes the disclosure structural
// rather than something a redesign can quietly drop.
func TestEveryPublicPageCarriesTheHonestyPanel(t *testing.T) {
	pages := htmlPages(t)
	// about.html makes the most claims of any page in the application -- it
	// exists to persuade -- so it carries the disclosure too. A pitch page
	// without one is exactly where overstatement creeps in.
	for _, name := range []string{"index.html", "redeem.html", "about.html"} {
		body, ok := pages[name]
		if !ok {
			t.Errorf("%s is missing", name)
			continue
		}
		if !strings.Contains(body, `class="honesty"`) {
			t.Errorf("%s has no honesty panel: every page that makes a claim about the chain must also state what it does not prove", name)
		}
		// Normalised, because the phrase wraps across source lines.
		flat := strings.ToLower(strings.Join(strings.Fields(body), " "))
		if !strings.Contains(flat, "taken on trust") {
			t.Errorf("%s does not say which parts are taken on trust", name)
		}
	}
}

// TestTheRedemptionPageVerifiesBeforeSigning pins the safety property the whole
// in-browser signing design exists for.
//
// The page holds the tag's private key and signs a transaction the server
// built. That is only defensible if the page checks the transaction pays the
// crabber first -- otherwise the server could hand it anything. Without this
// ordering, in-browser signing is theatre.
func TestTheRedemptionPageVerifiesBeforeSigning(t *testing.T) {
	js := readStatic(t, "static/redeem.js")

	for _, want := range []string{"verifyPayout", "derivePublicKey", "does not go to your wallet"} {
		if !strings.Contains(js, want) {
			t.Errorf("redeem.js no longer contains %q; the pre-signing payout check may have been removed", want)
		}
	}

	verifyAt := strings.Index(js, "await verifyPayout(")
	signAt := strings.Index(js, "signTagInput(tx")
	if verifyAt < 0 || signAt < 0 {
		t.Fatal("could not locate the verify and sign calls in redeem.js")
	}
	if verifyAt > signAt {
		t.Fatal("redeem.js signs the transaction before checking that it pays the crabber")
	}
}

// TestTheSecretNeverLeavesTheBrowser guards the other half of the design: the
// tag secret is read from the URL fragment precisely so it never reaches the
// server, and posting it anywhere would undo that.
func TestTheSecretNeverLeavesTheBrowser(t *testing.T) {
	js := readStatic(t, "static/redeem.js")
	for _, forbidden := range []string{"state.secret,", "secret: state.secret", "tag_secret", "state.tagKey.toString()"} {
		if strings.Contains(js, forbidden) {
			t.Errorf("redeem.js appears to send the tag secret to the server (%q)", forbidden)
		}
	}
}

// TestTheVendoredSDKIsPresent catches the failure mode where the bundle is
// dropped from the build: the page would load, the form would work, and
// signing would fail only at the last step with an unhelpful error.
func TestTheVendoredSDKIsPresent(t *testing.T) {
	body, err := fs.ReadFile(staticFS, "static/vendor/bsv-sdk.js")
	if err != nil {
		t.Fatalf("the vendored @bsv/sdk bundle is missing: %v", err)
	}
	if len(body) < 100_000 {
		t.Fatalf("the vendored bundle is %d bytes, which is too small to be @bsv/sdk", len(body))
	}
	if !strings.Contains(string(body[:400]), "bsv") {
		t.Error("the vendored bundle does not expose the global the pages expect")
	}
}

func htmlPages(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		body, rerr := fs.ReadFile(staticFS, path)
		if rerr != nil {
			return rerr
		}
		out[strings.TrimPrefix(path, "static/")] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("walk static: %v", err)
	}
	return out
}

func readStatic(t *testing.T, path string) string {
	t.Helper()
	body, err := fs.ReadFile(staticFS, path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// TestTheBrowserAttestsUnderTheSameDerivationAsTheServer guards the seam that
// broke in production: the pages sign a record and the server verifies it, and
// the two derive the signing key independently. If they drift, every redemption
// fails with "attestation signature does not verify" and neither side looks
// wrong on its own.
//
// The counterparty is the part that matters. A wallet always derives a BRC-42
// child rather than signing with the identity key, so the record's identity has
// to be re-derivable by the verifier -- which only holds for "anyone", whose
// private key is the publicly known value 1. Under "self" the derivation
// depends on a secret only the signer has.
func TestTheBrowserAttestsUnderTheSameDerivationAsTheServer(t *testing.T) {
	for _, page := range []string{"static/redeem.js", "static/admin.js"} {
		js := readStatic(t, page)

		if !strings.Contains(js, "wildtag observation") {
			t.Errorf("%s no longer signs under the %q protocol the server verifies with",
				page, record.AttestProtocol.Protocol)
		}
		if !strings.Contains(js, "counterparty: 'anyone'") {
			t.Errorf("%s does not attest with counterparty 'anyone'; the server will not be able to re-derive the signing key", page)
		}
		// 'self' anywhere in an attestation is the exact regression.
		if strings.Contains(js, "counterparty: 'self'") {
			t.Errorf("%s attests with counterparty 'self'; only the signer could verify that", page)
		}
	}
}

// TestTheAttestationProtocolMatchesTheClient pins the protocol string itself,
// so renaming it on one side fails here rather than in a marsh.
func TestTheAttestationProtocolMatchesTheClient(t *testing.T) {
	if record.AttestProtocol.Protocol != "wildtag observation" {
		t.Fatalf("the server signs under %q but the pages hardcode %q",
			record.AttestProtocol.Protocol, "wildtag observation")
	}
	if int(record.AttestProtocol.SecurityLevel) != 2 {
		t.Fatalf("security level is %d; the pages send 2", record.AttestProtocol.SecurityLevel)
	}
}

// TestTheClientSignsUnderTheCanonicalTagID guards the fix for an arming failure
// that had no visible cause: the console signed under the tag id as typed
// ("JTZ-DQT3"), while the server derives under the canonical form the record
// carries ("JTZDQT3"). Both pages must take the id from the server's response
// rather than from user input or a URL they parsed.
func TestTheClientSignsUnderTheCanonicalTagID(t *testing.T) {
	for _, page := range []string{"static/admin.js", "static/redeem.js"} {
		js := readStatic(t, page)
		if !strings.Contains(js, "keyID: canonicalTagID") {
			t.Errorf("%s does not sign under the canonical tag id the server returned", page)
		}
		if strings.Contains(js, "keyID: form.tag_id") {
			t.Errorf("%s signs under the typed tag id; a dash in it derives the wrong key", page)
		}
	}
}

// TestTheClientSignsThePayloadNotItsDigest guards the double-hash bug.
//
// createSignature hashes `data` before signing, so a page that passes a digest
// signs it twice and the attestation cannot verify. The pages must hand over
// the payload bytes and let the wallet hash once.
func TestTheClientSignsThePayloadNotItsDigest(t *testing.T) {
	for _, page := range []string{"static/redeem.js", "static/admin.js"} {
		js := readStatic(t, page)
		// Anchor on the attestation call itself. The protocol constant may be
		// declared far from it, so searching from there finds the wrong text.
		i := strings.Index(js, "ATTEST_PROTOCOL,")
		if i < 0 {
			t.Fatalf("%s no longer makes an attestation call", page)
		}
		window := js[i:min(i+1200, len(js))] // generous: these calls carry long explanatory comments
		if !strings.Contains(window, "data: Array.from(payloadBytes)") &&
			!strings.Contains(window, "data: Array.from(hexToBytes(preview.observation))") {
			t.Errorf("%s does not pass the raw payload to createSignature; a digest would be hashed twice", page)
		}
		if strings.Contains(window, "data: digest") || strings.Contains(window, "data: Array.from(digest)") {
			t.Errorf("%s passes a digest to createSignature; the wallet hashes it again", page)
		}
	}
}

// TestTheAboutPageCitesRatherThanAsserts guards the page written to persuade.
//
// Its whole argument rests on findings that are not ours -- SCDNR's own review
// of reward tagging, and the fisheries literature on why rewards fail to reach
// finders. A claim about the current programme that does not say where it comes
// from is a claim a reviewer is entitled to dismiss, and inventing a favourable
// statistic would discredit everything around it.
func TestTheAboutPageCitesRatherThanAsserts(t *testing.T) {
	about, ok := htmlPages(t)["about.html"]
	if !ok {
		t.Fatal("about.html is missing")
	}
	flat := strings.ToLower(strings.Join(strings.Fields(about), " "))

	// The load-bearing claims must be attributed, not asserted.
	for _, want := range []struct{ claim, attribution string }{
		{"fewer than half", "scdnr's own review"},
		{"weeks pass", "the fisheries literature"},
	} {
		if !strings.Contains(flat, want.claim) {
			t.Errorf("the page no longer makes the %q claim", want.claim)
			continue
		}
		if !strings.Contains(flat, want.attribution) {
			t.Errorf("the %q claim is no longer attributed to %q", want.claim, want.attribution)
		}
	}

	// A pitch page must not quietly promise what the rest of the application is
	// careful to disclaim.
	for _, forbidden := range []string{
		"guaranteed", "100% of tags", "eliminates fraud", "impossible to cheat",
	} {
		if strings.Contains(flat, forbidden) {
			t.Errorf("about.html claims %q, which the design does not support", forbidden)
		}
	}

	// And it must be honest about the one thing an adopter most wants to hear
	// is easy: that this runs beside the existing programme rather than
	// replacing it.
	if !strings.Contains(flat, "keep the phone number") {
		t.Error("the page does not say the existing phone-number route can stay; that is the whole low-risk argument")
	}
}

// TestTheAboutPageIsReachable stops the page existing but being unlinked, which
// is the usual fate of a page written once and never navigated to.
func TestTheAboutPageIsReachable(t *testing.T) {
	for _, page := range []string{"index.html", "redeem.html"} {
		body, ok := htmlPages(t)[page]
		if !ok {
			t.Fatalf("%s is missing", page)
		}
		if !strings.Contains(body, `href="/about"`) {
			t.Errorf("%s does not link to /about", page)
		}
	}
}

// TestSchemaDrivesTheForms is what keeps this application from being blue-crab
// shaped again.
//
// Before GET /api/schema existed, the crab's sex codes, moult stages, the
// five-inch minimum and the 50-260 mm plausible range were written out in four
// places -- redeem.html, admin.html, redeem.js and admin.js -- plus a Go file.
// Adding a species meant finding all five, and a page that missed one would
// tell somebody it was legal to keep an animal the server then refused.
//
// So: no page may name a species' vocabulary or a legal threshold. They come
// from the schema or they do not appear.
func TestSchemaDrivesTheForms(t *testing.T) {
	// Codes and numbers that belong to one species' profile and to nothing
	// else. Each is a string that only makes sense if a page has hardcoded
	// what the schema is supposed to own.
	forbidden := []string{
		"PEELER_WHITE", "PEELER_PINK", "PEELER_RED",
		`value="FI"`, `value="FM"`, `value="FS"`,
		"Carapace width", "carapace",
		"127", // South Carolina's five-inch minimum, in millimetres
	}

	pages := map[string]string{}
	for name, body := range htmlPages(t) {
		pages[name] = stripComments(body)
	}
	// The schema module and the canonical encoder are included too: they are
	// allowed to know about field shapes, but not about any species.
	for _, js := range []string{"redeem.js", "admin.js", "schema.js", "canonical.js"} {
		pages[js] = stripComments(readStatic(t, "static/"+js))
	}
	// about.html is prose written to persuade, and naming the pilot species in
	// it is the point rather than a leak.
	delete(pages, "about.html")

	for name, body := range pages {
		for _, bad := range forbidden {
			if strings.Contains(body, bad) {
				t.Errorf("%s hardcodes %q, which belongs to a species profile; it must come from GET /api/schema",
					name, bad)
			}
		}
	}

	// And the two forms must actually be built from it.
	for _, page := range []string{"redeem.js", "admin.js"} {
		if !strings.Contains(pages[page], "Schema.renderFields") {
			t.Errorf("%s does not build its form from the schema", page)
		}
		if !strings.Contains(pages[page], "Schema.read") {
			t.Errorf("%s does not read its form back through the schema", page)
		}
	}
	if !strings.Contains(pages["redeem.js"], "Schema.mustRelease") {
		t.Error("redeem.js does not check the profile's release rules; it would have to hardcode them instead")
	}
	if !strings.Contains(pages["admin.js"], "Schema.notTaggable") {
		t.Error("admin.js does not check the profile's tagging rules; it would have to hardcode them instead")
	}
}

// stripComments removes JavaScript line comments and HTML comments.
//
// The scan above is for hardcoded values, not for words. Several of these files
// carry comments explaining precisely why they do not hardcode a carapace width
// or a five-inch minimum, and failing on those would push the explanation out of
// the code that needs it.
func stripComments(body string) string {
	var out strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], "http") {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	text := out.String()
	for {
		start := strings.Index(text, "<!--")
		if start < 0 {
			break
		}
		end := strings.Index(text[start:], "-->")
		if end < 0 {
			text = text[:start]
			break
		}
		text = text[:start] + text[start+end+3:]
	}
	return text
}

// TestTheSchemaIsCacheableOffline pins the property a field app depends on.
//
// The pages that use the schema are opened in marshes. A form that cannot
// render until the server answers is a form that does not work where it is
// used, so the endpoint must carry an ETag and the client must keep a copy.
func TestTheSchemaIsCacheableOffline(t *testing.T) {
	js := readStatic(t, "static/schema.js")
	if !strings.Contains(js, "If-None-Match") {
		t.Error("schema.js does not revalidate with an ETag; every load would refetch the whole document")
	}
	if !strings.Contains(js, "localStorage") {
		t.Error("schema.js keeps no cached copy, so a phone with no signal cannot render a form")
	}
	if !strings.Contains(js, "304") {
		t.Error("schema.js does not handle a not-modified response")
	}
}

// TestTheAboutPageShowsTheScriptWeActuallyBuild keeps a technical explanation
// honest.
//
// The about page prints the locking script, opcode by opcode, because a
// reviewer who reads that far wants to know what "on chain" concretely means
// here. A printed script that has drifted from the one the program builds is
// worse than no script at all: it is a specific, checkable claim that happens
// to be false, and it would discredit everything around it.
//
// So the page is checked against a real lock rather than against a copy of
// itself.
func TestTheAboutPageShowsTheScriptWeActuallyBuild(t *testing.T) {
	about, ok := htmlPages(t)["about.html"]
	if !ok {
		t.Fatal("about.html is missing")
	}
	flat := strings.Join(strings.Fields(about), " ")

	// A real record, in a real lock, with the field count the format uses.
	key, err := ec.NewPrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	fields := make([][]byte, record.FieldCount)
	for i := range fields {
		fields[i] = []byte{byte(i + 1)}
	}
	lock, err := tagscript.Lock(key.PubKey(), key.PubKey(), fields)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	chunks, err := script.DecodeScript(*lock)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The opcodes the real script ends up with, in order.
	var ops []string
	for _, c := range chunks {
		switch c.Op {
		case script.OpCHECKSIGVERIFY:
			ops = append(ops, "OP_CHECKSIGVERIFY")
		case script.OpCHECKSIG:
			ops = append(ops, "OP_CHECKSIG")
		case script.Op2DROP:
			ops = append(ops, "OP_2DROP")
		case script.OpDROP:
			ops = append(ops, "OP_DROP")
		}
	}

	// Every one of them must appear on the page, in the same order and the same
	// number of times. A page showing three OP_2DROPs for a nine-field record
	// is describing a script that does not exist.
	cursor := 0
	for i, op := range ops {
		idx := strings.Index(flat[cursor:], op)
		if idx < 0 {
			t.Fatalf("the page does not show %s at position %d of the real script (%v)", op, i, ops)
		}
		cursor += idx + len(op)
	}

	// And the magic and version it prints have to be the ones records carry.
	if !strings.Contains(flat, record.Magic) {
		t.Errorf("the page does not show the record magic %q", record.Magic)
	}
	if !strings.Contains(flat, `"`+record.Version+`"`) {
		t.Errorf("the page does not show the current format version %q", record.Version)
	}
}
