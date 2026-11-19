#!/bin/sh
# Provision a fresh Domibus access point: upload a PMode and persist a message
# filter. Both are idempotent, so this re-runs cleanly on every `up`.
#
# Usage: provision.sh <host> <description>
#   host        Domibus service name, e.g. "domibus" or "domibus-verid"
#   description free text stored with the PMode
# The PMode itself is mounted at /pmode.xml.
#
# Used by the `verid` profile's provisioners (compose.override.yaml). The
# single-gateway `domibus-provision` service keeps its own inline copy of this
# logic; it predates this script and is left alone deliberately.
set -e

HOST="$1"
DESCRIPTION="$2"
BASE="http://${HOST}:8080/domibus"

if [ -z "$HOST" ]; then
  echo "provision.sh: <host> is required" >&2
  exit 2
fi

# Domibus uses CSRF double-submit: the XSRF-TOKEN cookie set at login must be
# echoed in the X-XSRF-TOKEN header on every write.
curl -s -c /tmp/cj.txt -o /dev/null -H "Content-Type: application/json" \
  -X POST "${BASE}/rest/security/authentication" \
  -d '{"username":"admin","password":"123456"}'
XSRF=$(grep -i XSRF-TOKEN /tmp/cj.txt | awk '{print $7}')
if [ -z "$XSRF" ]; then
  echo "provision.sh: no XSRF-TOKEN from ${BASE} — is the admin console up?" >&2
  exit 1
fi

echo "[${HOST}] uploading PMode (${DESCRIPTION})..."
curl -sf -b /tmp/cj.txt -H "X-XSRF-TOKEN: $XSRF" \
  -F "file=@/pmode.xml" -F "description=${DESCRIPTION}" \
  -X POST "${BASE}/rest/pmode"
echo "[${HOST}] PMode uploaded"

# The image ships THREE backend plugins (WS + JMS + FS) with no persisted message
# filter, so a *received* message matches no backend and is dropped to
# notification.unknown — listPendingMessages then stays empty and the recipient
# never sees it. Persist a filter list with backendWebservice first (no routing
# criteria => it catches everything) so retrieveMessage works.
echo "[${HOST}] configuring message filter (WS plugin first)..."
curl -sf -b /tmp/cj.txt -H "X-XSRF-TOKEN: $XSRF" -H "Content-Type: application/json" \
  -X PUT "${BASE}/rest/messagefilters" \
  -d '[{"entityId":0,"index":0,"routingCriterias":[],"backendName":"backendWebservice","persisted":true},{"entityId":0,"index":1,"routingCriterias":[],"backendName":"backendFSPlugin","persisted":true},{"entityId":0,"index":2,"routingCriterias":[],"backendName":"Jms","persisted":true}]'
echo "[${HOST}] message filter configured"
