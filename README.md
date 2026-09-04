# WildTag

QR-unlockable bounties for DNR wildlife tag returns, on BSV.

A wildlife agency tags an animal, releases it, and depends on whoever catches it
next to say so. South Carolina DNR does this with Atlantic blue crabs
(*Callinectes sapidus*) around Charleston and with
[46 marine game fish species across 20 families](https://www.dnr.sc.gov/marine/tagfish/tagfish.html).
The programmes work. Their incentive does not.

- **Fewer than half of encountered tags are ever reported.** That figure is
  SCDNR's own, from [*Don't Underestimate the
  Reward*](https://www.dnr.sc.gov/marine/tagfish/pdf/Sept13-Don't%20Underestimate%20the%20Reward.pdf),
  which also notes that tags carrying money report materially better than tags
  that don't.
- **The reward arrives too late to be an incentive.** The fisheries literature
  blames delivery friction rather than stinginess: the finder's identity gets
  lost when a captain collects the tag, weeks pass, and by the time a cheque or
  a T-shirt turns up the moment has gone. SCDNR currently offers swag or a
  chance at a $500 drawing — a lottery, not a payment.
- **Recapture positions are the weakest data in the study.** Release
  coordinates are precise because a biologist recorded them on the spot.
  Recapture coordinates are remembered, days later, and typed into a form. For
  a movement study that asymmetry is exactly backwards.

There is a fourth finding worth building around, also SCDNR's: many people who
report a tag turn out to be **more interested in the animal's history than the
reward**. One angler's explanation was simply that understanding his prey made
him better at catching it. Nobody currently gives them that.

WildTag replaces the printed phone number with a QR code carrying a bearer
secret. Arming a tag locks a real reward on chain; scanning it pays the finder
in seconds and writes their position into the same transaction that moves the
money — so the reward and the data cannot be separated afterwards.

**The machinery is not about crabs.** A bearer tag key, a one-time UTXO, an
attested timestamped record and a public dataset are not facts about
*Callinectes sapidus*. What is species-specific — the measurements, the
vocabularies, the size limits — is a JSON file in
`internal/species/profiles/`, served to every client by `GET /api/schema`.
Adding a species is a file, not a release. Two ship today: the blue crab, and
red drum (*Sciaenops ocellatus*), which has a slot limit rather than a minimum
and no moult stage at all — chosen precisely because it is shaped differently
enough to test the abstraction.

---

## What the chain does and does not do

This is the section to read before the architecture.

| Claim | Status |
|---|---|
| This record existed by block time *T* and has not been altered | **Proven.** Merkle proof verified locally against ChainTracks headers. |
| Whoever redeemed it held the physical tag | **Proven.** The spend requires a signature from the key printed on the tag. |
| The reward was paid, and can be claimed once | **Proven.** It is a UTXO. A second claim is a double-spend. |
| The phone was at those coordinates | **Not proven.** Browser geolocation is self-reported and spoofable. We record the reported accuracy and bind the fix to the tag key and to the finder's wallet identity — that raises the cost of lying and makes it attributable. It is an *attestation*. |
| The crab was released alive | **Not proven.** The escrow design corroborates it economically rather than cryptographically; see below. |

Every user-facing page states the bottom two rows plainly, and
`internal/web/prose_test.go` fails the build if the copy starts overstating them.

---

## How it works

### Arming a tag

A tagger — DNR staff or an authorised volunteer, since the fish programme runs
largely on volunteers — scans a printed tag, records whatever that species'
profile asks for, takes a position fix, and signs the record with their own
BRC-100 wallet. One transaction locks `base + bonus` satoshis into a single
output whose locking script *is* the tagging record.

### Reporting one

A finder scans the same tag, in a browser or in the phone app. The client reads
the bearer secret out of the URL fragment, derives the tag's spending key
locally, and never sends it anywhere. Three wallet prompts later they are paid,
and their report is in the same transaction that paid them.

### The re-release bonus

Nothing on chain can prove a crab went back in the water. So the bonus is not
paid on the reporter's word:

- **Report and keep the crab** → base reward, tag retires.
- **Report and put it back with the tag on** → base reward now, and the bonus is
  *escrowed into the next generation's output*, committed on chain to that
  reporter's identity key.
- **Someone catches the same crab again** → the new finder gets their base, and
  the previous reporter's escrowed bonus is released in the same transaction.

The tag turning up again is the only evidence a release can have, so that is
what releases the money. A false "released" claim earns the base reward and
forfeits a bonus that will never arrive.

### The locking script

Every tag output is BRC-48 PushDrop *shaped* — a key check followed by pushed
data fields that are immediately dropped — but with a two-of-two head:

```
<tagPub> OP_CHECKSIGVERIFY <dnrPub> OP_CHECKSIG <f0> <f1> … <fn> OP_2DROP …
```

unlocked by `<dnrSig> <tagSig>`.

**The second key is not decoration, and it is the design's real cost.** A tag's
bearer secret is printed on something that never changes, so the crabber who
reports a recapture holds exactly the same secret the next finder will hold.
Without a second factor that first finder could come back and drain the escrow
holding their own bonus *and* the next finder's reward. No key-derivation scheme
fixes this — a past bearer can always re-derive whatever a future bearer
derives.

What it buys: replay protection across generations, and the ability to refuse an
obviously fraudulent claim or a tag reported stolen. What it costs: **redemption
is DNR-gated.** A finder cannot unilaterally take the money. That is a real
reduction in the trustless story and it is documented rather than glossed. What
survives it: the finder's signature still independently proves physical
possession and binds their GPS attestation, and a refusal is publicly auditable
because the output simply stays unspent.

Because chunk 1 is `OP_CHECKSIGVERIFY` rather than `OP_CHECKSIG`, go-sdk's
`pushdrop.Decode` returns nil on these scripts. `internal/tagscript` ships the
reader, and a test pins that fact so a future SDK change is noticed.

### The record

Nine pushed fields inside the locking script:

```
f0 "WILDTAG"             f1 "2" (version)     f2 tag id      f3 "ACT" | "REC"
f4 generation            f5 observation (canonical JSON)
f6 observation signature f7 observer identity key
f8 settlement (canonical JSON, unsigned)
```

`f6`/`f7` are the *observer's* signature — the tagger's identity key at tagging,
the finder's at recapture — and are separate from the two signatures that unlock
the output. Those prove the spend was authorised; this proves who said so.

**The payload is in two halves, and the split is not cosmetic.** Version 1 put
everything in one signed blob, including the amounts paid and the outpoint being
spent. That was wrong twice over: a finder standing in a marsh cannot know the
escrow balance or which output their report will spend, so asking their wallet
to sign those numbers means the record they attest to is mostly claims made on
their behalf — and none of those values exist until a server is reachable, which
makes offline capture impossible.

So `f5` is what a person saw — position, measurements, what they did with the
animal — signed by them and buildable on a phone with no signal. `f8` is what
the programme paid and against which output, added at submission.

`f8` carries no signature and does not need one. The transaction is already
signed by both the tag key and DNR's, and every settlement value is
independently checkable against the transaction itself: `prev` is the input,
`paid` is output zero. `wildtag audit` performs exactly that cross-check, so a
settlement that disagrees with its own transaction is a finding rather than a
forgery nobody notices.

Version 1 records stay readable. `record.Decode` dispatches on `f1` and
`internal/record/legacy.go` lifts the old shape into the current one, so nothing
already on chain becomes unreadable — a timestamp format that dies the moment it
is superseded is not much of a timestamp.

The observation is signed, so its bytes have to be reproducible by anything that
wants to check it, on a phone or in a browser. Three consequences:

- Struct fields are declared alphabetically by JSON tag, so Go emits exactly
  what a sorted-key `JSON.stringify` emits.
- **The maps are sorted recursively.** Go's `encoding/json` sorts map keys for
  free; JavaScript's `JSON.stringify` uses *insertion order*. That asymmetry is
  the single most dangerous thing in this format, and it is why there is exactly
  one canonical encoder — `internal/web/static/canonical.js` — used by the web
  pages and, through a Metro alias rather than a copy, by the phone app.
- **No field is a float.** Coordinates are integer degrees × 1e7, distances whole
  metres, temperature and salinity hundredths. Float formatting is the classic
  cross-language signature break, and a test fails the build if a float ever
  appears in a payload struct.

`web.TestTheAppAndTheServerAgreeOnCanonicalBytes` builds the same observation on
both sides from the same human values and compares the bytes exactly. The
fixture hands the client its fields in *reverse* alphabetical order on purpose:
without that the test passes even with the client's sorting removed, which is
precisely the drift it exists to catch.

### Species as data

```go
type Profile struct {
    Code, Common, Scientific string
    Workflow                 Workflow    // mark-recapture | harvest
    Measures                 []Measure   // key, unit, scale, range, required
    Vocabs                   []Vocab     // sex, stage, gear, disposition
    PrimaryMeasure           string      // what a receipt leads with
    NotTaggable, MustRelease []Rule      // declarative predicates
    SweepAfterDays           int
    GrowthExpected           bool
}
```

A `Rule` is one shape covering "females carrying eggs must be returned", "under
five inches must be returned", a fish slot limit and a deer antler restriction.
The same rules are evaluated on the server, in the browser and on the phone —
from the same JSON, not three copies of the law — because a client that
disagrees tells somebody they may keep an animal the server then refuses, after
they have already killed it.

`Workflow` has a second value, `harvest`: one party, one event, compliance
rather than reward. That is what deer tagging is, and South Carolina already
mandates electronic deer reporting through
[SC Game Check](https://dnr.sc.gov/harvestreport/), which issues a confirmation
number a meat processor must trust DNR's database to validate — exactly the
thing a one-time, independently verifiable tag replaces. The value exists in the
model so the abstraction is shaped by real difference; the workflow itself is
not built, and the README says so rather than implying otherwise.

### Why the client signs at all

The redemption page — and the phone app, identically — holds the tag's private
key and signs a transaction the *server* built. That is only defensible because
the client checks what it is signing first:

1. BRC-29 payment outputs are derived with type-42.
2. So the finder's own wallet can derive the key output zero *should* be locked
   to — `getPublicKey({protocolID: [2, '3241645161d8'], keyID: "<prefix> <suffix>",
   counterparty: <server identity>, forSelf: true})`.
3. The page rebuilds the expected P2PKH script from that key and compares it
   against the transaction, byte for byte, before the tag key signs anything.

No private key of the finder's ever enters the page; the wallet does the
derivation. Without step 3, client-side signing would be theatre — and a test
fails the build if the verify call ever moves after the sign call.

---

## Keys

`data/keys.json`, mode 0600 in a 0700 directory, holds four secrets:

| Key | Role |
|---|---|
| wallet key | funds activations, pays fees, receives change |
| DNR co-signing key | the `dnrPub` half of every tag lock |
| **tag master seed** | derives every tag's bearer secret |
| BRC-29 derivation prefix/suffix | pins the deposit address across restarts |

```
secret16 = HMAC-SHA256(seed, "wildtag-v1|" + ordinal)[:16]
tagPriv  = SHA256("wildtag-v1-key|" + secret16)
tagID    = crockford32(SHA256("wildtag-v1-id|" + secret16)[:4]) + check
```

Tag keys are **derived rather than random**, and that is a deliberate trade.
Whoever holds the seed can spend any tag ever printed. In exchange, DNR can
reprint a lost batch and — the operationally necessary part — reclaim rewards
from tags that were never reported. Blue crabs live two or three years and shed
their carapace, and anything wired to it, at every moult. A meaningful fraction
of any batch is on the seabed within months. A programme that permanently burns
money for every crab that dies is one nobody funds twice.

`.gitignore` and `.dockerignore` carry deliberately overlapping, unanchored
patterns for this file. `rule-110-arcade` published live keys once because a
`/data/`-only rule did not match a directory someone had named
`data-sqlite-backup/`.

---

## The physical tag

SCDNR's blue crab tags are roughly 1″ × 2″ plastic rectangles wired through the
lateral spines, which leaves about a **0.75″ square** for a QR code.

Measured, not estimated: a payload of
`https://wildtag.dnr.sc.gov/t/K2M9Q7C#<22 chars>` encodes as **QR version 4,
33 × 33 modules** at error-correction level M — about 0.023″ per module, which
is comfortable at 600 dpi and readable by a phone at arm's length. Level M's 38%
redundancy is chosen for a tag that spends months being fouled and abraded in an
estuary; L would fit a denser payload and would not survive the barnacles.

**The public URL is baked into the printed code.** Changing it after a batch has
been printed makes those tags point at a host that no longer serves them, and a
tag on an animal cannot be fixed. `TestTheQRCodeFitsOnACrabTag` fails the build
if a configured host pushes the code past version 5.

The tag id is 7 Crockford base-32 characters (no I, L, O or U) with a
position-weighted check character. Weights are odd, which catches **100% of
single-character errors** at the cost of missing 3.2% of adjacent
transpositions — one base-32 check character provably cannot catch both, and a
misread character is far more common than a swap when somebody is squinting at a
barnacled tag. Both rates are measured by tests.

---

## Running it

```sh
go build -o wildtag ./cmd/wildtag

export WILDTAG_NETWORK=tstn
export WILDTAG_ARCADE_URL=https://arcade-v2-tstn-us-1.bsvblockchain.tech
export WILDTAG_PUBLIC_URL=https://bcrab.sc.gov
export WILDTAG_ADMIN_IDENTITY_KEYS=02...   # or WILDTAG_ADMIN_PASSWORD

./wildtag init                 # mint keys.json; refuses to overwrite
./wildtag address              # deposit address and how many tags it funds
./wildtag fund -tx <hex>       # import a mined funding transaction
./wildtag mkbatch -n 50 -species SCIOCE   # create tags (spends nothing)
./wildtag print -batch B... -o sheet.html
./wildtag serve
```

Arming from the command line, and finding out what a species records:

```sh
./wildtag activate -describe                  # every species this build knows
./wildtag activate -species SCIOCE -describe  # its measurements and vocabularies
./wildtag activate -tag ECX-ZMJP -lat 32.7522 -lon -79.8875 \
    -meas tl=520,wt=2790 -attr sex=U,cond=GOOD,gear=HANDLINE
```

Measurements are scaled integers, never decimals: `wt=2790` is 27.90 °C under
that measure's scale of 100. The CLI refuses a decimal rather than rounding it,
because a rounded measurement is wrong data that looks right.

`WILDTAG_NETWORK=test` runs fully offline — no arcade, no monitor, nothing
broadcast or proven — which is what the end-to-end tests use and the fastest way
to exercise the UI.

Other commands: `secrets` (one-shot export, audit-logged), `rearm`, `sweep`
(with `-tag` for early reclamation), `reclaim`, `release`, `export`, `audit`.

### The phone app

`apps/field` is one Expo codebase for Android and iOS. It scans tags, shows the
animal's story, pays the finder into a wallet the phone owns, and — behind a
sign-in — arms tags in the field with an offline capture queue. See
`apps/field/README.md`; the short version is that Electron cannot target mobile
and React Native is what actually satisfies "one codebase, both platforms".

### `wildtag audit`

Walks every tag, pulls the real transactions out of the wallet store, decodes
the locking scripts, re-verifies every attestation, and reports where the chain
and the database disagree. Exits non-zero on criticals so it works as a cron
check without a wrapper. This is what makes the tamper-evidence claim more than
a slogan.

---

## Layout

```
cmd/wildtag/        CLI: init address fund mkbatch print secrets activate
                         rearm sweep export audit serve
internal/tagscript/ the two-of-two lock: build, spend, decode, verify locally
internal/tagkey/    seed → secret → key → tag id, and the QR payload
internal/record/    the on-chain record format and its attestations
internal/species/   species profiles, declarative rules, and the generic domain
internal/species/profiles/  one JSON file per species; a new one is not a release
internal/store/     tags, events, escrows, sessions, audit log
internal/chain/     the single seam onto go-arcade-toolbox
internal/service/   the programme's rules and its ordering discipline
internal/auth/      BRC-100 identity login, password fallback
internal/web/       three pages, embedded, no build step
internal/qr/        printable tag sheets
internal/export/    the public dataset
internal/audit/     chain-versus-database reconciliation
apps/field/         the Android and iOS app: scan, report, get paid; arm tags
scripts/finder-flow.mjs  walks the whole finder flow against a live deployment
```

Nothing outside `internal/chain` imports the toolbox. That boundary is what made
the sibling applications' toolbox migrations a one-file change, and it is what
lets the whole tag lifecycle be tested without a wallet, a database or a network.

## Verification

```sh
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
cd apps/field && npm run typecheck && npm test
```

The Go suite needs `node` on PATH: the cross-language tests skip without it, and
a guard that skips silently is not a guard. CI fails if they skip.

Against a live deployment, which is what actually proved the species work:

```sh
wildtag mkbatch -n 6 -species SCIOCE
wildtag activate -tag <id> -meas tl=520 -attr sex=U,cond=GOOD,gear=HANDLINE
node scripts/finder-flow.mjs --tag <id> --secret <code> \
    --meas tl=545 --attr sex=U,gear=HANDLINE,disp=RELEASED
wildtag audit
```

Some tests worth knowing about, because they encode decisions rather than
behaviour:

| Test | What it pins |
|---|---|
| `tagscript.TestTheTagSignatureAloneIsNotEnough` | the exact attack the second key exists to stop |
| `tagscript.TestChangingAnOutputInvalidatesTheSpend` | the report and the payment cannot be separated |
| `tagkey.TestTheQRCodeFitsOnACrabTag` | a longer hostname cannot silently break printed tags |
| `species.TestRulesRefuseWhatTheLawRefuses` | the legal rules survived the move from code to data |
| `species.TestAProfileThatCannotFireItsOwnRulesIsRefused` | a rule naming a field nobody records would silently never fire |
| **`web.TestTheAppAndTheServerAgreeOnCanonicalBytes`** | **the one that matters most** — client and server produce identical signed bytes |
| `web.TestSchemaDrivesTheForms` | no page hardcodes a vocabulary or a size limit the schema owns |
| `record.TestV1RecordsStillDecode` | nothing already on chain became unreadable |
| `service.TestAQueuedObservationIsAcceptedLate` | an offline capture is accepted, and its delay recorded |
| `service.TestAReportMustNameTheSpeciesTheTagWasArmedFor` | a red drum reported on a crab tag is a finding, not a row |
| `record.TestNoPayloadFieldIsAFloat` | cross-language signature reproducibility |
| `store.TestOnlyOneRedemptionCanClaimATag` | two crabbers on one trap resolve to one winner |
| `chain.TestTheFirstRecaptureEscrowsRatherThanPays` | the incentive design itself |
| `web.TestTheRedemptionPageVerifiesBeforeSigning` | in-browser signing is not theatre |
| `web.TestPagesDoNotClaimTheChainProvesLocation` | the copy does not oversell the cryptography |

## Built on

[`go-arcade-toolbox`](https://github.com/bsv-blockchain/go-arcade-toolbox) and
[`go-sdk`](https://github.com/bsv-blockchain/go-sdk). Structure follows the
sibling applications `toolbox-app-arcade` and `rule-110-arcade`; the storage
options, the write-ahead-before-signing discipline, and the asset-stamping
tricks in `internal/web` are lifted from them more or less directly.
