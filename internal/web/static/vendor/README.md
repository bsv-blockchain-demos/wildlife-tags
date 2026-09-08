# Vendored dependency

`bsv-sdk.js` is the UMD build of [`@bsv/sdk`](https://www.npmjs.com/package/@bsv/sdk)
v2.0.13, copied verbatim from `node_modules/@bsv/sdk/dist/umd/bundle.js`. It
exposes itself as the global `bsv`.

It is committed rather than fetched because this application is served by a Go
binary with no JavaScript toolchain, and because a strict page that signs a
Bitcoin transaction should not be loading its cryptography from a CDN at
runtime.

The sibling application `rule-110-arcade` deliberately avoids this bundle and
hand-rolls its BRC-100 client instead. We do the same for the wallet client --
see `wallet.js`, which is about 200 lines over `window.CWI` and the loopback
substrate. The bundle is here for one job the hand-rolled client cannot do:
`redeem.js` has to parse a BEEF transaction, recompute a BIP-143 signature hash,
and sign it with the tag key from the QR fragment, entirely in the browser.

That in-browser signing is what makes the redemption page trustworthy. The
page checks that output zero really pays the crabber -- deriving the expected
locking script through their own wallet -- *before* the tag key signs anything.
Without it, the page would be signing whatever transaction the server handed it.

To update: replace the file, bump the version above, and re-run the redemption
flow end to end. There is no build step to re-run.

## leaflet.js / leaflet.css

Leaflet 1.9.4, from https://unpkg.com/leaflet@1.9.4/dist/. Used on the
redemption page to show where a crab was tagged and where it turned up.

Committed for the same reason as the SDK: this application is a Go binary with
no JavaScript toolchain, and a page that a crabber opens on one bar of signal
should not also be waiting on a CDN.

Two things to know about the map:

Map **tiles** come from OpenStreetMap at runtime, so unlike everything else here
they are a live network dependency. The page is built to survive their absence:
if tiles do not load, it falls back to a hand-drawn SVG track that needs nothing
but the coordinates. A marsh with no signal still shows the journey.

We use no Leaflet marker images, only CSS-styled `divIcon` markers, so the
stylesheet's references to `marker-icon.png` and friends are never fetched and
there is nothing else to vendor.

OpenStreetMap's tile usage policy covers light use and asks for an identifying
Referer, which a browser sends. A real deployment at scale should point
`TILE_URL` in `redeem.js` at its own tile server or a commercial provider rather
than leaning on donated infrastructure.

## fonts/

`google-sans-flex-latin.woff2` and `roboto-latin.woff2`, the latin-subset
variable-font files Google Fonts serves for **Google Sans Flex** (display/
headings) and **Roboto** (body). Both are licensed under the SIL Open Font
License 1.1 and both, as of this download, ship as a single variable file
covering their whole weight range rather than one file per weight -- so there
is exactly one file each to vendor, not four.

Self-hosted for the same reason as everything else in this directory: no
JavaScript toolchain, and a page a crabber opens with one bar of signal should
not also be waiting on `fonts.googleapis.com`. `style.css` declares each with
a `font-weight: <min> <max>` range rather than a fixed weight, so `font-weight:
500` in a rule picks the actual interpolated instance instead of falling back
to a synthetic bold.

To update: re-fetch `https://fonts.googleapis.com/css2?family=Google+Sans+Flex:wght@400..700&family=Roboto:wght@400..700&display=swap`
with a browser user agent (a bare `curl` gets served woff/ttf instead of
woff2), take the `url(...)` for the `unicode-range: U+0000-00FF, ...` ("latin")
block for each family, and replace the file it points to.

## animals/

Species and family-level silhouette icons from [PhyloPic](https://www.phylopic.org),
used as the icon on a card's ambient-gradient header. See `animals/ATTRIBUTION.md`
for the contributor and license of each -- PhyloPic silhouettes are
individually contributed and individually licensed, and most require credit.

## jsqr.min.js

[jsQR](https://github.com/cozmo/jsQR) 1.4.0, minified, Apache License 2.0 (see
`jsqr-LICENSE`). A pure-JS QR decoder, used by the admin console's tag scanner
as the fallback on browsers with no native `BarcodeDetector` -- most desktops,
and, as of this writing, iOS Safari.

Unlike everything else in this directory it is not loaded by any `<script>`
tag; `admin.js` injects it on demand, only once the scanner actually opens and
only on a browser that needs it, so a biologist who never taps "Scan" never
pays for it. Fetched from `https://cdn.jsdelivr.net/npm/jsqr@1.4.0/dist/jsQR.min.js`
with the jsDelivr banner comment and its source-map reference stripped (we do
not vendor the map).

## logos/

The South Carolina DNR's own seal, fetched from `dnr.sc.gov/assets/logos/` --
`dnr-logo-white.png` (`DNR_White.png`, a flat white silhouette, used in every
page's footer) and `dnr-logo-color.png` (`DNR_Color.png`, the full-color
seal, resized to 200x200 and used as the center of the orbiting-icons visual
on `/about`). Both keep their original alpha channel: the color seal is
already circular with a transparent surround, so it needs no backing shape
of its own to sit cleanly on a gradient.
