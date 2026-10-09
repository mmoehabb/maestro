BINARY  := maestro
PKG     := github.com/mmoehabb/maestro
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION)
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.9.0

.PHONY: build run test lint fmt tidy snapshot clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/maestro
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/maestrod ./cmd/maestrod

run: build
	./bin/$(BINARY) $(ARGS)

test:
	go test -race -count=1 ./...

lint:
	$(GOLANGCI_LINT) run

fmt:
	$(GOLANGCI_LINT) fmt

tidy:
	go mod tidy

snapshot:
	goreleaser release --snapshot --clean --skip=publish

clean:
	rm -rf bin dist
