GO        ?= go
CONFIG    ?= deploy/switchyard.example.yaml
COMPOSE   := docker compose -f deploy/docker-compose.yml
STATICCHECK := $(shell $(GO) env GOPATH)/bin/staticcheck

.PHONY: build test lint fmt up down run mock bench web tools

build:
	$(GO) build -o bin/switchyard ./cmd/switchyard
	$(GO) build -o bin/mockllm ./cmd/mockllm
	$(GO) build -o bin/loadgen ./cmd/loadgen
	$(GO) build -o bin/benchreport ./cmd/benchreport

test:
	$(GO) test -race ./...

lint: tools
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...
	$(STATICCHECK) ./...

fmt:
	gofmt -w .

tools:
	@test -x $(STATICCHECK) || $(GO) install honnef.co/go/tools/cmd/staticcheck@latest

up:
	$(COMPOSE) up -d --wait

down:
	$(COMPOSE) down

run:
	$(GO) run ./cmd/switchyard -config $(CONFIG)

mock:
	$(GO) run ./cmd/mockllm

bench:
	./bench/run.sh

web:
	cd web && npm run dev
