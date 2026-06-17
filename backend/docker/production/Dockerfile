# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /app

ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -ldflags="-s -w" -o /bin/backend main.go

FROM gcr.io/distroless/static-nonroot

COPY --from=build /bin/backend /backend

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/backend", "-healthcheck"]

USER nonroot:nonroot

ENTRYPOINT ["/backend"]
