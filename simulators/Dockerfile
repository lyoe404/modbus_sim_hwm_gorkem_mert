# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/modbus-sim ./cmd/modbus-sim

FROM alpine:3.22
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/modbus-sim /usr/local/bin/modbus-sim
ENV MODBUS_ADDR=0.0.0.0:5020 ADMIN_ADDR=0.0.0.0:5021
EXPOSE 5020 5021
HEALTHCHECK --interval=5s --timeout=3s --start-period=3s --retries=5 \
  CMD wget -q -O /dev/null http://127.0.0.1:5021/state || exit 1
ENTRYPOINT ["/usr/local/bin/modbus-sim"]
