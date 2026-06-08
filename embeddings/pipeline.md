# DeepFry Embeddings Pipeline

## Context

The vector DB and embedding model have been picked (Qdrant + Qwen3-Embedding-0.6B, served over OpenAI-compatible `/v1/embeddings` — see `embeddings/PLAN.md`). This plan defines the **pipeline** that sits between them: it reads kind 1 notes from a **remote StrFry**, embeds each one (dense + sparse), and upserts to Qdrant so the Semantic Search service can query a fully populated hybrid index.

Constraints decided with the user:
- **Backfill everything**, then live stream (one binary, two modes).
- **Ignore kind 5 deletions** — vector index is best-effort wrt deletion.
- **Detect language, index all** — store `lang` in payload.
- **StrFry is remote** — pipeline connects over WebSocket to a separate host.
- **Hybrid (dense + sparse) at launch** — already required by the DB plan.

## Language choice: Python

Not Go. The deciding factor is **`fastembed`** (maintained by Qdrant) which produces both the dense vector (against the local OpenAI-compatible embed server) AND the BM25 sparse vector in-process, with zero extra services. The equivalent Go path requires either spinning up a separate sparse-encoding sidecar or hand-implementing IDF/BM25 — wrong tradeoff for a daemon whose throughput ceiling is set by the remote embed server, not by orchestrator speed. Secondary wins: mature `qdrant-client` async API, `nostr-sdk` and `pynostr` are both viable, `fasttext-langdetect` / `lingua` for cheap language ID, single Dockerfile, asyncio is sufficient for the I/O profile.

Go is fine for the existing StrFry-adjacent daemons (whitelist plugin, event-forwarder) where there is no ML/encoding work — that's not this service.

## Topology

```
   remote StrFry                local docker-compose
   (ws://relay:7777)            ┌──────────────────────────────────────────────┐
        │                       │                                              │
        │   NIP-01 REQ (kind 1) │   ┌────────────────┐                          │
        ├──────────────────────►│   │  embeddings-   │                          │
        │   (backfill + stream) │   │  generator     │                          │
        │                       │   │   (Python)     │                          │
        │                       │   │                │                          │
        │                       │   │  ─source       │   POST /v1/embeddings    │
        │                       │   │  ─clean ──────►│──────────► embed-server  │
        │                       │   │  ─langdetect   │            (Ollama/TEI)  │
        │                       │   │  ─batch        │                          │
        │                       │   │  ─dense (HTTP) │   gRPC upsert            │
        │                       │   │  ─sparse (BM25)│──────────► qdrant:6334   │
        │                       │   │  ─writer       │                          │
        │                       │   │  ─checkpoint   │   sqlite volume          │
        │                       │   └────────────────┘                          │
        │                       └──────────────────────────────────────────────┘
```

## Pipeline stages

Each stage is an async coroutine connected by bounded `asyncio.Queue`s. Bounded queues give natural backpressure: when the embed server slows, the source slows.

| Stage | Module | Responsibility |
|---|---|---|
| Source | `source.py` | NIP-01 client over WebSocket. Two modes: `backfill()` walks history backward via `{kinds:[1], until: cursor}` pagination; `stream()` opens a live REQ with `{kinds:[1], since: last_seen}`. Both run concurrently on first launch; backfill stops when it catches up to the earliest streamed event. Reconnect with exponential backoff. |
| Clean | `clean.py` | Strip `nostr:` URIs to bare mentions, collapse whitespace, drop empty/whitespace-only notes, cap at 8K chars (well under model 32K). Don't lowercase, don't drop hashtags — they carry semantic signal. |
| Language detect | `lang.py` | `lingua-language-detector` (better short-text accuracy than fasttext for sub-100 char text). Output ISO 639-1 code or `"und"`. ~0.5–2ms per event. |
| Batch | `batch.py` | Size-or-time trigger: flush at 64 events OR 1s, whichever first. Returns `Batch(events, texts)`. |
| Dense embed | `embed.py` | `openai.AsyncOpenAI(base_url=EMBEDDINGS_BASE_URL).embeddings.create(model=EMBEDDINGS_MODEL, input=texts)`. Retry with exponential backoff on 5xx / connection error. **Apply Matryoshka truncation client-side to `DENSE_DIM`** (default 1024; 768 for storage saving) then L2-normalize. |
| Sparse embed | `sparse.py` | `fastembed.SparseTextEmbedding("Qdrant/bm25").embed(texts)`. Pure CPU, no GPU. Runs in a thread pool to avoid blocking the event loop. |
| Writer | `writer.py` | `AsyncQdrantClient.upsert(collection, points=[...])` with named vectors `dense` + `bm25`. Point id is `uuid5(NAMESPACE_OID, event_id_hex)` — deterministic, so re-runs are idempotent. |
| Checkpoint | `checkpoint.py` | SQLite single-row `(stream_since INT, backfill_until INT, backfill_done BOOL)` on a docker volume. Flushed after every successful batch. |

## Qdrant collection schema

```
collection: nostr_kind1
vectors:
  dense:
    size: 1024            # or 768 via Matryoshka — pick after smoke test
    distance: Cosine
    on_disk: true
sparse_vectors:
  bm25:
    modifier: idf
payload_schema:
  event_id:   keyword   (indexed — for ops/debug, NOT primary identity; point id is uuid5)
  pubkey:     keyword   (indexed — filter by author / trust band)
  created_at: integer   (indexed — recency filters)
  lang:       keyword   (indexed — language-filtered search)
quantization:
  scalar: int8
hnsw:
  m: 16
  ef_construct: 200
```

**No event body in the payload.** Per the DeepFry data-separation rule, canonical event content lives only in StrFry's LMDB. Search results return ids; the search service re-hydrates from StrFry.

## Configuration (env vars)

| Var | Purpose | Example |
|---|---|---|
| `STRFRY_RELAY_URL` | Remote StrFry WebSocket | `wss://relay.example.com` |
| `EMBEDDINGS_BASE_URL` | OpenAI-compatible endpoint | `http://embed-server:8080/v1` |
| `EMBEDDINGS_MODEL` | Model name as the server expects | `qwen3-embedding:0.6b` |
| `DENSE_DIM` | Matryoshka truncation target | `1024` (or `768`) |
| `QDRANT_URL` | gRPC endpoint | `http://qdrant:6334` |
| `QDRANT_COLLECTION` | Collection name | `nostr_kind1` |
| `BATCH_SIZE` | Events per embed call | `64` |
| `BATCH_TIMEOUT_MS` | Max wait before flush | `1000` |
| `BACKFILL_SINCE` | Earliest unix ts to backfill (0 = all) | `0` |
| `CHECKPOINT_PATH` | SQLite file on a volume | `/data/checkpoint.db` |

## Failure modes

| Failure | Behaviour |
|---|---|
| StrFry WS drops | Reconnect with exp backoff; resume from `stream_since` checkpoint (no event loss) |
| Embed server 5xx / timeout | Retry batch with exp backoff; queue pressure stalls source |
| Qdrant unreachable | Retry; if persistent past N retries, halt with non-zero exit so Docker restart policy alerts |
| Single malformed event | Log + write to `dead-letter.jsonl`, never crash the loop |
| Restart mid-backfill | UUID5 point ids → re-upserts are no-ops; resume from `backfill_until` cursor |

## Throughput estimate

- Dense: 200 emb/s × batch 64 against Qwen3-0.6B on M2 Pro.
- Sparse: BM25 via fastembed is ~3-5K events/s on a single core — never the bottleneck.
- Backfill 10M notes ≈ 14 hours, single pipeline. Acceptable; if not, run two pipelines pointed at non-overlapping `since/until` windows.

## Critical files to create

- `embeddings-generator/` — currently a stub README; becomes the Python project root.
  - `pyproject.toml` (deps: `qdrant-client[fastembed]`, `openai`, `pynostr` or `nostr-sdk`, `lingua-language-detector`, `aiosqlite`, `structlog`)
  - `src/embeddings_generator/{config,source,clean,lang,batch,embed,sparse,writer,checkpoint,pipeline}.py`
  - `Dockerfile` (python:3.12-slim base, multi-stage)
  - `tests/` — unit tests for clean/lang/batch/checkpoint; integration test against a local Qdrant + mock embed server
- `docker-compose.embeddings.yml` (new, repo root) — services: `qdrant`, `embed-server` (Ollama with Qwen3 model pulled, or TEI), `embeddings-generator`. Persistent volumes for Qdrant data and the checkpoint sqlite.
- `docs/architecture/architecture.md` lines 29–47 — update to reflect Qdrant + Python pipeline + Qwen3 (the doc still says "Go workers" and "pgvector").

## Verification

Run on the target Mac mini against a remote StrFry:

1. `docker compose -f docker-compose.embeddings.yml up -d qdrant embed-server` — both come up healthy on ARM64.
2. `curl http://localhost:8080/v1/embeddings -d '{"model":"qwen3-embedding:0.6b","input":"hello"}'` returns a 1024-dim vector.
3. Bring up the pipeline pointed at a real remote StrFry. Within seconds, `curl http://localhost:6333/collections/nostr_kind1` shows growing point count.
4. Issue a hybrid query (dense + sparse → RRF fusion via Qdrant Query API) for a topical phrase. Top-10 should be plausible matches with diverse pubkeys.
5. **Idempotency**: `docker compose restart embeddings-generator` mid-backfill. Point count does not double; backfill resumes from checkpoint.
6. **Backpressure**: `docker compose stop embed-server`. Pipeline stays alive, queues fill, source stalls — no events lost. `docker compose start embed-server` → backfill resumes.
7. **Language tagging**: scroll a sample of points (`curl /collections/nostr_kind1/points/scroll`) and confirm `lang` covers multiple ISO codes for a multilingual relay.

## Out of scope (for follow-on plans)

- Semantic Search Service (`/index` is unnecessary since this pipeline owns indexing; `/search` API + trust-score fusion are separate).
- Search Plugin (the StrFry stdin/stdout plugin that turns NIP-50 REQs into search service calls).
- Model swap / re-embedding strategy (a `model_version` payload field lets us re-embed selectively later).
- Profile Builder feature vectors — different consumer entirely.
