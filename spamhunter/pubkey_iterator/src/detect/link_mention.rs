//! L4 link & mention detection (DETECT-04).
//!
//! Four FP-averse components, each normalized to `[0,1]` via `min(value/knee,
//! 1.0)`; the sub-score is their `max` (RESEARCH §L4) so a single mild feature
//! never stacks into a flag and the result stays bounded:
//!
//! 1. **URL ratio** — events containing ≥1 URL / total events.
//! 2. **Repeated-domain concentration** — (max events sharing one host) /
//!    events-with-URLs. A pubkey hammering the same domain is the signal.
//! 3. **Mass p-tag mentions** — mean `p`-tag count per event (mention-spam).
//! 4. **Hashtag stuffing** — mean `t`-tag count per event (Nostr hashtags are
//!    `["t", …]` tags).
//!
//! Hosts are parsed with the WHATWG-correct `url` crate (`Url::parse(token)
//! .ok().and_then(|u| u.host_str()…)`), NEVER a regex (RESEARCH §Don't
//! Hand-Roll). L4 only PARSES URLs for their host strings — it NEVER fetches
//! them, so there is no SSRF vector (T-04-09). All event `content`/`tags` are
//! UNTRUSTED and treated as opaque text (V5).
//!
//! Determinism (OPS-02): every component is a pure, order-independent reduction
//! over the events (counts + means + a single max-share scan); no RNG. The
//! host-count HashMap is summed for the value and max-reduced for the persisted
//! `top_domain` evidence — that max-reduction uses a TOTAL order (highest count,
//! ties broken by the smallest host string), so neither the score nor the
//! evidence depends on HashMap iteration order (MD-01).

use crate::config::L4Config;
use crate::detect::{Layer, LayerOutput};
use crate::graphql::queries::Event;
use serde_json::json;
use std::collections::HashMap;

/// L4 link & mention layer (DETECT-04). Stable `signal.layer` name
/// `L4_link_mention` (D-02).
pub struct LinkMentionLayer {
    url_ratio_knee: f64,
    domain_concentration_knee: f64,
    mean_ptags_knee: f64,
    mean_ttags_knee: f64,
    /// Below this event count the layer emits 0.0 (FP-averse — too little signal).
    min_events: usize,
}

impl LinkMentionLayer {
    /// Build from the L4 config entry.
    pub fn new(cfg: &L4Config) -> Self {
        LinkMentionLayer {
            url_ratio_knee: cfg.url_ratio_knee,
            domain_concentration_knee: cfg.domain_concentration_knee,
            mean_ptags_knee: cfg.mean_ptags_knee,
            mean_ttags_knee: cfg.mean_ttags_knee,
            min_events: cfg.min_events,
        }
    }
}

/// Map a value to a `[0,1]` component via `min(value/knee, 1.0)`. A non-positive
/// knee disables the component (returns 0.0) rather than dividing by zero.
fn knee_component(value: f64, knee: f64) -> f64 {
    if knee > 0.0 {
        (value / knee).min(1.0)
    } else {
        0.0
    }
}

/// Extract the host of the FIRST parseable URL token in `content`, if any, and
/// report whether the content contained at least one URL. Whitespace-tokenize
/// (URLs do not contain spaces) and parse each token with the `url` crate;
/// `.host_str()` is `None` for schemes without an authority (e.g. `mailto:`),
/// which we treat as "not a host-bearing URL". Returns `(contains_url, hosts)`
/// where `hosts` is every host found (for the concentration scan).
fn hosts_in(content: &str) -> Vec<String> {
    content
        .split_whitespace()
        .filter_map(|token| {
            url::Url::parse(token)
                .ok()
                .and_then(|u| u.host_str().map(|h| h.to_ascii_lowercase()))
        })
        .collect()
}

impl Layer for LinkMentionLayer {
    fn name(&self) -> &'static str {
        "L4_link_mention"
    }

    fn score(&self, events: &[Event], _whitelisted: bool) -> LayerOutput {
        let n = events.len();
        // FP-averse gate: too few events to draw a conclusion.
        if n < self.min_events {
            return LayerOutput {
                value: 0.0,
                evidence: json!({ "reason": "below_min_events", "events": n }),
            };
        }

        // --- URL ratio + repeated-domain concentration ---
        let mut events_with_url = 0usize;
        // Host → count of EVENTS that linked it (count an event once per host so
        // a single event spamming one host 10× doesn't dominate the share).
        let mut host_event_counts: HashMap<String, usize> = HashMap::new();
        for e in events {
            let hosts = hosts_in(&e.content);
            if !hosts.is_empty() {
                events_with_url += 1;
                // Distinct hosts within this one event.
                let mut seen: Vec<&String> = Vec::new();
                for h in &hosts {
                    if !seen.contains(&h) {
                        seen.push(h);
                        *host_event_counts.entry(h.clone()).or_insert(0) += 1;
                    }
                }
            }
        }
        let url_ratio = events_with_url as f64 / n as f64;
        // Concentration = (max events sharing one host) / events-with-URLs.
        // The count feeds the value; the host STRING feeds `signal.evidence`.
        // `max_by_key` on the count alone resolves ties by HashMap iteration
        // order, which is unstable across runs — so the persisted `top_domain`
        // could differ between byte-identical re-runs (OPS-02 hazard, MD-01).
        // Use a TOTAL order: highest count wins, ties broken by the smallest
        // host string lexicographically. `max_by` keeps the LAST maximal
        // element, so to make the smallest host the winner on a tie we invert
        // the host comparison (`hb.cmp(ha)`) — the count comparison stays
        // ascending so the highest count still wins.
        let (top_host, top_host_events) = host_event_counts
            .iter()
            .max_by(|(ha, ca), (hb, cb)| ca.cmp(cb).then_with(|| hb.cmp(ha)))
            .map(|(h, c)| (h.clone(), *c))
            .unwrap_or_default();
        let domain_concentration = if events_with_url > 0 {
            top_host_events as f64 / events_with_url as f64
        } else {
            0.0
        };

        // --- Mean p-tag and t-tag counts per event ---
        // tags[i][0] is the tag name (p = mention, t = hashtag).
        let total_p: usize = events
            .iter()
            .map(|e| e.tags.iter().filter(|t| t.first().map(String::as_str) == Some("p")).count())
            .sum();
        let total_t: usize = events
            .iter()
            .map(|e| e.tags.iter().filter(|t| t.first().map(String::as_str) == Some("t")).count())
            .sum();
        let mean_p_tags = total_p as f64 / n as f64;
        let mean_t_tags = total_t as f64 / n as f64;

        // --- Components → max sub-score, clamped [0,1]. ---
        let url_ratio_c = knee_component(url_ratio, self.url_ratio_knee);
        let domain_concentration_c =
            knee_component(domain_concentration, self.domain_concentration_knee);
        let mean_p_tags_c = knee_component(mean_p_tags, self.mean_ptags_knee);
        let mean_t_tags_c = knee_component(mean_t_tags, self.mean_ttags_knee);
        let value = url_ratio_c
            .max(domain_concentration_c)
            .max(mean_p_tags_c)
            .max(mean_t_tags_c)
            .clamp(0.0, 1.0);

        LayerOutput {
            value,
            evidence: json!({
                "url_ratio": url_ratio,
                "top_domain": top_host,
                "top_domain_share": domain_concentration,
                "mean_p_tags": mean_p_tags,
                "mean_t_tags": mean_t_tags,
            }),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn layer() -> LinkMentionLayer {
        LinkMentionLayer {
            url_ratio_knee: 0.8,
            domain_concentration_knee: 0.7,
            mean_ptags_knee: 10.0,
            mean_ttags_knee: 8.0,
            min_events: 5,
        }
    }

    /// Build an event with `content` and the given tags (each `(name, value)`).
    fn ev(idx: usize, content: &str, tags: &[(&str, &str)]) -> Event {
        Event {
            id: format!("e{idx}"),
            pubkey: "pk".into(),
            kind: 1,
            created_at: 1_700_000_000 + idx as i64,
            content: content.to_string(),
            tags: tags
                .iter()
                .map(|(k, v)| vec![k.to_string(), v.to_string()])
                .collect(),
        }
    }

    /// URL ratio: events nearly all containing a URL ramp the url_ratio component
    /// to 1.0 at/above the knee (0.8).
    #[test]
    fn url_ratio_ramps_at_knee() {
        // 10 events, 9 contain a URL → ratio 0.9 ≥ knee 0.8 → component 1.0.
        // Spread across DIFFERENT hosts so domain_concentration stays low and the
        // url_ratio component is the one being asserted.
        let mut events = Vec::new();
        for i in 0..9 {
            events.push(ev(i, &format!("check this https://site{i}.example/page"), &[]));
        }
        events.push(ev(9, "no link here just text", &[]));
        let out = layer().score(&events, false);
        assert!(
            out.value > 0.99,
            "9/10 events with URLs across distinct hosts → url_ratio component ≈ 1.0 (value {}, url_ratio {})",
            out.value,
            out.evidence["url_ratio"]
        );
        // Sanity: the firing component is url_ratio, not concentration.
        assert!(out.evidence["url_ratio"].as_f64().unwrap() >= 0.8);
    }

    /// Repeated-domain concentration: many events linking the SAME host → the
    /// domain_concentration component near 1.0; diverse hosts → low.
    #[test]
    fn repeated_domain_concentration() {
        // 6 events all linking the SAME host (but url_ratio is also high here, so
        // assert via the evidence that concentration is what's high). To isolate
        // concentration from url_ratio we keep both high — the property under test
        // is that the top-domain share is ≈1.0.
        let same: Vec<Event> = (0..6)
            .map(|i| ev(i, "buy now https://spam.example/x", &[]))
            .collect();
        let out_same = layer().score(&same, false);
        assert!(
            (out_same.evidence["top_domain_share"].as_f64().unwrap() - 1.0).abs() < 1e-9,
            "all events share one host → top_domain_share ≈ 1.0 (got {})",
            out_same.evidence["top_domain_share"]
        );
        assert_eq!(out_same.evidence["top_domain"], "spam.example");

        // Diverse hosts → low concentration (each host appears once of 6).
        let diverse: Vec<Event> = (0..6)
            .map(|i| ev(i, &format!("link https://host{i}.example/p"), &[]))
            .collect();
        let out_div = layer().score(&diverse, false);
        let share = out_div.evidence["top_domain_share"].as_f64().unwrap();
        assert!(
            (share - 1.0 / 6.0).abs() < 1e-9,
            "diverse hosts → top_domain_share ≈ 1/6 (got {share})"
        );
        // Concentration component (share/0.7) is well below the url_ratio one.
        assert!(share < 0.7, "diverse hosts stay below the concentration knee");
    }

    /// Determinism (OPS-02 / MD-01): the persisted `top_domain` evidence must be
    /// byte-identical across repeated calls on the same input, AND on a host
    /// count-tie the tie-break is total-ordered (smallest host string wins), so
    /// the winner does not depend on HashMap iteration order.
    #[test]
    fn top_domain_tie_break_is_deterministic() {
        // 6 events: 3 link host "bbb.example", 3 link host "aaa.example" → a
        // 3-vs-3 count tie. The documented winner is the lexicographically
        // smallest host string, "aaa.example".
        let mut events = Vec::new();
        for i in 0..3 {
            events.push(ev(i, "buy https://bbb.example/x", &[]));
        }
        for i in 3..6 {
            events.push(ev(i, "buy https://aaa.example/x", &[]));
        }

        // Repeated calls on identical input yield the SAME top_domain.
        let first = layer().score(&events, false);
        for _ in 0..20 {
            let again = layer().score(&events, false);
            assert_eq!(
                again.evidence["top_domain"], first.evidence["top_domain"],
                "top_domain must be deterministic across repeated calls"
            );
        }

        // The tie is broken toward the smallest host string (documented winner).
        assert_eq!(
            first.evidence["top_domain"], "aaa.example",
            "count-tie must pick the lexicographically smallest host (got {})",
            first.evidence["top_domain"]
        );
        // Both hosts tied at 3 of 6 events → share is 3/6 = 0.5 regardless.
        assert!(
            (first.evidence["top_domain_share"].as_f64().unwrap() - 0.5).abs() < 1e-9,
            "tied hosts → top_domain_share 0.5 (got {})",
            first.evidence["top_domain_share"]
        );
    }

    /// Mass p-tags: events with many `p` tags ramp the mean_p_tags component.
    #[test]
    fn mass_p_tags_ramps() {
        // 5 events, each with 10 p-tags → mean 10 ≥ knee 10 → component 1.0. No
        // URLs, so url_ratio is 0 and the firing component is mean_p_tags.
        let tags: Vec<(&str, &str)> = (0..10).map(|_| ("p", "deadbeef")).collect();
        let events: Vec<Event> = (0..5).map(|i| ev(i, "hi", &tags)).collect();
        let out = layer().score(&events, false);
        assert!(
            out.value > 0.99,
            "mean 10 p-tags/event → mean_p_tags component ≈ 1.0 (value {}, mean {})",
            out.value,
            out.evidence["mean_p_tags"]
        );
        assert_eq!(out.evidence["url_ratio"].as_f64().unwrap(), 0.0, "no URLs");
    }

    /// Hashtag stuffing: events with many `t` tags ramp the mean_t_tags component.
    #[test]
    fn hashtag_stuffing_ramps() {
        // 5 events, each with 8 t-tags → mean 8 ≥ knee 8 → component 1.0.
        let tags: Vec<(&str, &str)> = (0..8).map(|_| ("t", "crypto")).collect();
        let events: Vec<Event> = (0..5).map(|i| ev(i, "gm", &tags)).collect();
        let out = layer().score(&events, false);
        assert!(
            out.value > 0.99,
            "mean 8 t-tags/event → mean_t_tags component ≈ 1.0 (value {}, mean {})",
            out.value,
            out.evidence["mean_t_tags"]
        );
    }

    /// min_events gate + bound: below min_events → 0.0; the sub-score is always
    /// in [0,1] across arbitrary fixtures.
    #[test]
    fn min_events_gate_and_bound() {
        // 4 events (< min_events 5), all spammy → still 0.0 (FP-averse).
        let tags: Vec<(&str, &str)> = (0..20).map(|_| ("p", "x")).collect();
        let few: Vec<Event> = (0..4)
            .map(|i| ev(i, "spam https://a.example/x", &tags))
            .collect();
        assert_eq!(
            layer().score(&few, false).value,
            0.0,
            "below min_events → 0.0 (FP-averse)"
        );

        // Arbitrary fixtures stay bounded in [0,1].
        let fixtures: Vec<Vec<Event>> = vec![
            (0..5).map(|i| ev(i, "", &[])).collect(),
            (0..5).map(|i| ev(i, "plain text no links", &[])).collect(),
            (0..6).map(|i| ev(i, "https://x.example a b https://x.example", &[("p", "1"), ("t", "a")])).collect(),
            (0..7).map(|i| ev(i, "mailto:foo@bar.example not-a-url ftp://h.example/x", &[])).collect(),
        ];
        for (k, f) in fixtures.iter().enumerate() {
            let v = layer().score(f, false).value;
            assert!(
                (0.0..=1.0).contains(&v),
                "fixture {k} produced out-of-range value {v}"
            );
        }
    }
}
