# DeepFry

DeepFry is the backend stack that powers **Humble Horse** — a Nostr-based ecosystem for trust-aware content discovery, semantic search, and thread reconstruction.

This repository contains the **StrFry relay** and all backend subsystems that feed, enhance, and query it. The design keeps StrFry stock for maximum performance and NIP compliance, while surrounding it with additional services for discovery, trust scoring, and search.

We welcome external contributors to help build the fastest and smartest relay stack in the Nostr network.

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

---

## Contributing

We welcome contributions from the community.

1. Fork the repository and create a branch.
2. Work on your feature or fix.
3. Submit a PR with a clear description and test coverage.

---

## License

Mozilla Public License Version 2.0
