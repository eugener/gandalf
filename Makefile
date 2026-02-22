.PHONY: build test lint check clean run

BINARY := gandalf
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/gandalf

run: build
	./bin/$(BINARY) -config configs/gandalf.yaml

test:
	go test -race -count=1 ./...

bench:
	go test -bench=. -benchmem ./...

lint:
	go vet ./...
	golangci-lint run

check: ## Full verification pipeline
	go build ./...
	go fix ./...
	go vet ./...
	go test -race -count=1 ./...
	govulncheck ./...

clean:
	rm -rf bin/ coverage.out

coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

docker:
	docker build -f deploy/Dockerfile -t $(BINARY):$(VERSION) .
