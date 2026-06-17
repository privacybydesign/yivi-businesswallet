# syntax=docker/dockerfile:1
FROM golang:1.26-alpine

ENV CGO_ENABLED=0

WORKDIR /app

# Install air (Go live-reload tool).
RUN go install github.com/air-verse/air@latest

CMD ["air", "-c", ".air.toml"]
