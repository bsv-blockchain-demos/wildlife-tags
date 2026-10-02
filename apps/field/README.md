# WildTag field application

An Expo and React Native application for Android and iOS, with a BRC-100 wallet on the device. Finders scan tags and submit observations; authorised taggers sign in to activate tags in the field.

Use the [project README](../../README.md) for the Go service, network configuration and the limits of the evidence recorded by a tag.

## Development setup

Requires Node.js and npm, plus Android Studio and its SDK for Android, or Xcode on macOS for iOS. From this directory:

```sh
npm install
npm run typecheck
npm test
npm run android
```

`npm run android` invokes `expo run:android` and builds the native application. Use `npm run ios` for iOS. After installing a development build, `npm start` starts Metro for it. Expo Go cannot supply the native cryptography and wallet modules used here.

Set the service origin in Settings for tagger sign-in. Scanning a tag can also select the origin encoded in its QR code. The wallet follows the service's `wallet_chain` value from `/api/info`; restart the app after changing to a deployment on another network.

## Wallet and offline behaviour

A wallet is created on first launch. Keep its recovery phrase or printable recovery shares before receiving funds. Native key storage, recovery and payment behaviour require device testing.

The app can capture and sign observations without a connection, then queue them for submission. It cannot complete a reward payment offline: the service must fund the transaction and supply its co-signature. Delayed submissions retain the original observation timestamp and record the queue delay.

Before the tag key signs a server-built transaction, `verifyPayout` checks the expected recipient output. Knowing the tag key proves access to its bearer secret, not current possession of the physical tag or the truth of the observation.

## Source map

| Location | Purpose |
| --- | --- |
| `src/wildtag/redeem.ts` | Finder flow and payout verification. |
| `src/wildtag/rules.ts` | Species-rule evaluation. |
| `src/wildtag/queue.ts` | Offline outbox. |
| `src/wildtag/canonical.ts` | Shared canonical encoder, resolved through Metro to the web implementation. |
| `src/wallet/` | Wallet construction and recovery. |
| [src/storage/](src/storage/README.md) | Expo SQLite storage adapter. |
| `test/` | Node tests for rules, canonicalisation and related helpers. |

Species definitions come from the server's `/api/schema`. The tests use the repository's actual profile files. Native release signing is a separate setup task; the checked-in Android demonstration configuration is not a production signing arrangement.

See [ATTRIBUTION.md](ATTRIBUTION.md) for code adapted from BSV Browser.

## Licence

**Open BSV Licence v6.** See [LICENSE.txt](../../LICENSE.txt) for the full terms. The licence applies to this project's original code and documentation and restricts use to the BSV blockchain defined in the licence. Third-party code, assets and referenced standards retain their respective terms. See [mobile attribution](ATTRIBUTION.md) and [vendored web dependencies](../../internal/web/static/vendor/README.md) for copied code and assets.
