# Lifted code

Four things in this app were copied from [`bsv-browser`](https://github.com/bsv-blockchain/bsv-browser),
a shipping self-custodial BRC-100 wallet for React Native, at commit
`f8e1ef67cc6e0da3940dd727d627838e80b2eb7d` (17 June 2026).

| Path here | From | Why |
|---|---|---|
| `src/storage/StorageExpoSQLite.ts`, `src/storage/schema/`, `src/storage/methods/` | `storage/` | **The published `@bsv/wallet-toolbox-mobile` does not ship a mobile storage provider.** This is the missing piece: a `StorageProvider` over expo-sqlite, which inherits all the wallet's business logic by subclassing the abstract provider. Writing it from scratch would be re-deriving two thousand lines of already-debugged SQL. |
| `src/wallet/mnemonicWallet.ts` | `utils/mnemonicWallet.ts` | BIP-39 plus `m/0'/0'` hardened derivation. |
| `src/wallet/backupShares.ts` | `utils/backupShares.ts` | Shamir 2-of-3 printable recovery shares. A lost mnemonic is lost satoshis, so this is here from day one rather than later. |
| `index.js`, `metro.config.js` | same names | `react-native-quick-crypto` must be installed via JSI **before any BSV SDK import**, and Metro has to resolve `node:crypto` to it. Days of debugging already done; rediscovering it is not a good use of anyone's time. |

## Deliberately not carried over

`bsv-desktop`'s `src/lib/utils/keyMaterial.ts` writes the root key and mnemonic
in **plaintext to localStorage**. The mnemonic here goes in `expo-secure-store`,
which is hardware-backed, as bsv-browser does.

## The risk this creates

`StorageExpoSQLite` is a copy, not a dependency, and it tracks an abstract base
class in `@bsv/wallet-toolbox-mobile` that can change under it. So the toolbox
version is pinned exactly rather than floated, and the commit it came from is
recorded above. When the toolbox is upgraded, diff `storage/` in bsv-browser at
the matching version before assuming this still fits.

## Edits made to the lifted files

Six one-character edits, all of the same kind: this project compiles with
`noUncheckedIndexedAccess`, which bsv-browser does not, so `xs[0]` is typed as
possibly-undefined here and is not there. Each site is a non-null assertion at
the index, nothing more, so a future diff against upstream stays readable:

- `src/wallet/backupShares.ts` — `existingShares[0]`, the two `split()` results
  in the date stamp, and `shares[0]` in the printed instructions
- `src/storage/methods/listOutputsSql.ts` — `baskets[0]`, twice
- `src/storage/StorageExpoSQLite.ts` — `scores[scores.length - 1]`

One substantive difference: `@bsv/sdk` is pinned to `2.1.2` with an npm
`overrides` entry, so the toolbox does not nest a second copy. Two copies of the
SDK produce two structurally identical but nominally distinct `Beef`,
`MerklePath` and `BigNumber` types, and every value crossing between the wallet
and the app then fails to typecheck for reasons that have nothing to do with the
code.
