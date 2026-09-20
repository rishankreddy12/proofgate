.PHONY: build test test-integration lint guard e2e up down
LDFLAGS := -s -w -X github.com/proofgate/proofgate/internal/version.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

test: guard
	go test -race ./...

test-integration:
	go test -race -tags integration ./...

lint:
	go vet ./...
	golangci-lint run

guard:
	bash scripts/tests/test_check_no_provider_sdk.sh
	bash scripts/check_no_provider_sdk.sh go.mod

up:
	docker compose -f deploy/docker-compose.yml up -d --build --wait

down:
	docker compose -f deploy/docker-compose.yml down -v

e2e: up
	go test -race -tags e2e -count=1 ./e2e/...
	bash scripts/sdk_smoke.sh
