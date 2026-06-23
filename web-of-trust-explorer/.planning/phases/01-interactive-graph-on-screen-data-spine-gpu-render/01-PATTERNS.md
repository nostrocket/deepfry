# Phase 1: Interactive Graph On Screen (Data Spine + GPU Render) - Pattern Map

**Mapped:** 2026-06-23
**Files analyzed:** 13 new files (all greenfield TS) + scaffold
**Analogs found:** 4 cross-language (Go) convention analogs / 13 files — the rest are genuinely novel (no analog exists)

> **CRITICAL CONTEXT FOR THE PLANNER:** `web-of-trust-explorer/` is GREENFIELD. There are
> **no existing TypeScript / WebGL / cosmos.gl files** anywhere in this repo to copy from.
> Do NOT expect TS analogs — none exist. The only useful analogs live one level up in the
> surrounding **Go** stack (`../web-of-trust/`, `../whitelist-plugin/`). They are
> **cross-language convention references**, not code to reuse. Their value is in pinning down
> exactly two things consistently with the rest of DeepFry:
> 1. **The Dgraph query shape** — how the `Profile`/`follows` graph is actually queried
>    (read-only txn, `after:<uid>` cursor paging, ID-only `{uid follows{uid}}` shape, the
>    `{data:{...},extensions:{...}}` response envelope to parse).
> 2. **The config / connection convention** — `~/deepfry/*.yaml` via the established loader,
>    `localhost:8080` (GraphQL/HTTP) and `localhost:9080` (gRPC), and the read-only / never-mutate rule.
>
> For the render layer (cosmos.gl), the SoA typed-buffer store, the in-browser BA generator,
> the auto-freeze sampler, and the loader/verdict instrument, **there is no analog** — the
> authoritative spec is `01-RESEARCH.md` (verified cosmos.gl 3.0.0 `.d.ts` API) and
> `.claude/CLAUDE.md`. Those files are listed under "No Analog Found" and the planner should
> drive them from RESEARCH, not from any Go file.

## File Classification

| New File | Role | Data Flow | Closest Analog | Match Quality |
|----------|------|-----------|----------------|---------------|
| `src/transport/GraphTransport.ts` | interface / contract | request-response | — (pure design, RESEARCH Pattern 1) | none |
| `src/transport/DgraphTransport.ts` | service (data loader) | request-response (paged fetch) | `../web-of-trust/pkg/dgraph/dgraph.go` `backfillFollowerCountPaged` + `../whitelist-plugin/pkg/repository/dgraph_repository.go` | role-match (cross-lang convention) |
| `src/transport/SyntheticTransport.ts` | service (generator driver) | batch / transform | — (BA generator, RESEARCH §Synthetic) | none |
| `src/workers/dgraph.worker.ts` | worker (fetch+parse+remap) | streaming-ish (chunked parse) | `../web-of-trust/pkg/dgraph/clusterscan.go` (query+unmarshal shape) | role-match (query shape only) |
| `src/workers/synthetic.worker.ts` | worker (BA generator) | batch / transform | — (RESEARCH §Synthetic Power-Law Generator) | none |
| `src/graph/cosmos.ts` | render adapter | event-driven (GPU loop) | — (cosmos.gl 3.0.0, RESEARCH §cosmos.gl Verified API) | none |
| `src/graph/autofreeze.ts` | utility (motion sampler) | event-driven (rAF/interval) | — (RESEARCH §Auto-Freeze Threshold) | none |
| `src/ui/loader.ts` | UI component | event-driven (progress msgs) | — (RESEARCH §Staged Loader / D-09) | none |
| `src/ui/verdict.ts` | utility (instrumentation) | transform / readout | — (RESEARCH §Verdict readout / D-10) | none |
| `src/types.ts` | model (typed-buffer shapes) | — | — (RESEARCH Pattern 1 `GraphBuffers`) | none |
| `src/main.ts` | app entry / wiring | event-driven | — | none |
| `vite.config.ts` (+ COOP/COEP, worker) | config | — | — (RESEARCH §Pitfall 4) | none |
| `tests/*.test.ts` (remap, generator, parse, transport, precision) | test | — | `../web-of-trust/pkg/dgraph/*_test.go` (table-test convention, conceptual only) | weak / convention |

**Config-loading convention analog (applies to how DgraphTransport reads its endpoint):**
`../web-of-trust/pkg/config/config.go` — the `~/deepfry/*.yaml` + `localhost:9080`/`localhost:8080`
default convention. The TS tool has **no backend and no viper**; this is a *convention reference*
for the default Dgraph URL and the read-only stance, not a code analog. v1 will likely hardcode /
env-var `http://localhost:8080` rather than read `~/deepfry/*.yaml`.

## Pattern Assignments

### `src/transport/DgraphTransport.ts` (service, request-response, paged)

**Primary analog:** `../web-of-trust/pkg/dgraph/dgraph.go` — `backfillFollowerCountPaged` (lines 1435-1496)
**Secondary analog:** `../whitelist-plugin/pkg/repository/dgraph_repository.go` — `fetchAllPubkeysFromDgraph` / `fetchPubkeysPage` (lines 80-200)

This is the single most important convention to copy: **the exact `after:<uid>` cursor-paging
loop shape** the research recommends (RESEARCH §Dgraph DQL Bulk-Load). The Go code already
runs it against the live `Profile` graph, so the TS transport should mirror its loop invariants:
start cursor `0x0`, advance to the last uid of each page, terminate on a short/empty page.

**Cursor-paging loop pattern to copy** (`dgraph.go` lines 1441-1493) — translate the loop structure to TS:
```go
cursor := "0x0" // uid cursor; `after: 0x0` starts before the first node.
for {
    query := fmt.Sprintf(`
    query {
        page(func: has(pubkey), first: %d, after: %s) { uid }
    }`, pageSize, cursor)
    // ... run read-only query, unmarshal ...
    if len(result.Page) == 0 { break }            // end of set
    cursor = result.Page[len(result.Page)-1].UID  // advance cursor to last uid
    if len(result.Page) < pageSize { break }       // short page = done
}
```
Carry these invariants into `dgraph.worker.ts`: cursor starts `0x0`, bump to last uid per page,
stop on short/empty page. **The TS version queries `{ q(func: has(follows), first:N, after:<cursor>){ uid follows{uid} } }`** (RESEARCH §Dgraph DQL Bulk-Load) — the outer node set is the paged axis.

**HTTP request convention to copy** (`dgraph_repository.go` lines 118-200) — the browser `fetch`
equivalent of this Go HTTP path:
```go
req, _ := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewBuffer(jsonData))
req.Header.Set("Content-Type", "application/json")   // TS: "application/dql" for DQL /query (see note)
resp, _ := r.httpClient.Do(req)
body, _ := io.ReadAll(resp.Body)
if resp.StatusCode != http.StatusOK { /* error with body */ }
```
> **Divergence note (deliberate):** the whitelist repo uses the **GraphQL** endpoint
> (`Content-Type: application/json`, `queryProfile(offset, first)`). Phase 1 instead uses the
> **DQL** endpoint per `.claude/CLAUDE.md` "What NOT to Use" (GraphQL is chatty for a bulk dump):
> `POST /query`, `Content-Type: application/dql`, body = raw DQL string. Copy the *request/error
> handling structure*, but switch endpoint + content-type to DQL. Also prefer `after:<uid>` over
> the whitelist repo's `offset` (offset paging degrades at scale — see `dgraph.go` line 1404 note).

**Count-for-progress convention** (`dgraph.go` `CountPubkeys` lines 1499-1530): a cheap
`count(func: has(pubkey)){ count(uid) }` exists if an upfront total is wanted — but D-09 explicitly
declined a % bar, so this is optional (useful only for the "~30M" denominator in the live counter).

---

### `src/workers/dgraph.worker.ts` (worker, chunked parse + hex→uint32 remap)

**Analog:** `../web-of-trust/pkg/dgraph/clusterscan.go` (lines 45-135) — for the **query+unmarshal
shape only**. The chunked-parse / Transferable / memory-budget discipline has **no Go analog**
(Go has no `JSON.parse` doubling problem); drive that entirely from RESEARCH §Memory & Parse Budgeting.

**Read-only txn + DQL query + typed unmarshal pattern to mirror** (`clusterscan.go` lines 57-87):
```go
query := fmt.Sprintf(`{ nodes(func: eq(pubkey, [%s])) { uid pubkey } }`, ...)
txn := c.dg.NewReadOnlyTxn()   // READ-ONLY — never mutate (data-separation rule)
defer txn.Discard(ctx)
resp, err := txn.Query(ctx, query)
var result struct {
    Nodes []struct { UID string `json:"uid"`; Pubkey string `json:"pubkey"` } `json:"nodes"`
}
json.Unmarshal(resp.Json, &result)
```
TS equivalent parses the `{ data: { q: [...] }, extensions: {...} }` envelope (RESEARCH
§response shape lines 277-283). The `extensions.server_latency.encoding_ns` field is part of the
D-10 verdict — extract and log it. **Read-only is a hard convention** (every Go query uses
`NewReadOnlyTxn`); the TS tool only ever issues read DQL, never a mutation.

**hex→uint32 remap (NO analog — novel):** RESEARCH §Memory & Parse Budgeting point 2 mandates a
`Map<string,number>` assigning a dense index on first sighting of each hex uid, edges stored as
`Uint32Array` pairs. Note the Go side works in raw hex/uid strings — the dense-remap-into-typed-array
is browser-memory-budget-specific and has no Go counterpart.

---

### `tests/*.test.ts` (tests — convention reference only)

**Analog:** `../web-of-trust/pkg/dgraph/*_test.go` and `../whitelist-plugin/pkg/repository/dgraph_repository_test.go`
exist and demonstrate the project's **table-driven unit-test convention** — but they are Go.
The TS tests use **vitest** (RESEARCH §Validation Architecture). Use the Go tests only as a
reminder that the DeepFry convention is: pure-function/data-pipeline units are unit-tested,
infra/perf is left to spikes. The five Wave-0 vitest files (remap, generator, parse, transport,
precision) are specified in RESEARCH §Wave 0 Gaps — no analog to copy, drive from RESEARCH.

## Shared Patterns

### Read-Only Dgraph Access (hard project rule)
**Source:** `../web-of-trust/pkg/dgraph/clusterscan.go` line 65 (`c.dg.NewReadOnlyTxn()`), repeated
in every query method; `deepfry/CLAUDE.md` §"Protocol Rules" / data-separation rule.
**Apply to:** `DgraphTransport.ts`, `dgraph.worker.ts`.
The explorer NEVER mutates Dgraph or StrFry. Canonical events live only in StrFry's LMDB; Dgraph
holds the ID-only graph. The TS tool issues read DQL exclusively — no mutation request is ever built.
```go
txn := c.dg.NewReadOnlyTxn()
defer txn.Discard(ctx)
resp, err := txn.Query(ctx, query)
```

### Dgraph `Profile` Schema (the data contract)
**Source:** `deepfry/config/dgraph/schema.graphql` (the live schema):
```graphql
type Profile @dgraph(type: "Profile") {
  pubkey: String! @id @search(by: [exact]) @dgraph(pred: "pubkey")
  follows: [Profile] @hasInverse(field: followers) @dgraph(pred: "follows")
  followers: [Profile] @dgraph(pred: "followers")
  kind3CreatedAt: Int @search(by: [int])
  last_db_update: Int @search(by: [int])
}
```
**Apply to:** the DQL query in `dgraph.worker.ts` and the `GraphBuffers`/types.
- Page the **outer node set** via `func: has(follows)` (or `has(pubkey)`), pull only `uid` +
  inner `follows { uid }` — the most compact ID-only shape (RESEARCH lines 262-273).
- **Do NOT query `followers`** — in-degree is derived client-side in one O(E) pass (RESEARCH
  line 274; mirrors the Go `count(~follows)` idea but done in-browser). Querying the inverse
  doubles the payload and double-counts.

### Dgraph Connection Convention
**Source:** `../web-of-trust/pkg/config/config.go` lines 98 (`dgraph_addr: localhost:9080`),
`deepfry/CLAUDE.md` §Infrastructure (`:8080` HTTP/GraphQL, `:9080` gRPC, `:8000` Ratel).
**Apply to:** `DgraphTransport.ts` default endpoint.
The Go stack talks gRPC (`:9080`); the **browser cannot** — it MUST use HTTP `:8080` (`POST /query`,
`Content-Type: application/dql`). This is the one place the TS tool deliberately diverges from the
Go transport (gRPC→HTTP/JSON), an accepted tradeoff per `.claude/CLAUDE.md`. Bring Dgraph up with
`docker-compose -f docker-compose.dgraph.yml up -d` from the deepfry root (D-04 real-wire spike).

### Config / `~/deepfry/*.yaml` Convention (reference, likely NOT replicated in v1)
**Source:** `../web-of-trust/pkg/config/config.go` lines 73-105 (viper, `~/deepfry/<name>.yaml`,
sensible defaults, write-default-if-missing).
**Apply to:** only insofar as the planner decides whether the explorer's Dgraph URL should follow
the `~/deepfry/` convention. Given v1 is a single-dev browser tool with no Go backend, hardcoding
or a Vite env var for `http://localhost:8080` is acceptable; full viper-style config is out of scope.
This analog documents the *house convention* so the planner makes a conscious choice.

## No Analog Found

These files are genuinely novel — there is **no codebase analog** (Go or TS). The planner MUST
drive them from `01-RESEARCH.md` and `.claude/CLAUDE.md`, not from any existing file:

| File | Role | Data Flow | Why No Analog / Authoritative Source |
|------|------|-----------|--------------------------------------|
| `src/graph/cosmos.ts` | render adapter | event-driven (GPU) | No WebGL/cosmos.gl code exists in repo. Source: RESEARCH §cosmos.gl 3.0.0 Verified API (lines 304-373) — `setPointPositions`/`setLinks`/`create`/`start`/`pause`/`unpause`/`stop`/`fitView`/`onPointMouseOver`, all `[VERIFIED: shipped .d.ts]`. |
| `src/workers/synthetic.worker.ts` | worker / generator | batch | No graph-generator exists (D-01 chose in-browser over a Go seeder). Source: RESEARCH §Synthetic Power-Law Generator (lines 237-250) — Barabási–Albert edge-copying, `m=6`, mulberry32 PRNG, straight into typed arrays. |
| `src/graph/autofreeze.ts` | utility | event-driven | Novel. Source: RESEARCH §Auto-Freeze Threshold (lines 349-353) — sample `getPointPositions()` on a ~10k-node subset, mean-displacement < ~0.5 units/500ms × 3 windows → `pause()`. |
| `src/transport/SyntheticTransport.ts` | service | batch | Novel (wraps the synthetic worker behind `GraphTransport`). Source: RESEARCH §Architecture diagram + Pattern 1. |
| `src/transport/GraphTransport.ts` | interface | — | Pure design, researcher's discretion (D). Source: RESEARCH Pattern 1 (lines 199-212) — `GraphBuffers` / `LoadProgress` / `GraphTransport`. |
| `src/ui/loader.ts` | UI | event-driven | Novel; no UI shell exists. Source: D-09 + RESEARCH §Staged Loader — staged labels + live counter, no % bar. |
| `src/ui/verdict.ts` | utility | transform | Novel instrumentation. Source: D-10 + RESEARCH §Verdict readout (lines 413-423) — fetch/parse/layout ms + peak heap (`measureUserAgentSpecificMemory` w/ COOP+COEP, fallback `performance.memory`). |
| `src/types.ts` | model | — | Novel. SoA typed-buffer shapes. Source: RESEARCH Pattern 1 + §Memory Budgeting (Float32 link buffer, `nodeCount < 2^24` precision guard, Pitfall 1). |
| `src/main.ts` | entry | event-driven | Novel wiring. Source: RESEARCH §Architecture diagram trace (line 173). |
| `vite.config.ts` | config | — | Novel. Source: RESEARCH §Pitfall 4 — COOP (`same-origin`) + COEP (`require-corp`) headers for `measureUserAgentSpecificMemory`; Web Worker support is Vite-native. |

## Metadata

**Analog search scope:** `../web-of-trust/` (pkg/dgraph, pkg/config, cmd/), `../whitelist-plugin/`
(pkg/repository, pkg/config), `deepfry/config/dgraph/schema.graphql`, `deepfry/CLAUDE.md`.
**Files scanned:** ~60 Go files enumerated; 5 read in full (dgraph_repository.go, clusterscan.go,
config.go, schema.graphql, pubkeys/main.go) + targeted reads of dgraph.go (cursor-paging + count).
**Key takeaway:** Phase 1 is ~90% novel TS with no in-repo analog (correctly anticipated by CONTEXT
§"Reusable Assets: None — greenfield"). The only transferable conventions are the Dgraph **query
shape** (`after:<uid>` paging, read-only txn, ID-only `{uid follows{uid}}`, `{data,extensions}`
envelope) and the **read-only / `:8080`-HTTP / `Profile`-schema** contract. Everything render-,
generator-, memory-, and instrument-related is RESEARCH-driven, not analog-driven.
**Pattern extraction date:** 2026-06-23
