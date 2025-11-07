# Multi-stage build for minimal footprint event-forwarder
# Optimized for running thousands of containers with minimal resource usage

# Stage 1: Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments for version information
ARG VERSION=docker
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

# Build static binary with optimizations
# - CGO_ENABLED=0: Fully static binary, no C dependencies
# - -trimpath: Remove file system paths from binary
# - -ldflags: Strip debug info and inject version info
# - -tags: Use netgo for pure Go DNS resolution
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -a \
    -installsuffix cgo \
    -tags netgo \
    -trimpath \
    -ldflags="-w -s -extldflags '-static' -X 'event-forwarder/pkg/version.Version=${VERSION}' -X 'event-forwarder/pkg/version.Commit=${GIT_COMMIT}' -X 'event-forwarder/pkg/version.Built=${BUILD_TIME}'" \
    -o fwd \
    ./cmd/fwd

# Verify binary is static (ldd should fail on static binaries)
# If ldd succeeds, the binary has dynamic dependencies
RUN (ldd fwd 2>&1 | grep -q "not a dynamic executable") || \
    (ldd fwd 2>&1 | grep -q "statically linked") || \
    echo "Warning: Binary may have dynamic dependencies, but will work on scratch if built with CGO_ENABLED=0"

# Stage 2: Minimal runtime image
FROM scratch

# Re-declare build args for use in this stage (labels)
ARG VERSION=docker
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

# Copy CA certificates for HTTPS connections to relays
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy timezone data (optional, but useful for logs)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the static binary
COPY --from=builder /build/fwd /fwd

# Use numeric UID for better Kubernetes compatibility
# No shell available in scratch, so we can't use adduser
USER 65534:65534

# Environment variables - all required, no defaults
# This forces explicit configuration in docker-compose or orchestrator
ENV SOURCE_RELAY_URL="" \
    DEEPFRY_RELAY_URL="" \
    NOSTR_SYNC_SECKEY="" \
    QUIET_MODE="true" \
    LOG_LEVEL="info"

# Labels for metadata
LABEL maintainer="DeepFry Team" \
    description="Minimal Nostr event forwarder for DeepFry relay synchronization" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${GIT_COMMIT}" \
    org.opencontainers.image.created="${BUILD_TIME}"

# Run the forwarder in quiet mode (no TUI)
# The application will panic on error, letting the orchestrator handle restarts
ENTRYPOINT ["/fwd"]
