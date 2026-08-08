#!/usr/bin/env bash
# QTSP CSC-signing demo driver (issue #194). Seeds a credential, calls
# /csc/v2/signatures/signHash, and cryptographically verifies the returned
# signature against the credential's certificate. Requires the `signer` profile
# to be up (docker compose --profile signer up) and `openssl`.
#
#   ./docker/development/qtsp-signer/run-demo.sh
set -euo pipefail
BASE=${BASE:-http://localhost:8085}
SUB=${SUB:-demo-user}
MSG=${MSG:-hello business wallet}
COMPOSE_MYSQL=${COMPOSE_MYSQL:-signer-mysql}

echo "== 1) seed a signing credential (SoftHSM key-gen + wrap + self-sign, no EJBCA) =="
SEED=$(curl -fsS -X POST "$BASE/spike/seed-credential?sub=$SUB")
echo "$SEED"
CRED=$(echo "$SEED" | sed -n 's/.*"credentialID"[: ]*"\([^"]*\)".*/\1/p')
[ -n "$CRED" ] || { echo "FAILED to obtain credentialID" >&2; exit 1; }

HASH=$(printf '%s' "$MSG" | openssl dgst -sha256 -binary | base64)
echo "credentialID=$CRED"
echo "message=\"$MSG\"  digest(b64)=$HASH"

echo "== 2) signHash =="
HTTP=$(curl -sS -o /tmp/qtsp-signhash.json -w '%{http_code}' -X POST "$BASE/csc/v2/signatures/signHash" \
  -H 'Content-Type: application/json' \
  -H "X-Stub-Sub: $SUB" -H "X-Stub-Credential-Id: $CRED" -H 'X-Stub-Num-Signatures: 1' \
  -H 'X-Stub-Hash-Alg: 2.16.840.1.101.3.4.2.1' -H "X-Stub-Hashes: $HASH" \
  -d "{\"credentialID\":\"$CRED\",\"hashes\":[\"$HASH\"],\"hashAlgorithmOID\":\"2.16.840.1.101.3.4.2.1\",\"signAlgo\":\"1.2.840.10045.4.3.2\",\"operationMode\":\"S\"}")
echo "HTTP $HTTP"; cat /tmp/qtsp-signhash.json; echo
[ "$HTTP" = "200" ] || { echo "signHash did not return 200" >&2; exit 1; }
SIG=$(sed -n 's/.*"signatures":\["\([^"]*\)".*/\1/p' /tmp/qtsp-signhash.json)

echo "== 3) verify the signature against the credential's certificate =="
CERT=$(docker compose exec -T "$COMPOSE_MYSQL" \
  mysql -N -uqtsp -pqtsp -e "SELECT certificate FROM qtsp.credentials WHERE id='$CRED';" 2>/dev/null | tr -d '\r\n ')
echo "$CERT" | base64 -d > /tmp/qtsp-cert.der
openssl x509 -inform DER -in /tmp/qtsp-cert.der -noout -subject -issuer >&2
openssl x509 -inform DER -in /tmp/qtsp-cert.der -pubkey -noout > /tmp/qtsp-pub.pem
echo "$SIG" | base64 -d > /tmp/qtsp-sig.der
printf '%s' "$MSG" > /tmp/qtsp-msg.txt
openssl dgst -sha256 -verify /tmp/qtsp-pub.pem -signature /tmp/qtsp-sig.der /tmp/qtsp-msg.txt
