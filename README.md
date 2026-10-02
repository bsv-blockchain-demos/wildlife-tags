# WildTag

A wildlife-tagging demonstration that links QR tags, signed field observations and BSV reward transactions. It includes a Go service with embedded web pages and an Expo field application for Android and iOS.

The repository demonstrates tag activation, recapture reports, repeat-capture rewards and an auditable record of observations. Blue crab and red drum profiles supply the example measurements and rules. This is a software demonstration; the repository does not establish an operational wildlife-agency programme or validate current wildlife regulations.

## Workflow

1. An operator creates a batch of tags and prints QR codes carrying a tag identifier and bearer secret.
2. An authorised tagger records an observation and activates a tag. In an online deployment, the service funds a two-signature output containing the record and reward.
3. A finder scans the tag, records an observation and signs through a wallet. The client checks the proposed payout before signing with the tag key.
4. The service co-signs the transaction. A base reward goes to the finder; a release bonus can be reserved for payment when the tag is reported again.

Redemption depends on the service's co-signature. Anyone who has copied the bearer secret knows the tag key, so possession of that key does not prove current physical possession of the tag. The server's second signature and lifecycle checks control reuse.

## What the evidence establishes

| Evidence | Meaning |
| --- | --- |
| Transaction and verified inclusion proof | The committed record was included in the checked chain. |
| Tag-key signature | The signer knew the bearer secret, which may have been copied. |
| Observer signature | Binds the observation bytes to the signing wallet key. |
| Reward output | Records a payment under the transaction's spending conditions. |
| Reported coordinates and release condition | Assertions from the observer; neither physical location nor release is proven by the blockchain. |

The signed observation and settlement are distinct. The version-2 record stores the observation, its signature and observer identity alongside settlement data that can be checked against the transaction. Version-1 records remain readable.

## Build and explore offline

Use Go 1.26.3 or a compatible newer toolchain, as declared in [go.mod](go.mod). Node.js is also needed for the cross-language tests. The web pages are embedded in the Go binary and need no separate web build.

```sh
git clone https://github.com/bsv-blockchain-demos/wildlife-tags.git
cd wildlife-tags
go build -o wildtag ./cmd/wildtag

export WILDTAG_NETWORK=test
export WILDTAG_PUBLIC_URL=http://localhost:8120
export WILDTAG_ADDR=127.0.0.1:8120
export WILDTAG_DATA_DIR=./data-local
export WILDTAG_ADMIN_PASSWORD='<choose-a-local-presenter-password>'

./wildtag init
./wildtag serve
```

Replace the password placeholder before running. `init` creates the local key file and refuses to overwrite it. Open [localhost:8120](http://localhost:8120).

In this application, `WILDTAG_NETWORK=test` selects the offline mode: no Arcade connection, broadcasting or mined proof. It is distinct from the online `tstn` and `ttn` configurations. Offline exploration does not demonstrate that a reward has been paid on a blockchain.

## Online operation

The CLI accepts `main`, `test`, `ttn` and `tstn`. An online deployment needs a compatible Arcade service, chain-header access, a funded service wallet, persistent storage and an HTTPS public origin. Configure administrators through `WILDTAG_ADMIN_IDENTITY_KEYS` or the password fallback before starting the web service.

| Setting | Purpose |
| --- | --- |
| `WILDTAG_NETWORK` | Network or offline mode; the source default is `tstn`. |
| `WILDTAG_ARCADE_URL` | Transaction service for online operation. |
| `WILDTAG_CHAINTRACKS_URL` | Header service; otherwise derived from the Arcade URL. |
| `WILDTAG_PUBLIC_URL` | Origin encoded into printed tags. Preserve it for the lifetime of those tags. |
| `WILDTAG_DATA_DIR` | Key file and local SQLite databases. |
| `WILDTAG_POSTGRES_DSN` | Optional PostgreSQL storage for wallet and application data. |
| `WILDTAG_ADDR` | Web listen address; defaults to `:8120`. |
| `WILDTAG_BASE_SATS`, `WILDTAG_BONUS_SATS` | Base reward and conditional release bonus. |

The `address`, `fund`, `mkbatch`, `print`, `activate`, `rearm`, `sweep`, `reclaim`, `release`, `export` and `audit` commands support the operational lifecycle. Use the CLI usage and [cmd/wildtag/](cmd/wildtag/) for their arguments. Funding, activation and settlement commands can move real BSV in online mode.

Back up the service key file and databases together. The tag master seed can derive every printed bearer secret, while the co-signing key authorises tag spends. They are operational secrets, not public dataset fields.

## Source map

| Location | Purpose |
| --- | --- |
| `cmd/wildtag/` | CLI and server entry points. |
| `internal/chain/` | Wallet and transaction-service integration. |
| `internal/tagscript/`, `internal/tagkey/` | Tag locks, key derivation and identifiers. |
| `internal/record/` | Versioned observation and settlement records. |
| `internal/species/profiles/` | Embedded species profiles and rules. |
| `internal/service/`, `internal/store/` | Lifecycle operations and persistence. |
| `internal/web/` | Embedded pages, API and shared JavaScript canonicalisation. |
| `internal/audit/`, `internal/export/` | Record reconciliation and dataset export. |
| [apps/field/](apps/field/README.md) | Mobile finder and tagger application. |

Species profiles are served through `/api/schema`. Adding or changing an embedded profile requires rebuilding the Go application. The `harvest` workflow is represented in the model but is not implemented as a complete workflow.

## Checks

```sh
go build ./...
go vet ./...
go test ./... -race
cd apps/field
npm install
npm run typecheck
npm test
```

Keep Node.js on `PATH` for the Go tests that compare the JavaScript and Go encoders. The mobile checks do not replace native device testing. `scripts/finder-flow.mjs` targets a running deployment and performs a payment flow; it is not part of the offline test command above.

## Dependencies and attribution

The service uses `go-sdk` and `go-arcade-toolbox`. [go.mod](go.mod) pins the toolbox through a `replace` directive to its recorded source repository. Parts of the structure follow `toolbox-app-arcade` and `rule-110-arcade`.

See [mobile attribution](apps/field/ATTRIBUTION.md) and [vendored web dependencies](internal/web/static/vendor/README.md) for copied code and assets.

## Licence

**Open BSV Licence v6.** See [LICENSE.txt](LICENSE.txt) for the full terms. The licence applies to this project's original code and documentation and restricts use to the BSV blockchain defined in the licence. Third-party code, assets and referenced standards retain their respective terms. See [mobile attribution](apps/field/ATTRIBUTION.md) and [vendored web dependencies](internal/web/static/vendor/README.md) for copied code and assets.
