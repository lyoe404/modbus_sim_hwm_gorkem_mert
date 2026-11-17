# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/temp-humidity-sim ./cmd/temp-humidity-sim && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/relay-sim ./cmd/relay-sim && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/thermocouple-sim ./cmd/thermocouple-sim && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/speed-pwm-sim ./cmd/speed-pwm-sim && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/remote-reset-sim ./cmd/remote-reset-sim

FROM alpine:3.22
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/* /usr/local/bin/
