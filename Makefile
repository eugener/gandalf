.PHONY: build test lint check clean run

BINARY := gandalf
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

# json/v2 experiment: ~8 fewer allocs in JSON-heavy hot paths.
# Safe to use: all tests pass, no code changes needed.
# Will become default in a future Go release.
export GOEXPERIMENT := jsonv2

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/gandalf

run: build
	./bin/$(BINARY) -config configs/gandalf.yaml

test:
	go test -race -count=1 ./...

bench:
	@go test -run='^$$' -bench=. -benchmem ./internal/server/ | awk '/^Benchmark/ { \
		name=$$1; sub(/-[0-9]+$$/, "", name); sub(/^Benchmark/, "", name); \
		rps=int(1000000000/$$3); \
		printf "%-28s %8s ns/op  %6d rps  %8s B/op  %4s allocs/op\n", name, $$3, rps, $$5, $$7 }'

lint:
	go vet ./...
	golangci-lint run

check: ## Full verification pipeline
	go build ./...
	go fix ./...
	go vet ./...
	go test -race -count=1 ./...
	govulncheck ./...
	$(MAKE) bench

clean:
	rm -rf bin/ coverage.out

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

docker:
	docker build -f deploy/Dockerfile -t $(BINARY):$(VERSION) .
