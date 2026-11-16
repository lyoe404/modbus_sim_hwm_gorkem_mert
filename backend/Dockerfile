# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Pure-Go SQLite driver (modernc.org/sqlite): no cgo, so a static binary is enough.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/backend ./cmd/backend

FROM alpine:3.22
RUN adduser -D -u 10001 app && mkdir /data && chown app /data
USER app
WORKDIR /app
COPY --from=build /out/backend /app/backend
ENV HTTP_ADDR=:8080 DB_PATH=/data/backend.db
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/backend"]
