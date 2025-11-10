APP=fwd
PKG=event-forwarder

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

LDFLAGS=-X 'event-forwarder/pkg/version.Version=$(VERSION)' \
        -X 'event-forwarder/pkg/version.Commit=$(GIT_COMMIT)' \
        -X 'event-forwarder/pkg/version.Built=$(BUILD_TIME)'

BUILD_FLAGS=-ldflags "$(LDFLAGS)"

.PHONY: all build run test fmt vet tidy clean help

all: build

## Build the application
build:
	go build $(BUILD_FLAGS) -o bin/$(APP)$(BINARY_EXT) ./cmd/$(APP)

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
	@echo   build  - Build the application
	@echo   run    - Run the application
	@echo   test   - Run tests
	@echo   fmt    - Format code
	@echo   vet    - Vet code
	@echo   tidy   - Update dependencies
	@echo   clean  - Clean build artifacts

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

## Docker targets
DOCKER_IMAGE ?= deepfry/event-forwarder
DOCKER_TAG ?= $(VERSION)

## Build Docker image
docker-build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		-t $(DOCKER_IMAGE):latest \
		.

## Build multi-platform Docker image (requires buildx)
docker-buildx:
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(DOCKER_IMAGE):$(DOCKER_TAG) \
		-t $(DOCKER_IMAGE):latest \
		--push \
		.

## Run Docker container locally for testing
docker-run:
	docker run --rm -it \
		-e SOURCE_RELAY_URL="wss://relay.damus.io" \
		-e DEEPFRY_RELAY_URL="ws://localhost:7777" \
		-e NOSTR_SYNC_SECKEY="$(NOSTR_SYNC_SECKEY)" \
		-e QUIET_MODE="true" \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

## Show Docker image size
docker-size:
	@docker images $(DOCKER_IMAGE):$(DOCKER_TAG) --format "Size: {{.Size}}"

## Run docker-compose with example configuration
docker-compose-up:
	docker-compose -f docker-compose.example.yml up -d

## Stop docker-compose services
docker-compose-down:
	docker-compose -f docker-compose.example.yml down

## View docker-compose logs
docker-compose-logs:
	docker-compose -f docker-compose.example.yml logs -f
