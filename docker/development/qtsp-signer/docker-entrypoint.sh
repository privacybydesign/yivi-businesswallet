#!/bin/sh
# Entrypoint for the QTSP demo image. One image carries both the CSC resource
# server and the OAuth2/OID4VP authorization server; QTSP_SERVER selects which to
# run (resource | authz, default resource).
#
#  - authz    -> the authorization server (port 8084). No HSM; just start the jar.
#  - resource -> the CSC resource server (port 8085). Initializes a SoftHSM token
#                on first boot, resolves its CK_SLOT_ID, and exports the jacknji11
#                env the app's HsmService reads (JACKNJI11_*), then starts the jar.
set -eu

QTSP_SERVER="${QTSP_SERVER:-resource}"

if [ "$QTSP_SERVER" = "authz" ]; then
  echo "entrypoint: starting authorization_server (profiles=${SPRING_PROFILES_ACTIVE:-default})"
  exec java -jar /authorization_server.jar "$@"
fi

# ---------------------------------------------------------------------------
# resource server: SoftHSM (the QSCD stand-in)
# ---------------------------------------------------------------------------
: "${PKCS11_TOKEN_LABEL:=qtsp-token}"
: "${PKCS11_PIN:=1234}"
: "${PKCS11_SO_PIN:=1234}"

# Locate the SoftHSM PKCS#11 module (path varies by distro/arch).
LIB=""
for cand in \
  /usr/lib/softhsm/libsofthsm2.so \
  /usr/lib/x86_64-linux-gnu/softhsm/libsofthsm2.so \
  /usr/lib/aarch64-linux-gnu/softhsm/libsofthsm2.so ; do
  if [ -f "$cand" ]; then LIB="$cand"; break; fi
done
if [ -z "$LIB" ]; then echo "entrypoint: FATAL could not find libsofthsm2.so" >&2; exit 1; fi

# Initialize the token once (idempotent across restarts of the same volume).
if softhsm2-util --show-slots 2>/dev/null | grep -q "$PKCS11_TOKEN_LABEL"; then
  echo "entrypoint: SoftHSM token '$PKCS11_TOKEN_LABEL' already initialized"
else
  echo "entrypoint: initializing SoftHSM token '$PKCS11_TOKEN_LABEL'"
  softhsm2-util --init-token --free --label "$PKCS11_TOKEN_LABEL" \
    --so-pin "$PKCS11_SO_PIN" --pin "$PKCS11_PIN"
fi

# Resolve the CK_SLOT_ID SoftHSM assigned to our token (a large reassigned id after
# --free init, NOT necessarily 0), which is what CE.OpenSession(slot) needs.
SLOT_ID=$(softhsm2-util --show-slots | awk -v lbl="$PKCS11_TOKEN_LABEL" '
  /^Slot [0-9]+/ { slot=$2 }
  index($0,"Label:")>0 && index($0,lbl)>0 { print slot; exit }')
if [ -z "${SLOT_ID:-}" ]; then echo "entrypoint: FATAL could not resolve slot id" >&2; exit 1; fi
echo "entrypoint: token '$PKCS11_TOKEN_LABEL' on slot $SLOT_ID (module $LIB)"

export JACKNJI11_PKCS11_LIB_PATH="$LIB"
export JACKNJI11_TEST_TESTSLOT="$SLOT_ID"
export JACKNJI11_TEST_INITSLOT="$SLOT_ID"
export JACKNJI11_TEST_USER_PIN="$PKCS11_PIN"
export JACKNJI11_TEST_SO_PIN="$PKCS11_SO_PIN"

echo "entrypoint: starting resource_server (profiles=${SPRING_PROFILES_ACTIVE:-default})"
exec java -jar /resource_server.jar "$@"
