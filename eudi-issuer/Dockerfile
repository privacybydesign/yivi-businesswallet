# The EU reference issuer for the WE BUILD rulebook credentials, with
# issuer-state-in-take.patch on top. A new upstream tag fails the build until the
# patch applies to it.

FROM ghcr.io/eu-digital-identity-wallet/eudi-srv-web-issuing-eudiw-py:0.9.4@sha256:958378ef3ccd3258f465b3a92b4b20fa55c3cd766e59eb29c91fb91e2674a9c3 AS upstream

FROM busybox:1.37@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec AS patched
WORKDIR /src
COPY --from=upstream /app/app/formatter_func.py /app/app/dynamic_func.py app/
COPY issuer-state-in-take.patch .
RUN patch -p1 < issuer-state-in-take.patch

FROM upstream
COPY --from=patched /src/app/formatter_func.py /src/app/dynamic_func.py /app/app/
