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
