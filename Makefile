COMPOSE = docker compose -f docker/compose.yaml
# Production: the development file plus compose.prod.yaml, settings from docker/.env (see docker/.env.example).
PROD = docker compose $(if $(wildcard docker/.env),--env-file docker/.env) -f docker/compose.yaml -f docker/compose.prod.yaml

.PHONY: bench dev db test docker-up docker-down prod-up prod-down prod-logs docker-test build publish-themes builder builder-watch builder-test builder-parity builder-bump release

## dev: run Postgres in Docker and the app locally, reading templates from ./web
dev: db
	WPGO_WEB_DIR=web go run -tags devhub ./cmd/wpgo serve

db:
	$(COMPOSE) up -d db

## bench: Pursae vs WordPress benchmark (bench/run.py, about an hour; docs/developer/benchmarks.md).
## Builds both stacks in Docker on ports 18081/18082, results in bench/results/<date>/. BENCH_ARGS passes
## options, e.g. BENCH_ARGS="--setups wp-stock,pursae-cache --runs 1 --duration 5s" for a smoke test.
bench:
	python3 bench/run.py $(BENCH_ARGS)

build:
	CGO_ENABLED=0 go build -buildmode=exe -tags nodynamic -o bin/wpgo ./cmd/wpgo

## test: unit tests plus the Postgres integration tests against the compose database. -p 1: the packages
## with integration tests (server, store, settings) each drop and recreate WPGO_TEST_DB, so they must not
## run at the same time; the internal/server suite alone takes about ten minutes, past go test's default
## timeout. TEST_DB names the database (default wpgo_test): give parallel checkouts their own.
TEST_DB ?= wpgo_test
test: db
	WPGO_TEST_ADMIN_DB=postgres://wpgo:wpgo@localhost:$${WPGO_DB_PORT:-5432}/wpgo?sslmode=disable \
	WPGO_TEST_DB=postgres://wpgo:wpgo@localhost:$${WPGO_DB_PORT:-5432}/$(TEST_DB)?sslmode=disable \
	go test ./... -p 1 -count=1 -timeout 40m

docker-up:
	$(COMPOSE) up -d --build

docker-down:
	$(COMPOSE) down

## prod-up: production build (updates from hub.pursae.com), settings from docker/.env, on 127.0.0.1 behind your TLS proxy
prod-up:
	@test -f docker/.env || { echo "Create docker/.env first: cp docker/.env.example docker/.env (set WPGO_DB_PASSWORD and WPGO_SITE_URL)"; exit 1; }
	$(PROD) up -d --build

prod-down:
	$(PROD) down

prod-logs:
	$(PROD) logs -f wpgo

## release: build every platform and publish GitHub release v$(VERSION) for Pursae Manager to import
## (tools/release.sh). BETA=1 marks a pre-release, DRY_RUN=1 only builds into dist/release.
release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=0.2.0 [BETA=1] [DRY_RUN=1]"; exit 2; }
	tools/release.sh $(VERSION) $(if $(BETA),--beta) $(if $(DRY_RUN),--dry-run)

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

## builder-test: host unit tests (vitest), UI string lint (no Turkish outside the i18n catalog), audit the built dist
## (no AI provider strings, manifest/BUILD.json shape, size budgets) and check BUILD.json against builder/UPSTREAM_SHA
## and the patch hashes. The host tests import the patched upstream copy (builder/.work/upstream), so it is
## prepared first from the pinned submodule and builder/patches (the dist in web/web-builder is not touched).
builder-test: builder/node_modules/.package-lock.json
	git submodule update --init builder/upstream
	cd builder && node scripts/build.mjs --prepare
	cd builder && npm test --silent
	node builder/scripts/lint-strings.mjs
	node builder/test/audit.mjs
	go test ./web -run TestWebBuilderDistIsCurrent -count=1

## builder-parity: Playwright parity suite (builder/test/parity): every renderStatic fixture drawn by the editor's live
## preview and as the static snapshot, pixel-compared at 1280/820/400 px (≤ 0.1 % differing pixels), the snapshot
## without JavaScript, and the site runtime's behaviours. Needs `make builder` (run here when its scratch copy is
## missing) and Playwright's Chromium; skips with a message when the browser is not installed
## (cd builder && npx playwright install chromium); set PARITY_STRICT=1 (CI) to make a skip fail. Report: builder/.work/parity/report.json.
builder-parity: builder/node_modules/.package-lock.json
	@test -d builder/.work/upstream/src/builder/live || $(MAKE) builder
	@cd builder && { node test/parity/check-browsers.mjs; rc=$$?; if [ $$rc -eq 3 ]; then if [ -n "$$PARITY_STRICT" ]; then echo '[parity] PARITY_STRICT set: skip counts as failure'; exit 1; fi; exit 0; elif [ $$rc -ne 0 ]; then exit $$rc; fi; \
		npx playwright test -c test/parity/playwright.config.ts; }

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
