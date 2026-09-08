// Package ogimage renders the 1200x630 share-preview images that appear
// when a WildTag link is pasted into Slack, iMessage, or a social feed.
//
// This has to be pure Go: the rest of the program is a single static
// binary with no JavaScript toolchain and no headless browser, and an
// image generator that shelled out to one would be the one place that
// stopped being true. github.com/fogleman/gg (built on golang.org/x/image
// and the vendored freetype rasteriser) draws shapes, gradients, and text
// entirely in-process.
//
// The composition is the same for every image: a warm neutral background,
// a cluster of soft gradient blobs bottom-right (the app's own gradient
// family, see style.css's --grad-* tokens), the wordmark top-left, a
// headline and subtitle on the left, and a gradient card on the right
// carrying a circular icon badge and, for a tag's own page, its journey
// stats. One layout, reused for the home page, the about page, and every
// tag's detail page, with different content each time.
package ogimage

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"strings"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
)

// Width and Height match Open Graph's recommended share-image size, 1.91:1.
const (
	Width  = 1200
	Height = 630
)

// Stat is one small pill on a tag's card: "214 days", "6.4 km", and so on.
type Stat struct {
	Value string
	Label string
}

// Card describes one image to render. Not every field is used by every
// caller: the home and about pages leave Stats empty, a freshly minted tag
// with no provenance yet leaves both Stats and Subtitle sparse.
type Card struct {
	// GradientFrom/To are hex colors, the same two stops style.css's
	// --grad-* tokens use, so a card's color always matches the one the
	// browser itself would have shown for that species or section.
	GradientFrom string
	GradientTo   string
	// IconKey selects a pre-rendered white silhouette: "blue-crab",
	// "red-drum", "crab-generic", "tag" (the brand mark, for pages with
	// no animal of their own), and so on. Empty draws no icon.
	IconKey string

	Eyebrow  string
	Title    string
	Subtitle string
	Stats    []Stat
}

// Renderer holds everything expensive to build once: parsed fonts and
// decoded icon images. Constructed a single time at startup and reused for
// every request.
type Renderer struct {
	displayTTF *truetype.Font
	bodyTTF    *truetype.Font
	icons      map[string]image.Image
}

// New parses the two vendored font files and decodes every icon PNG handed
// to it. The icon map's keys are what Card.IconKey selects.
func New(displayTTF, bodyTTF []byte, icons map[string][]byte) (*Renderer, error) {
	df, err := truetype.Parse(displayTTF)
	if err != nil {
		return nil, fmt.Errorf("ogimage: parse display font: %w", err)
	}
	bf, err := truetype.Parse(bodyTTF)
	if err != nil {
		return nil, fmt.Errorf("ogimage: parse body font: %w", err)
	}

	decoded := make(map[string]image.Image, len(icons))
	for name, b := range icons {
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("ogimage: decode icon %q: %w", name, err)
		}
		decoded[name] = img
	}

	return &Renderer{displayTTF: df, bodyTTF: bf, icons: decoded}, nil
}

func (r *Renderer) face(ttf *truetype.Font, points float64) font.Face {
	return truetype.NewFace(ttf, &truetype.Options{
		Size:    points,
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// Palette: the warm neutral background and the app's own brand colors, so
// the wordmark and the blob cluster read as the same product as the page
// the link points to.
const (
	bgColor    = "#f6f4ef"
	inkColor   = "#1a1d21"
	mutedColor = "#5c6470"
	brandFrom  = "#0f5c56"
	brandTo    = "#2dd4bf"
)

// blob is one soft decorative circle in the background cluster.
type blob struct {
	x, y, r  float64
	from, to string
	opacity  float64
}

// Render draws one Card and writes it as a PNG to w.
func (r *Renderer) Render(w io.Writer, c Card) error {
	dc := gg.NewContext(Width, Height)

	// Background.
	dc.SetHexColor(bgColor)
	dc.Clear()

	// A cluster of soft gradient circles, bottom-right, echoing the
	// reference this whole layout is modelled on -- overlapping blobs
	// behind the "screenshot". Colors come from the same family as the
	// card itself so the blobs and the card read as one palette rather
	// than a random decoration.
	for _, b := range []blob{
		{x: 1000, y: 600, r: 320, from: c.GradientFrom, to: c.GradientTo, opacity: 0.30},
		{x: 880, y: 420, r: 230, from: c.GradientTo, to: c.GradientFrom, opacity: 0.22},
		{x: 1180, y: 260, r: 190, from: brandFrom, to: brandTo, opacity: 0.16},
		{x: 260, y: 600, r: 200, from: c.GradientFrom, to: c.GradientTo, opacity: 0.10},
	} {
		grad := gg.NewRadialGradient(b.x, b.y, 0, b.x, b.y, b.r)
		grad.AddColorStop(0, withAlpha(b.from, b.opacity))
		grad.AddColorStop(1, withAlpha(b.to, 0))
		dc.SetFillStyle(grad)
		dc.DrawCircle(b.x, b.y, b.r)
		dc.Fill()
	}

	// Wordmark, top-left: the same tag-glyph-in-a-gradient-square as the
	// favicon, plus the brand name in the display face.
	r.drawLogo(dc, 72, 64)

	// Headline block, left column.
	textX := 72.0
	textWidth := 560.0
	y := 220.0

	if c.Eyebrow != "" {
		dc.SetFontFace(r.face(r.bodyTTF, 20))
		dc.SetHexColor(mutedColor)
		dc.DrawStringWrapped(upper(c.Eyebrow), textX, y, 0, 0, textWidth, 1.3, gg.AlignLeft)
		y += 34
	}

	dc.SetFontFace(r.face(r.displayTTF, 52))
	dc.SetHexColor(inkColor)
	wrapped := strings.Join(dc.WordWrap(c.Title, textWidth), "\n")
	_, titleH := dc.MeasureMultilineString(wrapped, 1.12)
	dc.DrawStringWrapped(c.Title, textX, y, 0, 0, textWidth, 1.12, gg.AlignLeft)
	y += titleH + 28

	if c.Subtitle != "" {
		dc.SetFontFace(r.face(r.bodyTTF, 24))
		dc.SetHexColor(mutedColor)
		dc.DrawStringWrapped(c.Subtitle, textX, y, 0, 0, textWidth, 1.35, gg.AlignLeft)
	}

	// The card: a big rounded gradient panel on the right, carrying the
	// icon badge and, when there are any, the stat pills -- the same
	// visual unit as .grad-card in the real page, just larger.
	r.drawCard(dc, c)

	return dc.EncodePNG(w)
}

// drawLogo draws the tag-in-a-gradient-square mark plus the wordmark, the
// same pairing used in the browser tab's favicon and the top bar's brand.
func (r *Renderer) drawLogo(dc *gg.Context, x, y float64) {
	const size = 40
	grad := gg.NewLinearGradient(x, y, x+size, y+size)
	grad.AddColorStop(0, hexColor(brandFrom))
	grad.AddColorStop(1, hexColor(brandTo))
	dc.SetFillStyle(grad)
	dc.DrawRoundedRectangle(x, y, size, size, size*0.22)
	dc.Fill()

	if icon, ok := r.icons["tag"]; ok {
		drawIconCentered(dc, icon, x+size/2, y+size/2, size*0.56)
	}

	dc.SetFontFace(r.face(r.displayTTF, 26))
	dc.SetHexColor(inkColor)
	dc.DrawStringAnchored("SCDNR Wildlife Tags", x+size+16, y+size/2, 0, 0.35)
}

// drawCard draws the gradient panel on the right: the icon badge, centered,
// and below it a row of stat pills when the caller supplied any.
func (r *Renderer) drawCard(dc *gg.Context, c Card) {
	const (
		cw, ch = 420.0, 420.0
		cx     = Width - 120 - cw
		cy     = (Height - ch) / 2
	)

	grad := gg.NewLinearGradient(cx, cy, cx+cw, cy+ch)
	grad.AddColorStop(0, hexColorOr(c.GradientFrom, brandFrom))
	grad.AddColorStop(1, hexColorOr(c.GradientTo, brandTo))
	dc.SetFillStyle(grad)
	dc.DrawRoundedRectangle(cx, cy, cw, ch, 32)
	dc.Fill()

	// The same soft highlight/vignette pairing as .grad-card-header in
	// the real page: a light source upper-left, a faint shadow opposite.
	hi := gg.NewRadialGradient(cx+cw*0.28, cy+ch*0.22, 0, cx+cw*0.28, cy+ch*0.22, cw*0.55)
	hi.AddColorStop(0, color.NRGBA{255, 255, 255, 80})
	hi.AddColorStop(1, color.NRGBA{255, 255, 255, 0})
	dc.SetFillStyle(hi)
	dc.DrawRoundedRectangle(cx, cy, cw, ch, 32)
	dc.Fill()
	lo := gg.NewRadialGradient(cx+cw*0.82, cy+ch*0.92, 0, cx+cw*0.82, cy+ch*0.92, cw*0.6)
	lo.AddColorStop(0, color.NRGBA{0, 0, 0, 36})
	lo.AddColorStop(1, color.NRGBA{0, 0, 0, 0})
	dc.SetFillStyle(lo)
	dc.DrawRoundedRectangle(cx, cy, cw, ch, 32)
	dc.Fill()

	badgeCY := cy + ch*0.38
	if len(c.Stats) > 0 {
		badgeCY = cy + ch*0.30
	}
	const badgeR = 92.0
	dc.SetColor(color.NRGBA{255, 255, 255, 56})
	dc.DrawCircle(cx+cw/2, badgeCY, badgeR)
	dc.Fill()
	dc.SetColor(color.NRGBA{255, 255, 255, 140})
	dc.SetLineWidth(2)
	dc.DrawCircle(cx+cw/2, badgeCY, badgeR)
	dc.Stroke()

	if icon, ok := r.icons[c.IconKey]; ok {
		drawIconCentered(dc, icon, cx+cw/2, badgeCY, badgeR*1.15)
	}

	if len(c.Stats) == 0 {
		return
	}

	// Stat pills: up to three, evenly spaced under the badge.
	n := len(c.Stats)
	if n > 3 {
		n = 3
	}
	pillW := (cw - 24*float64(n+1)) / float64(n)
	pillY := cy + ch - 108
	pillH := 84.0
	px := cx + 24
	for i := 0; i < n; i++ {
		dc.SetColor(color.NRGBA{255, 255, 255, 46})
		dc.DrawRoundedRectangle(px, pillY, pillW, pillH, 14)
		dc.Fill()

		dc.SetFontFace(r.face(r.displayTTF, 26))
		dc.SetColor(color.White)
		dc.DrawStringAnchored(c.Stats[i].Value, px+pillW/2, pillY+30, 0.5, 0.5)

		dc.SetFontFace(r.face(r.bodyTTF, 13))
		dc.SetColor(color.NRGBA{255, 255, 255, 210})
		dc.DrawStringAnchored(upper(c.Stats[i].Label), px+pillW/2, pillY+58, 0.5, 0.5)

		px += pillW + 24
	}
}

// drawIconCentered scales an icon (any aspect ratio) to fit inside a
// diameter-`size` circle and draws it centered at (cx, cy).
func drawIconCentered(dc *gg.Context, icon image.Image, cx, cy, size float64) {
	b := icon.Bounds()
	iw, ih := float64(b.Dx()), float64(b.Dy())
	scale := size / math.Max(iw, ih)
	w, h := iw*scale, ih*scale

	resized := resize(icon, int(w), int(h))
	dc.DrawImageAnchored(resized, int(cx), int(cy), 0.5, 0.5)
}

// resize is a minimal nearest-neighbor scaler. The icons are simple single-
// color silhouettes, so nearest-neighbor at these sizes (well over 100px)
// shows no visible artifacting, and it avoids pulling in a whole resampling
// library for one shape per image.
func resize(src image.Image, w, h int) image.Image {
	if w <= 0 || h <= 0 {
		return src
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := b.Min.Y + y*b.Dy()/h
		for x := 0; x < w; x++ {
			sx := b.Min.X + x*b.Dx()/w
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

func upper(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		out = append(out, c)
	}
	return string(out)
}

func withAlpha(hex string, alpha float64) color.Color {
	c := hexColor(hex)
	r, g, b, _ := c.RGBA()
	return color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(alpha * 255)}
}

func hexColor(hex string) color.Color {
	c, err := parseHex(hex)
	if err != nil {
		return color.Black
	}
	return c
}

func hexColorOr(hex, fallback string) color.Color {
	if hex == "" {
		hex = fallback
	}
	return hexColor(hex)
}

func parseHex(s string) (color.Color, error) {
	if len(s) != 7 || s[0] != '#' {
		return nil, fmt.Errorf("ogimage: %q is not a #rrggbb color", s)
	}
	var r, g, b int
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return nil, err
	}
	return color.NRGBA{uint8(r), uint8(g), uint8(b), 255}, nil
}
