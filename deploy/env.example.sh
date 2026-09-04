# A deployment profile. Copy it, fill it in, and keep your copy out of git.
#
# The real one used to live at run-ttn/env.sh; that whole directory is ignored,
# because a deployment profile accumulates things that must never be published:
# an admin password, a wallet, and printed tag sheets whose every QR code is a
# bearer instrument.

export WILDTAG_NETWORK=ttn                # main | test | ttn | tstn
export WILDTAG_ARCADE_URL=https://arcade-v2-ttn-us-1.bsvblockchain.tech
export WILDTAG_DATA_DIR=./data            # keys.json and the databases
export WILDTAG_ADDR=127.0.0.1:8120

# Printed into every QR code. Changing it after a batch has been printed points
# those tags at a host that no longer serves them, so it is validated at startup
# and recorded with each batch.
export WILDTAG_PUBLIC_URL=https://tags.example.gov

# Who may administer the programme. Comma-separated BRC-100 identity keys.
# Keys named here are added to the allowlist at every start; removing one does
# NOT revoke it, because a config typo that locked every biologist out of a live
# programme would be worse than a stale entry. Use the store's RemoveAdmin for
# an actual revocation.
export WILDTAG_ADMIN_IDENTITY_KEYS=

# The fallback credential, for a device with no wallet set up. Records signed
# this way are attributed to "operator" rather than to a person, and the public
# dataset says so. Leave it empty in production if you can.
export WILDTAG_ADMIN_PASSWORD=

# Optional: Postgres for both databases. Empty means SQLite.
export WILDTAG_POSTGRES_DSN=
