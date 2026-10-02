# Expo SQLite wallet storage

`StorageExpoSQLite` adapts `@bsv/wallet-toolbox-mobile`'s `StorageProvider` to `expo-sqlite`. It implements database access for wallet records and a key-value store for application state. Business operations inherited from `StorageProvider` use these database methods.

This is part of the [mobile application](../../README.md), not a separately published package. Install dependencies through that application's manifest and use its native development build.

## Integration

The running application's construction is in [the wallet provider](../wallet/WalletProvider.tsx). Use that code as the integration example because it supplies the chain, fee model, identity, services and database selection together.

The constructor accepts `StorageProviderOptions` plus optional `identityKey` and `databaseName` values. Without an explicit database name, it uses `wallet-<last-eight-identity-characters>-<chain>net.db`, or `default` in place of the identity suffix.

Call `migrate(storageName, storageIdentityKey)` before database access. It opens SQLite, creates the schema, initialises settings when necessary and returns the schema version string. The method creates tables; it is not a general migration framework for arbitrary schema changes.

## Files and behaviour

| File or method | Purpose |
| --- | --- |
| [StorageExpoSQLite.ts](StorageExpoSQLite.ts) | Provider, CRUD operations, validation and lifecycle methods. |
| [schema/createTables.ts](schema/createTables.ts) | Table and index definitions. |
| [methods/listActionsSql.ts](methods/listActionsSql.ts) | Action-list queries. |
| [methods/listOutputsSql.ts](methods/listOutputsSql.ts) | Output-list queries. |
| `getKeyValue`, `setKeyValue` | Application state stored alongside wallet tables. |
| `transaction(scope, trx?)` | Runs a scope through an exclusive SQLite transaction; a supplied transaction token reuses the existing scope. |
| `destroy()` | Closes the connection and clears the provider's loaded settings. |

The schema covers users, transactions, outputs, baskets, tags, labels, certificates, proof data, synchronisation state, monitor events and settings. Query arguments and record types come from the pinned wallet-toolbox dependency. Inspect its interfaces before constructing records directly; partial examples that omit required fields are not valid wallet transactions.

Date, boolean and binary values are converted between SQLite representations and wallet-toolbox values by the provider. Keep the schema, conversions and query builders consistent when changing a field.

## Limits

- `dropAllData()` deliberately throws because the database contains wallet data.
- `purgeData()` currently returns a zero-count result without removing records.
- `adminStats()` is intentionally unimplemented for personal storage.
- Native SQLite behaviour must be verified in the mobile application; a TypeScript check alone does not exercise database transactions or recovery.

Keep a recoverable backup before changing a real wallet database. Use disposable application data when exercising schema or transaction changes.

## Attribution

This adapter was adapted from BSV Browser. See [the field application attribution](../../ATTRIBUTION.md) for provenance.

## Licence

The original adapter documentation identifies the Open BSV Licence. The copied adapter retains its upstream terms; see [mobile attribution](../../ATTRIBUTION.md) for its provenance. The project's own code uses [Open BSV Licence v6](../../../../LICENSE.txt).
