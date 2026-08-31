# syntax=docker/dockerfile:1
#
# Qualified-signing demo image (issue #194). Builds the EUDI wallet-driven /
# RP-centric QTSP reference implementation at a pinned commit, applies a small
# demo patch, and ships it with SoftHSM2 as the QSCD stand-in so it can produce
# real CSC-API-v2 signatures on a laptop.
#
# DEMO ONLY. This proves QES-over-CSC *plumbing* on a software HSM; it is not a
# qualified trust service and the patch below trades qualified behaviour for
# local runnability. See README.md and .ai/features/qtsp-signing-demo.md.

ARG UPSTREAM_REPO=https://github.com/eu-digital-identity-wallet/eudi-srv-web-walletdriven-rpcentric-signer-qtsp-java.git
# Pinned so the demo is reproducible; bump deliberately.
#
# 4f9b14d (upstream 0.5.0) is the first ref whose authorization server sends
# `intended_use_id` / `registration_certificate` on the OID4VP InitTransaction
# request. The EUDI verifier now REQUIRES one of the two — without it
# /ui/presentations answers `400 {"error":"MissingRegistrationCertificate"}` and
# the wallet ceremony never starts. The bumped VerifierConfig also @AssertTrue's
# that one of them is configured, so INTENDED-USE-ID (or
# REGISTRATION-CERTIFICATE-JWT) MUST be passed to the authz server from here on
# or it fails to boot. See compose.override.yaml and the ops repo's qtsp_authz
# deployment.
ARG UPSTREAM_REF=4f9b14d74bb9cff84c1c822f5a9ef7bfeb3b36ff

# --------------------------------------------------------------------------
# Build stage: clone upstream, apply the demo patch, compile.
# --------------------------------------------------------------------------
FROM maven:3.8.5-eclipse-temurin-17 AS builder
ARG UPSTREAM_REPO
ARG UPSTREAM_REF
RUN apt-get update \
 && apt-get install -y --no-install-recommends git \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN git clone "$UPSTREAM_REPO" . && git checkout "$UPSTREAM_REF"

# qtsp-demo.patch carries three things, all clearly marked [SPIKE]/[DEMO] at the
# call sites (git diff against the pinned commit — regenerate if you bump the ref):
#   1. SoftHSM-portable PKCS#11 mechanisms (CKM_AES_KEY_WRAP_PAD; pre-hash + raw
#      ECDSA) — upstream targets Utimaco's laxer PKCS#11.
#   2. An EJBCA-free self-sign branch (ejbca.enabled=false) so a credential can be
#      created without a Certificate Authority.
#   3. Sourcing authz claims from X-Stub-* headers under the `noauth` profile.
COPY qtsp-demo.patch /tmp/qtsp-demo.patch
RUN git apply --verbose /tmp/qtsp-demo.patch

# The noauth-only seed/health controller is a new file (not in the patch).
COPY src/SpikeSeedController.java \
     resource_server/src/main/java/eu/europa/ec/eudi/signer/r3/resource_server/web/controllers/SpikeSeedController.java

RUN mvn -B -e -DskipTests clean install

# --------------------------------------------------------------------------
# Runtime stage: JRE + SoftHSM2 (the software QSCD) + BOTH servers.
# One image carries both jars; docker-entrypoint.sh runs the one named by
# QTSP_SERVER (resource | authz). The resource server (CSC API, port 8085) needs
# SoftHSM; the authorization server (OAuth2 + OID4VP, port 8084) does not.
# --------------------------------------------------------------------------
FROM eclipse-temurin:17
WORKDIR /opt/app
# softhsm2 -> the PKCS#11 module jacknji11 loads (the "HSM"/QSCD stand-in)
# opensc   -> pkcs11-tool for debugging the token
# curl     -> container HEALTHCHECK
RUN apt-get update \
 && apt-get install -y --no-install-recommends softhsm2 opensc curl \
 && rm -rf /var/lib/apt/lists/*
COPY --from=builder /src/resource_server/target/*.jar /resource_server.jar
COPY --from=builder /src/authorization_server/target/*.jar /authorization_server.jar
# The authorization server reads its trusted PID-issuer anchors from
# ./certificate_of_issuers (relative to WORKDIR), so ship the ones from upstream.
COPY --from=builder /src/certificate_of_issuers ./certificate_of_issuers
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN sed -i 's/\r$//' /usr/local/bin/docker-entrypoint.sh \
 && chmod +x /usr/local/bin/docker-entrypoint.sh
EXPOSE 8084 8085
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
