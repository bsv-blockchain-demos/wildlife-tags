#!/usr/bin/env bash
# Destroy a development deployment and start over.
#
# Refuses to touch anything holding money. The check is not paranoia: a wallet
# with a balance and a keys.json is a live deployment, and the tags it has armed
# are on animals in the water. Deleting its seed strands every one of those
# rewards permanently -- derived keys are the only way back to them.
set -euo pipefail

cd "$(dirname "$0")/.."

DATA_DIR="${WILDTAG_DATA_DIR:-./data-dev}"

if [ ! -d "$DATA_DIR" ]; then
  echo "nothing at $DATA_DIR"
  exit 0
fi

if [ -f "$DATA_DIR/keys.json" ]; then
  BALANCE=$(WILDTAG_DATA_DIR="$DATA_DIR" ./wildtag address 2>/dev/null | awk '/^balance/ {print $2}' || echo "unknown")

  if [ "$BALANCE" = "unknown" ]; then
    echo "Refusing to reset $DATA_DIR: could not read its balance."
    echo "A wallet whose balance cannot be read is one whose contents are unknown."
    exit 1
  fi

  if [ "$BALANCE" != "0" ]; then
    echo "Refusing to reset $DATA_DIR: it holds $BALANCE satoshis."
    echo
    echo "Sweep or drain it first. Deleting keys.json destroys the master seed,"
    echo "and with it every tag key ever printed from this deployment -- including"
    echo "the rewards on tags currently attached to live crabs."
    exit 1
  fi
fi

TAGS=$(python3 -c "
import sqlite3, sys
try:
    print(sqlite3.connect('$DATA_DIR/tags.db').execute('select count(*) from tags').fetchone()[0])
except Exception:
    print(0)
" 2>/dev/null || echo 0)

echo "About to delete $DATA_DIR"
echo "  keys.json:  $([ -f "$DATA_DIR/keys.json" ] && echo present || echo absent)"
echo "  tags:       $TAGS"
echo
read -r -p "Type 'destroy' to continue: " CONFIRM
[ "$CONFIRM" = "destroy" ] || { echo "aborted"; exit 1; }

rm -rf "$DATA_DIR"
echo "removed $DATA_DIR"
