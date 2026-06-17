#!/bin/sh
set -e

LOCK_HASH="$(sha256sum package-lock.json | cut -d' ' -f1)"
MARKER="node_modules/.lockhash"

if [ "$LOCK_HASH" != "$(cat "$MARKER" 2>/dev/null)" ]; then
  echo "Lockfile changed — installing dependencies…"
  npm ci
  echo "$LOCK_HASH" > "$MARKER"
fi

exec "$@"
