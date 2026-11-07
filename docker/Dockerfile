FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION (optional) is the core version the binary reports, e.g. --build-arg VERSION=0.4.0.
ARG VERSION=
# A static binary (no ELF interpreter), so the same file runs on musl and glibc systems:
# -buildmode=exe, and -tags nodynamic keeps github.com/gen2brain/avif off purego (it decodes AVIF
# with its embedded WebAssembly build instead of a system libavif).
RUN CGO_ENABLED=0 go build -trimpath -buildmode=exe -tags nodynamic -ldflags="-s -w${VERSION:+ -X wpgo/internal/server.Version=$VERSION}" -o /out/wpgo ./cmd/wpgo

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 wpgo \
	&& mkdir -p /data/uploads /data/plugins /data/plugin-cache /data/mail /data/themes /data/wpgo-cache /data/languages /data/bin \
	&& chown -R wpgo /data
# The image's binary; `wpgo launch` installs it into /data/bin (a volume) and runs it from there, so
# Dashboard › Updates can replace it and the update survives restarts, while a newer image still wins.
COPY --from=build /out/wpgo /usr/local/lib/pursae/wpgo
RUN ln -s /usr/local/lib/pursae/wpgo /usr/local/bin/wpgo
# Demo marketplace: example plugin packages built by tools/build-plugins.sh (if present).
COPY examples/plugins/dis[t]/*.zip examples/plugins/dis[t]/dev-key.pub /usr/share/wpgo/plugins/
USER wpgo
ENV WPGO_BIN_DIR=/data/bin
EXPOSE 8080
# Liveness: GET /healthz on WPGO_ADDR through the binary itself (no curl in the image). The start
# period covers the migrations a new version runs before it listens.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 CMD ["/usr/local/lib/pursae/wpgo", "healthcheck"]
ENTRYPOINT ["/usr/local/lib/pursae/wpgo", "launch"]
CMD ["serve"]
