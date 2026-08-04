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
