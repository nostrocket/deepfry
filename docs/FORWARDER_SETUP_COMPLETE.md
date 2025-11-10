# Event Forwarder Deployment - Setup Complete ✅

## What Was Created

### 1. **docker-compose.evtfwd.yml**

Complete Docker Compose configuration with 6 event-forwarder services:

**Live Forwarders (3)** - Stream from "now" onwards

- `fwd-damus-live` → relay.damus.io
- `fwd-primal-live` → relay.primal.net
- `fwd-nostr-lol-live` → relay.nostr.lol

**Historical Forwarders (3)** - Sync from 2020-01-01

- `fwd-damus-history` → relay.damus.io (from 2020)
- `fwd-primal-history` → relay.primal.net (from 2020)
- `fwd-nostr-lol-history` → relay.nostr.lol (from 2020)

### 2. **.env.example**

Updated with forwarder-specific configuration:

- `NOSTR_SYNC_SECKEY_LIVE` - Secret key for live forwarders
- `NOSTR_SYNC_SECKEY_HISTORY` - Secret key for historical forwarders
- Build configuration variables
- Comprehensive documentation

### 3. **FORWARDER_DEPLOYMENT.md**

Complete deployment guide covering:

- Architecture overview
- Quick start instructions
- Configuration details
- Network architecture diagram
- Monitoring and troubleshooting
- Security best practices
- Scaling guidance

### 4. **forwarder-commands.sh**

Quick reference script with all common commands for:

- Setup and configuration
- Starting/stopping services
- Monitoring and debugging
- Updates and maintenance
- Health checks

## Key Configuration Highlights

### Network Setup

- **External network**: Uses existing `deepfry-net` created by main docker-compose.yml
- **Connection**: Forwarders connect to `ws://strfry:7777` (internal DNS)
- **No port exposure**: All communication via internal network

### Live Forwarders

```yaml
SOURCE_RELAY_URL: "wss://relay.damus.io"
DEEPFRY_RELAY_URL: "ws://strfry:7777"
NOSTR_SYNC_SECKEY: ${NOSTR_SYNC_SECKEY_LIVE}
SYNC_WINDOW_SECONDS: "5" # 5 second windows
SYNC_MAX_BATCH: "1000" # 1k events per batch
```

### Historical Forwarders

```yaml
SOURCE_RELAY_URL: "wss://relay.damus.io"
DEEPFRY_RELAY_URL: "ws://strfry:7777"
NOSTR_SYNC_SECKEY: ${NOSTR_SYNC_SECKEY_HISTORY}
SYNC_START_TIME: "2020-01-01T00:00:00Z" # Start from 2020
SYNC_WINDOW_SECONDS: "3600" # 1 hour windows
SYNC_MAX_BATCH: "5000" # 5k events per batch
SYNC_MAX_CATCHUP_LAG_SECONDS: "86400" # 1 day tolerance
```

### Resource Limits (per container)

```yaml
limits:
  cpus: "0.5" # 50% of one core max
  memory: 128M # 128 MB max
reservations:
  cpus: "0.1" # 10% minimum
  memory: 32M # 32 MB minimum
```

**Total for 6 forwarders:**

- Memory: ~200-400 MB actual usage
- CPU: ~0.3-1.2 cores
- Disk: ~100 MB (images + logs)

## Quick Start

### 1. Setup Environment

```bash
# Generate secret keys
openssl rand -hex 32  # For NOSTR_SYNC_SECKEY_LIVE
openssl rand -hex 32  # For NOSTR_SYNC_SECKEY_HISTORY

# Create .env file
cp .env.example .env
nano .env  # Add the generated keys
```

### 2. Start Infrastructure

```bash
# Main stack must be running first
docker-compose up -d
```

### 3. Start Forwarders

```bash
# Build and start all 6 forwarders
docker-compose -f docker-compose.evtfwd.yml up -d
```

### 4. Monitor

```bash
# View all logs
docker-compose -f docker-compose.evtfwd.yml logs -f

# Check status
docker-compose -f docker-compose.evtfwd.yml ps

# Resource usage
docker stats $(docker ps --filter name=fwd- -q)
```

## Architecture

```
┌─────────────────────────────────────────────────────┐
│              deepfry-net (external)                 │
│                                                     │
│  ┌──────────┐         ┌─────────────────────┐      │
│  │  strfry  │◄────────│  6x event-forwarder │      │
│  │ :7777    │         │  - 3 live           │      │
│  └──────────┘         │  - 3 history        │      │
│                       └─────────────────────┘      │
└─────────────────────────────────────────────────────┘
              ▲                     ▲
              │                     │
       ┌──────┴──────┐       ┌─────┴──────┐
       │  Main Stack │       │  External  │
       │  compose.yml│       │  Relays    │
       └─────────────┘       └────────────┘
```

## Why Separate Keys?

Each forwarder publishes sync progress events (kind 30078) with a unique identifier:

- Identifier = `d` tag = hash(seckey + source_relay_url)
- Live and history forwarders for the **same relay** need **different keys**
- This ensures separate sync progress tracking and resumability

Example:

- `fwd-damus-live` uses `NOSTR_SYNC_SECKEY_LIVE`
- `fwd-damus-history` uses `NOSTR_SYNC_SECKEY_HISTORY`
- Both forward from relay.damus.io but maintain independent sync states

## Expected Behavior

### Live Forwarders

- ✅ Start streaming from current time
- ✅ Near real-time sync (<1 second lag)
- ✅ Small batches (1000 events)
- ✅ Continuous operation
- ✅ Automatically resume on restart

### Historical Forwarders

- ✅ Start from 2020-01-01T00:00:00Z
- ✅ Larger batches (5000 events) for efficiency
- ✅ 1-hour sync windows
- ✅ Catch up to present, then continue streaming
- ✅ Automatically resume from last checkpoint
- ✅ No end time (runs indefinitely)

## Monitoring

### Log Output (every 10 seconds in quiet mode)

```
[fwd] 2025/11/05 10:30:25 Status - Events: received=150, forwarded=147, rate=15.2/s, errors=0
[fwd] 2025/11/05 10:30:25 Connections - Source: true, DeepFry: true
[fwd] 2025/11/05 10:30:25 Sync window: 1730800825 to 1730800830, lag: 0.5s, mode: realtime
```

### Key Metrics

- **Event rate**: Events/second
- **Lag**: Time behind source relay
- **Errors**: Connection or forward failures
- **Memory**: Should stay <64 MB typically
- **Restarts**: Check for frequent restarts

## Troubleshooting

### Forwarder won't start

```bash
# Check logs
docker logs fwd-damus-live

# Common issues:
# 1. Missing .env variables
# 2. Network not available (main stack not running)
# 3. strfry not accessible
```

### Can't connect to strfry

```bash
# Ensure main stack is running
docker-compose ps

# Test connectivity
docker run --rm --network deepfry-net alpine/curl curl -v ws://strfry:7777

# Check network
docker network inspect deepfry-net
```

### High memory usage

```bash
# Check actual usage
docker stats --no-stream fwd-damus-live

# If consistently >100MB, check relay activity
docker logs --tail=100 fwd-damus-live
```

## Security Notes

- ✅ Never commit `.env` with real keys
- ✅ Keys should be unique per environment
- ✅ Rotate keys if compromised
- ✅ Use secrets manager in production
- ✅ Forwarders have no exposed ports
- ✅ Communication only via internal network

## Next Steps

1. ✅ **Generate keys**: Create two unique secret keys
2. ✅ **Configure .env**: Add keys to .env file
3. ✅ **Start services**: Run main stack, then forwarders
4. ✅ **Monitor**: Watch logs for first few hours
5. ⏭️ **Add more relays**: Scale up as needed
6. ⏭️ **Set up monitoring**: Prometheus + Grafana
7. ⏭️ **Production secrets**: Move to vault/secrets manager

## Files Reference

- `docker-compose.evtfwd.yml` - Forwarder services configuration
- `.env.example` - Environment variables template
- `FORWARDER_DEPLOYMENT.md` - Full deployment guide
- `forwarder-commands.sh` - Quick command reference
- `event-forwarder/DOCKER.md` - Docker build guide
- `event-forwarder/README.md` - Application documentation

## Support

For issues:

1. Check logs: `docker-compose -f docker-compose.evtfwd.yml logs`
2. Review: `FORWARDER_DEPLOYMENT.md`
3. Check event-forwarder docs in `event-forwarder/`

---

**Status**: ✅ Ready to deploy
**Last Updated**: 2025-11-05
