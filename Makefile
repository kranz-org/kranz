.PHONY: all build run install fmt-check test vet verify lint clean snapshot release-check tag

APP_NAME := kranz
BIN_DIR := bin
GO := go
GORELEASER_VERSION ?= v2.17.0
GORELEASER := $(GO) run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

all: build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP_NAME) ./cmd/$(APP_NAME)

run: build
	./$(BIN_DIR)/$(APP_NAME)

install:
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/$(APP_NAME)

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo "Go files required formatting" >&2; exit 1)

test:
	$(GO) test -race -cover ./...

vet:
	$(GO) vet ./...

verify: fmt-check vet test build

lint:
	@command -v golangci-lint >/dev/null || (echo "golangci-lint is not installed" >&2; exit 1)
	golangci-lint run ./...

release-check:
	$(GORELEASER) check

snapshot:
	$(GORELEASER) release --snapshot --clean

tag:
	@test -n "$(RELEASE_VERSION)" || (echo "usage: make tag RELEASE_VERSION=0.1.0" >&2; exit 2)
	./scripts/tag-release.sh "$(RELEASE_VERSION)"

clean:
	rm -rf $(BIN_DIR) dist .release
