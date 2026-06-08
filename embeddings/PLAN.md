# Vector DB & Embedding Model Selection for DeepFry Semantic Search

## Context

DeepFry's `semantic-search`, `embeddings-generator`, and `search-plugin` subsystems are placeholder READMEs. The architecture doc (`docs/architecture/architecture.md` lines 29–47) tentatively names **pgvector** as the vector store but marks it TBD. Before any worker code is written, the two foundational picks — vector database and embedding model — need to be locked in so that:

- The vector store can run as a Docker sidecar on consumer hardware (Mac mini M-series, 16–32 GB RAM, ARM64).
- The data source is kind 1 notes from the local StrFry relay (short, tweet-length English text; volume TBD but must scale from ~100K to ~10M without re-architecting).
- Hybrid ranking (semantic + BM25 + trust score) works **at launch**, not as a follow-up.
- The embedding generator is decoupled from the serving stack so we can swap between Ollama, TEI, llama.cpp, or MLX depending on the host.

This plan documents the decision and the rationale. It does **not** cover the Go worker that wires these together — that's a follow-up plan.

## Decision Summary

| Component | Pick | Runner-up |
|---|---|---|
| Vector database | **Qdrant** | pgvector |
| Embedding model (primary) | **Qwen3-Embedding-0.6B** (Apache-2.0) | EmbeddingGemma-300M |
| Serving abstraction | **OpenAI-compatible `/v1/embeddings` HTTP contract** | — |

## Vector DB: Qdrant

**Why Qdrant beats pgvector for this deployment**

- **Hybrid search is native, not bolted on.** Since v1.10 the Query API does server-side dense + sparse (BM25) fusion with RRF/DBSF, and filters (pubkey, timestamp, trust band) run *during* HNSW traversal (ACORN), so trust/recency filtering doesn't tank recall. pgvector requires hand-rolled SQL glue between `tsvector` BM25 and `vector` cosine search.
- **ARM64 native.** Official multi-arch Docker image, no Rosetta. Rust binary, fast cold start on Apple Silicon.
- **RAM efficient at scale.** With mmap'd vectors + scalar/binary quantization, 10M × 1024-dim sits in roughly 6–10 GB resident. pgvector adds ~40% overhead for equivalent recall.
- **Operationally simple.** One container, one volume, snapshot API for backup. Matches the rest of the DeepFry stack (one daemon per concern).
- **Go client.** `github.com/qdrant/go-client` is official, gRPC, Apache-2.0, stable through 2025–26 releases. Strongest Go story of the candidates.
- **Active in 2026.** Qdrant and Milvus are the two most actively developed open vector DBs. Chroma OSS has slowed; sqlite-vss is abandoned; sqlite-vec is pre-1.0 with no production hybrid.

**Switch criteria**

- Switch to **pgvector** if trust graph state ends up in Postgres and you want one fewer daemon, *and* you accept writing the hybrid SQL yourself, *and* volume plateaus under ~2M notes.
- Switch to **Milvus** only when you outgrow a single Mac mini (64 GB+ server tier). Qdrant's own distributed mode is also fine at that point.

**Rejected**

- **Weaviate** — best hybrid ergonomics but 2–4× Qdrant RAM, weaker Go client.
- **Milvus** — overbuilt for consumer hardware; standalone still bundles etcd + MinIO; 32 GB RAM floor at 10M.
- **Chroma** — prototyping only; no real Go client; OSS momentum slowing.
- **LanceDB** — embedded-first; CGO Go bindings; hybrid is weaker.
- **sqlite-vec** — edge tier; not for tens of millions of vectors with hybrid.

## Embedding Model: Qwen3-Embedding-0.6B

**Why this model**

- Best MTEB quality of any sub-1B Apache-2.0 model as of May 2026; the only open model in its weight class competitive with Gemini-Embedding.
- **Matryoshka output (32 → 1024 dims).** Index at 1024 during evaluation, store at 768 or 512 with negligible loss — directly controls storage cost in Qdrant.
- **32K context.** Eliminates any truncation risk for the occasional long kind 1; instruction-aware (apply the query prefix only at retrieval time).
- **Multi-runtime.** First-class GGUF; runs on Ollama (`qwen3-embedding:0.6b`), TEI (with Metal build), llama.cpp server, and MLX. Fits in <2 GB at Q8.
- **Throughput.** ~200–500 embeddings/sec batched on M2 Pro — well above the 100/sec target for backfilling.

**Fast/small alternative: EmbeddingGemma-300M**

If RAM ends up tight or backfill speed dominates: highest-ranked open <500M on MTEB, 768-dim native with MRL → 512/256/128, sub-15 ms latency, <200 MB quantized, ~800–2000 emb/s on M3. Commercial-use Gemma license (permissive but not OSI). The quality gap vs Qwen3-0.6B is small for tweet-length English. Same serving stacks apply, so a swap is cheap if we re-embed.

**Rejected**

- `bge-m3` — strong but superseded by Qwen3 at this size.
- `bge-base/small-en-v1.5`, `e5-base-v2` — aging; quality below newer 300–600M models.
- `mxbai-embed-large-v1` — 512-ctx cap is a hard ceiling.
- `stella_en_*` — quality fine, but MIT lineage less clear than Apache 2.0.
- `gte-large-en-v1.5` — solid but no MRL; locked to 1024 dim.
- **Jina v3 / v4** — excluded; CC-BY-NC / Qwen Research licenses are non-commercial.
- `Qwen3-Embedding-4B/8B` — top of MMTEB but too heavy for a 16–32 GB Mac mini under sustained load.

## Serving Abstraction

To satisfy the "should support llama.cpp / MLX / Ollama / TEI depending on hardware" requirement: **the embeddings worker talks to the model server only via the OpenAI-compatible `POST /v1/embeddings` contract.** All four runtimes (Ollama, TEI, llama.cpp server, MLX-based servers like `mlx-omni-server` / `mlx-llm`) expose this endpoint. The worker reads `EMBEDDINGS_BASE_URL` and `EMBEDDINGS_MODEL` from env, so swapping runtimes is a docker-compose change with zero code touched.

The Mac mini production default is **TEI with Metal** (highest throughput, native dynamic batching) or **Ollama** (simplest UX, MLX backend on Apple Silicon). The HF TEI image is linux/amd64 — use the Apple-Silicon native build (`cargo build --features metal`) outside Docker, or run Ollama in Docker.

## Storage & Index Settings

- Cosine distance on L2-normalized vectors.
- HNSW: `M=16`, `efConstruction=200`. Tune `ef` at query time per latency budget.
- Stored payload (no event body — that lives in StrFry LMDB per the data-separation rule): `event_id`, `pubkey`, `kind`, `created_at`, optionally `trust_band`.
- Quantization: start with scalar int8; revisit binary quantization above ~5M vectors.

## Critical Files / Locations

- `docs/architecture/architecture.md` lines 29–47 — update the pgvector mention to reflect this decision before implementation begins.
- `embeddings-generator/` — currently a placeholder README; will become the Go worker (separate plan).
- `semantic-search/` — does not exist on disk; will host the search API service (separate plan).
- `search-plugin/` — placeholder; the StrFry stdin/stdout plugin that delegates NIP-50 queries to `semantic-search`.

## Verification

This plan is a decision document; verification = the picks survive contact with the implementation plan. Concrete acceptance gates for the follow-on work:

1. `docker compose up qdrant embeddings-server` starts cleanly on a Mac mini M-series with the official Qdrant ARM64 image.
2. A smoke script embeds 1,000 sampled kind 1 notes via `POST /v1/embeddings` against whichever runtime is configured, upserts to Qdrant, and a hybrid query (`prefetch: { dense + sparse }` → RRF) returns sensible top-10.
3. Swapping `EMBEDDINGS_BASE_URL` between Ollama and TEI requires no code change.
4. Re-embedding the same 1,000 notes with EmbeddingGemma (the fallback) requires only an env change and a fresh collection — confirming runtime/model decoupling.

## Open Items (Out of Scope Here)

- Embeddings-generator worker design (consumes from StrFry, chunks, embeds, upserts).
- Semantic Search Service API surface (`/index`, `/search`) and trust-score fusion weights.
- Search Plugin (StrFry stdin/stdout) wiring for NIP-50.
- Backfill strategy and rate limiting against the local model server.
