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

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./

RUN go build -ldflags="-s -w" -o /bin/backend ./cmd/api
RUN go build -ldflags="-s -w" -o /bin/migrate ./cmd/migrate

# ---------- Runtime ----------
FROM alpine:3.22

COPY --from=build /bin/backend /backend
COPY --from=build /bin/migrate /migrate
COPY --from=frontend /app/frontend/dist /frontend

# The API serves the bundled frontend as an SPA from this directory.
ENV STATIC_DIR=/frontend

EXPOSE 8080

USER nobody

ENTRYPOINT ["/backend"]
