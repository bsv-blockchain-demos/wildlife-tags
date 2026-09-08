# Animal Silhouette Attribution

All silhouette icons in this directory were sourced from [PhyloPic](https://www.phylopic.org)
(API: `https://api.phylopic.org`), a free, community-contributed database of
organism silhouettes. Every image below is licensed **CC0 1.0 Universal (Public
Domain Dedication)** — no attribution is legally required, but contributor
credit is listed here as good practice and per PhyloPic community norms.

All files are vector SVGs (single-color, `fill="#000000"`) and can be recolored
via CSS (e.g. `fill: currentColor` after stripping/overriding the inline fill,
or a CSS `mask`/`filter` approach) for use as themeable icons.

Format resolved via the PhyloPic v2 API chain:
`GET /nodes?filter_name={lowercase scientific name}&build=554` → node UUID →
`GET /nodes/{uuid}?build=554&embed_primaryImage=true` → embedded image with
`_links.vectorFile.href` (SVG), `_links.license.href`, `_links.contributor.title`.

---

## blue-crab.svg
- **Subject represented:** Callinectes sapidus (Atlantic blue crab) — exact species match
- **PhyloPic image UUID:** `7197c71a-0653-4e82-bcbb-b156c150826a`
- **Permalink:** https://www.phylopic.org/images/7197c71a-0653-4e82-bcbb-b156c150826a
- **API resource:** https://api.phylopic.org/images/7197c71a-0653-4e82-bcbb-b156c150826a
- **Contributor:** Guillaume Dera
- **License:** CC0 1.0 Universal (Public Domain Dedication) — https://creativecommons.org/publicdomain/zero/1.0/
- **Format:** SVG (vector)

## red-drum.svg
- **Subject represented:** Sciaenops ocellatus (red drum) — exact species match
- **PhyloPic image UUID:** `2f984c47-7b30-4b33-ae6f-d411d2b96003`
- **Permalink:** https://www.phylopic.org/images/2f984c47-7b30-4b33-ae6f-d411d2b96003
- **API resource:** https://api.phylopic.org/images/2f984c47-7b30-4b33-ae6f-d411d2b96003
- **Contributor:** Edwin Price
- **License:** CC0 1.0 Universal (Public Domain Dedication) — https://creativecommons.org/publicdomain/zero/1.0/
- **Format:** SVG (vector)

## crab-generic.svg
- **Subject represented:** Carcinus maenas (European green crab) — used as a generic Brachyura/decapod crab silhouette
- **PhyloPic image UUID:** `96523a56-068b-436f-a44f-ebf05605e700`
- **Permalink:** https://www.phylopic.org/images/96523a56-068b-436f-a44f-ebf05605e700
- **API resource:** https://api.phylopic.org/images/96523a56-068b-436f-a44f-ebf05605e700
- **Contributor:** Alexandra Hahn
- **License:** CC0 1.0 Universal (Public Domain Dedication) — https://creativecommons.org/publicdomain/zero/1.0/
- **Format:** SVG (vector)

## fish-generic.svg
- **Subject represented:** Sardinops melanostictus (Pacific sardine, Actinopterygii) — used as a generic bony fish side-profile silhouette
- **PhyloPic image UUID:** `34c2ed3a-aa57-4e52-9ae8-a1e543c9bef8`
- **Permalink:** https://www.phylopic.org/images/34c2ed3a-aa57-4e52-9ae8-a1e543c9bef8
- **API resource:** https://api.phylopic.org/images/34c2ed3a-aa57-4e52-9ae8-a1e543c9bef8
- **Contributor:** Mathieu Pélissié
- **License:** CC0 1.0 Universal (Public Domain Dedication) — https://creativecommons.org/publicdomain/zero/1.0/
- **Format:** SVG (vector)

## sea-turtle.svg
- **Subject represented:** Chelonia mydas (green sea turtle, Cheloniidae) — exact-family sea turtle silhouette
- **PhyloPic image UUID:** `95a56d8c-896f-44ed-bf00-5f07d64ca6f8`
- **Permalink:** https://www.phylopic.org/images/95a56d8c-896f-44ed-bf00-5f07d64ca6f8
- **API resource:** https://api.phylopic.org/images/95a56d8c-896f-44ed-bf00-5f07d64ca6f8
- **Attribution text on record:** James R. Spotila and Ray Chatterji
- **Contributor (uploader):** Ray Chatterji
- **License:** CC0 1.0 Universal (Public Domain Dedication) — https://creativecommons.org/publicdomain/zero/1.0/
- **Format:** SVG (vector)

## shark.svg
- **Subject represented:** Carcharodon carcharias (great white shark, Selachimorpha) — exact-species shark silhouette
- **PhyloPic image UUID:** `545d45f0-0dd1-4cfd-aad6-2b835223ea0d`
- **Permalink:** https://www.phylopic.org/images/545d45f0-0dd1-4cfd-aad6-2b835223ea0d
- **API resource:** https://api.phylopic.org/images/545d45f0-0dd1-4cfd-aad6-2b835223ea0d
- **Contributor:** Steven Traver
- **License:** CC0 1.0 Universal (Public Domain Dedication) — https://creativecommons.org/publicdomain/zero/1.0/
- **Format:** SVG (vector)

## bird-generic.svg
- **Subject represented:** Larus argentatus (herring gull, Aves) — used as a generic recognizable bird silhouette
- **PhyloPic image UUID:** `0871b630-2dcd-4d35-b0a4-6a6551140553`
- **Permalink:** https://www.phylopic.org/images/0871b630-2dcd-4d35-b0a4-6a6551140553
- **API resource:** https://api.phylopic.org/images/0871b630-2dcd-4d35-b0a4-6a6551140553
- **Contributor:** Sharon Wegner-Larsen
- **License:** CC0 1.0 Universal (Public Domain Dedication) — https://creativecommons.org/publicdomain/zero/1.0/
- **Format:** SVG (vector)

---

## Summary table

| File | Species used | License | Contributor |
|---|---|---|---|
| blue-crab.svg | Callinectes sapidus | CC0 1.0 | Guillaume Dera |
| red-drum.svg | Sciaenops ocellatus | CC0 1.0 | Edwin Price |
| crab-generic.svg | Carcinus maenas | CC0 1.0 | Alexandra Hahn |
| fish-generic.svg | Sardinops melanostictus | CC0 1.0 | Mathieu Pélissié |
| sea-turtle.svg | Chelonia mydas | CC0 1.0 | Ray Chatterji (James R. Spotila and Ray Chatterji) |
| shark.svg | Carcharodon carcharias | CC0 1.0 | Steven Traver |
| bird-generic.svg | Larus argentatus | CC0 1.0 | Sharon Wegner-Larsen |

All seven images are CC0 — no attribution is legally required for use, but
crediting the contributors above (and linking back to
[phylopic.org](https://www.phylopic.org)) is appreciated by the PhyloPic
community and recommended for a credits/about section.
