COMPOSE = docker compose -f docker/compose.yaml

.PHONY: dev db test docker-up docker-down docker-test build publish-themes builder builder-watch builder-test builder-bump

## dev: run Postgres in Docker and the app locally, reading templates from ./web
dev: db
	WPGO_WEB_DIR=web go run ./cmd/wpgo serve

db:
	$(COMPOSE) up -d db

build:
	CGO_ENABLED=0 go build -buildmode=exe -tags nodynamic -o bin/wpgo ./cmd/wpgo

## test: unit tests plus the Postgres integration test against the compose database
test: db
	WPGO_TEST_ADMIN_DB=postgres://wpgo:wpgo@localhost:5432/wpgo?sslmode=disable \
	WPGO_TEST_DB=postgres://wpgo:wpgo@localhost:5432/wpgo_test?sslmode=disable \
	go test ./...

docker-up:
	$(COMPOSE) up -d --build

docker-down:
	$(COMPOSE) down

## docker-test: run the whole test suite inside a Go container next to Postgres
docker-test:
	$(COMPOSE) --profile test run --rm test

## publish-themes: import library/themes into Pursae Manager (tools/publish-themes.sh). Needs
## PURSAE_MANAGER_DIR (a pursae-manager checkout with its environment) or PURSAE_MANAGER (its binary);
## optional PREINSTALL=slug,slug|all (installed on every new site) and ACCESS=public|pro|private.
publish-themes:
	tools/publish-themes.sh $(if $(PREINSTALL),-preinstall $(PREINSTALL)) $(if $(ACCESS),-access $(ACCESS))

## builder: build the Pursae Web Builder (builder/ → web/web-builder/, committed). Needs Node 22+ (builder/.nvmrc)
## and git; Go and Docker builds use the committed dist and need no Node.
builder: builder/node_modules/.package-lock.json
	git submodule update --init builder/upstream
	cd builder && node scripts/build.mjs

builder/node_modules/.package-lock.json: builder/package.json builder/package-lock.json
	cd builder && npm ci --ignore-scripts --no-audit --no-fund

## builder-watch: rebuild the editor on change (pair with `make dev`, which reads web/ live)
builder-watch: builder/node_modules/.package-lock.json
	git submodule update --init builder/upstream
	cd builder && node scripts/build.mjs --watch

## builder-test: audit the built dist (no AI provider strings, manifest/BUILD.json shape, size budgets)
## and check BUILD.json against builder/UPSTREAM_SHA and the patch hashes
builder-test:
	node builder/test/audit.mjs
	go test ./web -run TestWebBuilderDistIsCurrent -count=1

## builder-bump SHA=<commit>: move builder/upstream and builder/UPSTREAM_SHA to an upstream commit, then
## rebuild (patches must still apply). Stages the pin; commit it with web/web-builder.
builder-bump:
	@test -n "$(SHA)" || { echo "usage: make builder-bump SHA=<upstream commit>"; exit 1; }
	git submodule update --init builder/upstream
	git -C builder/upstream fetch --quiet origin
	git -C builder/upstream -c advice.detachedHead=false checkout --quiet --detach "$(SHA)"
	git -C builder/upstream rev-parse HEAD > builder/UPSTREAM_SHA
	git add builder/upstream builder/UPSTREAM_SHA
	$(MAKE) builder
