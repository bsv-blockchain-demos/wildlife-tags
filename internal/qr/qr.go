// Package qr renders the physical artefact the whole program depends on: a
// sheet of tags, each carrying a QR code and a human-readable id.
//
// There is no PDF library here on purpose. The sheet is HTML with the codes
// embedded as data URIs, and the printer is the browser's own print dialog.
// That keeps the dependency list at one pure-Go QR encoder, keeps CGO off, and
// gives whoever is printing a preview before they commit a sheet of adhesive
// tag stock to it.
package qr

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"image"
	"image/color"
	"image/png"
	"io"

	"rsc.io/qr"
)

// Level is the error correction level.
//
// M gives 38% redundancy, which is the level these codes ship at. A crab tag
// spends months in an estuary: it gets fouled, abraded and bent, and a code
// that scans perfectly on paper has to survive all of that. L would fit a
// denser payload; it would not survive the barnacles.
const Level = qr.M

// MaxVersion is the largest QR version a crab tag may carry.
//
// SCDNR's blue crab tags are roughly one inch by two, wired through the lateral
// spines, which leaves about a three-quarter-inch square. Version 5 is 37x37
// modules, about 0.020 inches each at that size -- around the limit for a phone
// camera reading a wet, fouled tag at arm's length. Anything denser needs a
// field trial before it ships, not a code change.
const MaxVersion = 5

// Code is a rendered QR code.
type Code struct {
	PNG     []byte
	Size    int // modules of code, excluding the quiet zone
	Version int
	// PixelsPerSide is the rendered image's width, which includes the quiet
	// zone. See moduleScale for why it is not Size times the scale.
	PixelsPerSide int
}

// moduleScale is how many image pixels each QR module gets.
//
// The library defaults to 8, which for a version-4 code renders 328 pixels.
// Printed at the 0.75 inch a crab tag can spare that is 437 dpi -- under the
// 600 dpi these tags are specified at, and the shortfall lands exactly where it
// hurts, on the fine module edges a phone camera needs to resolve through
// seawater fouling. 16 doubles it to 875 dpi. The cost is nil: a QR code is
// two-tone with long runs, so PNG compresses it to a few hundred bytes either
// way.
const moduleScale = 16

// quietZoneModules is the all-white border the QR specification requires on
// every side. It is part of the rendered image rather than something the
// caller is trusted to leave room for, because a code butted against printed
// text does not scan and the failure looks like a bad camera rather than a bad
// layout.
const quietZoneModules = 4

// Encode renders a payload, refusing anything too dense for a crab tag.
func Encode(payload string) (*Code, error) {
	code, err := qr.Encode(payload, Level)
	if err != nil {
		return nil, fmt.Errorf("qr: encode %q: %w", payload, err)
	}
	version := (code.Size - 17) / 4
	if version > MaxVersion {
		return nil, fmt.Errorf(
			"qr: %q needs QR version %d (%dx%d modules), over the version-%d budget for a crab tag; shorten the public URL",
			payload, version, code.Size, code.Size, MaxVersion)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, render(code)); err != nil {
		return nil, fmt.Errorf("qr: render png: %w", err)
	}
	return &Code{
		PNG:           buf.Bytes(),
		Size:          code.Size,
		Version:       version,
		PixelsPerSide: (code.Size + 2*quietZoneModules) * moduleScale,
	}, nil
}

// DataURI renders the code for embedding directly in the sheet, so the printed
// page has no external requests to fail.
func (c *Code) DataURI() template.URL {
	return template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG)) //nolint:gosec // base64 of our own PNG
}

// SheetTag is one tag on a printed sheet.
type SheetTag struct {
	TagID    string
	Display  string
	Ordinal  uint64
	Payload  string
	Code     *Code
	Position int
}

// Sheet is a printable run of tags.
type Sheet struct {
	BatchID   string
	CreatedAt string
	PublicURL string
	Tags      []SheetTag
}

// Render writes the printable sheet.
func Render(w io.Writer, sheet Sheet) error {
	if err := sheetTemplate.Execute(w, sheet); err != nil {
		return fmt.Errorf("qr: render sheet: %w", err)
	}
	return nil
}

var sheetTemplate = template.Must(template.New("sheet").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Crab tags &mdash; batch {{.BatchID}}</title>
<style>
  /* This sheet has two jobs with different requirements, so it has two layouts.
     On paper every dimension is physical: each cell is the printable face of a
     1x2in plastic tag wired through a blue crab's lateral spines, and getting
     that wrong wastes a sheet of adhesive stock. On a monitor those same inches
     resolve to 96 CSS pixels each, which renders a 0.75in code at 72px -- too
     small to check before committing to a print run.

     So screen scales everything by --preview, and print pins it back to 1. */
  :root {
    --tag-w: 2in;
    --tag-h: 1in;
    --qr: 0.86in;      /* of which ~0.7in is code; the rest is the quiet zone */
    --preview: 2.6;
  }

  @page { size: letter; margin: 0.4in; }

  body {
    font-family: ui-monospace, "SF Mono", Menlo, Consolas, monospace;
    margin: 0; padding: 16px; color: #000; background: #fff;
  }
  header { padding: 0 0 12pt; border-bottom: 1pt solid #000; margin-bottom: 12pt; }
  h1 { font-size: 12pt; margin: 0 0 4pt; }
  .meta { font-size: 8pt; color: #444; }

  .grid { display: flex; flex-wrap: wrap; gap: 8pt; }

  .tag {
    width: var(--tag-w); height: var(--tag-h);
    border: 0.5pt dashed #999; box-sizing: border-box;
    display: flex; align-items: center; gap: 0.06in;
    padding: 0.05in;
    break-inside: avoid; page-break-inside: avoid;
  }
  .tag img {
    width: var(--qr); height: var(--qr); flex: none;
    /* Never smooth a QR code: interpolation blurs the module edges a scanner
       is looking for. */
    image-rendering: pixelated;
    image-rendering: crisp-edges;
  }
  .id { font-size: 11pt; font-weight: 700; letter-spacing: 0.5pt; line-height: 1.1; }
  .sub { font-size: 5.5pt; color: #555; margin-top: 3pt; line-height: 1.35; }

  /* Base appearance for the two callouts. These MUST be declared before the
     media queries below: they carry the same specificity, so a base rule
     placed afterwards would win on source order and the screen-only banner
     could never appear. */
  .screen-only {
    display: none;
    margin: 0 0 14pt; padding: 8pt 10pt;
    border: 1pt solid #000; background: #f4f4f4; font-size: 8pt; line-height: 1.5;
  }
  .warn { margin-top: 14pt; font-size: 8pt; border: 1pt solid #000; padding: 8pt; line-height: 1.5; }

  /* On screen: scale the physical layout up so it can actually be inspected,
     and say plainly that what is on the monitor is not the printed size. */
  @media screen {
    .grid { gap: calc(8pt * var(--preview)); }
    .tag {
      width: calc(var(--tag-w) * var(--preview));
      height: calc(var(--tag-h) * var(--preview));
      gap: calc(0.06in * var(--preview));
      padding: calc(0.05in * var(--preview));
      border-width: 1px;
    }
    .tag img { width: calc(var(--qr) * var(--preview)); height: calc(var(--qr) * var(--preview)); }
    .id  { font-size: calc(11pt * var(--preview) * 0.62); }
    .sub { font-size: calc(5.5pt * var(--preview) * 0.62); }
    .screen-only { display: block; }
  }

  @media print {
    .screen-only { display: none; }
    .warn { page-break-before: always; }
  }

</style>
</head>
<body>
<header>
  <h1>SCDNR blue crab tags &mdash; batch {{.BatchID}}</h1>
  <div class="meta">{{len .Tags}} tags &middot; created {{.CreatedAt}} &middot; {{.PublicURL}}</div>
</header>

<div class="screen-only">
  <strong>This is a preview, not the printed size.</strong> Tags are shown
  enlarged so the codes can be checked on a monitor; a browser renders one inch
  as 96&nbsp;pixels, which would otherwise show each code at about 72&nbsp;pixels
  across. Printing restores the physical 1&times;2&nbsp;inch tag face
  automatically &mdash; print to PDF first and measure one if you want to be sure
  before committing a sheet of stock.
</div>

<div class="grid">
{{range .Tags}}
  <div class="tag">
    <img src="{{.Code.DataURI}}" alt="QR code for tag {{.Display}}" width="{{.Code.PixelsPerSide}}" height="{{.Code.PixelsPerSide}}">
    <div>
      <div class="id">{{.Display}}</div>
      <div class="sub">SCDNR BLUE CRAB<br>REWARD &mdash; SCAN OR REPORT</div>
    </div>
  </div>
{{end}}
</div>

<div class="warn">
  <strong>Before printing.</strong> The QR codes on this sheet point at
  {{.PublicURL}}. That address is baked into the printed tag: if it changes,
  every tag on this sheet stops working, and there is no way to fix one once it
  is on an animal.
  <br><br>
  Print on waterproof adhesive stock at 600&nbsp;dpi or better, with scaling set
  to 100% &mdash; &ldquo;fit to page&rdquo; will resize the tags and they will no
  longer match the plastic. These tags spend months in an estuary; a code printed
  on ordinary paper will not last a season.
  <br><br>
  <strong>Every code here is a bearer instrument.</strong> Anyone who can
  photograph a tag can redeem it, which is the design &mdash; but it also means a
  stack of unused tags is a stack of unclaimed rewards, and so is a copy of this
  page left in a printer spool.
</div>
</body>
</html>
`))

// render draws the code, one module to a block of moduleScale pixels, inside a
// quiet zone.
//
// It exists because rsc.io/qr's own Code.Image() does not work. Its Bounds()
// reports (Size+8)*Scale pixels, but the At(x, y) behind it passes raw *pixel*
// coordinates straight to Code.Black(x, y), which compares them against Size in
// *modules* and indexes the bitmap accordingly. Scale is therefore never
// applied and the quiet-zone offset is never added: everything outside the
// top-left Size-by-Size pixels comes back white.
//
// The result is a mostly blank PNG with a Size-pixel code in one corner, which
// is a particularly nasty failure because the image is a valid PNG, the data
// URI is well-formed, and the code in the corner is a real scannable QR -- just
// far too small to read. Raising Scale makes it worse rather than better, since
// the code stays Size pixels while the canvas grows.
//
// A paletted image keeps this cheap: two colours, long runs, so a 656-pixel
// code encodes to a couple of kilobytes.
func render(code *qr.Code) *image.Paletted {
	side := (code.Size + 2*quietZoneModules) * moduleScale
	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{
		color.Gray{Y: 0xFF}, // 0 = white, and the zero value, so the quiet zone
		color.Gray{Y: 0x00}, // 1 = black
	})

	for my := 0; my < code.Size; my++ {
		for mx := 0; mx < code.Size; mx++ {
			if !code.Black(mx, my) {
				continue
			}
			x0 := (mx + quietZoneModules) * moduleScale
			y0 := (my + quietZoneModules) * moduleScale
			for y := y0; y < y0+moduleScale; y++ {
				row := y * img.Stride
				for x := x0; x < x0+moduleScale; x++ {
					img.Pix[row+x] = 1
				}
			}
		}
	}
	return img
}
