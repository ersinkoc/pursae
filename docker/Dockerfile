FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wpgo ./cmd/wpgo

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 wpgo \
	&& mkdir -p /data/uploads /data/plugins /data/plugin-cache /data/mail /data/themes /data/wpgo-cache /data/languages && chown -R wpgo /data
COPY --from=build /out/wpgo /usr/local/bin/wpgo
# Demo marketplace: example plugin packages built by tools/build-plugins.sh (if present).
COPY examples/plugins/dis[t]/*.zip examples/plugins/dis[t]/dev-key.pub /usr/share/wpgo/plugins/
USER wpgo
EXPOSE 8080
ENTRYPOINT ["wpgo"]
CMD ["serve"]
