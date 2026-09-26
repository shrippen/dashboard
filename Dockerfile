# syntax=docker/dockerfile:1

# ── Build: static binary, no cgo (ncruces/go-sqlite3 runs SQLite as Wasm — no C
#    toolchain needed, which is the whole point on a Raspberry Pi target) ──
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/andon ./cmd/andon

# ── Runtime ──
FROM alpine:3.20
RUN apk add --no-cache wget su-exec \
 && addgroup -S -g 10001 andon \
 && adduser -S -u 10001 -G andon -h /app andon \
 && mkdir -p /data && chown andon:andon /data
ENV DATA_DIR=/data
COPY --from=build /out/andon /usr/local/bin/andon
# Starts as root to fix /data ownership and read secrets, then drops to
# PUID:PGID (default 10001:10001).
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD wget -q -O- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["entrypoint.sh"]
