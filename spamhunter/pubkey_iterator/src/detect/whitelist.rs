//! L0 whitelist-absence detection (DETECT-01).
//!
//! Two units:
//!
//! - [`WhitelistAbsenceLayer`] — the CPU-only [`crate::detect::Layer`]. It does
//!   NO HTTP; membership is PRE-RESOLVED in the tokio fetch stage and handed in
//!   as the `whitelisted: bool` arg (Pitfall 5: never block the consumer on the
//!   network). Absence (`whitelisted == false`) emits `absence_subscore`
//!   (default 1.0, a small WEIGHT in the combiner so absence alone never flags);
//!   presence (`whitelisted == true`) emits 0.0 — it clears ONLY this layer, it
//!   is NEVER a gate or exemption (D-03). A whitelisted pubkey still flows
//!   through every other layer.
//!
//! - [`WhitelistClient`] — the async membership resolver used by the fetch
//!   stage. It reuses ONE [`reqwest::Client`] (connection pooling) and issues
//!   `GET {base}/check/{pubkey}` → `{"whitelisted":bool}` (the code-verified
//!   whitelist-plugin API — there is NO batch endpoint, so one GET per pubkey).
//!   Results are cached in a per-run no-TTL `HashMap` (a TTL would make a long
//!   run time-dependent and threaten OPS-02 determinism — RESEARCH §L0 / A7).
//!
//! Fail-toward-not-flagging (T-04-07 / Pitfall 2): on ANY transport/decode error
//! [`WhitelistClient::is_whitelisted`] returns `true` for L0 purposes (sub-score
//! 0.0) rather than mapping an outage to "absent". Treating an unreachable
//! whitelist as "absent" would emit the absence sub-score for EVERY pubkey during
//! an outage — mass false positives. We bias toward NOT flagging (FP-averse, D-08)
//! and the caller records the outage in evidence for a deferred manual re-check.

use crate::config::L0Config;
use crate::detect::{Layer, LayerOutput};
use crate::graphql::queries::Event;
use serde::Deserialize;
use serde_json::json;
use std::collections::HashMap;
use std::sync::Mutex;

/// The whitelist `/check/{pubkey}` 200 body: a single boolean field
/// (`type checkResponse struct { Whitelisted bool }`, server.go:102).
#[derive(Deserialize)]
struct CheckResponse {
    whitelisted: bool,
}

/// The whitelist `POST /check` bulk 200 body: a `pubkey → bool` map
/// (`{"results":{"<pubkey>":true,...}}`, server.go bulk handler). A pubkey the
/// server considers empty is OMITTED from the map (README §"Bulk check").
#[derive(Deserialize)]
struct BulkCheckResponse {
    results: HashMap<String, bool>,
}

/// The async whitelist membership resolver (L0). Reuses one pooled
/// [`reqwest::Client`]; caches every resolved `(pubkey → bool)` for the run with
/// NO TTL (determinism, OPS-02). Lives in the tokio fetch stage so the CPU
/// consumer never blocks on the network (Pitfall 5).
pub struct WhitelistClient {
    http: reqwest::Client,
    /// The whitelist-plugin base URL (e.g. `http://127.0.0.1:8081`), no trailing
    /// slash. Operator-supplied config, not user input (T-04-09 accept).
    base: String,
    /// Per-run resolved-membership cache. No TTL — a long run must resolve a
    /// pubkey to the SAME value every time it is looked up (OPS-02 / A7).
    cache: Mutex<HashMap<String, bool>>,
}

impl WhitelistClient {
    /// Build a client targeting `base` (the whitelist-plugin base URL) with a
    /// fresh pooled [`reqwest::Client`]. A trailing `/` on `base` is trimmed so
    /// `{base}/check/{pubkey}` never doubles the separator.
    pub fn new(base: impl Into<String>) -> Self {
        let base = base.into();
        let base = base.trim_end_matches('/').to_string();
        WhitelistClient {
            // Bounded timeouts: a slow or black-holed whitelist response must NOT
            // hang the (serial) fetch stage indefinitely. reqwest's default has no
            // request timeout, so without this one stalled connection freezes the
            // whole pipeline. On timeout the call fails toward not-flagging (true).
            http: reqwest::Client::builder()
                .connect_timeout(std::time::Duration::from_secs(3))
                .timeout(std::time::Duration::from_secs(10))
                .build()
                .expect("build whitelist reqwest client"),
            base,
            cache: Mutex::new(HashMap::new()),
        }
    }

    /// Resolve whitelist membership for `pubkey` (already a 64-hex from the
    /// enumerate stage). Returns the cached value on a hit (no second HTTP
    /// round-trip); on a miss issues `GET {base}/check/{pubkey}` and caches the
    /// parsed `whitelisted` bool.
    ///
    /// Fail-toward-not-flagging (Pitfall 2): on ANY transport/non-200/decode
    /// error returns `true` (→ L0 sub-score 0.0) — NEVER maps an outage to
    /// "absent". The caller distinguishes a real `false` from a fail-safe `true`
    /// only via this method's contract; for L0 the conservative effect is the
    /// same (do not emit the absence sub-score). The fail-safe value is NOT
    /// cached, so a transient outage does not poison the run.
    pub async fn is_whitelisted(&self, pubkey: &str) -> bool {
        // Cache first (no TTL): a pubkey resolves to the same value all run.
        if let Some(&hit) = self.cache.lock().expect("whitelist cache mutex").get(pubkey) {
            return hit;
        }
        // The pubkey is already validated 64-hex from enumerate; place it as a
        // single trimmed path segment (defensive — never inject a slashed value).
        let safe_pubkey = pubkey.trim_matches('/');
        let url = format!("{}/check/{}", self.base, safe_pubkey);
        let resolved = match self.http.get(&url).send().await {
            Ok(resp) if resp.status().is_success() => {
                // Decode {"whitelisted":bool}. A decode failure fails toward
                // not-flagging (true) and is NOT cached.
                match resp.json::<CheckResponse>().await {
                    Ok(body) => Some(body.whitelisted),
                    Err(_) => None, // decode error → fail-safe, do not cache
                }
            }
            // Non-success status (e.g. 503 while loading) or transport error →
            // fail toward not-flagging; do not cache the fail-safe value.
            Ok(_) => None,
            Err(_) => None,
        };
        match resolved {
            Some(value) => {
                self.cache
                    .lock()
                    .expect("whitelist cache mutex")
                    .insert(pubkey.to_string(), value);
                value
            }
            // Fail-safe: treat as whitelisted (clears L0) WITHOUT caching, so a
            // later retry can still resolve the real value (Pitfall 2 / D-14).
            None => true,
        }
    }

    /// Resolve whitelist membership for a whole batch in ONE round-trip via the
    /// whitelist-plugin `POST {base}/check` bulk endpoint
    /// (`{"pubkeys":[...]}` → `{"results":{"<pubkey>":bool}}`). This replaces up
    /// to `batch.len()` serial `GET /check/{pubkey}` round-trips with a single
    /// request — the dominant cost (and a serial unbounded-hang point) in the
    /// fetch stage before the bulk endpoint existed.
    ///
    /// Cache-first per pubkey (no TTL, OPS-02): already-resolved keys are served
    /// without a network hit and only the misses are POSTed. Newly resolved
    /// values are cached.
    ///
    /// Fail-toward-not-flagging (Pitfall 2): on ANY transport/non-200/decode
    /// error EVERY requested-but-unresolved pubkey resolves to `true` (→ L0
    /// sub-score 0.0) and nothing is cached, so a transient outage neither flags
    /// everyone nor poisons the run. A pubkey the server OMITS from `results`
    /// (e.g. an empty string) also fails safe to `true`. The returned map always
    /// contains an entry for every pubkey in `pubkeys`.
    pub async fn is_whitelisted_bulk(&self, pubkeys: &[String]) -> HashMap<String, bool> {
        let mut out: HashMap<String, bool> = HashMap::with_capacity(pubkeys.len());
        let mut misses: Vec<String> = Vec::new();
        {
            let cache = self.cache.lock().expect("whitelist cache mutex");
            for pk in pubkeys {
                match cache.get(pk) {
                    Some(&hit) => {
                        out.insert(pk.clone(), hit);
                    }
                    None => misses.push(pk.clone()),
                }
            }
        }
        if misses.is_empty() {
            return out;
        }

        let url = format!("{}/check", self.base);
        let resolved: Option<HashMap<String, bool>> = match self
            .http
            .post(&url)
            .json(&json!({ "pubkeys": misses }))
            .send()
            .await
        {
            Ok(resp) if resp.status().is_success() => {
                match resp.json::<BulkCheckResponse>().await {
                    Ok(body) => Some(body.results),
                    Err(_) => None, // decode error → fail-safe, do not cache
                }
            }
            // Non-success status or transport error → fail toward not-flagging.
            Ok(_) => None,
            Err(_) => None,
        };

        match resolved {
            Some(results) => {
                let mut cache = self.cache.lock().expect("whitelist cache mutex");
                for pk in misses {
                    match results.get(&pk) {
                        Some(&value) => {
                            cache.insert(pk.clone(), value);
                            out.insert(pk, value);
                        }
                        // Server omitted this pubkey (e.g. empty string) → fail
                        // safe to true, NOT cached.
                        None => {
                            out.insert(pk, true);
                        }
                    }
                }
            }
            // Whole-request failure → every miss fails safe to true, uncached, so
            // a later retry can still resolve the real value (Pitfall 2).
            None => {
                for pk in misses {
                    out.insert(pk, true);
                }
            }
        }
        out
    }
}

/// L0 whitelist-absence layer (DETECT-01). Stable `signal.layer` name
/// `L0_whitelist_absence` (D-02). CPU-only: it consumes the pre-resolved
/// `whitelisted` bool and does NO HTTP in [`Layer::score`].
pub struct WhitelistAbsenceLayer {
    /// The sub-score emitted when the pubkey is NOT whitelisted (config; default
    /// 1.0). The combiner's small L0 weight keeps absence alone below τ.
    absence_subscore: f64,
}

impl WhitelistAbsenceLayer {
    /// Build from the L0 config entry.
    pub fn new(cfg: &L0Config) -> Self {
        WhitelistAbsenceLayer {
            absence_subscore: cfg.absence_subscore,
        }
    }
}

impl Layer for WhitelistAbsenceLayer {
    fn name(&self) -> &'static str {
        "L0_whitelist_absence"
    }

    /// Pure: `whitelisted` → 0.0 (clears only this layer, D-03); NOT whitelisted
    /// → `absence_subscore`. Evidence carries only the boolean (T-04-08: never
    /// the raw HTTP body). The `events` slice is unused — L0 is a membership
    /// signal, not a content signal (a zero-event pubkey still gets an L0 score).
    fn score(&self, _events: &[Event], whitelisted: bool) -> LayerOutput {
        let value = if whitelisted {
            0.0
        } else {
            self.absence_subscore
        };
        LayerOutput {
            value,
            evidence: json!({ "whitelisted": whitelisted }),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::Arc;
    use std::thread;

    const PK: &str = "aa00000000000000000000000000000000000000000000000000000000000001";

    fn block_on<F: std::future::Future>(f: F) -> F::Output {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("build current-thread runtime")
            .block_on(f)
    }

    fn layer() -> WhitelistAbsenceLayer {
        WhitelistAbsenceLayer::new(&L0Config {
            enabled: true,
            weight: 0.8,
            absence_subscore: 1.0,
        })
    }

    /// Presence clears ONLY this layer → value 0.0, evidence {"whitelisted":true}.
    #[test]
    fn presence_clears_layer() {
        let out = layer().score(&[], true);
        assert_eq!(out.value, 0.0, "whitelisted → 0.0 (clears only this layer)");
        assert_eq!(out.evidence, json!({ "whitelisted": true }));
    }

    /// Absence fires at the configured sub-score, evidence {"whitelisted":false}.
    #[test]
    fn absence_emits_subscore() {
        let out = layer().score(&[], false);
        assert_eq!(out.value, 1.0, "not whitelisted → absence_subscore (1.0)");
        assert_eq!(out.evidence, json!({ "whitelisted": false }));

        // A non-default absence_subscore is honored.
        let weak = WhitelistAbsenceLayer::new(&L0Config {
            enabled: true,
            weight: 0.8,
            absence_subscore: 0.5,
        });
        assert_eq!(weak.score(&[], false).value, 0.5);
    }

    /// A loopback stub serving N requests, each replying `{"whitelisted":body}`.
    /// Counts the connections it accepts so a test can prove the cache prevents
    /// a second round-trip. Returns `(url, hits)`.
    fn check_stub(body_whitelisted: bool, max_conns: usize) -> (String, Arc<AtomicUsize>) {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}");
        let hits = Arc::new(AtomicUsize::new(0));
        let hits_c = Arc::clone(&hits);
        thread::spawn(move || {
            for (i, conn) in listener.incoming().enumerate() {
                if i >= max_conns {
                    break;
                }
                let mut sock = match conn {
                    Ok(s) => s,
                    Err(_) => break,
                };
                hits_c.fetch_add(1, Ordering::SeqCst);
                let mut buf = [0u8; 4096];
                let _ = sock.read(&mut buf);
                let resp_body = format!(r#"{{"whitelisted":{body_whitelisted}}}"#);
                let bytes = resp_body.as_bytes();
                let head = format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                    bytes.len()
                );
                let _ = sock.write_all(head.as_bytes());
                let _ = sock.write_all(bytes);
                let _ = sock.flush();
            }
        });
        (url, hits)
    }

    /// The client parses `{"whitelisted":true}` → true and `{"whitelisted":false}`
    /// → false against a loopback stub.
    #[test]
    fn client_parses_both_booleans() {
        let (url_t, _) = check_stub(true, 1);
        let client_t = WhitelistClient::new(url_t);
        assert!(block_on(client_t.is_whitelisted(PK)), "stub true → true");

        let (url_f, _) = check_stub(false, 1);
        let client_f = WhitelistClient::new(url_f);
        assert!(!block_on(client_f.is_whitelisted(PK)), "stub false → false");
    }

    /// The per-run cache returns the cached value on a second call WITHOUT a
    /// second HTTP round-trip (the stub only accepts ONE connection).
    #[test]
    fn cache_avoids_second_roundtrip() {
        let (url, hits) = check_stub(false, 1);
        let client = WhitelistClient::new(url);
        let first = block_on(client.is_whitelisted(PK));
        let second = block_on(client.is_whitelisted(PK));
        assert_eq!(first, second, "cache returns the same value");
        assert!(!first, "stub said not-whitelisted");
        assert_eq!(
            hits.load(Ordering::SeqCst),
            1,
            "second lookup hit the cache, not the network (OPS-02 no-TTL cache)"
        );
    }

    /// A loopback stub for the bulk `POST /check` endpoint: replies once with a
    /// fixed `{"results":{...}}` body built from `(pubkey, bool)` pairs, counting
    /// connections so a test can prove the cache suppresses a second POST.
    fn bulk_stub(entries: &[(&str, bool)], max_conns: usize) -> (String, Arc<AtomicUsize>) {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind ephemeral port");
        let addr = listener.local_addr().expect("local addr");
        let url = format!("http://{addr}");
        let pairs: Vec<(String, bool)> =
            entries.iter().map(|(k, v)| (k.to_string(), *v)).collect();
        let hits = Arc::new(AtomicUsize::new(0));
        let hits_c = Arc::clone(&hits);
        thread::spawn(move || {
            let results: Vec<String> = pairs
                .iter()
                .map(|(k, v)| format!(r#""{k}":{v}"#))
                .collect();
            let resp_body = format!(r#"{{"results":{{{}}}}}"#, results.join(","));
            for (i, conn) in listener.incoming().enumerate() {
                if i >= max_conns {
                    break;
                }
                let mut sock = match conn {
                    Ok(s) => s,
                    Err(_) => break,
                };
                hits_c.fetch_add(1, Ordering::SeqCst);
                let mut buf = [0u8; 8192];
                let _ = sock.read(&mut buf);
                let bytes = resp_body.as_bytes();
                let head = format!(
                    "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                    bytes.len()
                );
                let _ = sock.write_all(head.as_bytes());
                let _ = sock.write_all(bytes);
                let _ = sock.flush();
            }
        });
        (url, hits)
    }

    const PK2: &str = "bb00000000000000000000000000000000000000000000000000000000000002";

    /// Bulk resolve parses the `{"results":{...}}` map, caches each value, and a
    /// second call for the SAME keys hits the cache (stub accepts ONE connection).
    #[test]
    fn bulk_parses_caches_and_reuses() {
        let (url, hits) = bulk_stub(&[(PK, true), (PK2, false)], 1);
        let client = WhitelistClient::new(url);
        let keys = vec![PK.to_string(), PK2.to_string()];

        let first = block_on(client.is_whitelisted_bulk(&keys));
        assert_eq!(first.get(PK), Some(&true), "PK parsed true");
        assert_eq!(first.get(PK2), Some(&false), "PK2 parsed false");

        // Second call: every key is cached → no second POST (stub serves only 1).
        let second = block_on(client.is_whitelisted_bulk(&keys));
        assert_eq!(second.get(PK), Some(&true));
        assert_eq!(second.get(PK2), Some(&false));
        assert_eq!(
            hits.load(Ordering::SeqCst),
            1,
            "second bulk lookup hit the cache, not the network (OPS-02)"
        );
    }

    /// Bulk fail-toward-not-flagging: a transport error resolves EVERY requested
    /// pubkey to `true` and caches nothing.
    #[test]
    fn bulk_transport_error_fails_toward_not_flagging() {
        let dead_url = {
            let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
            let addr = listener.local_addr().expect("addr");
            format!("http://{addr}")
        };
        let client = WhitelistClient::new(dead_url);
        let keys = vec![PK.to_string(), PK2.to_string()];
        let out = block_on(client.is_whitelisted_bulk(&keys));
        assert_eq!(out.get(PK), Some(&true), "transport error → fail-safe true");
        assert_eq!(out.get(PK2), Some(&true), "transport error → fail-safe true");
        assert!(
            client.cache.lock().unwrap().is_empty(),
            "fail-safe values must not be cached (Pitfall 2)"
        );
    }

    /// Fail-toward-not-flagging (Pitfall 2 / T-04-07): a transport error (nothing
    /// listening on the port) resolves to `true` (→ L0 sub-score 0.0), NEVER
    /// "absent". The fail-safe value is not cached.
    #[test]
    fn transport_error_fails_toward_not_flagging() {
        // Bind then immediately drop the listener so the port is closed → the
        // GET fails at the transport layer.
        let dead_url = {
            let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
            let addr = listener.local_addr().expect("addr");
            format!("http://{addr}")
            // listener dropped here → connection refused
        };
        let client = WhitelistClient::new(dead_url);
        let resolved = block_on(client.is_whitelisted(PK));
        assert!(
            resolved,
            "transport error must fail TOWARD not-flagging (true → L0 0.0), never 'absent'"
        );
        // The fail-safe value is NOT cached (a retry can still resolve the real
        // value) — assert the cache stayed empty.
        assert!(
            client.cache.lock().unwrap().is_empty(),
            "fail-safe value must not be cached (Pitfall 2)"
        );
    }

    /// The fetch-stage maps an unreachable whitelist's fail-safe `true` to an L0
    /// sub-score of 0.0 — proving the outage produces NO false-positive nudge.
    #[test]
    fn unreachable_maps_to_zero_subscore() {
        // Closed port → fail-safe true.
        let dead_url = {
            let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
            let addr = listener.local_addr().expect("addr");
            format!("http://{addr}")
        };
        let client = WhitelistClient::new(dead_url);
        let whitelisted = block_on(client.is_whitelisted(PK));
        // The layer maps the fail-safe true → 0.0 (cleared), not the absence
        // sub-score — so an outage never inflates spam scores (T-04-07).
        let out = layer().score(&[], whitelisted);
        assert_eq!(
            out.value, 0.0,
            "an unreachable whitelist must emit L0 0.0 (fail toward not-flagging)"
        );
    }

    /// D-14 live, self-skipping: a real GET against the live whitelist at
    /// 127.0.0.1:8081 deserializes `{"whitelisted":bool}`. On transport
    /// unreachability it eprintln-skips, never failing CI. Override the base via
    /// `WHITELIST_URL`.
    #[test]
    fn live_check_self_skipping() {
        const DEFAULT_BASE: &str = "http://127.0.0.1:8081";
        let base = std::env::var("WHITELIST_URL").unwrap_or_else(|_| DEFAULT_BASE.to_string());

        // Probe /health first so we can distinguish "server down" (skip) from a
        // genuine parse/protocol bug (which should surface). A fixed 64-hex key.
        block_on(async {
            let probe = reqwest::Client::new()
                .get(format!("{base}/check/{PK}"))
                .send()
                .await;
            match probe {
                Ok(resp) if resp.status().is_success() => {
                    // Must deserialize the verified {"whitelisted":bool} shape.
                    let parsed = resp.json::<CheckResponse>().await;
                    assert!(
                        parsed.is_ok(),
                        "live /check/{{pubkey}} must deserialize {{\"whitelisted\":bool}} (D-14)"
                    );
                    eprintln!(
                        "live_check_self_skipping: parsed whitelisted={} from {base} (D-14 OK)",
                        parsed.unwrap().whitelisted
                    );
                }
                Ok(resp) => {
                    // Reachable but non-200 (e.g. 503 loading) — degrade to a
                    // deferred manual check, never fail CI (D-14).
                    eprintln!(
                        "live_check_self_skipping: whitelist at {base} returned {} \
                         — D-14 deferred to manual check",
                        resp.status()
                    );
                }
                Err(_) => {
                    eprintln!(
                        "live_check_self_skipping: whitelist unreachable at {base} \
                         — D-14 deferred to manual check"
                    );
                }
            }
        });
    }
}
