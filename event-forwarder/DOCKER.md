# Docker Deployment Guide

This guide covers deploying the event-forwarder using Docker for running multiple relay synchronization instances.

## Quick Start

### 1. Build the Docker Image

```bash
# Build with version info
make docker-build VERSION=1.0.0

# Or build directly
docker build -t deepfry/event-forwarder:latest .
```

Expected image size: **~8-12 MB** (using scratch base with static binary)

### 2. Run a Single Container

```bash
docker run -d \
  --name fwd-damus \
  --restart unless-stopped \
  -e SOURCE_RELAY_URL="wss://relay.damus.io" \
  -e DEEPFRY_RELAY_URL="wss://your-deepfry.com" \
  -e NOSTR_SYNC_SECKEY="nsec1..." \
  -e QUIET_MODE="true" \
  deepfry/event-forwarder:latest
```

### 3. Run Multiple Forwarders with Docker Compose

```bash
# Copy example files
cp .env.example .env
cp docker-compose.example.yml docker-compose.forwarders.yml

# Edit .env with your credentials
nano .env

# Start all forwarders
docker-compose -f docker-compose.forwarders.yml up -d

# View logs
docker-compose -f docker-compose.forwarders.yml logs -f
```

## Configuration

### Environment Variables

| Variable            | Required | Description                          | Example                  |
| ------------------- | -------- | ------------------------------------ | ------------------------ |
| `SOURCE_RELAY_URL`  | Yes      | Source relay WebSocket URL           | `wss://relay.damus.io`   |
| `DEEPFRY_RELAY_URL` | Yes      | DeepFry relay WebSocket URL          | `wss://your-deepfry.com` |
| `NOSTR_SYNC_SECKEY` | Yes      | Nostr secret key (nsec or hex)       | `nsec1abc...`            |
| `QUIET_MODE`        | No       | Run in headless mode (default: true) | `true`                   |
| `LOG_LEVEL`         | No       | Logging level                        | `info`                   |

### Resource Limits

For running **thousands of containers**, recommended limits per container:

```yaml
deploy:
  resources:
    limits:
      cpus: "0.5" # 50% of one CPU core
      memory: 128M # 128 MB RAM
    reservations:
      cpus: "0.1" # 10% minimum
      memory: 32M # 32 MB minimum
```

Typical actual usage:

- **Memory**: 20-40 MB per container
- **CPU**: 0.05-0.2 cores under normal load
- **Network**: Varies by relay activity

## Scaling to Thousands of Containers

### Option 1: Docker Compose (Up to ~100 containers)

Generate a compose file programmatically:

```bash
# Example script to generate forwarders for multiple relays
#!/bin/bash
RELAYS=(
  "wss://relay.damus.io"
  "wss://nos.lol"
  "wss://nostr.wine"
  # ... add thousands more
)

cat > docker-compose.generated.yml << 'EOF'
version: '3.8'
services:
EOF

for i in "${!RELAYS[@]}"; do
  relay="${RELAYS[$i]}"
  name=$(echo "$relay" | sed 's/wss:\/\///' | sed 's/[^a-zA-Z0-9]/-/g')

  cat >> docker-compose.generated.yml << EOF
  forwarder-$name:
    image: deepfry/event-forwarder:latest
    restart: unless-stopped
    environment:
      SOURCE_RELAY_URL: "$relay"
      DEEPFRY_RELAY_URL: \${DEEPFRY_RELAY_URL}
      NOSTR_SYNC_SECKEY: \${NOSTR_SYNC_SECKEY}
      QUIET_MODE: "true"
    deploy:
      resources:
        limits: {cpus: '0.5', memory: 128M}
        reservations: {cpus: '0.1', memory: 32M}
    networks: [forwarder-net]
EOF
done

cat >> docker-compose.generated.yml << 'EOF'
networks:
  forwarder-net:
    driver: bridge
EOF
```

### Option 2: Docker Swarm (100s to 1000s of containers)

```bash
# Initialize swarm
docker swarm init

# Create service per relay
for relay in wss://relay1.com wss://relay2.com; do
  name=$(echo "$relay" | sed 's/wss:\/\///' | sed 's/[^a-zA-Z0-9]/-/g')

  docker service create \
    --name "fwd-$name" \
    --replicas 1 \
    --limit-cpu 0.5 \
    --limit-memory 128M \
    --reserve-cpu 0.1 \
    --reserve-memory 32M \
    --env SOURCE_RELAY_URL="$relay" \
    --env DEEPFRY_RELAY_URL="$DEEPFRY_URL" \
    --env NOSTR_SYNC_SECKEY="$SECRET" \
    --env QUIET_MODE="true" \
    --restart-condition on-failure \
    --restart-max-attempts 3 \
    deepfry/event-forwarder:latest
done
```

### Option 3: Kubernetes (Production scale: 1000s+)

Example Deployment template:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: forwarder-damus
spec:
  replicas: 1
  selector:
    matchLabels:
      app: event-forwarder
      source: damus
  template:
    metadata:
      labels:
        app: event-forwarder
        source: damus
    spec:
      containers:
        - name: forwarder
          image: deepfry/event-forwarder:latest
          env:
            - name: SOURCE_RELAY_URL
              value: "wss://relay.damus.io"
            - name: DEEPFRY_RELAY_URL
              valueFrom:
                configMapKeyRef:
                  name: forwarder-config
                  key: deepfry-url
            - name: NOSTR_SYNC_SECKEY
              valueFrom:
                secretKeyRef:
                  name: forwarder-secrets
                  key: nostr-seckey
            - name: QUIET_MODE
              value: "true"
          resources:
            limits:
              cpu: 500m
              memory: 128Mi
            requests:
              cpu: 100m
              memory: 32Mi
          securityContext:
            runAsNonRoot: true
            runAsUser: 65534
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
```

Generate deployments for each relay using a script or Helm chart.

## Monitoring

### Check Container Status

```bash
# List all running forwarders
docker ps --filter name=fwd-

# Check resource usage
docker stats $(docker ps --filter name=fwd- -q)

# View logs for specific container
docker logs -f fwd-damus

# View logs for all forwarders
docker-compose -f docker-compose.forwarders.yml logs -f
```

### Health Checks

The container will exit on fatal errors (as designed). Monitor with:

```bash
# Check if containers are restarting frequently
docker ps -a --filter name=fwd- --filter status=exited

# View container restart count
docker inspect fwd-damus --format='{{.RestartCount}}'
```

### Log Aggregation

For thousands of containers, use centralized logging:

```yaml
# Add to docker-compose services
logging:
  driver: "fluentd"
  options:
    fluentd-address: "localhost:24224"
    tag: "forwarder.{{.Name}}"
```

Or use Docker's built-in JSON logging with rotation:

```yaml
logging:
  driver: "json-file"
  options:
    max-size: "10m"
    max-file: "3"
```

## Troubleshooting

### Container Exits Immediately

```bash
# Check logs
docker logs fwd-damus

# Common issues:
# 1. Missing environment variables (SOURCE_RELAY_URL, DEEPFRY_RELAY_URL, NOSTR_SYNC_SECKEY)
# 2. Invalid NOSTR_SYNC_SECKEY format
# 3. Unable to connect to relays

# Test with verbose output
docker run --rm -it \
  -e SOURCE_RELAY_URL="wss://relay.damus.io" \
  -e DEEPFRY_RELAY_URL="ws://localhost:7777" \
  -e NOSTR_SYNC_SECKEY="nsec1..." \
  deepfry/event-forwarder:latest
```

### High Memory Usage

```bash
# Check actual memory usage
docker stats --no-stream fwd-damus

# If consistently above 100MB, may need to:
# 1. Check for relay with extremely high event volume
# 2. Increase memory limits
# 3. Investigate potential memory leak (file issue)
```

### Connection Issues

```bash
# Test relay connectivity from container
docker run --rm -it alpine:latest /bin/sh
# Inside container:
apk add curl
curl -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: test" \
  wss://relay.damus.io
```

## Performance Optimization

### For Maximum Density (Most containers per host)

1. **Use scratch base image** (current default): ~8-12 MB per image
2. **Share network namespace**: Reduce network overhead
3. **Disable logging** for non-critical forwarders:
   ```yaml
   logging:
     driver: "none"
   ```
4. **Use host networking** if firewall allows:
   ```yaml
   network_mode: "host"
   ```

### Expected Capacity Per Host

With recommended limits (0.5 CPU, 128MB RAM):

- **Small instance** (4 CPU, 8GB RAM): ~50-100 forwarders
- **Medium instance** (8 CPU, 16GB RAM): ~200-300 forwarders
- **Large instance** (16 CPU, 32GB RAM): ~500-800 forwarders

Actual limits depend on relay activity and network I/O.

## Build Variants

### Multi-platform Build

```bash
# Build for multiple architectures
docker buildx create --use
make docker-buildx VERSION=1.0.0
```

Supported platforms:

- `linux/amd64` (x86_64)
- `linux/arm64` (ARM64/aarch64)

### Alpine-based Build (with shell for debugging)

Edit Dockerfile stage 2:

```dockerfile
FROM alpine:latest
RUN apk add --no-cache ca-certificates
COPY --from=builder /build/fwd /usr/local/bin/fwd
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/fwd"]
```

Image size: ~15-25 MB (adds ~10MB for Alpine base)

## Security Considerations

1. **Non-root user**: Container runs as UID 65534 (nobody)
2. **Read-only filesystem**: No write access needed (scratch base)
3. **Secret management**: Use Docker secrets or Kubernetes secrets for NOSTR_SYNC_SECKEY
4. **Network isolation**: Use Docker networks to isolate forwarders

Example with Docker secrets:

```bash
echo "nsec1..." | docker secret create nostr_seckey -

docker service create \
  --name fwd-damus \
  --secret nostr_seckey \
  -e NOSTR_SYNC_SECKEY_FILE=/run/secrets/nostr_seckey \
  deepfry/event-forwarder:latest
```

## CI/CD Integration

### GitHub Actions Example

```yaml
name: Build and Push Docker Image

on:
  push:
    tags:
      - "v*"

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v2

      - name: Login to Docker Hub
        uses: docker/login-action@v2
        with:
          username: ${{ secrets.DOCKER_USERNAME }}
          password: ${{ secrets.DOCKER_PASSWORD }}

      - name: Build and push
        uses: docker/build-push-action@v4
        with:
          context: .
          platforms: linux/amd64,linux/arm64
          push: true
          tags: deepfry/event-forwarder:${{ github.ref_name }},deepfry/event-forwarder:latest
          build-args: |
            VERSION=${{ github.ref_name }}
            GIT_COMMIT=${{ github.sha }}
            BUILD_TIME=${{ github.event.head_commit.timestamp }}
```

## Additional Resources

- [Main README](README.md) - Application documentation
- [Telemetry Guide](docs/TELEMETRY.md) - Metrics and observability
- [Docker Best Practices](https://docs.docker.com/develop/dev-best-practices/)
- [Kubernetes Production Best Practices](https://kubernetes.io/docs/concepts/configuration/overview/)
