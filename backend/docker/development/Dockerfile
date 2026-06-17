# syntax=docker/dockerfile:1
FROM golang:1.26-alpine

ENV CGO_ENABLED=0

WORKDIR /app

CMD ["go", "tool", "air", "-c", ".air.toml"]
