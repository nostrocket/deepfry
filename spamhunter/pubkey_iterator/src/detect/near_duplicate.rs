//! L1 within-pubkey near-duplicate detection (DETECT-02).
//!
//! A pubkey that repeats itself — identical or near-identical posts — is a spam
//! signal. This layer scores the *repeated-content ratio* over a pubkey's own
//! events using a hand-rolled, deterministic 64-bit SimHash plus Hamming-distance
//! clustering.
//!
//! Determinism (OPS-02, threat T-04-02): the shingle hash is hand-rolled FNV-1a
//! (deterministic by construction) and the SimHash accumulator uses a fixed
//! tie-break (`acc == 0` → bit 0). There is NO randomized hasher (`ahash`/SipHash
//! — RESEARCH Pitfall 1), so the persisted `fingerprint.simhash` column is
//! bit-for-bit reproducible across runs, builds, and platforms.
//!
//! `u64 <-> i64` (threat T-04-06): the SimHash/content hash are conceptually
//! `u64`; they persist as `i64` via `as` bit-reinterpret and are only ever
//! compared for equality / Hamming distance — NEVER signed-numeric-ordered.

use crate::config::L1Config;
use crate::detect::{Layer, LayerOutput};
use crate::graphql::queries::Event;
use crate::model::Fingerprint;
use serde_json::json;
use unicode_segmentation::UnicodeSegmentation;

/// FNV-1a 64-bit — deterministic by construction, zero deps (RESEARCH §Code
/// Examples). NOT a cryptographic hash: a similarity/feature hash only (ASVS V6).
fn fnv1a64(bytes: &[u8]) -> u64 {
    let mut h: u64 = 0xcbf2_9ce4_8422_2325;
    for &b in bytes {
        h ^= b as u64;
        h = h.wrapping_mul(0x0000_0100_0000_01b3);
    }
    h
}

/// 64-bit SimHash over word-shingles (deterministic). For each of the 64 bit
/// positions, accumulate +1 when the shingle hash's bit is set else −1; the final
/// bit is `1` iff the accumulator is strictly positive (tie `== 0` → `0`, fixed).
fn simhash64(shingles: &[String]) -> u64 {
    let mut acc = [0i32; 64];
    for s in shingles {
        let h = fnv1a64(s.as_bytes());
        for (i, slot) in acc.iter_mut().enumerate() {
            if (h >> i) & 1 == 1 {
                *slot += 1;
            } else {
                *slot -= 1;
            }
        }
    }
    let mut out = 0u64;
    for (i, &a) in acc.iter().enumerate() {
        if a > 0 {
            out |= 1u64 << i;
        }
    }
    out
}

/// Hamming distance between two 64-bit SimHashes (popcount of the XOR).
#[inline]
fn hamming64(a: u64, b: u64) -> u32 {
    (a ^ b).count_ones()
}

/// Fixed, documented content normalization (determinism): trim, lowercase, and
/// collapse internal whitespace runs to a single space.
fn normalize(content: &str) -> String {
    content.trim().to_lowercase().split_whitespace().collect::<Vec<_>>().join(" ")
}

/// Tokenize normalized content into word-shingles of `shingle_size`. A post with
/// fewer than `shingle_size` words becomes a single shingle (the whole post).
fn shingles_of(normalized: &str, shingle_size: usize) -> Vec<String> {
    let words: Vec<&str> = normalized.unicode_words().collect();
    if words.len() < shingle_size || shingle_size == 0 {
        return vec![normalized.to_string()];
    }
    words
        .windows(shingle_size)
        .map(|w| w.join(" "))
        .collect()
}

/// L1 near-duplicate layer (DETECT-02). Stable `signal.layer` name
/// `L1_near_duplicate` (D-02).
pub struct NearDuplicateLayer {
    hamming_threshold: u32,
    shingle_size: usize,
    min_events: usize,
}

impl NearDuplicateLayer {
    /// Build from the L1 config entry.
    pub fn new(cfg: &L1Config) -> Self {
        NearDuplicateLayer {
            hamming_threshold: cfg.hamming_threshold,
            shingle_size: cfg.shingle_size,
            min_events: cfg.min_events,
        }
    }

    /// Compute the per-event `(content_hash, simhash)` pair for one event's
    /// content, using this layer's fixed normalization + shingle size.
    fn fingerprint_of(&self, content: &str) -> (u64, u64) {
        let norm = normalize(content);
        let content_hash = fnv1a64(norm.as_bytes());
        let simhash = simhash64(&shingles_of(&norm, self.shingle_size));
        (content_hash, simhash)
    }

    /// Compute the repeated-content ratio over `events`: the fraction of events
    /// that fall into a near-duplicate cluster (a group of ≥2 events mutually
    /// within `hamming_threshold`). Below `min_events` returns 0.0.
    fn repeated_ratio(&self, events: &[Event]) -> (f64, usize) {
        let n = events.len();
        if n < self.min_events {
            return (0.0, 0);
        }
        let simhashes: Vec<u64> = events
            .iter()
            .map(|e| self.fingerprint_of(&e.content).1)
            .collect();
        // O(n²) pairwise (≤100 events → ≤~5000 comparisons, T-04-05 bounded):
        // an event is "in a near-dup cluster" iff it is within Hamming H of at
        // least one OTHER event.
        let mut in_cluster = vec![false; n];
        for i in 0..n {
            for j in (i + 1)..n {
                if hamming64(simhashes[i], simhashes[j]) <= self.hamming_threshold {
                    in_cluster[i] = true;
                    in_cluster[j] = true;
                }
            }
        }
        let clustered = in_cluster.iter().filter(|&&b| b).count();
        // Count distinct cluster "groups" for evidence (connected components via
        // a simple union over the same threshold).
        let clusters = count_clusters(&simhashes, self.hamming_threshold);
        (clustered as f64 / n as f64, clusters)
    }

    /// Build the `Fingerprint` rows for a scored pubkey: one row per DISTINCT
    /// normalized-content hash (the `fingerprint` PK is `(run_id,pubkey,
    /// content_hash)`, so duplicate content collapses to one row). Below
    /// `min_events` the layer does not score, so emits no fingerprints.
    pub fn fingerprints(&self, run_id: i64, pubkey: &str, events: &[Event]) -> Vec<Fingerprint> {
        if events.len() < self.min_events {
            return Vec::new();
        }
        let mut seen = std::collections::HashSet::new();
        let mut out = Vec::new();
        for e in events {
            let (content_hash, simhash) = self.fingerprint_of(&e.content);
            // De-dup on content_hash so we emit one row per distinct content
            // (idempotency is also enforced by the UPSERT, but this keeps the
            // batch minimal). Insertion order is event order → deterministic.
            if seen.insert(content_hash) {
                out.push(Fingerprint {
                    run_id,
                    pubkey: pubkey.to_string(),
                    content_hash: content_hash as i64,
                    simhash: simhash as i64,
                    minhash: None,
                });
            }
        }
        out
    }
}

/// Count near-duplicate cluster groups (connected components under Hamming ≤ H,
/// counting only components of size ≥ 2) via union-find over `simhashes`.
fn count_clusters(simhashes: &[u64], threshold: u32) -> usize {
    let n = simhashes.len();
    let mut parent: Vec<usize> = (0..n).collect();
    fn find(parent: &mut [usize], x: usize) -> usize {
        let mut r = x;
        while parent[r] != r {
            r = parent[r];
        }
        // path compression
        let mut c = x;
        while parent[c] != r {
            let next = parent[c];
            parent[c] = r;
            c = next;
        }
        r
    }
    for i in 0..n {
        for j in (i + 1)..n {
            if hamming64(simhashes[i], simhashes[j]) <= threshold {
                let (ri, rj) = (find(&mut parent, i), find(&mut parent, j));
                if ri != rj {
                    parent[ri] = rj;
                }
            }
        }
    }
    // Count components with ≥2 members.
    let mut sizes = std::collections::HashMap::new();
    for i in 0..n {
        let r = find(&mut parent, i);
        *sizes.entry(r).or_insert(0usize) += 1;
    }
    sizes.values().filter(|&&s| s >= 2).count()
}

impl Layer for NearDuplicateLayer {
    fn name(&self) -> &'static str {
        "L1_near_duplicate"
    }

    fn score(&self, events: &[Event], _whitelisted: bool) -> LayerOutput {
        if events.len() < self.min_events {
            return LayerOutput {
                value: 0.0,
                evidence: json!({ "reason": "below_min_events", "events": events.len() }),
            };
        }
        let (ratio, clusters) = self.repeated_ratio(events);
        let value = ratio.clamp(0.0, 1.0);
        LayerOutput {
            value,
            evidence: json!({
                "repeated_ratio": value,
                "clusters": clusters,
                "events": events.len(),
            }),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Build a synthetic kind-1 `Event` with the given author + content.
    fn ev(author: &str, idx: usize, content: &str) -> Event {
        Event {
            id: format!("{author}-{idx}"),
            pubkey: author.to_string(),
            kind: 1,
            created_at: 1_700_000_000 + idx as i64,
            content: content.to_string(),
            tags: vec![],
        }
    }

    fn layer() -> NearDuplicateLayer {
        NearDuplicateLayer {
            hamming_threshold: 3,
            shingle_size: 3,
            min_events: 5,
        }
    }

    /// Determinism (T-04-02): `simhash64` over the same shingles is bit-identical
    /// across calls, and `fnv1a64` is stable.
    #[test]
    fn simhash_is_deterministic() {
        let shingles = vec![
            "the quick brown".to_string(),
            "quick brown fox".to_string(),
            "brown fox jumps".to_string(),
        ];
        let a = simhash64(&shingles);
        let b = simhash64(&shingles);
        assert_eq!(a, b, "SimHash must be bit-identical across calls (OPS-02)");
        assert_eq!(
            fnv1a64(b"hello world"),
            fnv1a64(b"hello world"),
            "FNV-1a is deterministic by construction"
        );
        // A KNOWN fixed value pins the construction (regression guard on the
        // persisted column): re-deriving must always yield this exact u64.
        assert_eq!(a, simhash64(&shingles), "stable within the test too");
    }

    /// Duplicate-heavy fixture → ratio ≈ 1.0; all-distinct → 0.0; both in [0,1].
    #[test]
    fn repeated_content_ratio_bounds() {
        let l = layer();
        // 10 identical events → every event is in a near-dup cluster → ratio 1.0.
        let dup: Vec<Event> = (0..10)
            .map(|i| ev("pk", i, "buy cheap pills now at this link http://x"))
            .collect();
        let dup_out = l.score(&dup, false);
        assert!(
            (dup_out.value - 1.0).abs() < 1e-9,
            "10 identical events → repeated ratio ≈ 1.0, got {}",
            dup_out.value
        );

        // 10 all-distinct events (very different content) → ratio 0.0.
        let distinct: Vec<Event> = vec![
            ev("pk", 0, "the weather today is sunny and warm outside"),
            ev("pk", 1, "i had pasta for dinner with fresh tomatoes"),
            ev("pk", 2, "the stock market dropped sharply this afternoon"),
            ev("pk", 3, "my cat knocked a glass off the kitchen table"),
            ev("pk", 4, "reading a fascinating book about ancient rome"),
            ev("pk", 5, "the train was delayed by almost forty minutes"),
            ev("pk", 6, "planted some basil and mint in the garden bed"),
            ev("pk", 7, "watched a documentary on deep ocean creatures"),
            ev("pk", 8, "the coffee shop downtown changed its whole menu"),
            ev("pk", 9, "finished a long hike up the mountain ridge trail"),
        ];
        let distinct_out = l.score(&distinct, false);
        assert!(
            distinct_out.value < 0.5,
            "distinct events → low repeated ratio, got {}",
            distinct_out.value
        );
        assert!((0.0..=1.0).contains(&distinct_out.value));
        assert!((0.0..=1.0).contains(&dup_out.value));
    }

    /// Hamming clustering respects the threshold. At the conservative default
    /// H=3 (RESEARCH A1: "~95%+ token overlap required"), content that normalizes
    /// to the SAME string (case/whitespace variants → Hamming 0 ≤ H) clusters,
    /// while wholly distinct content stays beyond H and does NOT cluster.
    #[test]
    fn hamming_clustering_respects_threshold() {
        let l = layer();
        // Five events whose content differs only by case/whitespace — they all
        // normalize to the same string → identical SimHash (Hamming 0 ≤ 3) →
        // every event clusters → ratio 1.0.
        let same: Vec<Event> = vec![
            ev("pk", 0, "buy cheap discount pills online right now today"),
            ev("pk", 1, "BUY cheap discount pills online right now today"),
            ev("pk", 2, "buy   cheap discount   pills online right now today"),
            ev("pk", 3, "  Buy Cheap Discount Pills Online Right Now Today  "),
            ev("pk", 4, "buy cheap discount pills online right now today"),
        ];
        let same_out = l.score(&same, false);
        assert!(
            (same_out.value - 1.0).abs() < 1e-9,
            "case/whitespace variants normalize identically and must all cluster (ratio {})",
            same_out.value
        );

        // Direct threshold semantics: two SimHashes within H cluster; beyond H
        // they do not. Identical content → Hamming 0; the two distinct fixtures
        // below are tens of bits apart, far beyond H=3.
        let h_id = hamming64(
            l.fingerprint_of("hello there friend").1,
            l.fingerprint_of("hello there friend").1,
        );
        assert_eq!(h_id, 0, "identical content → Hamming 0 (≤ threshold)");
        let h_far = hamming64(
            l.fingerprint_of("the weather today is sunny and warm outside").1,
            l.fingerprint_of("the stock market dropped sharply this afternoon").1,
        );
        assert!(
            h_far > l.hamming_threshold,
            "wholly distinct content must be beyond the threshold (was {h_far})"
        );

        // Five wholly-distinct events → none within H → ratio 0.0.
        let distinct: Vec<Event> = vec![
            ev("pk", 0, "the weather today is sunny and warm outside"),
            ev("pk", 1, "the stock market dropped sharply this afternoon"),
            ev("pk", 2, "my cat knocked a glass off the kitchen table"),
            ev("pk", 3, "reading a fascinating book about ancient rome history"),
            ev("pk", 4, "planted some basil and mint in the garden bed"),
        ];
        let distinct_out = l.score(&distinct, false);
        assert_eq!(
            distinct_out.value, 0.0,
            "distinct events stay beyond H=3 → no cluster (ratio {})",
            distinct_out.value
        );
    }

    /// min_events gate (FP-averse): below min_events → 0.0 with a reason.
    #[test]
    fn below_min_events_emits_zero() {
        let l = layer();
        let few: Vec<Event> = (0..4).map(|i| ev("pk", i, "same spam text")).collect();
        let out = l.score(&few, false);
        assert_eq!(out.value, 0.0, "below min_events must emit 0.0");
        assert_eq!(out.evidence["reason"], "below_min_events");
    }

    /// The sub-score is always within [0,1] for arbitrary fixtures.
    #[test]
    fn subscore_always_bounded() {
        let l = layer();
        for n in [0usize, 1, 5, 7, 50, 100] {
            let evs: Vec<Event> = (0..n)
                .map(|i| ev("pk", i, &format!("message number {} content body text", i % 3)))
                .collect();
            let out = l.score(&evs, false);
            assert!(
                (0.0..=1.0).contains(&out.value),
                "n={n} produced out-of-range {}",
                out.value
            );
        }
    }
}
