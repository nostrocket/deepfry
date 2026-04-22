APP=whitelist
APP_SERVER=whitelist-server
APP_ROUTER=router
PKG=whitelist-plugin

# Version can be set via environment variable or defaults to dev
VERSION ?= dev

# Cross-platform build flags for version information
ifeq ($(OS),Windows_NT)
    # Windows
    GIT_COMMIT = $(shell git rev-parse --short HEAD 2>nul || echo unknown)
    BUILD_TIME = $(shell powershell -Command "Get-Date -Format 'yyyy-MM-ddTHH:mm:ssZ'")
    BINARY_EXT = .exe
else
    # Linux/Unix
    GIT_COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
    BUILD_TIME = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
    BINARY_EXT =
endif

LDFLAGS=-X 'whitelist-plugin/pkg/version.Version=$(VERSION)' \
        -X 'whitelist-plugin/pkg/version.Commit=$(GIT_COMMIT)' \
        -X 'whitelist-plugin/pkg/version.Built=$(BUILD_TIME)'

BUILD_FLAGS=-ldflags "$(LDFLAGS)"

.PHONY: all build build-server build-router run test fmt vet tidy clean help bench build-alpine build-linux build-server-alpine build-server-linux build-router-alpine build-router-linux

all: build build-server build-router

## Build the plugin (thin client)
build:
	go build $(BUILD_FLAGS) -o bin/$(APP)$(BINARY_EXT) ./cmd/$(APP)

## Build the whitelist server
build-server:
	go build $(BUILD_FLAGS) -o bin/$(APP_SERVER)$(BINARY_EXT) ./cmd/server

## Build the router plugin (quarantine-routing alternative to the whitelist plugin)
build-router:
	go build $(BUILD_FLAGS) -o bin/$(APP_ROUTER)$(BINARY_EXT) ./cmd/$(APP_ROUTER)

## Build static plugin binary for Alpine Linux (musl libc)
build-alpine:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP)-alpine ./cmd/$(APP)
	@echo "Built static binary for Alpine Linux: bin/$(APP)-alpine"

## Build static server binary for Alpine Linux
build-server-alpine:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP_SERVER)-alpine ./cmd/server
	@echo "Built static server binary for Alpine Linux: bin/$(APP_SERVER)-alpine"

## Build static plugin binary for generic Linux
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP)-linux ./cmd/$(APP)
	@echo "Built static binary for Linux: bin/$(APP)-linux"

## Build static server binary for generic Linux
build-server-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP_SERVER)-linux ./cmd/server
	@echo "Built static server binary for Linux: bin/$(APP_SERVER)-linux"

## Build static router plugin binary for Alpine Linux (musl libc)
build-router-alpine:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP_ROUTER)-alpine ./cmd/$(APP_ROUTER)
	@echo "Built static router binary for Alpine Linux: bin/$(APP_ROUTER)-alpine"

## Build static router plugin binary for generic Linux
build-router-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-a -installsuffix cgo \
		-ldflags "$(LDFLAGS) -w -s -extldflags '-static'" \
		-tags netgo \
		-o bin/$(APP_ROUTER)-linux ./cmd/$(APP_ROUTER)
	@echo "Built static router binary for Linux: bin/$(APP_ROUTER)-linux"

## Run the application
run:
	go run $(BUILD_FLAGS) ./cmd/$(APP)

## Run tests
test:
	go test ./... -short -cover

## Run integration tests
test-integration:
	go test -tags=integration ./...

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

## Show help
help:
	@echo Available targets:
	@echo   build                - Build the plugin (thin client)
	@echo   build-server         - Build the whitelist server
	@echo   build-alpine         - Build static plugin binary for Alpine Linux
	@echo   build-server-alpine  - Build static server binary for Alpine Linux
	@echo   build-linux          - Build static plugin binary for generic Linux
	@echo   build-server-linux   - Build static server binary for generic Linux
	@echo   run                  - Run the plugin
	@echo   test                 - Run tests
	@echo   bench                - Run benchmarks
	@echo   fmt                  - Format code
	@echo   vet                  - Vet code
	@echo   lint                 - Run linters
	@echo   lint-fix             - Run linters with auto-fix
	@echo   tidy                 - Update dependencies
	@echo   clean                - Clean build artifacts

## Run linters
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest "; \
		exit 1; \
	fi

## Run Linter and auto fix where possible
lint-fix:
	@if command -v golangci-lint >/dev/null 2>&1; then \
        golangci-lint run --fix; \
    else \
        echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
        exit 1; \
    fi

BENCHPKG ?= ./...
BENCHFLAGS ?= -bench . -benchmem -run none -count=1

ifdef BENCHTIME
    BENCHFLAGS := $(BENCHFLAGS) -benchtime=$(BENCHTIME)
endif

bench:
	@echo "Running benchmarks: pkg=$(BENCHPKG) flags=$(BENCHFLAGS)"
	go test $(BENCHFLAGS) $(BENCHPKG)

