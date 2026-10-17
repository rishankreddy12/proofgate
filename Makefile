.PHONY: build lint guard up down bench-overhead quickstart
LDFLAGS := -s -w -X github.com/proofgate/proofgate/internal/version.Version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

lint:
	go vet ./...
	golangci-lint run

guard:
	bash scripts/check_no_provider_sdk.sh go.mod
	bash scripts/check_dockerfile_go_version.sh

deploy/secrets/local_kek.b64:
	go run ./cmd/proofgatectl kek generate > $@

up: deploy/secrets/local_kek.b64
	docker compose -f deploy/docker-compose.yml up -d --build --wait

down:
	docker compose -f deploy/docker-compose.yml down -v

quickstart: up
	@bash scripts/welcome.sh

bench-overhead: up
	bash scripts/bench_overhead.sh
