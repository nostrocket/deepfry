APP_BRIDGE=bridge

# Cross-platform binary extension
ifeq ($(OS),Windows_NT)
    BINARY_EXT = .exe
else
    BINARY_EXT =
endif

.PHONY: all build run test fmt vet tidy clean help lint lint-fix

## Default target: tidy, format, vet, test, build
all: tidy fmt vet test build

## Build the bridge binary to bin/
build:
	go build -o bin/$(APP_BRIDGE)$(BINARY_EXT) ./cmd/$(APP_BRIDGE)

## Run the bridge (connects to Dgraph gRPC localhost:9080, serves 127.0.0.1:8081)
run:
	go run ./cmd/$(APP_BRIDGE)

## Run unit tests
test:
	go test ./... -count=1

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

## Run linters (does not fail if golangci-lint is missing)
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run || echo "WARNING: golangci-lint issues detected, but continuing"; \
	else \
		echo "WARNING: golangci-lint not installed. Skipping lint step."; \
	fi

## Run linters and auto-fix where possible
lint-fix:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --fix || echo "WARNING: golangci-lint issues detected, but continuing"; \
	else \
		echo "WARNING: golangci-lint not installed. Skipping lint-fix step."; \
	fi

## Show help
help:
	@echo Available targets:
	@echo "  all    - tidy, format, vet, test, build"
	@echo "  build  - Build the bridge binary to bin/"
	@echo "  run    - Run the bridge (go run ./cmd/bridge)"
	@echo "  test   - Run unit tests"
	@echo "  fmt    - Format code"
	@echo "  vet    - Vet code"
	@echo "  tidy   - Update dependencies"
	@echo "  clean  - Clean build artifacts"
	@echo "  lint   - Run linters"
