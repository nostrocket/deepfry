# Copilot / AI Agent Project Instructions

Goal: Provide minimal, high-signal context so an AI agent can quickly become productive in this repository.

## 1. System Overview

DeepFry is a modular backend stack around a stock StrFry Nostr relay. StrFry remains unmodified for performance + protocol compliance; added capabilities live in sidecar services communicating via standard Nostr WebSocket protocol or plugin stdin/stdout.

Core runtime pieces:

- StrFry relay (authoritative event store; LMDB backend)
- Search plugin (invoked by StrFry for NIP-50 requests; stateless pass-through to Semantic Search Service)
- Subsystems (each in its own folder) consuming / enriching events: event-forwarder, web-of-trust, semantic-search, embeddings-generator, profile-builder, thread-inference.

Data separation principle: canonical Nostr events only in StrFry; graphs in Dgraph; vectors in pgvector (planned).

## 2. Repo Layout Conventions

Each subsystem has a top-level folder with a README describing intent & requirements (event-forwarder is most complete). Placeholder READMEs indicate future detail—do not invent undocumented behavior.
No shared monorepo framework yet; treat each subsystem as an independently deployable service.

## 3. Subsystem Responsibilities (Actionable Snapshot)

- event-forwarder: Sync from external relay -> StrFry; maintains progress via kind 30078 parameterized replaceable event tagged with d=<source relay URL>, from, to.
- web-of-trust: (planned) build trust graph from NIP-02 follows -> Dgraph pubkey nodes.
- semantic-search: (planned) query composition (semantic similarity + BM25 + trust weighting).
- embeddings-generator: (planned) produce embeddings into vector DB.
- profile-builder: (planned) aggregate engagement signals (likes, mutes, replies, reposts, zaps).
- thread-inference: (planned) build reply/quote graphs (event id edges) in Dgraph.
- search-plugin: minimal translation layer for NIP-50 between StrFry and semantic-search.

## 4. Protocol / Domain Rules to Preserve

- Keep StrFry unmodified; extension only via its JSON plugin interface (stdin/stdout) for search.
- Use standard Nostr WebSocket interactions (NIP-01). Do not introduce custom protocol frames.
- Sync progress event (event-forwarder) must be kind 30078 (NIP-33) with required tags; publishing is fire-and-forget (no OK wait) but must occur only after a batch is sent.
- One forwarder process per distinct source relay (per d tag) to avoid race conditions.

## 5. State & Storage Boundaries

- StrFry: sole store of raw events (immutable retention).
- Dgraph: only for graphs (WoT pubkeys, thread event ids). Store ID references, not full event payloads.
- Vector DB (pgvector initial): only embeddings & derived vectors.
- No cross-writing of canonical event payloads outside StrFry.

## 6. Coding / Implementation Expectations

(Development scaffolding not yet present—add pragmatically, keeping boundaries above.)

- Introduce language/runtime per subsystem as needed; keep service-local dependencies isolated.
- Centralize Nostr key handling per service; secrets via env vars (example: NOSTR_SYNC_SECKEY for event-forwarder) and never logged raw.
- Prefer structured logging (JSON) with latency + counts (see event-forwarder NFR metrics list for naming inspiration: events_received, events_forwarded, lag_s).

## 7. Adding a New Feature (Example Workflow)

Example: extend event-forwarder with metrics export.

1. Add metrics lib (e.g., Prometheus) only within event-forwarder.
2. Expose HTTP /metrics; do not couple other subsystems.
3. Emit counters matching existing naming style.
4. Update README section 'Next Steps' if scope evolves.

## 8. Testing Guidance

- Start with integration-level tests against a test Nostr relay (in-memory or test container) for forwarder logic (subscribe, forward, resume).
- Validate sync progress by querying latest kind 30078 event in StrFry after simulated batches.
- Do not write speculative tests for placeholder subsystems until README gains concrete requirements.

## 9. When Unsure

If README is placeholder, request product/owner clarification rather than guessing. Maintain explicitness of tags, kinds, storage boundaries, and statelessness of the search plugin.

## 10. Future Docs Hooks

As subsystems mature, expand their README with: configuration keys, metric names, failure modes. Mirror the requirement formatting style of event-forwarder.

End.
