# Deferred Items — Phase 02

Out-of-scope discoveries logged during CR-01 fix (not fixed; pre-existing / unrelated).

## Pre-existing, unrelated to CR-01

- **`cargo clippy --all-targets` fails on `build.rs`** with
  `error: the -Z unstable-options flag must also be passed to enable the flag check-cfg`.
  This is a toolchain/build-script issue that fails before reaching library code. It is
  unrelated to the scan.rs change and pre-dates this work. The CR-mandated verification
  command `cargo test --all-targets` passes cleanly. Investigate the build.rs `check-cfg`
  emission / toolchain pin separately.

- **`tests/scan_test.rs` unused-function warning**: `kind_reverse_high_key` is defined but
  never used in that integration test file (confirmed present at HEAD, pre-existing). Harmless
  warning; left untouched per scope boundary. Resolve by either using it in a reverse
  integration test or removing the dead helper.
