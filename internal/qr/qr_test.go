package qr

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestATagPayloadEncodesWithinTheCrabTagBudget(t *testing.T) {
	code, err := Encode("https://bcrab.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if code.Version > MaxVersion {
		t.Fatalf("version %d exceeds the version-%d budget", code.Version, MaxVersion)
	}
	if code.Size != 17+4*code.Version {
		t.Fatalf("size %d does not match version %d", code.Size, code.Version)
	}
	if !bytes.HasPrefix(code.PNG, []byte{0x89, 'P', 'N', 'G'}) {
		t.Error("the rendered code is not a PNG")
	}
}

// TestAnOverlongPayloadIsRefusedWithAnActionableError is the guard that stops a
// long hostname silently producing tags too dense to scan once they are wired
// to an animal and in the water.
func TestAnOverlongPayloadIsRefusedWithAnActionableError(t *testing.T) {
	long := "https://" + strings.Repeat("a-very-long-subdomain.", 12) + "example.gov/t/K2M9Q7C#" + strings.Repeat("x", 22)
	_, err := Encode(long)
	if err == nil {
		t.Fatal("a payload far too dense for a crab tag was accepted")
	}
	if !strings.Contains(err.Error(), "shorten the public URL") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}

func TestTheSheetEmbedsItsCodesRatherThanLinkingThem(t *testing.T) {
	// A printed sheet that fetches images would print blank squares on a
	// machine that cannot reach the server.
	code, err := Encode("https://bcrab.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var buf bytes.Buffer
	err = Render(&buf, Sheet{
		BatchID: "B1", CreatedAt: "today", PublicURL: "https://bcrab.sc.gov",
		Tags: []SheetTag{{TagID: "K2M9Q7C", Display: "K2M-9Q7", Code: code, Position: 1}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "data:image/png;base64,") {
		t.Error("the sheet does not embed its QR codes")
	}
	if strings.Contains(out, "src=\"http") {
		t.Error("the sheet fetches something over the network")
	}
	if !strings.Contains(out, "K2M-9Q7") {
		t.Error("the sheet does not print the human-readable tag id")
	}
	// The sheet must warn about the three things that ruin a print run: a host
	// that will change, stock that will not survive an estuary, and a stack of
	// unused tags being a stack of unclaimed rewards.
	for _, warning := range []string{"bcrab.sc.gov", "waterproof", "unclaimed rewards"} {
		if !strings.Contains(strings.ToLower(out), strings.ToLower(warning)) {
			t.Errorf("the sheet does not mention %q", warning)
		}
	}
}

// TestTheSheetLabelsItselfWithTheBatchsOwnSpecies guards against a sheet
// printing one species' name on another species' tags. The template used to
// hardcode "blue crab" regardless of what Sheet.SpeciesCommon/SpeciesUpper
// said, which would have printed "BLUE CRAB" on a red drum batch -- a wrong
// label on a real animal, not a cosmetic bug.
func TestTheSheetLabelsItselfWithTheBatchsOwnSpecies(t *testing.T) {
	code, err := Encode("https://wildtag.dnr.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var buf bytes.Buffer
	err = Render(&buf, Sheet{
		BatchID: "B2", CreatedAt: "today", PublicURL: "https://wildtag.dnr.sc.gov",
		SpeciesCommon: "Red drum", SpeciesUpper: "RED DRUM",
		Tags: []SheetTag{{TagID: "K2M9Q7C", Display: "K2M-9Q7", Code: code, Position: 1}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Red drum") {
		t.Error("the sheet does not name its own species in the heading")
	}
	if !strings.Contains(out, "RED DRUM") {
		t.Error("the sheet does not label the tag face with its own species")
	}
	if strings.Contains(strings.ToLower(out), "crab") {
		t.Error("a red drum sheet mentions crab -- the species label is still hardcoded")
	}
}

func TestTheErrorCorrectionLevelSuitsAnEstuary(t *testing.T) {
	// Level M's redundancy is chosen for a tag that spends months being fouled
	// and abraded. Dropping to L would fit a denser payload and would not
	// survive the barnacles.
	if int(Level) != 1 {
		t.Fatalf("error correction level is %v, want M", Level)
	}
}

// TestTheCodePrintsAboveSixHundredDPI pins the print resolution.
//
// The library's default scale renders a version-4 code at 328 pixels, which
// over the 0.75 inch a crab tag can spare is 437 dpi -- below the 600 dpi these
// tags are specified at. The shortfall lands on the module edges, which is
// exactly what a phone camera needs to resolve through fouling, so it shows up
// as "sometimes won't scan" rather than as an obvious defect.
func TestTheCodePrintsAboveSixHundredDPI(t *testing.T) {
	const tagInches = 0.75
	const wantDPI = 600

	code, err := Encode("https://wildtag.dnr.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dpi := float64(code.PixelsPerSide) / tagInches
	t.Logf("%d modules + quiet zone -> %d px -> %.0f dpi at %.2fin",
		code.Size, code.PixelsPerSide, dpi, tagInches)
	if dpi < wantDPI {
		t.Errorf("printed resolution is %.0f dpi, below the %d dpi these tags are specified at", dpi, wantDPI)
	}
}

// TestTheQuietZoneIsInTheImage keeps the required white border inside the PNG
// rather than trusting the layout to leave room. A code butted against printed
// text does not scan, and the failure reads as a bad camera rather than a bad
// sheet.
func TestTheQuietZoneIsInTheImage(t *testing.T) {
	code, err := Encode("https://bcrab.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := (code.Size + 2*quietZoneModules) * moduleScale
	if code.PixelsPerSide != want {
		t.Fatalf("image is %d px, want %d (%d modules of code plus %d of quiet zone each side)",
			code.PixelsPerSide, want, code.Size, quietZoneModules)
	}
}

// TestTheSheetPrintsAtPhysicalSizeButPreviewsLarger is the fix for a real
// complaint: the sheet is dimensioned in inches for print, so a browser
// rendered every code at about 72 pixels and they were unreadable on screen.
// Screen scales up; print must stay pinned to the physical tag.
func TestTheSheetPrintsAtPhysicalSizeButPreviewsLarger(t *testing.T) {
	code, err := Encode("https://bcrab.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, Sheet{
		BatchID: "B1", CreatedAt: "today", PublicURL: "https://bcrab.sc.gov",
		Tags: []SheetTag{{TagID: "K2M9Q7C", Display: "K2M-9Q7", Code: code, Position: 1}},
	}); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()

	// The physical tag face is what a print run depends on.
	for _, want := range []string{"--tag-w: 2in", "--tag-h: 1in", "@page", "@media print"} {
		if !strings.Contains(out, want) {
			t.Errorf("the sheet no longer pins its printed dimensions (%q missing)", want)
		}
	}
	// And the screen preview has to exist, or the codes are unreviewable.
	if !strings.Contains(out, "@media screen") || !strings.Contains(out, "--preview") {
		t.Error("the sheet has no screen preview scaling; codes will render at about 72px")
	}
	// A preview that does not say it is a preview invites printing at the
	// wrong size, which wastes a sheet of adhesive tag stock.
	if !strings.Contains(out, "not the printed size") {
		t.Error("the preview does not warn that it is not printed size")
	}
	// Scaling on the print dialog would resize the tags off the plastic.
	if !strings.Contains(out, "100%") {
		t.Error("the sheet does not tell the operator to print without scaling")
	}
	// Smoothing a QR blurs exactly the edges a scanner looks for.
	if !strings.Contains(out, "pixelated") {
		t.Error("the sheet allows the browser to smooth the codes")
	}
}

// TestTheCodeFillsTheImage is the test that was missing, and its absence is why
// a badly broken sheet passed every check.
//
// rsc.io/qr's own Code.Image() reports a scaled size but never applies the
// scale: it hands pixel coordinates to a function expecting module
// coordinates, so everything outside the top-left Size-by-Size pixels comes
// back white. The output is a perfectly valid PNG, correctly dimensioned,
// carrying a real scannable QR code in one corner at about a twentieth of the
// intended size.
//
// Every earlier test passed on that output, because they checked the image was
// a PNG, that it was embedded, and how many pixels it claimed. None looked at
// the pixels. This one does.
func TestTheCodeFillsTheImage(t *testing.T) {
	code, err := Encode("https://bcrab.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(code.PNG))
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	b := img.Bounds()
	if b.Dx() != code.PixelsPerSide || b.Dy() != code.PixelsPerSide {
		t.Fatalf("image is %dx%d, want %d square", b.Dx(), b.Dy(), code.PixelsPerSide)
	}

	black := func(x, y int) bool {
		r, g, bl, _ := img.At(x, y).RGBA()
		return r < 0x8000 && g < 0x8000 && bl < 0x8000
	}

	// A QR code is roughly half dark. Anything far below that means most of the
	// image is blank.
	dark := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if black(x, y) {
				dark++
			}
		}
	}
	ratio := float64(dark) / float64(b.Dx()*b.Dy())
	t.Logf("%.1f%% of the image is dark", ratio*100)
	if ratio < 0.20 {
		t.Errorf("only %.1f%% of the image is dark; the code is not filling it", ratio*100)
	}

	// The finder pattern is a 7x7 solid square at the code's top-left corner,
	// inside the quiet zone. Its far corner must be dark, which is only true
	// once the module scale is actually applied.
	fx := (quietZoneModules)*moduleScale + moduleScale/2
	fy := fx
	far := (quietZoneModules+6)*moduleScale + moduleScale/2
	if !black(fx, fy) || !black(far, fy) || !black(fx, far) {
		t.Error("the finder pattern is not drawn at full scale; the module scale is being ignored")
	}

	// And the quiet zone really is quiet.
	if black(1, 1) || black(b.Max.X-2, b.Max.Y-2) {
		t.Error("the quiet zone contains dark pixels")
	}
}

// TestEveryModuleRowAndColumnIsRepresented catches the specific corner-only
// failure directly: dark pixels must appear across the whole image, not bunched
// into the first Size pixels.
func TestEveryModuleRowAndColumnIsRepresented(t *testing.T) {
	code, err := Encode("https://bcrab.sc.gov/t/K2M9Q7C#OvIhMOLKGhm4G8K-iUaCaw")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(code.PNG))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The bottom-left finder pattern sits near the far edge of the code. If the
	// scale is being ignored it lands inside the first Size pixels instead.
	x := quietZoneModules*moduleScale + moduleScale/2
	y := (quietZoneModules+code.Size-4)*moduleScale + moduleScale/2
	r, g, b, _ := img.At(x, y).RGBA()
	if !(r < 0x8000 && g < 0x8000 && b < 0x8000) {
		t.Errorf("no dark pixel at the bottom-left finder pattern (%d,%d); the code occupies only the top-left corner", x, y)
	}
}
