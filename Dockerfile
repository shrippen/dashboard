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
RUN apk add --no-cache wget \
 && adduser --system --uid 10001 --home /app andon \
 && mkdir -p /data && chown andon /data
ENV DATA_DIR=/data
COPY --from=build /out/andon /usr/local/bin/andon
USER andon
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD wget -q -O- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["andon"]
