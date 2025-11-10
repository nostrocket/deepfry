# Event Forwarder Deployment Guide

This guide covers deploying the event-forwarder services to sync events from multiple Nostr relays into your DeepFry relay.

## Overview

The deployment consists of **6 event-forwarder instances**:

### Live Forwarders (3)

Stream events from "now" onwards in real-time:

- `fwd-damus-live` - relay.damus.io
- `fwd-primal-live` - relay.primal.net
- `fwd-nostr-lol-live` - relay.nostr.lol

### Historical Forwarders (3)

Sync historical events from January 1, 2020:

- `fwd-damus-history` - relay.damus.io
- `fwd-primal-history` - relay.primal.net
- `fwd-nostr-lol-history` - relay.nostr.lol

## Prerequisites

1. **Main infrastructure running**: The DeepFry stack (strfry, dgraph, etc.) must be up

   ```bash
   docker-compose up -d
   ```

2. **Network created**: The `deepfry-net` network must exist (created by main docker-compose.yml)

3. **Event forwarder image built**:
   ```bash
   cd event-forwarder
   make docker-build
   ```

## Quick Start

### 1. Configure Environment Variables

```bash
# Copy the example
cp .env.example .env

# Generate secret keys
openssl rand -hex 32  # For NOSTR_SYNC_SECKEY_LIVE
openssl rand -hex 32  # For NOSTR_SYNC_SECKEY_HISTORY

# Edit .env and add your keys
nano .env
```

**Required variables in `.env`:**

```bash
NOSTR_SYNC_SECKEY_LIVE=<your_live_secret_key_hex>
NOSTR_SYNC_SECKEY_HISTORY=<your_history_secret_key_hex>
```

### 2. Start the Forwarders

```bash
# Build and start all forwarders
docker-compose -f docker-compose.evtfwd.yml up -d

# Or build first, then start
docker-compose -f docker-compose.evtfwd.yml build
docker-compose -f docker-compose.evtfwd.yml up -d
```

### 3. Monitor Status

```bash
# View all forwarder logs
docker-compose -f docker-compose.evtfwd.yml logs -f

# View specific forwarder
docker logs -f fwd-damus-live

# Check resource usage
docker stats $(docker ps --filter name=fwd- -q)

# Check container status
docker-compose -f docker-compose.evtfwd.yml ps
```

## Configuration Details

### Live Forwarders

**Configuration:**

- Start time: Current time (no explicit start time)
- Sync window: 5 seconds (near real-time)
- Max batch: 1000 events
- Secret key: `NOSTR_SYNC_SECKEY_LIVE`

**Behavior:**

- Streams events as they arrive at the source relay
- Low latency (~1 second)
- Continuous operation
- Automatically resumes from last sync point on restart

### Historical Forwarders

**Configuration:**

- Start time: `2020-01-01T00:00:00Z`
- Sync window: 3600 seconds (1 hour chunks)
- Max batch: 5000 events
- Max catchup lag: 86400 seconds (1 day)
- Secret key: `NOSTR_SYNC_SECKEY_HISTORY`

**Behavior:**

- Syncs events from 2020-01-01 onwards
- Larger batches for efficiency
- Catches up to present time, then continues streaming
- Automatically resumes from last sync point on restart
- No explicit end time (continues indefinitely)

## Network Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      deepfry-net                            │
│                                                             │
│  ┌──────────────┐                    ┌──────────────┐      │
│  │   strfry     │◄───────────────────│ fwd-damus    │      │
│  │  (port 7777) │◄───────────────────│   -live      │      │
│  └──────────────┘                    └──────────────┘      │
│         ▲                             ┌──────────────┐      │
│         │                        ┌────│ fwd-primal   │      │
│         │                        │    │   -live      │      │
│         │                        │    └──────────────┘      │
│         │                        │    ┌──────────────┐      │
│         │                        ├────│ fwd-nostr    │      │
│         │                        │    │   -lol-live  │      │
│         │                        │    └──────────────┘      │
│         │                        │                          │
│         │                        │    ┌──────────────┐      │
│         │                        ├────│ fwd-damus    │      │
│         │                        │    │   -history   │      │
│         └────────────────────────┤    └──────────────┘      │
│                                  │    ┌──────────────┐      │
│                                  ├────│ fwd-primal   │      │
│                                  │    │   -history   │      │
│                                  │    └──────────────┘      │
│                                  │    ┌──────────────┐      │
│                                  └────│ fwd-nostr    │      │
│                                       │   -lol       │      │
│                                       │   -history   │      │
│                                       └──────────────┘      │
└─────────────────────────────────────────────────────────────┘
                    │                             │
         ┌──────────┴──────────┐     ┌───────────┴──────────┐
         │  External Nostr     │     │  External Nostr      │
         │  Source Relays      │     │  Source Relays       │
         │  (wss://)           │     │  (wss://)            │
         └─────────────────────┘     └──────────────────────┘
```

## Resource Usage

**Per Container:**

- Memory limit: 128 MB
- Memory reservation: 32 MB
- CPU limit: 0.5 cores
- CPU reservation: 0.1 cores

**Total for 6 Forwarders:**

- Memory: ~200-400 MB (actual usage)
- CPU: ~0.3-1.2 cores (varies with relay activity)
- Disk: ~100 MB (images + logs)

## Management Commands

### Start/Stop

```bash
# Start all forwarders
docker-compose -f docker-compose.evtfwd.yml up -d

# Stop all forwarders
docker-compose -f docker-compose.evtfwd.yml down

# Restart specific forwarder
docker-compose -f docker-compose.evtfwd.yml restart fwd-damus-live

# Stop without removing containers
docker-compose -f docker-compose.evtfwd.yml stop
```

### Logs

```bash
# All forwarders
docker-compose -f docker-compose.evtfwd.yml logs -f

# Specific forwarder
docker-compose -f docker-compose.evtfwd.yml logs -f fwd-damus-live

# Last 100 lines
docker-compose -f docker-compose.evtfwd.yml logs --tail=100

# Since specific time
docker-compose -f docker-compose.evtfwd.yml logs --since 2025-11-05T10:00:00
```

### Status

```bash
# Container status
docker-compose -f docker-compose.evtfwd.yml ps

# Resource usage
docker stats $(docker ps --filter name=fwd- -q)

# Inspect specific container
docker inspect fwd-damus-live
```

### Updates

```bash
# Rebuild images
docker-compose -f docker-compose.evtfwd.yml build

# Pull and restart
docker-compose -f docker-compose.evtfwd.yml pull
docker-compose -f docker-compose.evtfwd.yml up -d

# Restart with new image
docker-compose -f docker-compose.evtfwd.yml up -d --force-recreate
```

## Troubleshooting

### Forwarder Won't Start

```bash
# Check logs
docker logs fwd-damus-live

# Common issues:
# 1. Missing environment variables
docker inspect fwd-damus-live | grep -A 20 Env

# 2. Network not available
docker network ls | grep deepfry-net

# 3. strfry not running
docker ps | grep strfry
```

### Can't Connect to strfry

```bash
# Test connectivity from forwarder network
docker run --rm --network deepfry-net alpine/curl \
  curl -v ws://strfry:7777

# Ensure strfry is on same network
docker inspect strfry | grep -A 10 Networks
```

### High Memory Usage

```bash
# Check current usage
docker stats --no-stream fwd-damus-live

# If consistently above 100MB, check relay activity
docker logs --tail=100 fwd-damus-live

# Adjust limits in docker-compose.evtfwd.yml if needed
```

### Forwarder Keeps Restarting

```bash
# Check restart count
docker inspect fwd-damus-live --format='{{.RestartCount}}'

# Check exit reason
docker inspect fwd-damus-live --format='{{.State.Status}}: {{.State.Error}}'

# View recent logs
docker logs --tail=50 fwd-damus-live
```

### Sync Progress Not Resuming

```bash
# Check if sync progress events are being published
# Query strfry for kind 30078 events
# The forwarder publishes progress to DeepFry relay

# Check forwarder logs for "published sync progress" messages
docker logs fwd-damus-live | grep "progress"
```

## Monitoring

### Key Metrics to Watch

1. **Event Rate**: Events received/forwarded per second
2. **Lag**: Time difference between source and DeepFry
3. **Error Count**: Failed forwards or connection issues
4. **Memory**: Should stay under 64MB typically
5. **Restart Count**: Frequent restarts indicate issues

### Log Output

Live forwarders (quiet mode) log every 10 seconds:

```
[fwd] 2025/11/05 10:30:25 Status - Events: received=150, forwarded=147, rate=15.2/s, errors=0
[fwd] 2025/11/05 10:30:25 Connections - Source: true, DeepFry: true
[fwd] 2025/11/05 10:30:25 Sync window: 1730800825 to 1730800830, lag: 0.5s, mode: realtime
```

## Security

### Secret Key Management

- ✅ Never commit `.env` to git
- ✅ Use different keys per environment (dev, staging, prod)
- ✅ Rotate keys if compromised
- ✅ Store in secrets manager for production (Vault, AWS Secrets, etc.)

### Network Isolation

- ✅ Forwarders only accessible within `deepfry-net`
- ✅ No exposed ports (communicate via internal network)
- ✅ strfry is the only external-facing component

## Adding More Forwarders

To add additional relays:

1. Copy an existing service definition in `docker-compose.evtfwd.yml`
2. Change: service name, container name, SOURCE_RELAY_URL
3. Keep the same NOSTR_SYNC_SECKEY (live or history as appropriate)
4. Restart the stack

Example:

```yaml
fwd-nos-live:
  # ... copy from fwd-damus-live ...
  container_name: fwd-nos-live
  environment:
    SOURCE_RELAY_URL: "wss://nos.lol"
    # ... rest same as fwd-damus-live ...
```

## Scaling Considerations

For running many more forwarders (10s to 100s):

1. **Resource limits**: Adjust based on actual usage
2. **Log rotation**: Reduce max-size if disk space limited
3. **Orchestration**: Consider Kubernetes for 50+ forwarders
4. **Monitoring**: Set up Prometheus + Grafana
5. **Network**: May need to tune Docker network settings

## Next Steps

1. **Monitor performance**: Watch logs for first few hours
2. **Adjust resources**: Based on actual usage patterns
3. **Add more relays**: Scale up as needed
4. **Set up alerts**: For failures or high resource usage
5. **Backup sync progress**: strfry db contains sync state

## Support

For issues with the event-forwarder itself, see:

- [Event Forwarder README](event-forwarder/README.md)
- [Event Forwarder Docker Guide](event-forwarder/DOCKER.md)
- [Event Forwarder Telemetry](event-forwarder/docs/TELEMETRY.md)
