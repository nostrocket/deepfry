/// hydrate.rs — Hydration step (QRY-04): point-look-up + decode for a slice of levIds.
///
/// `hydrate_lev_ids` takes the levIds the merge selected and resolves each to a
/// [`DecodedEvent`] via [`get_event_payload`] + [`decode_event_payload_with_cache`].
///
/// Policy (D-06, D-08, D-11):
/// - Hydration runs AFTER merge selection — only finally-selected levIds are decoded.
/// - Each levId opens and drops its own short read txn via `get_event_payload` (D-08).
/// - A missing levId (structural index error) is propagated as `QueryError::Payload`; it
///   is NOT silently skipped — a levId present in a real index scan must exist in
///   `EventPayload` (only a decode/corrupt failure is skipped, not a lookup miss).
/// - A decode failure increments `skip_count` and emits `tracing::warn!` but does NOT
///   abort the batch — the remaining levIds are still hydrated (D-11).
/// - Output preserves the caller-supplied levId order exactly (D-10: ordering is the
///   merge's responsibility).
use crate::lmdb::payload::{decode_event_payload_with_cache, get_event_payload, DictCache};
use crate::lmdb::types::{DecodedEvent, LevId};
use crate::query::filter::QueryError;

/// Hydrate a slice of levIds into [`DecodedEvent`]s (QRY-04).
///
/// For each `&lev_id` in `lev_ids`:
/// 1. Calls `get_event_payload(env, lev_id)?` — opens a short read txn, copies the raw bytes
///    out, and drops the txn (D-08). A `LevIdNotFound` or LMDB error is returned as
///    `QueryError::Payload(..)` and aborts the batch (structural error — not a skip).
/// 2. Calls `decode_event_payload_with_cache(&raw, dict_cache, env)`.
///    - `Ok(decoded)` → pushed to results.
///    - `Err(e)` → `tracing::warn!(lev_id, reason = %e, ...)`, `*skip_count += 1`, continue
///      (D-11: a single corrupt payload never sinks a query).
///
/// Returns `DecodedEvent`s in input order. Hydrates ONLY the provided levIds (D-06).
pub fn hydrate_lev_ids(
    env: &heed::Env,
    lev_ids: &[LevId],
    dict_cache: &DictCache,
    skip_count: &mut usize,
) -> Result<Vec<DecodedEvent>, QueryError> {
    let mut results = Vec::with_capacity(lev_ids.len());
    for &lev_id in lev_ids {
        // get_event_payload opens its own short read txn and drops it before returning (D-08).
        // A missing levId is a structural error — propagate immediately, do NOT skip.
        let raw = get_event_payload(env, lev_id)?;

        // Decode with the dict cache. On failure: warn + count + skip (D-11).
        match decode_event_payload_with_cache(&raw, dict_cache, env) {
            Ok(decoded) => results.push(decoded),
            Err(e) => {
                tracing::warn!(
                    lev_id,
                    reason = %e,
                    "skipping undecodable EventPayload in hydrate batch"
                );
                *skip_count += 1;
            }
        }
    }
    Ok(results)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lmdb::env::open_fixture_env;

    /// Open a temporary copy of the committed fixture env.
    /// Each test gets its own copy so tests can run in parallel (LMDB cannot share a path).
    fn open_temp_fixture_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb")).expect("copy data.mdb");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb")).expect("copy lock.mdb");
        let env = open_fixture_env(tmp.path()).expect("open fixture env copy");
        (env, tmp)
    }

    /// Test 1: Hydrate three fixture levIds; verify in-order results and zero skips.
    ///
    /// Golden-vector mapping (from tests/fixture/golden_vectors/Event__id.json — the levId
    /// field in event_ids_in_order is the authoritative levId → event-id map):
    ///   levId=4 → id=ee5e90b9d772f63757ffa6d2ee0a66b77e90d0ffef79e304a70c42c5f0b4f171 (kind=1, pk2)
    ///   levId=5 → id=4d401c513571d1b439fbce0e8f1e11fb27b3729056473684034ba305451a4939 (kind=1, pk1)
    ///   levId=6 → id=ae9bd395aa8cceaed728df24280f967298d8ef48234a470491397f3dee346118 (kind=1, pk1)
    #[test]
    fn test_hydrate_three_lev_ids_in_order_zero_skips() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        let mut skip_count = 0usize;

        let result = hydrate_lev_ids(&env, &[4, 5, 6], &cache, &mut skip_count)
            .expect("hydrate_lev_ids must succeed for valid fixture levIds");

        // Three levIds → three DecodedEvents, no skips.
        assert_eq!(result.len(), 3, "expected 3 decoded events");
        assert_eq!(skip_count, 0, "no corrupt payloads — skip_count must stay 0");

        // Verify event.id matches the fixture golden vectors (Event__id.json authoritative mapping).
        assert_eq!(
            result[0].event.id,
            "ee5e90b9d772f63757ffa6d2ee0a66b77e90d0ffef79e304a70c42c5f0b4f171",
            "levId=4 event id mismatch"
        );
        assert_eq!(
            result[1].event.id,
            "4d401c513571d1b439fbce0e8f1e11fb27b3729056473684034ba305451a4939",
            "levId=5 event id mismatch"
        );
        assert_eq!(
            result[2].event.id,
            "ae9bd395aa8cceaed728df24280f967298d8ef48234a470491397f3dee346118",
            "levId=6 event id mismatch"
        );
    }

    /// Test 2: A levId that does not exist in the fixture propagates as QueryError::Payload.
    ///
    /// A missing levId from a real index scan is a structural error (the index and EventPayload
    /// are out of sync) — it must NOT be silently skipped. The function must return Err.
    #[test]
    fn test_nonexistent_lev_id_propagates_as_payload_error() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        let mut skip_count = 0usize;

        // levId=9999 does not exist in the fixture EventPayload DB.
        let result = hydrate_lev_ids(&env, &[9999], &cache, &mut skip_count);

        assert!(
            result.is_err(),
            "missing levId must propagate as Err, not silently skip"
        );
        match result.unwrap_err() {
            QueryError::Payload(_) => { /* expected path */ }
            other => panic!("expected QueryError::Payload, got {:?}", other),
        }
        // A missing levId is NOT a skip — skip_count must remain 0.
        assert_eq!(skip_count, 0, "missing levId must not increment skip_count");
    }

    /// Test 3: Output preserves the caller-supplied levId order (no reordering).
    ///
    /// hydrate_lev_ids is order-agnostic — ordering is the merge's responsibility (D-10).
    /// Pass [6, 4] and verify events come back in that exact order (6 then 4).
    #[test]
    fn test_hydrate_preserves_input_order() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        let mut skip_count = 0usize;

        // Request levIds in reverse order relative to insertion: [6, 4].
        let result = hydrate_lev_ids(&env, &[6, 4], &cache, &mut skip_count)
            .expect("hydrate_lev_ids must succeed for valid fixture levIds");

        assert_eq!(result.len(), 2, "expected 2 decoded events");
        assert_eq!(skip_count, 0, "no corrupt payloads — skip_count must stay 0");

        // First result must be levId=6 (ae9bd395...).
        assert_eq!(
            result[0].event.id,
            "ae9bd395aa8cceaed728df24280f967298d8ef48234a470491397f3dee346118",
            "first result must be levId=6 (input order preserved)"
        );
        // Second result must be levId=4 (ee5e90b9...).
        assert_eq!(
            result[1].event.id,
            "ee5e90b9d772f63757ffa6d2ee0a66b77e90d0ffef79e304a70c42c5f0b4f171",
            "second result must be levId=4 (input order preserved)"
        );
    }
}
