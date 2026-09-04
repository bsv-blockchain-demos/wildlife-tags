#!/usr/bin/env bash
# Stand up a local CrabTag with a few tags in it, on the offline network.
#
# The "test" network runs with no arcade and no monitor: nothing broadcasts and
# nothing is ever proven. That is exactly what you want for working on the UI --
# no funding, no waiting for blocks -- and exactly what you must not mistake for
# a working deployment.
set -euo pipefail

cd "$(dirname "$0")/.."

DATA_DIR="${WILDTAG_DATA_DIR:-./data-dev}"
ADDR="${WILDTAG_ADDR:-127.0.0.1:8120}"
TAGS="${1:-12}"

export WILDTAG_NETWORK=test
export WILDTAG_DATA_DIR="$DATA_DIR"
# localhost over plain http is the one origin browsers treat as secure, which
# is what lets navigator.geolocation work without a certificate.
export WILDTAG_PUBLIC_URL="http://${ADDR}"
export WILDTAG_ADMIN_PASSWORD="${WILDTAG_ADMIN_PASSWORD:-dev}"

echo "==> building"
go build -o wildtag ./cmd/wildtag

if [ ! -f "$DATA_DIR/keys.json" ]; then
  echo "==> minting keys"
  ./wildtag init
fi

echo "==> creating $TAGS tags"
BATCH=$(./wildtag mkbatch -n "$TAGS" 2>/dev/null | grep -oE 'B[0-9]{8}-[0-9A-F]{6}' | head -1)
echo "    batch $BATCH"

echo "==> writing the print sheet to $DATA_DIR/$BATCH.html"
./wildtag print -batch "$BATCH" -o "$DATA_DIR/$BATCH.html"

cat <<TEXT

Ready.

  ./wildtag serve -addr $ADDR

  dashboard  http://$ADDR/
  admin      http://$ADDR/admin     password: $WILDTAG_ADMIN_PASSWORD
  tag sheet  $DATA_DIR/$BATCH.html

Open the sheet, scan a code with a phone on the same network, or open one of
the URLs in the sheet directly.

Nothing here broadcasts. Arming a tag on the offline network will fail at the
wallet, because there is no arcade to accept the transaction and no money to
spend. Point WILDTAG_NETWORK and WILDTAG_ARCADE_URL at tstn to exercise that
half.
TEXT
