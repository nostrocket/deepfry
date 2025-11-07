APP_CRAWLER=crawler
APP_PUBKEYS=pubkeys
PKG=web-of-trust

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

LDFLAGS=-X 'web-of-trust/pkg/version.Version=$(VERSION)' \
        -X 'web-of-trust/pkg/version.Commit=$(GIT_COMMIT)' \
        -X 'web-of-trust/pkg/version.Built=$(BUILD_TIME)'

BUILD_FLAGS=-ldflags "$(LDFLAGS)"

.PHONY: all build build-crawler build-pubkeys run-crawler run-pubkeys test fmt vet tidy clean help lint lint-fix

## Default target: runs tidy, format, vet, tests, and builds all applications
all: tidy fmt vet test build

## Build all applications
build: build-crawler build-pubkeys

## Build crawler application
build-crawler:
	go build $(BUILD_FLAGS) -o bin/$(APP_CRAWLER)$(BINARY_EXT) ./cmd/$(APP_CRAWLER)

## Build pubkeys application
build-pubkeys:
	go build $(BUILD_FLAGS) -o bin/$(APP_PUBKEYS)$(BINARY_EXT) ./cmd/$(APP_PUBKEYS)

## Run crawler application
run-crawler:
	go run $(BUILD_FLAGS) ./cmd/$(APP_CRAWLER)

## Run pubkeys application
run-pubkeys:
	go run $(BUILD_FLAGS) ./cmd/$(APP_PUBKEYS)

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
	@echo   all             - Run complete workflow: tidy, format, vet, test, and build
	@echo   build           - Build all applications
	@echo   build-crawler   - Build crawler application
	@echo   build-pubkeys   - Build pubkeys application
	@echo   run-crawler     - Run crawler application
	@echo   run-pubkeys     - Run pubkeys application
	@echo   test            - Run tests
	@echo   fmt             - Format code
	@echo   vet             - Vet code
	@echo   tidy            - Update dependencies
	@echo   clean           - Clean build artifacts
	@echo   lint            - Run linters
	@echo   lint-fix        - Run linters and auto-fix where possible

## Run linters (does not fail if golangci-lint is not installed or incompatible)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run || echo "WARNING: golangci-lint issues detected, but continuing"; \
	else \
		echo "WARNING: golangci-lint not installed. Skipping lint step."; \
	fi

## Run Linter and auto fix where possible (does not fail if golangci-lint is not installed or incompatible)
lint-fix:
	@if command -v golangci-lint >/dev/null 2>&1; then \
        golangci-lint run --fix || echo "WARNING: golangci-lint issues detected, but continuing"; \
    else \
        echo "WARNING: golangci-lint not installed. Skipping lint-fix step."; \
    fi