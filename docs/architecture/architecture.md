# DeepFry Architecture

> DRAFT - SUBJECT TO CHANGE

---

## 1) Purpose

DeepFry is the backend stack for Humble Horse. It keeps **StrFry** stock for speed and NIP compliance and adds subsystems for semantic search, trust graphs, profiles, threads, and upstream ingestion. The **Search Plugin** integrates NIP‑50 without forking StrFry by delegating to an external Semantic Search service.

---

## 2) Diagrams

- **System Context**  
  ![C4 System Context](/docs/images/c4_system_context.png)

- **Containers**  
  ![C4 Containers](/docs/images/c4_container.png)

---

## 3) Data Model

- **Canonical events**: StrFry **LMDB** only. No duplication elsewhere.
- **Graphs (ID‑only in Dgraph)**
  - **WoT graph**: nodes = pubkeys; edges = follows/trust (NIP‑02).
  - **Thread graph**: nodes = event ids; edges = reply/quote/repost links.
- **Vectors**: pgvector initially. Keys include `{event_id, pubkey, kind, ts}`.  
  Profile features may be persisted as vectors or metadata (TBA).

---

## 4) Subsystems (summary)

| #   | Subsystem                         | Purpose                                                        | Tech / Storage                       | NIPs                         |
| --- | --------------------------------- | -------------------------------------------------------------- | ------------------------------------ | ---------------------------- |
| 1   | **StrFry Relay** (stock + plugin) | Canonical ingest/query. Dispatch NIP‑50 to plugin.             | C++ / **LMDB**                       | 1,2,4,9,11,15,22,28,40,70,77 |
| 2   | **Search Plugin**                 | Handle NIP‑50 inside StrFry. Delegate to Search API.           | StrFry plugin (stdin/stdout JSON)    | 50                           |
| 3   | **Semantic Search Service**       | `/index`, `/search`, hybrid ranking (semantic + BM25 + trust). | TS/Go + **vector**† (+optional FTS)  | 50                           |
| 4   | **Embeddings Generator**          | Clean → chunk → embed → upsert vectors.                        | **Go workers** → vector†             | —                            |
| 5   | **Event Forwarder**               | `stream/sync` from upstream → republish into StrFry.           | Service; offset as replaceable event | 1                            |
| 6   | **WoT Builder + Crawler**         | Build pubkey trust graph; export allowlist.                    | Go/.NET/Rust + **Dgraph**            | 2                            |
| 7   | **Profile Builder**               | Track Likes, Mutes, Replies, Reposts, Zaps; compute interests. | Service; writes features to vector†  | 18,25,51,57                  |
| 8   | **Thread Inference Engine**       | Build thread graph from replies/quotes.                        | Go/.NET/Rust + **Dgraph**            | 1                            |

† profile → vector DB usage is TBD and may store feature vectors or metadata for recommendations.

---
