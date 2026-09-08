package ogimage

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	displayTTF, err := os.ReadFile("../web/static/vendor/fonts/google-sans-flex-latin.ttf")
	if err != nil {
		t.Fatal(err)
	}
	bodyTTF, err := os.ReadFile("../web/static/vendor/fonts/roboto-latin.ttf")
	if err != nil {
		t.Fatal(err)
	}
	icons := map[string][]byte{}
	dir := "../web/static/vendor/animals/og"
	for _, name := range []string{"tag", "blue-crab", "red-drum", "crab-generic", "fish-generic", "sea-turtle", "shark", "bird-generic"} {
		b, err := os.ReadFile(filepath.Join(dir, name+"-white.png"))
		if err != nil {
			t.Fatal(err)
		}
		icons[name] = b
	}
	r, err := New(displayTTF, bodyTTF, icons)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestRenderProducesAValidImageOfTheRightSize guards the property every
// caller depends on without inspecting pixels: given a card, Render
// produces a decodable 1200x630 PNG, not an error, a truncated stream, or a
// panic partway through drawing (which a nil icon map entry or a font that
// failed to parse would otherwise turn into a 500 on a real request).
func TestRenderProducesAValidImageOfTheRightSize(t *testing.T) {
	r := testRenderer(t)

	cases := map[string]Card{
		"home, no stats": {
			GradientFrom: "#0f5c56", GradientTo: "#2dd4bf", IconKey: "tag",
			Eyebrow: "SCDNR Wildlife Tags",
			Title:   "Tagged animals, reported by whoever finds them, paid on the spot.",
		},
		"tag with stats": {
			GradientFrom: "#0f5c56", GradientTo: "#2dd4bf", IconKey: "blue-crab",
			Eyebrow: "Atlantic blue crab", Title: "Old Bertha", Subtitle: "Tag K2M9Q7C",
			Stats: []Stat{{Value: "214", Label: "days"}, {Value: "6.4 km", Label: "travelled"}, {Value: "3", Label: "sightings"}},
		},
		"unknown icon key falls back gracefully": {
			GradientFrom: "#9a3412", GradientTo: "#fb923c", IconKey: "not-a-real-species",
			Title: "An unnamed find",
		},
		"empty card": {},
	}

	for name, card := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := r.Render(&buf, card); err != nil {
				t.Fatalf("Render: %v", err)
			}
			img, err := png.Decode(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("the output is not a valid PNG: %v", err)
			}
			if b := img.Bounds(); b.Dx() != Width || b.Dy() != Height {
				t.Errorf("image is %dx%d, want %dx%d", b.Dx(), b.Dy(), Width, Height)
			}
		})
	}
}

// TestNewRejectsAnUnparsableFont catches a corrupt or truncated font file at
// startup rather than on the first request that needs it.
func TestNewRejectsAnUnparsableFont(t *testing.T) {
	if _, err := New([]byte("not a font"), []byte("also not a font"), nil); err == nil {
		t.Error("New accepted garbage as a font file")
	}
}
