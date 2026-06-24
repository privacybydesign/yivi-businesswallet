# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /app

ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -ldflags="-s -w" -o /bin/backend ./cmd/api
RUN go build -ldflags="-s -w" -o /bin/migrate ./cmd/migrate

FROM alpine:3.22

COPY --from=build /bin/backend /backend
COPY --from=build /bin/migrate /migrate

EXPOSE 8080

USER nobody

ENTRYPOINT ["/backend"]
