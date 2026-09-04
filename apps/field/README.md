# WildTag Field

The phone half of the tagging programme. One codebase, Android first, iOS from
the same source.

Two jobs, kept apart because they are not the same job:

- **Finder.** Scan a tag on an animal you caught, see where it came from and how
  far it has travelled, report it, and be paid into a wallet this phone owns.
  No account, no sign-in, no bank details.
- **Tagger.** DNR staff or an authorised volunteer, signed in, arming a tag on
  an animal about to go back in the water. Note the role is *tagger* rather than
  *admin*: SCDNR's marine game fish programme runs largely on volunteers, and
  minting batches or sweeping rewards is deliberately not the same permission.

## Why React Native rather than Electron

Electron bundles Chromium and Node for a desktop operating system and has no
mobile target. The requirement behind the ask -- one codebase for Android and
iOS -- is right, and React Native is what satisfies it. That is not a guess:
`@bsv/wallet-toolbox-mobile` is published specifically for React Native, and
`bsv-browser` is a shipping, fully self-custodial BRC-100 wallet built on it.

## The parts worth reading

| File | Why |
|---|---|
| `src/wildtag/redeem.ts` | The finder flow, and `verifyPayout` in particular. This device holds the tag's private key and signs a transaction the *server* built; without checking who that transaction pays, signing on the device would be theatre. |
| `src/wildtag/rules.ts` | The evaluator for the species profile's rules. It imports nothing, so it can be tested against the server's own shipped JSON under plain node -- because if it ever disagrees with `internal/species`, this app tells somebody they may keep an animal the server then refuses, after they have already killed it. |
| `src/wildtag/queue.ts` | The offline outbox, and an honest account of what can and cannot happen without signal. |
| `src/wildtag/canonical.ts` | A re-export. There is exactly one canonical encoder in this repository and both the web pages and this app use it; see `metro.config.js`. |
| `src/storage/` | Lifted from bsv-browser. See `ATTRIBUTION.md`. |

## Offline

Full offline redemption is impossible and nothing here pretends otherwise: the
server funds the transaction and adds DNR's half of the two-of-two lock, so
payment needs signal.

What works offline is the part that matters scientifically. The observation is
built and signed on the device at the moment of the catch, so the position fix
and the timestamp are bound by a signature made there. A report submitted six
hours later still says exactly where and when the animal was found -- and the
delay is recorded rather than smoothed away, reaching the public dataset as
`queued_seconds`.

## Running it

This app needs a development build, not Expo Go: `react-native-quick-crypto`
installs native code via JSI, and the wallet cannot generate a key without it.

```sh
npm install
npm run typecheck
npm test           # the rule evaluator and the shared encoder, against the real profiles
npm run android    # a dev build on a connected device
```

Point it at a deployment in Settings, or just scan a tag -- a QR code carries
the origin of whichever DNR issued it.

## Installing the APK

The build produces `android/app/build/outputs/apk/release/app-release.apk`.

```sh
adb install -r app-release.apk       # or copy it to the phone and open it
```

Android will warn about installing from an unknown source, because the APK is
signed with the debug keystore rather than a registered one. That is deliberate
for a sideloaded build — it means nobody has to invent and then look after a
signing key — but it is **not** a production signing setup, and a Play Store
release needs a real one.

Three things to know about the installed app:

- **A wallet is created the first time it opens.** No sign-in, no account. That
  is the point: a finder should be able to scan a tag and be paid without
  provisioning anything. It also means the key on that phone is the only copy —
  print the recovery sheets from the wallet screen before it holds anything you
  would miss.
- **Scanning a tag configures the app.** A QR code carries the origin of the
  deployment that issued it, so the finder flow needs no setup. The tagger side
  does, because it signs in before it has scanned anything: set the address in
  Settings first.
- **The wallet is built for the network the deployment names.** `GET /api/info`
  publishes `wallet_chain`, and the app follows it. Pointing the app at a
  deployment on a different network needs a restart, because the wallet is built
  once at launch and a wallet built for the wrong chain cannot verify a payment
  made on the right one.

## Adding a species

Nothing here. Add a JSON file to `internal/species/profiles/` on the server; the
forms, the rules, the units and the receipt all follow from `GET /api/schema`.
`test/rules.test.mjs` runs against those same files, so a new profile is covered
the moment it ships.
