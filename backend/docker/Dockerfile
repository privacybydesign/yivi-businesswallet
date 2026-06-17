# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

ENV CGO_ENABLED 0

COPY ../go.mod go.sum ./
RUN go mod download

COPY .. .

RUN go build -o bin/backend main.go

CMD ["/src/bin/backend"]
