/// hydrate.rs — Hydration step (QRY-04): point-look-up + decode for a slice of levIds.
///
/// `hydrate_lev_ids` takes the levIds the merge selected and resolves each to a
/// `(LevId, DecodedEvent)` pair via [`get_event_payload`] + [`decode_event_payload_with_cache`].
///
/// Policy (D-06, D-08, D-11):
/// - Hydration runs AFTER merge selection — only finally-selected levIds are decoded.
/// - Each levId opens and drops its own short read txn via `get_event_payload` (D-08).
/// - A missing levId (structural index error) is propagated as `QueryError::Payload`; it
///   is NOT silently skipped — a levId present in a real index scan must exist in
///   `EventPayload` (only a decode/corrupt failure is skipped, not a lookup miss).
/// - A decode failure increments `skip_count` and emits `tracing::warn!` but does NOT
///   abort the batch — the remaining levIds are still hydrated (D-11). The slot for
///   the failed levId is simply ABSENT from the returned Vec.
/// - Output preserves the caller-supplied levId order exactly for surviving pairs (D-10:
///   ordering is the merge's responsibility).
///
/// ## IMPORTANT: callers MUST join on lev_id, never positionally zip.
///
/// Because a decode failure removes a slot without shifting surrounding slots' lev_ids,
/// the returned `Vec<(LevId, DecodedEvent)>` may be shorter than the input `lev_ids`
/// slice. Callers MUST match results back to their authoritative `(ts, lev_id)` keys
/// from the scan layer by looking up the `LevId` component — not by positional index.
/// Using `.zip()` on the raw return value is a correctness error (CR-05).
use crate::lmdb::payload::{decode_event_payload_with_cache, get_event_payload, DictCache};
use crate::lmdb::types::{DecodedEvent, LevId};
use crate::query::filter::QueryError;

/// Hydrate a slice of levIds into `(LevId, DecodedEvent)` pairs (QRY-04).
///
/// For each `&lev_id` in `lev_ids`:
/// 1. Calls `get_event_payload(env, lev_id)?` — opens a short read txn, copies the raw bytes
///    out, and drops the txn (D-08). A `LevIdNotFound` or LMDB error is returned as
///    `QueryError::Payload(..)` and aborts the batch (structural error — not a skip).
/// 2. Calls `decode_event_payload_with_cache(&raw, dict_cache, env)`.
///    - `Ok(decoded)` → pushes `(lev_id, decoded)` to results.
///    - `Err(e)` → `tracing::warn!(lev_id, reason = %e, ...)`, `*skip_count += 1`, the slot
///      is omitted (D-11: a single corrupt payload never sinks a query; the absent slot
///      cannot misalign surviving entries because callers join on lev_id, not position).
///
/// Returns `(LevId, DecodedEvent)` pairs in input order for surviving entries.
/// Hydrates ONLY the provided levIds (D-06).
///
/// ## IMPORTANT: callers MUST join on lev_id, never positionally zip.
/// The returned Vec may be shorter than `lev_ids` when payloads are skipped.
pub fn hydrate_lev_ids(
    env: &heed::Env,
    lev_ids: &[LevId],
    dict_cache: &DictCache,
    skip_count: &mut usize,
) -> Result<Vec<(LevId, DecodedEvent)>, QueryError> {
    let mut results = Vec::with_capacity(lev_ids.len());
    for &lev_id in lev_ids {
        // get_event_payload opens its own short read txn and drops it before returning (D-08).
        // A missing levId is a structural error — propagate immediately, do NOT skip.
        let raw = get_event_payload(env, lev_id)?;

        // Decode with the dict cache. On failure: warn + count + skip (D-11).
        // The slot is simply absent — callers join on lev_id so absence cannot misalign anything.
        match decode_event_payload_with_cache(&raw, dict_cache, env) {
            Ok(decoded) => results.push((lev_id, decoded)),
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
    use crate::lmdb::payload::EVENT_PAYLOAD_DB_NAME;

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

    /// Open a writable LMDB env over a temp copy of the fixture so a test can inject corrupt
    /// payloads. The caller is responsible for committing the write txn and then reopening
    /// read-only (via `open_fixture_env`) for the actual hydrate call.
    ///
    /// Safety: reads and writes to a temp copy of the fixture that no other process uses.
    fn open_temp_writable_env() -> (heed::Env, tempfile::TempDir) {
        let src = std::path::Path::new("tests/fixture");
        let tmp = tempfile::tempdir().expect("create tempdir for writable env");
        std::fs::copy(src.join("data.mdb"), tmp.path().join("data.mdb"))
            .expect("copy data.mdb for writable env");
        std::fs::copy(src.join("lock.mdb"), tmp.path().join("lock.mdb"))
            .expect("copy lock.mdb for writable env");
        let env = unsafe {
            heed::EnvOpenOptions::new()
                .max_dbs(20)
                .map_size(10_995_116_277_760)
                // No READ_ONLY — writable env for test payload injection.
                // NO_LOCK: safe for isolated temp copy with no concurrent writers.
                .flags(heed::EnvFlags::NO_LOCK)
                .open(tmp.path())
                .expect("open writable fixture env")
        };
        (env, tmp)
    }

    /// Inject a corrupt payload (a single byte `0x02` — unknown EventPayload type tag) at
    /// `corrupt_lev_id` in the EventPayload sub-DB of `env`.
    ///
    /// `0x02` is rejected by `decode_event_payload_with_cache` via `PayloadError::UnknownTypeTag`,
    /// which maps to the D-11 skip path (not a structural lookup miss). This means:
    ///   - `get_event_payload(env, corrupt_lev_id)` succeeds (the key exists),
    ///   - `decode_event_payload_with_cache` returns `Err(UnknownTypeTag)`,
    ///   - `hydrate_lev_ids` increments `skip_count` and omits the slot.
    fn inject_corrupt_payload(env: &heed::Env, corrupt_lev_id: LevId) {
        let mut wtxn = env.write_txn().expect("write_txn for corrupt inject");
        let db: heed::Database<heed::types::Bytes, heed::types::Bytes, heed::IntegerComparator> =
            env.database_options()
                .types::<heed::types::Bytes, heed::types::Bytes>()
                .key_comparator::<heed::IntegerComparator>()
                .name(EVENT_PAYLOAD_DB_NAME)
                .open(&wtxn)
                .expect("open EventPayload db for write")
                .expect("EventPayload sub-DB must exist in fixture");
        let key_bytes = corrupt_lev_id.to_ne_bytes();
        // 0x02 = unknown EventPayload type tag → UnknownTypeTag error → D-11 skip path.
        db.put(&mut wtxn, key_bytes.as_ref(), &[0x02u8])
            .expect("put corrupt payload");
        wtxn.commit().expect("commit corrupt payload write txn");
    }

    /// Test 1: Hydrate three fixture levIds; verify in-order pairs and zero skips.
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

        // Three levIds → three (LevId, DecodedEvent) pairs, no skips.
        assert_eq!(result.len(), 3, "expected 3 decoded pairs");
        assert_eq!(skip_count, 0, "no corrupt payloads — skip_count must stay 0");

        // Verify lev_id associations are correct (pair.0 = lev_id, pair.1 = DecodedEvent).
        assert_eq!(result[0].0, 4, "first pair must carry lev_id=4");
        assert_eq!(result[1].0, 5, "second pair must carry lev_id=5");
        assert_eq!(result[2].0, 6, "third pair must carry lev_id=6");

        // Verify event.id matches the fixture golden vectors (Event__id.json authoritative mapping).
        assert_eq!(
            result[0].1.event.id,
            "ee5e90b9d772f63757ffa6d2ee0a66b77e90d0ffef79e304a70c42c5f0b4f171",
            "levId=4 event id mismatch"
        );
        assert_eq!(
            result[1].1.event.id,
            "4d401c513571d1b439fbce0e8f1e11fb27b3729056473684034ba305451a4939",
            "levId=5 event id mismatch"
        );
        assert_eq!(
            result[2].1.event.id,
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
    /// Pass [6, 4] and verify pairs come back in that exact order (6 then 4).
    #[test]
    fn test_hydrate_preserves_input_order() {
        let (env, _tmp) = open_temp_fixture_env();
        let cache = DictCache::new();
        let mut skip_count = 0usize;

        // Request levIds in reverse order relative to insertion: [6, 4].
        let result = hydrate_lev_ids(&env, &[6, 4], &cache, &mut skip_count)
            .expect("hydrate_lev_ids must succeed for valid fixture levIds");

        assert_eq!(result.len(), 2, "expected 2 decoded pairs");
        assert_eq!(skip_count, 0, "no corrupt payloads — skip_count must stay 0");

        // First pair must carry lev_id=6 (ae9bd395...).
        assert_eq!(result[0].0, 6, "first pair must carry lev_id=6 (input order preserved)");
        assert_eq!(
            result[0].1.event.id,
            "ae9bd395aa8cceaed728df24280f967298d8ef48234a470491397f3dee346118",
            "first result must be levId=6 (input order preserved)"
        );
        // Second pair must carry lev_id=4 (ee5e90b9...).
        assert_eq!(result[1].0, 4, "second pair must carry lev_id=4 (input order preserved)");
        assert_eq!(
            result[1].1.event.id,
            "ee5e90b9d772f63757ffa6d2ee0a66b77e90d0ffef79e304a70c42c5f0b4f171",
            "second result must be levId=4 (input order preserved)"
        );
    }

    /// Test 4 (CR-05 regression): A corrupt payload mid-batch is skipped slot-for-slot;
    /// surviving pairs carry their own correct lev_ids with zero positional shift.
    ///
    /// This test WOULD FAIL under the old `Vec<DecodedEvent>` return type + positional zip:
    /// the zip would pair (ts=X, lev_id=VALID_A) with the decoded event for VALID_B, silently
    /// corrupting the PageCursor and result ordering for every event after the first skip.
    ///
    /// Regression guard: if this function is reverted to returning Vec<DecodedEvent> (dropping
    /// lev_id association), callers cannot detect the misalignment and CR-05 is reintroduced.
    ///
    /// Setup: inject a corrupt payload at levId=100 (unused in fixture, but scannable key).
    /// Call hydrate_lev_ids with [4, 100, 5] — corrupt levId interleaved with valid ones.
    /// Expect: result has 2 pairs with lev_ids [4, 5] in that order; skip_count == 1.
    #[test]
    fn test_hydrate_skips_corrupt_payload_slot_aligned() {
        // Step 1: Open a writable copy of the fixture, inject a corrupt payload, then close.
        let (writable_env, tmp) = open_temp_writable_env();
        inject_corrupt_payload(&writable_env, 100);
        // Drop the writable env before reopening read-only (LMDB: one writer at a time).
        drop(writable_env);

        // Step 2: Reopen the same temp dir read-only (via open_fixture_env which uses READ_ONLY|NO_LOCK).
        let env = open_fixture_env(tmp.path()).expect("reopen fixture env read-only");
        let cache = DictCache::new();
        let mut skip_count = 0usize;

        // levId=100 has a corrupt 0x02 payload; levId=4 and levId=5 are valid fixture events.
        // The corrupt slot is INTERLEAVED (middle position) to verify no positional shift.
        let result = hydrate_lev_ids(&env, &[4, 100, 5], &cache, &mut skip_count)
            .expect("hydrate_lev_ids must succeed even with a corrupt mid-batch payload");

        // skip_count must be exactly 1 (the corrupt levId=100).
        assert_eq!(skip_count, 1, "expected exactly 1 skip for the corrupt payload at levId=100");

        // Result must have exactly 2 pairs (the corrupt slot is absent, not shifted).
        assert_eq!(result.len(), 2, "expected 2 surviving pairs (corrupt slot absent)");

        // Surviving pairs must carry THEIR OWN lev_ids — no positional shift.
        // levId=4 must be first, levId=5 must be second (input order preserved for survivors).
        assert_eq!(
            result[0].0, 4,
            "first surviving pair must carry lev_id=4 (no positional shift from corrupt skip)"
        );
        assert_eq!(
            result[1].0, 5,
            "second surviving pair must carry lev_id=5 (no positional shift from corrupt skip)"
        );

        // Verify the events' ids match the golden vectors (correct events, not a shifted neighbor).
        assert_eq!(
            result[0].1.event.id,
            "ee5e90b9d772f63757ffa6d2ee0a66b77e90d0ffef79e304a70c42c5f0b4f171",
            "result[0].event must be lev_id=4's event (ee5e90b9...)"
        );
        assert_eq!(
            result[1].1.event.id,
            "4d401c513571d1b439fbce0e8f1e11fb27b3729056473684034ba305451a4939",
            "result[1].event must be lev_id=5's event (4d401c51...)"
        );
    }
}
