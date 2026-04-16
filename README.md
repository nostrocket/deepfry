# DeepFry
This provides deep fried nostr events for humble horse. The core problem we propose to solve is how to surface the most relevant signal in a sea of noise. 

The design uses StrFry for maximum performance and NIP compliance, while surrounding it with additional services for discovery, trust scoring, and search.

We welcome external contributors. 

---

## Overview

- **Relay Core**: Stock [StrFry](https://github.com/hoytech/strfry) for ultra-fast event storage and delivery (LMDB backend).
- **Plugin System**: Uses StrFry’s JSON stdin/stdout plugin interface to integrate custom capabilities without forking.
- **Subsystems**: Modular services for semantic search, embeddings, trust graph building, event forwarding, profile analytics, and thread reconstruction.
- **Data Model**:
  - **Canonical events**: Stored only in StrFry LMDB.
  - **Graphs**: Stored in Dgraph as **ID-only graphs** (WoT = pubkeys, Threads = event IDs).
  - **Vectors**: Stored in pgvector (initially) for semantic search.

---

## Subsystems

| #   | Subsystem                            | Purpose                                                                          | Key Storage                    | Key NIPs                           |
| --- | ------------------------------------ | -------------------------------------------------------------------------------- | ------------------------------ | ---------------------------------- |
| 1   | StrFry Relay (stock + search plugin) | Primary relay for ingest/distribution; full event storage; plugin handles NIP-50 | LMDB                           | 1, 2, 4, 9, 11, 22, 28, 40, 70, 77 |
| 2   | Search Plugin                        | Intercept NIP-50 REQ; delegate to Semantic Search Service                        | None (stateless)               | 50                                 |
| 3   | Event Forwarder                      | Subscribe to external relays and republish to StrFry                             | Replaceable event in StrFry    | 1                                  |
| 4   | Web of Trust Builder + Crawler       | Build trust graph from NIP-02 follows                                            | Dgraph (pubkey graph)          | 2                                  |
| 5   | Semantic Search Service              | Index/query events for semantic search                                           | Vector DB (TBA)                | 50                                 |
| 6   | Embeddings Generator                 | Produce embeddings for search                                                    | Vector DB (TBA)                | —                                  |
| 7   | Profile Builder                      | Aggregate actions: likes, mutes, replies, reposts, zaps                          | ID references in profile store | 18, 25, 51, 57                     |
| 8   | Thread Inference Engine              | Build thread graphs from replies/quotes                                          | Dgraph (event-id graph)        | 1                                  |

---

## Architecture Summary

- **StrFry Relay** is the single point of truth for events.
- **Event Forwarder** pulls events from upstream relays into StrFry.
- **Web of Trust Builder** in Dgraph maintains trust scores and allowlists.
- **Search Plugin** in StrFry handles NIP-50 requests and calls the **Semantic Search Service**.
- **Semantic Search** indexes events using **Embeddings Generator** and ranks them using semantic similarity, BM25, and trust scores.
- **Profile Builder** and **Thread Inference Engine** consume events from StrFry to build profiles and thread graphs.

## Running it

The stack is split into separate compose files so each layer can be managed independently.

### Services

| Service | Container | Compose File | Port | Description |
|---------|-----------|-------------|------|-------------|
| Dgraph | `dgraph` | `docker-compose.dgraph.yml` | 8080 (HTTP/GraphQL), 9080 (gRPC) | Graph database for pubkey relationships |
| Dgraph Ratel | `dgraph-ratel` | `docker-compose.dgraph.yml` | 8000 | Dgraph web UI |
| Whitelist Server | `whitelist-server` | `docker-compose.dgraph.yml` | 8081 | Centralized pubkey whitelist cache (refreshes from Dgraph) |
| StrFry | `strfry` | `docker-compose.strfry.yml` | 7777 (WebSocket) | Nostr relay with whitelist client plugin |
| Event Forwarders | `fwd-*` | `docker-compose.evtfwd.yml` | -- | Sync events from upstream relays |

### Startup

```bash
# 1. Start Dgraph + Whitelist Server
docker-compose -f docker-compose.dgraph.yml up -d

# 2. Start StrFry (waits for whitelist-server on deepfry-net)
docker-compose -f docker-compose.strfry.yml up -d

# 3. Start event forwarders (requires .env with keys)
docker-compose -f docker-compose.evtfwd.yml up -d
```

### Shutdown

```bash
docker-compose -f docker-compose.evtfwd.yml down
docker-compose -f docker-compose.strfry.yml down
docker-compose -f docker-compose.dgraph.yml down
```

## Quick Commands

```bash
# Stream from top 20 relays (uses tmux)
./stream-relays.sh

# Attach to monitor streams
./stream-relays.sh attach

# Stop all streams
./stream-relays.sh stop

# Switch Dgraph to a remote instance (updates whitelist server + wot crawler)
./switch-dgraph.sh remote

# Switch back to local Dgraph container
./switch-dgraph.sh local

# Check which mode Dgraph is in
./switch-dgraph.sh status

# Check whitelist server health
curl http://localhost:8081/health

# Check whitelist stats
curl http://localhost:8081/stats

# Check if a pubkey is whitelisted
curl http://localhost:8081/check/<64-char-hex-pubkey>
```

## Next Steps

1. Set up `.env` file with `STRFRY_PRIVATE_KEY` (copy from `.env.example`)
2. Test Nostr WebSocket connection to `ws://localhost:7777`
3. Access Dgraph UI at http://localhost:8000
4. Begin implementing subsystem services


---

## Contributing

We merge all PRs that solve exactly one clearly stated problem and don't break any existing functionality. 

1. Fork the repository and create a branch.
2. Work on your feature or fix.
3. Submit a PR with a clear one line description of the problem being solved. 

---

## License

Mozilla Public License Version 2.0
