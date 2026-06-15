# Deferred items — quick task 260615-1e6

Out-of-scope discoveries logged per the SCOPE BOUNDARY rule. NOT fixed by this task.

## Pre-existing clippy errors (unrelated files)

`cargo clippy --all-targets -- -D warnings` reports 31 errors under clippy 0.1.96, none in
files touched by this task. They live entirely in:

- `src/query/engine.rs` — `manual RangeInclusive::contains`, `>= y + 1` simplification
- `src/query/merge.rs` — `assertions_on_constants` (`assert!(true, ...)`)
- `src/graphql/resolvers.rs` — `unnecessary_min_or_max` on constant clamps (test scaffolding)
- multiple files — `doc list item overindented` (11), `empty line after doc comment` (7),
  `deref which would be done by auto-deref` (2), `manual is_multiple_of`,
  `unnecessary map of identity`, `get(..).is_none()`, `very complex type`, `match` → `if let`

These predate this task (newer clippy than the last lint pass). The files changed by this
task (`src/lmdb/self_check.rs`, `src/main.rs`, `tests/self_check_test.rs`) are clippy-clean:
`cargo clippy` reports zero findings located in any changed file.

## Toolchain note (not a code issue)

A stray `cargo-clippy` / rustdoc 0.1.71 in `/usr/local/bin` shadows the pinned
`stable-x86_64-apple-darwin` (rustc 1.96.0) toolchain on PATH. Build/test/clippy/fmt must be
run with the pinned toolchain's `bin/` prepended to PATH (or via `rustup run`), otherwise the
shadow toolchain rejects the dependency MSRVs and rustdoc fails with the `-Z unstable-options`
/ `check-cfg` error. With the pinned toolchain, the full `cargo test` (incl. doctests) is green.
