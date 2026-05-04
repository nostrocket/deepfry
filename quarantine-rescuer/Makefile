APP=quarantine-rescue
PKG=quarantine-rescuer

VERSION ?= dev

ifeq ($(OS),Windows_NT)
    GIT_COMMIT = $(shell git rev-parse --short HEAD 2>nul || echo unknown)
    BUILD_TIME = $(shell powershell -Command "Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ'")
    BINARY_EXT = .exe
else
    GIT_COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
    BUILD_TIME = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
    BINARY_EXT =
endif

LDFLAGS=-X 'main.Version=$(VERSION)' \
        -X 'main.Commit=$(GIT_COMMIT)' \
        -X 'main.Built=$(BUILD_TIME)'

BUILD_FLAGS=-ldflags "$(LDFLAGS)"

.PHONY: all build run test fmt vet tidy clean help build-alpine build-linux lint lint-fix

all: build

## Build the rescuer binary
build:
	go build $(BUILD_FLAGS) -o bin/$(APP)$(BINARY_EXT) ./cmd/$(APP)

## Run the rescuer (passes through args, e.g. make run ARGS="--dry-run")
run:
	go run $(BUILD_FLAGS) ./cmd/$(APP) $(ARGS)

## Build static binary for Alpine Linux (musl libc)
build-alpine:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP)-alpine ./cmd/$(APP)
	@echo "Built static binary for Alpine Linux: bin/$(APP)-alpine"

## Build static binary for generic Linux
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP)-linux ./cmd/$(APP)
	@echo "Built static binary for Linux: bin/$(APP)-linux"

## Run tests
test:
	go test ./... -short -cover

## Format code
fmt:
	go fmt ./...

## Vet code
vet:
	go vet ./...

## Update dependencies
tidy:
	go mod tidy

## Clean build artifacts
clean:
ifeq ($(OS),Windows_NT)
	if exist bin rmdir /s /q bin
else
	rm -rf bin
endif

## Run linters
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

## Run linters with auto-fix
lint-fix:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --fix; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

## Show help
help:
	@echo "Available targets:"
	@echo "  build         - Build the rescuer binary"
	@echo "  build-alpine  - Build static binary for Alpine Linux"
	@echo "  build-linux   - Build static binary for generic Linux"
	@echo "  run           - Run the rescuer (use ARGS=...)"
	@echo "  test          - Run tests"
	@echo "  fmt           - Format code"
	@echo "  vet           - Vet code"
	@echo "  lint          - Run linters"
	@echo "  lint-fix      - Run linters with auto-fix"
	@echo "  tidy          - Update dependencies"
	@echo "  clean         - Clean build artifacts"
