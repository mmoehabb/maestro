BINARY  := maestro
PKG     := github.com/mmoehabb/maestro
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION)

.PHONY: build run test lint fmt tidy snapshot clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/maestro

run: build
	./bin/$(BINARY) $(ARGS)

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

tidy:
	go mod tidy

snapshot:
	goreleaser release --snapshot --clean --skip=publish

clean:
	rm -rf bin dist
