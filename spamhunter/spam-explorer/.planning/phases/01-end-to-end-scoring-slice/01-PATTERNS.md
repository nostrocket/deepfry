# Phase 1: End-to-End Scoring Slice - Pattern Map

**Mapped:** 2026-06-24
**Files analyzed:** 13 (all created from scratch — greenfield Go module)
**Analogs found:** 13 / 13 (every file maps to a verified sibling-source analog)

> **Context for the planner:** This is a NEW independent Go module (`spam-explorer`). Nearly every file is created from scratch. RESEARCH.md already transcribed the load-bearing excerpts; this PATTERNS.md confirms them against live sibling source (line numbers re-verified 2026-06-24) and attaches the exact analog + excerpt per file so each plan's action section can point at "copy from X lines A-B."
>
> **Project boundary (CLAUDE.md):** `web-of-trust` and `quarantine-rescuer` are REFERENCE-ONLY. Copy idioms; do NOT modify them. All Phase-1 edits stay inside `spam-explorer/`.

## File Classification

| New File | Role | Data Flow | Closest Analog | Match Quality |
|----------|------|-----------|----------------|---------------|
| `go.mod` | config | — | `../web-of-trust/go.mod` (versions) + `../quarantine-rescuer/go.mod` (single-module shape) | exact (versions verbatim) |
| `Makefile` | config | — | `../quarantine-rescuer/Makefile` | exact (one-shot CLI, same targets) |
| `cmd/spam-explorer/main.go` | controller (entry/orchestrator) | request-response → batch | `../web-of-trust/cmd/clusterscan/main.go` lines 39-99 | role+flow match (resolve-seed-then-loop) |
| `internal/dgraph/client.go` | service (connection) | request-response | `../web-of-trust/pkg/dgraph/dgraph.go` lines 30-60 | exact (dial idiom) |
| `internal/dgraph/resolve.go` | service (query) | request-response | `../web-of-trust/pkg/dgraph/clusterscan.go` lines 43-88 (`ResolvePubkeysToUIDs`) | exact (eq(pubkey,...) query) |
| `internal/dgraph/frontier.go` | service (query) | request-response → streaming-by-level | `clusterscan.go` 196-223 (`DegreesForUIDs` uid-set root) + `dgraph.go` 356-366 (`follows{uid pubkey}` nesting) | role+flow match (composed from two idioms) |
| `internal/bfs/bfs.go` | utility (pure algorithm) | transform | no direct analog — driven by `internal/dgraph.ExpandFrontier`; orchestration shape from `clusterscan/main.go` 83-99 trust-closure loop | role-match (loop structure only) |
| `internal/score/score.go` | utility (pure algorithm) | transform | no graph-query analog (D-02 forbids `~follows`); RESEARCH §Scoring is the spec | RESEARCH-only (pure in-memory) |
| `internal/output/jsonl.go` | utility (I/O) | file-I/O / streaming write | `clusterscan/main.go` writes CSV+JSON (`encoding/json`, `os.Create`); RESEARCH §Output is the spec | role-match (file lifecycle + json) |
| `internal/bfs/bfs_test.go` | test | — | `../web-of-trust/pkg/dgraph/*_test.go` (table-driven Go `testing`) | role-match |
| `internal/score/score_test.go` | test | — | same | role-match |
| `internal/output/jsonl_test.go` | test | — | same (golden-file style) | role-match |
| `internal/dgraph/frontier_test.go` | test | — | web-of-trust package-constant query-shape assertion pattern | role-match |
| `cmd/spam-explorer/main_test.go` | test | — | flag-default assertion (CLI-01) | role-match |

---

## Pattern Assignments

### `go.mod` (config)

**Analog:** `../web-of-trust/go.mod` (lines 1-11 — pinned versions) + `../quarantine-rescuer/go.mod` (single-module-per-subdir shape).

**Pin verbatim** (VERIFIED 2026-06-24 against `web-of-trust/go.mod`):
```
module spam-explorer

go 1.24.1

require (
	github.com/dgraph-io/dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d
	google.golang.org/grpc v1.75.1
)
```
> web-of-trust pins `dgo/v210 v210.0.0-20230328113526-b66f8ae53a2d` and `grpc v1.75.1` at `go 1.24.1`. Match exactly — same Dgraph server (`dgraph/standalone:v25.3.0`), same gRPC protocol. `go mod tidy` will pull the indirect deps (`btcec`, `sonic`, etc. are NOT needed — those came from go-nostr which this tool does not use).

---

### `Makefile` (config)

**Analog:** `../quarantine-rescuer/Makefile` (whole file — it is the one-shot CLI analog).

**Copy targets verbatim, change only `APP`/`PKG`:**
```makefile
APP=spam-explorer
PKG=spam-explorer
VERSION ?= dev
# GIT_COMMIT / BUILD_TIME / LDFLAGS / BUILD_FLAGS blocks: copy as-is (lines 6-20)
.PHONY: all build run test fmt vet tidy clean help build-alpine build-linux lint lint-fix
```
**Key target shapes** (quarantine-rescuer Makefile lines 27-92):
- `build`: `go build $(BUILD_FLAGS) -o bin/$(APP)$(BINARY_EXT) ./cmd/$(APP)`
- `run`: `go run $(BUILD_FLAGS) ./cmd/$(APP) $(ARGS)`
- `test`: `go test ./... -short -cover`
- `fmt`/`vet`/`tidy`/`clean`: `go fmt ./...` / `go vet ./...` / `go mod tidy` / `rm -rf bin`
- `lint`/`lint-fix`: golangci-lint guarded by `command -v` (note: qr's variant `exit 1`s if missing; the stack's documented convention elsewhere is warn-and-continue — planner's call, but matching qr exactly is safe).
- `build-alpine`/`build-linux`: `CGO_ENABLED=0 GOOS=linux ... -tags netgo`.
> The version-injection `LDFLAGS` (`-X 'main.Version=...'`) means `main.go` should declare package-level `var Version, Commit, Built string` to receive them (matches qr).

---

### `cmd/spam-explorer/main.go` (controller, orchestrator)

**Analog:** `../web-of-trust/cmd/clusterscan/main.go` lines 1-99.

**Imports + flag pattern** (clusterscan/main.go 10-53):
```go
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	// spam-explorer/internal/dgraph, /bfs, /score, /output
)

seed := flag.String("seed", "", "trusted seed pubkey (64-char hex) to anchor BFS leveling")
threshold := flag.Int("threshold", 2, "emit accounts with valid_follower_count < N")
excludeShells := flag.Int("exclude-shells", 1, "exclude the seed and its first k shells (levels 1..k)")
dgraphAddr := flag.String("dgraph", "localhost:9080", "Dgraph gRPC endpoint")
maxLevel := flag.Int("max-level", 4, "Phase-1 bounding cap: stop BFS past this level (D-03)")
out := flag.String("out", "spam-candidates.jsonl", "output JSONL path")
flag.Parse()
```
> clusterscan uses `flag.Int`/`flag.String` with config-derived defaults; spam-explorer takes flag literals (no config file in Phase 1, per RESEARCH §Project Constraints). Defaults `threshold=2/exclude-shells=1/max-level=4` are RESEARCH placeholders (Open Question 1) — not calibrated, fine for Phase 1.

**Orchestration skeleton** (clusterscan/main.go 55-99 — the resolve-then-loop spine to mirror):
```go
ctx := context.Background()
client, err := dgraph.NewClient(*dgraphAddr)
if err != nil { log.Fatalf("Failed to create Dgraph client: %v", err) }
defer client.Close()

seedUID, err := client.ResolveSeed(ctx, *seed)
if err != nil { log.Fatalf("...: %v", err) }
// (ResolveSeed already returns an error on empty result — see resolve.go)

// BFS loop = same shape as clusterscan's trust-closure loop (83-99):
//   for level := 0; frontier non-empty && level < maxLevel; level++ { ExpandFrontier(...); accumulate }
// then: score.Score(levels, adjacency); output.Write(...)
log.Printf(...)  // summary line to stderr (count emitted, levels reached) — wording is Claude's discretion
```
> **Exit-code / fatal pattern:** `log.Fatalf` on connect/resolve failure (clusterscan 59, 66) — mirrors the stack. The missing-seed guard lives in `ResolveSeed` (below), so main just propagates.

---

### `internal/dgraph/client.go` (service, connection)

**Analog:** `../web-of-trust/pkg/dgraph/dgraph.go` lines 30-60 (VERIFIED verbatim 2026-06-24).

**Struct + dial + Close** — copy the connection idiom, DROP everything else (no `EnsureSchema`, no `Alter`, no mutations — D-06):
```go
import (
	"github.com/dgraph-io/dgo/v210"
	"github.com/dgraph-io/dgo/v210/protos/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	dg   *dgo.Dgraph
	conn *grpc.ClientConn
}

func NewClient(addr string) (*Client, error) {
	const maxRecvMsgSize = 256 << 20 // 256 MiB — LOAD-BEARING (gRPC default 4MB cap, Pitfall 1)
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxRecvMsgSize)),
	)
	if err != nil { return nil, err }
	return &Client{dg: dgo.NewDgraphClient(api.NewDgraphClient(conn)), conn: conn}, nil
}

func (c *Client) Close() error { return c.conn.Close() }
```
> `grpc.NewClient` (NOT deprecated `grpc.Dial`) — dgraph.go:43. The `256 << 20` is the single most important line to copy; without it frontier queries fail with `ResourceExhausted`.

---

### `internal/dgraph/resolve.go` (service, query)

**Analog:** `../web-of-trust/pkg/dgraph/clusterscan.go` lines 43-88 (`ResolvePubkeysToUIDs`) — single-seed specialization.

**Read-only txn + eq(pubkey) query** (clusterscan.go 57-87 idiom):
```go
func (c *Client) ResolveSeed(ctx context.Context, seed string) (string, error) {
	// %q / strconv.Quote escapes the pubkey into the DQL string (Security: DQL-injection mitigation).
	query := fmt.Sprintf(`{ node(func: eq(pubkey, %q), first: 1) { uid pubkey } }`, seed)
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, query)
	if err != nil { return "", fmt.Errorf("resolve seed failed: %w", err) }
	var result struct {
		Node []struct {
			UID    string `json:"uid"`
			Pubkey string `json:"pubkey"`
		} `json:"node"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return "", fmt.Errorf("unmarshal seed failed: %w", err)
	}
	if len(result.Node) == 0 {
		return "", fmt.Errorf("seed pubkey %q not found in graph", seed) // missing-seed guard (Pitfall 4)
	}
	return result.Node[0].UID, nil
}
```
> **Verified idioms transcribed:** `NewReadOnlyTxn()` + `defer txn.Discard(ctx)` (clusterscan.go 65-66); `eq(pubkey, ...)` with `strconv.Quote`/`%q` quoting (clusterscan.go 53-63 uses `strconv.Quote` over a slice; the single-seed `%q` is the same escape). The `len(result.Node) == 0` guard mirrors `clusterscan/main.go:68` (`if len(seedUIDs) == 0 { log.Fatalf(...) }`) — here returned as an error so main owns the exit.

---

### `internal/dgraph/frontier.go` (service, query — one BFS level per round-trip, D-01)

**Analog:** composed from two verified idioms —
- `../web-of-trust/pkg/dgraph/clusterscan.go` lines 196-223 (`DegreesForUIDs`): `func: uid(%s)` root over a comma-joined UID set + the `uidList` helper (clusterscan.go 39-41 — `strings.Join(uids, ",")`).
- `../web-of-trust/pkg/dgraph/dgraph.go` lines 356-366: the nested `follows { uid pubkey }` selection shape.

**Query shape** (RESEARCH §Frontier, primitives verified above):
```go
type FrontierResult struct {
	UID     string `json:"uid"`
	Pubkey  string `json:"pubkey"`
	Follows []struct {
		UID    string `json:"uid"`
		Pubkey string `json:"pubkey"`
	} `json:"follows"`
}

func (c *Client) ExpandFrontier(ctx context.Context, uids []string) ([]FrontierResult, error) {
	query := fmt.Sprintf(`
	{
		frontier(func: uid(%s)) {
			uid
			pubkey
			follows { uid pubkey }
		}
	}`, strings.Join(uids, ", "))
	txn := c.dg.NewReadOnlyTxn()
	defer txn.Discard(ctx)
	resp, err := txn.Query(ctx, query)
	if err != nil { return nil, fmt.Errorf("expand frontier failed: %w", err) }
	var result struct {
		Frontier []FrontierResult `json:"frontier"`
	}
	if err := json.Unmarshal(resp.Json, &result); err != nil {
		return nil, fmt.Errorf("unmarshal frontier failed: %w", err)
	}
	return result.Frontier, nil
}
```
> **Phase 2 seam:** pagination drops INSIDE this one function (split `uids` into batches); `bfs.go` never changes. This is the explicit reason D-01 chose frontier-expansion over `@recurse`. **Both `uid` AND `pubkey` in every block** (Pitfall 2 — BFS keys on UID, output emits pubkey).

---

### `internal/bfs/bfs.go` (utility, pure transform — LEVEL-01)

**Analog:** no direct query analog (this is pure, no I/O). The driving loop structure mirrors `../web-of-trust/cmd/clusterscan/main.go` lines 83-99 (the trust-closure `for round := ...` loop: call expander, accumulate new nodes, stop when none added). RESEARCH §Architecture Pattern 2 is the spec.

**Pattern to implement** (pure FIFO BFS over driver-fed frontiers):
```go
// Level drives ExpandFrontier level-by-level; returns levels{uid->int} + adjacency{F-uid->[]T-uid}
// + pubkeys{uid->pubkey}. Seed = level 0. A node enters `levels` exactly once at first
// discovery (shallowest wins — BFS invariant, LEVEL-01). Only followees ABSENT from `levels`
// join the next frontier (visited-set prevents re-leveling + cycle non-termination, Pitfall 3).
// Loop terminates when next frontier is empty OR level+1 > maxLevel (D-03 cap).
```
> Keep this package I/O-free EXCEPT it calls an injected expander (pass `client.ExpandFrontier` as a func value, or an interface, so `bfs_test.go` can feed a fake frontier without a live Dgraph). RESEARCH §Architectural Responsibility Map explicitly wants this seam.

---

### `internal/score/score.go` (utility, pure transform — SCORE-01/SCORE-02, D-02)

**Analog:** NONE in the codebase — D-02 forbids `~follows` queries, so there is no graph-query analog. RESEARCH §Scoring is the authoritative spec (the in-memory inversion). This is the load-bearing design insight of the phase.

**Pattern to implement** (pure, no Dgraph access — copy verbatim from RESEARCH §Scoring):
```go
// levels: uid -> BFS level (0 = seed). adjacency: F-uid -> []T-uid (materialized follows edges).
// Returns: uid -> valid_follower_count.
func Score(levels map[string]int, adjacency map[string][]string) map[string]int {
	vfc := make(map[string]int, len(levels))
	for follower, followees := range adjacency {
		lf, ok := levels[follower]
		if !ok { continue }
		for _, target := range followees {
			lt, ok := levels[target]
			if !ok { continue } // target beyond cap; never scored (D-04)
			if lf < lt { // strictly upstream — SCORE-01; same/deeper discarded — SCORE-02
				vfc[target]++
			}
		}
	}
	return vfc
}
```
> **Preserve the D-02/D-04 correctness proof** (RESEARCH §Don't Hand-Roll) in a doc comment so a future editor does not "fix" this by adding `~follows`: every valid follower of T (level < level(T)) was expanded before T's level completed, so its F→T edge is already in `adjacency`. The `--max-level M` cap only drops level-(M+1) discoveries, which are never scored. **Strict `<` only** (SCORE-02).

---

### `internal/output/jsonl.go` (utility, file-I/O — OUT-01/OUT-02)

**Analog:** `../web-of-trust/cmd/clusterscan/main.go` (uses `encoding/json` + `os.Create` for its report files — same std-lib file-write lineage). RESEARCH §Output is the spec.

**Pattern** (copy verbatim from RESEARCH §Output):
```go
type Record struct {
	Pubkey             string `json:"pubkey"`
	ValidFollowerCount int    `json:"valid_follower_count"`
}

func Write(path string, scored, levels map[string]int, pubkeys map[string]string, threshold, k int) (emitted int, err error) {
	f, err := os.Create(path)
	if err != nil { return 0, fmt.Errorf("create output: %w", err) }
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()
	enc := json.NewEncoder(w) // Encode appends "\n" per object == JSONL
	for uid, vfc := range scored {
		if levels[uid] <= k { continue }        // OUT-01: exclude seed (lvl 0) + shells 1..k
		if vfc >= threshold { continue }          // OUT-02: emit only vfc < N
		if err := enc.Encode(Record{Pubkey: pubkeys[uid], ValidFollowerCount: vfc}); err != nil {
			return emitted, fmt.Errorf("encode record: %w", err)
		}
		emitted++
	}
	return emitted, nil
}
```
> **Output ordering** (Claude's discretion / Open Question 2): map iteration is non-deterministic. RESEARCH recommends sorting emitted records by pubkey before writing — makes `jsonl_test.go` a trivial golden-file comparison and pre-positions Phase-2's determinism goal. Planner should adopt the sort.

---

### Test files (`*_test.go`)

**Analog:** `../web-of-trust/pkg/dgraph/*_test.go` (Go standard `testing`, table-driven). No framework install — `testing` is built in.

| Test file | Covers | Approach |
|-----------|--------|----------|
| `internal/bfs/bfs_test.go` | LEVEL-01 (incl. cycle termination + `--max-level` cap) | Inject a fake expander returning hand-built frontiers; assert `levels`/`adjacency`. |
| `internal/score/score_test.go` | SCORE-01, SCORE-02 | Table-driven on small hand-built `levels`+`adjacency` maps; assert strict-`<` and same/deeper exclusion. |
| `internal/output/jsonl_test.go` | OUT-01, OUT-02 | Write to a temp path, read back, compare to golden JSONL (sorted output makes this byte-stable). |
| `internal/dgraph/frontier_test.go` | INGEST-03 query shape | Assert the `fmt.Sprintf` query string shape WITHOUT a live DB — mirror web-of-trust's package-constant-query assertion pattern. |
| `cmd/spam-explorer/main_test.go` | CLI-01 | Assert flag defaults applied (`threshold=2`, `exclude-shells=1`, `max-level=4`, `dgraph=localhost:9080`). |

> `internal/dgraph/client.go` constructor (`TestNewClient`) is unit-testable (dial + Close, no live server needed for construction). The end-to-end smoke run (`go run ./cmd/spam-explorer --seed <known> --max-level 2 --out /tmp/x.jsonl`) is a MANUAL gate against live Dgraph — confirms RESEARCH Assumption A1.

---

## Shared Patterns

### Read-only Dgraph transaction
**Source:** every query method in `../web-of-trust/pkg/dgraph/clusterscan.go` (e.g. lines 65-68, 113-116, 208-211).
**Apply to:** `internal/dgraph/resolve.go`, `internal/dgraph/frontier.go` — and NOTHING else (only these two query).
```go
txn := c.dg.NewReadOnlyTxn()
defer txn.Discard(ctx)
resp, err := txn.Query(ctx, query)
```
> Read-only txn + `defer Discard` is the universal read pattern. No `txn.Mutate`, no `txn.Commit`, no `Alter` anywhere in this tool (read-only posture — D-06, Security V4).

### Error wrapping
**Source:** clusterscan.go throughout (`fmt.Errorf("...failed: %w", err)`).
**Apply to:** all `internal/dgraph` query methods, `internal/output`.
> Wrap with `%w`; package functions return errors; only `cmd/spam-explorer/main.go` calls `log.Fatalf` (clusterscan/main.go 59,66,69 idiom).

### DQL-injection safety (Security — Tampering)
**Source:** clusterscan.go:53-55 (`strconv.Quote`) and dgraph.go:358 (`%q`).
**Apply to:** `internal/dgraph/resolve.go` (the `--seed` interpolation).
> Interpolate the pubkey with `%q` / `strconv.Quote`, NEVER raw `%s`. Full hex-format validation is Phase 3 (CLI-02); Phase 1 just quotes.

### UID-vs-pubkey discipline
**Source:** Pitfall 2 (RESEARCH); enforced by the `follows { uid pubkey }` dual-field selection (dgraph.go 356-366).
**Apply to:** `internal/dgraph/frontier.go`, `internal/bfs/bfs.go`, `internal/output/jsonl.go`.
> Frontier/`levels`/`adjacency` maps key on **UID**. Every selection fetches BOTH `uid` and `pubkey` to build the `uid→pubkey` map. JSONL emits **pubkey**, never UID.

### Makefile common targets + version injection
**Source:** `../quarantine-rescuer/Makefile` (whole file).
**Apply to:** `Makefile`.
> `build/run/test/fmt/vet/tidy/clean/lint/lint-fix` + `build-alpine`/`build-linux`; `-ldflags -X 'main.Version=...'` requires `var Version, Commit, Built string` in `main.go`.

## No Analog Found

Files whose CORE logic has no codebase analog (planner uses RESEARCH.md spec, which is verbatim-ready):

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/score/score.go` | utility | transform | D-02 forbids `~follows`; the in-memory inversion is a NEW pattern (its whole point is to avoid the query the codebase would otherwise use). RESEARCH §Scoring + the D-02/D-04 proof are authoritative. |
| `internal/bfs/bfs.go` | utility | transform | No pure-BFS analog exists; web-of-trust does its leveling inside DQL (`@recurse`/trust-closure). Only the *driving-loop shape* (clusterscan/main.go 83-99) transfers; the FIFO + visited-set + cap logic is per RESEARCH Pattern 2. |

> Both are PURE functions — fully unit-testable offline, no live Dgraph needed. This is by design (RESEARCH §Architectural Responsibility Map): I/O isolated in `internal/dgraph`, algorithm in pure packages.

## Metadata

**Analog search scope:** `../web-of-trust/pkg/dgraph/`, `../web-of-trust/cmd/clusterscan/`, `../quarantine-rescuer/` (cmd + internal + Makefile + go.mod).
**Files scanned (read in full or targeted):** `web-of-trust/pkg/dgraph/dgraph.go` (1-70, 356-366), `web-of-trust/pkg/dgraph/clusterscan.go` (1-285, full), `web-of-trust/cmd/clusterscan/main.go` (1-100), `web-of-trust/go.mod`, `quarantine-rescuer/Makefile` (full), `quarantine-rescuer/go.mod`, qr cmd/internal layout.
**Line numbers re-verified against live source:** 2026-06-24 (dial idiom dgraph.go:37-60 ✓, ResolvePubkeysToUIDs clusterscan.go:43-88 ✓, follows-nesting dgraph.go:356-366 ✓, clusterscan flag/orchestration main.go:39-99 ✓).
**Pattern extraction date:** 2026-06-24
