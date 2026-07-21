# syntax=docker/dockerfile:1

# Build context is the repository root (see .github/workflows/delivery.yml) so
# this image can bundle both the Go API and the built frontend, letting one
# container serve the SPA on "/" and the API on "/api/v1".

# ---------- Frontend build ----------
FROM --platform=$BUILDPLATFORM node:24-alpine AS frontend

WORKDIR /app/frontend

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
# Same-origin deployment: the SPA calls the API via relative /api/v1 paths, so
# VITE_API_BASE_URL is left empty. Outputs to /app/frontend/dist.
RUN npm run build

# ---------- Backend build ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /app

ENV CGO_ENABLED=0

# The API binary is built with -tags wsca so a deployment configured with
# ATTESTATION_HOLDER_WSCA_URL boots (the default build fails that guard at
# startup). The tag links in the private wallet-provider (walletmobile) module,
# resolved through a Go workspace against a clone of that repo — the same
# approach as the backend-build-wsca CI job. Pin the commit so the image is
# reproducible; keep WALLET_PROVIDER_REF in sync with that job.
ARG WALLET_PROVIDER_REF=3f463ccc34aa617ce668cc2158de6e75b1e4967c

RUN apk add --no-cache git openssh-client

# Clone the private wallet-provider next to the backend module. The deploy key is
# supplied as a BuildKit secret (never written to an image layer); it mirrors the
# WALLET_PROVIDER_DEPLOY_KEY the CI wsca job uses.
RUN --mount=type=secret,id=wallet_provider_key,required=true \
    mkdir -p ~/.ssh && chmod 700 ~/.ssh && \
    ssh-keyscan github.com >> ~/.ssh/known_hosts 2>/dev/null && \
    GIT_SSH_COMMAND="ssh -i /run/secrets/wallet_provider_key -o IdentitiesOnly=yes" \
      git clone git@github.com:privacybydesign/wallet-provider.git /wallet-provider && \
    git -C /wallet-provider checkout "${WALLET_PROVIDER_REF}"

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

# Stitch the backend module and the wallet-provider clone into a Go workspace so
# the -tags wsca build resolves walletmobile locally (no module proxy, no
# checksum-DB lookup for the private repo). Mirrors the CI wsca job's go.work.
RUN go work init . /wallet-provider

RUN go build -tags wsca -ldflags="-s -w" -o /bin/backend ./cmd/api
RUN go build -ldflags="-s -w" -o /bin/migrate ./cmd/migrate
RUN go build -ldflags="-s -w" -o /bin/seed ./cmd/seed

# ---------- Runtime ----------
FROM alpine:3.22

COPY --from=build /bin/backend /backend
COPY --from=build /bin/migrate /migrate
COPY --from=build /bin/seed /seed
COPY --from=frontend /app/frontend/dist /frontend

# The API serves the bundled frontend as an SPA from this directory.
ENV STATIC_DIR=/frontend

EXPOSE 8080

USER nobody

ENTRYPOINT ["/backend"]
