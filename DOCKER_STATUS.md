# DeepFry Docker Stack Status

## Services Running

✅ **StrFry Relay**

- Container: `strfry`
- WebSocket: ws://localhost:7777
- Config: `./config/strfry/strfry.conf`
- Database: `./data/strfry-db/`

✅ **Dgraph** (Standalone - Zero + Alpha combined)

- Container: `dgraph`
- HTTP/GraphQL: http://localhost:8080
- gRPC: localhost:9080
- Data: `./data/dgraph/`
- Health: http://localhost:8080/health

✅ **Dgraph Ratel** (UI)

- Container: `dgraph-ratel`
- Web UI: http://localhost:8000
- Connect to Dgraph at: `localhost:8080`

## Quick Commands

```bash
# Start stack
docker-compose up -d

# Stop stack
docker-compose down

# View logs
docker-compose logs [service-name]

# Check status
docker-compose ps
```

## Next Steps

1. Set up `.env` file with `STRFRY_PRIVATE_KEY` (copy from `.env.example`)
2. Test Nostr WebSocket connection to `ws://localhost:7777`
3. Access Dgraph UI at http://localhost:8000
4. Begin implementing subsystem services
