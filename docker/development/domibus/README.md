# Domibus bench keystores

`gateway_keystore.jks` / `gateway_truststore.jks` overlay the ones baked into the
`fiware/domibus-tomcat` image, which ship the standard `blue_gw` / `red_gw`
sample certificates — **expired 2025-12-01**. With the expired certs the AS4
self-send fails on receipt (WSS4J `SignatureTrustValidator`:
`CertificateExpiredException` → `EBMS:0005` / `EBMS:0004`), so a message never
reaches `SENT`. These regenerated certs are valid for 10 years from generation.

They keep the image's aliases (`blue_gw`, `red_gw`) and password (`test123`), so
`domibus.properties` needs no change — they're mounted in the `domibus` service
in `compose.override.yaml`. **Bench only — self-signed test certs, never for
real qualified delivery.**

## Regenerate

```sh
cd docker/development/domibus
rm -f gateway_keystore.jks gateway_truststore.jks
for a in blue_gw red_gw; do
  keytool -genkeypair -alias "$a" -keyalg RSA -keysize 2048 -sigalg SHA256withRSA \
    -validity 3650 -dname "CN=$a, O=QERDS Bench, C=NL" \
    -keystore gateway_keystore.jks -storepass test123 -keypass test123 -storetype JKS
  keytool -exportcert -alias "$a" -keystore gateway_keystore.jks -storepass test123 -rfc -file "$a.cer"
  keytool -importcert -noprompt -alias "$a" -file "$a.cer" \
    -keystore gateway_truststore.jks -storepass test123 -storetype JKS
done
rm -f blue_gw.cer red_gw.cer
```

Type must be `JKS` (the image's `domibus.security.keystore.type=jks`), not the
modern keytool `PKCS12` default.

## ver.id gateway keystores (`verid` profile)

`verid_keystore.jks` / `verid_truststore.jks` belong to the **second** access
point, the one standing in for ver.id in the two-gateway bench
(`--profile domibus --profile verid`). They exist so an inbound credential offer
arrives from a genuinely foreign party: signed with ver.id's own key, over a real
cross-gateway AS4 leg.

That distinction is the whole point. A single-gateway loopback signs with *our*
key, so it can never exercise "do we accept a message from someone else" — and
Domibus will not let a submission claim a `From` party that is not the submitting
gateway's own, so the loopback cannot fake it either.

| File | Holds | Mounted into |
|---|---|---|
| `verid_keystore.jks` | private key, alias `verid_gw` | `domibus-verid` as its `gateway_keystore.jks` |
| `verid_truststore.jks` | `blue_gw`, `red_gw`, `verid_gw` certs | `domibus-verid` as its `gateway_truststore.jks` |
| `gateway_truststore.jks` | now also holds `verid_gw` | `domibus` (ours), so it trusts ver.id's signature |

The keystores are mounted at the image's stock paths, so `domibus.properties`
needs no change. One property *is* overridden, in `compose.override.yaml`:
`-Ddomibus.security.key.private.alias=verid_gw`. Without it the second gateway
would sign as `blue_gw` and be a copy of ours rather than a separate party.

**Bench only — self-signed test certs, never for real qualified delivery.**

### Regenerate

Run from this directory. This creates ver.id's keypair, adds its certificate to
our truststore, and builds ver.id's truststore from our certificates — i.e. the
certificate exchange a real partner onboarding performs out of band.

```sh
cd docker/development/domibus
rm -f verid_keystore.jks verid_truststore.jks

# ver.id's own AS4 identity.
keytool -genkeypair -alias verid_gw -keyalg RSA -keysize 2048 -sigalg SHA256withRSA   -validity 3650 -dname "CN=verid_gw, O=Ver.ID QERDS Bench, C=NL"   -keystore verid_keystore.jks -storepass test123 -keypass test123 -storetype JKS
keytool -exportcert -alias verid_gw -keystore verid_keystore.jks -storepass test123 -rfc -file verid_gw.cer

# Our gateway must trust ver.id's signature.
keytool -importcert -noprompt -alias verid_gw -file verid_gw.cer   -keystore gateway_truststore.jks -storepass test123 -storetype JKS

# ver.id's gateway must trust ours (AS4 encrypts to the receiver's certificate).
for a in blue_gw red_gw; do
  keytool -exportcert -alias $a -keystore gateway_keystore.jks -storepass test123 -rfc -file $a.cer
  keytool -importcert -noprompt -alias $a -file $a.cer -keystore verid_truststore.jks -storepass test123 -storetype JKS
done
keytool -importcert -noprompt -alias verid_gw -file verid_gw.cer -keystore verid_truststore.jks -storepass test123 -storetype JKS
rm -f blue_gw.cer red_gw.cer verid_gw.cer
```

Re-running the `gateway_truststore.jks` import on a store that already has the
alias fails; delete the alias first (`keytool -delete -alias verid_gw ...`) if
you are rotating.

Note `*.jks binary` in `.gitattributes`: without it a Windows checkout would
line-ending-normalise these files and silently corrupt them.

## PMode files

| File | Gateway | Purpose |
|---|---|---|
| `../../../backend/internal/qerdsprovider/testdata/pmode.xml` | ours | blue→red loopback. Embedded by the integration test, uploaded by `domibus-provision`. **Leave alone** — CI depends on it |
| `pmode-ours-with-verid.xml` | ours | the loopback **plus** ver.id as an initiator-only party. Uploaded by `domibus-provision-verid`, which is ordered after the loopback provisioner so it wins |
| `pmode-verid.xml` | ver.id's | their side: `verid_gw` initiates, our `blue_gw` responds |

Send-only is structural in both files: `verid_gw` never appears under
`<responderParties>`, so neither gateway will push toward ver.id. Nothing else
enforces the one-way channel, so do not "tidy" that asymmetry away.

`provision.sh` is the shared login + PMode-upload + message-filter script the two
`verid` provisioners use. The older `domibus-provision` service keeps its own
inline copy; it predates the script and is left as-is deliberately.

