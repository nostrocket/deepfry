# Requirements: spam-explorer

**Defined:** 2026-06-23
**Core Value:** Score every account by seed-relative valid-follower count so dense spam pods bridged by one weak edge collapse to ~1, while well-connected accounts keep high counts.

## v1 Requirements

Requirements for the initial release. Each maps to a roadmap phase.

### CLI & Configuration

- [ ] **CLI-01**: User can run the tool supplying `--seed <pubkey>`, `--threshold <N>`, `--exclude-shells <k>`, a Dgraph endpoint flag, and `--out <path>` as command-line flags
- [ ] **CLI-02**: Tool validates inputs (seed is a well-formed pubkey, `N > 0`, `k >= 0`, output path writable) and exits with a clear, non-zero error on bad input

### Graph Ingestion

- [ ] **INGEST-01**: Tool connects to the web-of-trust Dgraph using the `dgo/v210` gRPC client
- [ ] **INGEST-02**: Tool reads the follow graph via paginated DQL queries rather than a single query that pulls the entire ~1.54M-node graph at once
- [ ] **INGEST-03**: Tool materializes enough of the `follows` / `~follows` adjacency to BFS-level and score every reachable account

### BFS Leveling

- [ ] **LEVEL-01**: Tool assigns the seed level 0 and BFS-traverses outward along `follows` edges, assigning each reachable account a level equal to its shortest follow-hop distance from the seed
- [ ] **LEVEL-02**: Level assignment is deterministic — first-reached (shallowest) wins, as guaranteed by breadth-first traversal; same seed + same snapshot ⇒ same levels

### Scoring

- [ ] **SCORE-01**: For each scored account `T`, tool counts as `valid_follower_count` the followers `F` (via reverse `~follows`) whose `level(F) < level(T)` (strictly upstream / closer to the seed)
- [ ] **SCORE-02**: Followers at the same level as `T` or deeper are discarded as invalid "follow-backs" and not counted

### Output

- [ ] **OUT-01**: Tool excludes the seed itself and the seed's first `k` shells (levels `1..k`) from scoring output
- [ ] **OUT-02**: Tool emits JSONL — one `{pubkey, valid_follower_count}` object per line — for every scored account with `valid_follower_count < N`
- [ ] **OUT-03**: Output is written to the user-specified path (and the run prints a summary of how many accounts were leveled, scored, and emitted)

### Operations

- [ ] **OPS-01**: Tool detects accounts that exist in the graph but are unreachable from the seed and logs them as errors (the seed network is assumed connected; disconnection is surfaced, not silently skipped)
- [ ] **OPS-02**: Tool logs run progress and a final summary without logging secrets or event content

## v2 Requirements

Deferred to a future release. Tracked but not in the current roadmap.

### Multi-Signal & Reuse

- **MULTI-01**: Intersect this metric with the spike's other spam signals (follow-blast out-degree, mutual-follow rings, burst-creation timing) for multiply-confirmed candidates
- **MULTI-02**: Support multiple seeds per run (min-level across seeds, or per-seed columns)
- **MULTI-03**: Emit a denylist / whitelist-plugin-consumable artifact downstream of the score file

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Reading Nostr event content / payloads | Structural inference only on the ID-only Dgraph graph; canonical events stay in StrFry (data-separation rule) |
| Writing back to Dgraph / StrFry / whitelist | This tool produces an analysis file; enforcement is a separate concern |
| Long-running service / live scoring | Offline, one-shot batch CLI by design |
| Cross-signal spam confirmation | One clean signal here; intersection is a v2 milestone (MULTI-01) |
| Reusing `fc <= 1` as a discriminator | The graph's `follower_count` floor is 1 (~49% of nodes), so it matches half the graph and cannot discriminate |

## Traceability

Which phases cover which requirements. Populated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| CLI-01 | TBD | Pending |
| CLI-02 | TBD | Pending |
| INGEST-01 | TBD | Pending |
| INGEST-02 | TBD | Pending |
| INGEST-03 | TBD | Pending |
| LEVEL-01 | TBD | Pending |
| LEVEL-02 | TBD | Pending |
| SCORE-01 | TBD | Pending |
| SCORE-02 | TBD | Pending |
| OUT-01 | TBD | Pending |
| OUT-02 | TBD | Pending |
| OUT-03 | TBD | Pending |
| OPS-01 | TBD | Pending |
| OPS-02 | TBD | Pending |

**Coverage:**
- v1 requirements: 14 total
- Mapped to phases: 0 (roadmap pending)
- Unmapped: 14 ⚠️

---
*Requirements defined: 2026-06-23*
*Last updated: 2026-06-23 after initial definition*
