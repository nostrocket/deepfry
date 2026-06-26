# pubkey_iterator

Per-pubkey Nostr spam classifier. Enumerates every pubkey in the corpus via the
LMDB2GraphQL adapter, scores each one through weighted detection layers fused by
a logistic combiner, and emits a reviewable suspected-spammer list with per-layer
reasons. Weights are correctable from human labels under a no-regression backtest.

Output is a **reviewable list, never enforcement**. Verdicts are pubkey-level.

## Setup

```bash
cargo build --release
cp pubkey_iterator_config.example.toml ~/deepfry/pubkey_iterator_config.toml
# edit adapter_url / whitelist_url / weights to taste
```

Config defaults to `~/deepfry/pubkey_iterator_config.toml`; the SQLite store
defaults to `./spamhunter.sqlite`. Override either with `--config` / `--db`.
All layer weights and thresholds are tunable without recompiling.

## Usage

```bash
# Score a full batch: enumerate → fetch → score → persist
pubkey_iterator                       # `run` is the default — bare invocation runs a batch
pubkey_iterator run                   # explicit form, identical to the above
pubkey_iterator run --resume          # continue the latest unfinished run
pubkey_iterator run --limit 1000      # stop after ~1000 pubkeys (bounded test run)

# Materialize the suspected-spammer snapshot (prints pubkey + reasons)
pubkey_iterator export                # latest completed run
pubkey_iterator export --run-id 42

# Re-fit weights from human labels; adopt ONLY if the backtest passes
pubkey_iterator tune
```

`--limit` caps the enumeration walk so you can drive the full
enumerate→fetch→score→persist flow over a bounded subset instead of the entire
(millions-of-pubkeys) keyspace — useful for end-to-end testing. The count is
across pages pre-dedup (page size 500), so the walk stops at the first page
boundary ≥ the limit; the cursor is preserved, so `--resume` continues from there.

`export` prints each suspected pubkey joined to its per-layer `signal` rows so a
reviewer can see *why* it fired.

## Correcting false positives

There is no `label` subcommand by design. Record ground truth by inserting
directly into the `backpropagation` table with any SQLite client:

```sql
INSERT INTO backpropagation (pubkey, is_spam, source, note)
VALUES ('<hex-pubkey>', 0, 'review', 'confirmed not spam');
```

Then run `pubkey_iterator tune`. It fits a logistic model over the labeled
signals and adopts the new weights **only** if the backtest finds zero new false
negatives and zero new false positives; otherwise the live weights are untouched.

## Detection layers

| Layer | Signal |
|-------|--------|
| L0 | whitelist absence (absence is a weak spam signal; presence clears only this layer) |
| L1 | within-pubkey near-duplicate content (SimHash + Hamming) |
| L3 | content entropy (templated / gibberish) + emoji/hashtag density |
| L4 | link & mention ratios (URLs, repeated domains, mass `p`-tags) |

Each layer is independently enable/disable-able in the config.

## Test

```bash
cargo test
```
