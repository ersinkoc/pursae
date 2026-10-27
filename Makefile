COMPOSE = docker compose -f docker/compose.yaml

.PHONY: dev db test docker-up docker-down docker-test build

## dev: run Postgres in Docker and the app locally, reading templates from ./web
dev: db
	WPGO_WEB_DIR=web go run ./cmd/wpgo serve

db:
	$(COMPOSE) up -d db

build:
	CGO_ENABLED=0 go build -o bin/wpgo ./cmd/wpgo

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
