# syntax=docker/dockerfile:1

# Build context is backend/ (see .github/workflows/delivery.yml). A local
# OpenID4VP relying party for driving the business wallet's inbound presentation
# flow end to end on a dev or staging deployment — see cmd/devverifier and
# .ai/features/openid4vp-inbound.md §8. Staging/demo tooling, not a product
# component: it verifies presentations, it is not a qualified verifier.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /app

# GOWORK=off: a developer checkout may carry a go.work pointing at a local
# wallet-provider clone (the opt-in WSCA dev layer); this build wants neither.
ENV CGO_ENABLED=0 GOWORK=off

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

# No -tags wsca: the verifier talks to no wallet-provider, so it needs neither
# the private module nor a Go workspace.
RUN go build -ldflags="-s -w" -o /bin/devverifier ./cmd/devverifier

# Runtime: distroless static — no shell, no package manager, no OpenSSL. The
# binary is statically linked (CGO_ENABLED=0) and Go does its own TLS, so an
# Alpine base only added packages to scan (its libssl/libcrypto carried fixable
# criticals the binary never called). The image ships the CA bundle the verifier
# needs to fetch a did:web issuer document over HTTPS, and runs as the
# non-root user 65532.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /bin/devverifier /devverifier

EXPOSE 8090

ENTRYPOINT ["/devverifier"]
