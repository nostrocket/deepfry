//! L3 content entropy + density detection (DETECT-03).
//!
//! Flags two-sided character entropy — LOW entropy (templated / repeated text)
//! and HIGH entropy (random gibberish / base64-ish dumps) — plus emoji and
//! hashtag density. The sub-score is the `max` of the component flags so a single
//! mild feature never stacks into a flag (FP-averse, RESEARCH §L3). Every cutoff
//! is a conservative config value (D-08).
//!
//! Determinism (OPS-02): Shannon entropy sums per-character probabilities; the
//! sum is order-independent (addition is commutative), so the `HashMap<char,u64>`
//! count is the documented safe exception to the no-HashMap-in-score-path rule —
//! we only SUM the counts, never derive the score from iteration order.

use crate::config::L3Config;
use crate::detect::{Layer, LayerOutput};
use crate::graphql::queries::Event;
use serde_json::json;
use std::collections::HashMap;
use unicode_segmentation::UnicodeSegmentation;

/// Shannon entropy in bits/char over the character distribution of `s`:
/// `H = -Σ p(c) log2 p(c)`. Order-independent (commutative sum) → deterministic.
fn shannon_bits_per_char(s: &str) -> f64 {
    let mut counts: HashMap<char, u64> = HashMap::new();
    let mut n = 0u64;
    for c in s.chars() {
        *counts.entry(c).or_insert(0) += 1;
        n += 1;
    }
    if n == 0 {
        return 0.0;
    }
    counts
        .values()
        .map(|&c| {
            let p = c as f64 / n as f64;
            -p * p.log2()
        })
        .sum()
}

/// Heuristic emoji test: a grapheme is counted as an emoji when its first
/// codepoint falls in a common Unicode emoji block. NOT exhaustive (ZWJ
/// sequences / skin-tone modifiers collapse to one grapheme, counted once),
/// which is fine for a density ratio. Deterministic.
fn is_emoji_grapheme(g: &str) -> bool {
    g.chars().next().is_some_and(|c| {
        let u = c as u32;
        // Misc symbols & pictographs, emoticons, transport/map, supplemental
        // symbols & pictographs, dingbats, regional indicators, misc symbols.
        (0x1F300..=0x1FAFF).contains(&u)
            || (0x2600..=0x27BF).contains(&u)
            || (0x1F1E6..=0x1F1FF).contains(&u)
            || (0xFE00..=0xFE0F).contains(&u) // variation selectors
    })
}

/// L3 content-entropy + density layer (DETECT-03). Stable `signal.layer` name
/// `L3_content_entropy` (D-02).
pub struct ContentEntropyLayer {
    entropy_low: f64,
    entropy_high: f64,
    min_len_for_low: usize,
    emoji_density_knee: f64,
    hashtag_density_knee: f64,
}

impl ContentEntropyLayer {
    /// Build from the L3 config entry.
    pub fn new(cfg: &L3Config) -> Self {
        ContentEntropyLayer {
            entropy_low: cfg.entropy_low,
            entropy_high: cfg.entropy_high,
            min_len_for_low: cfg.min_len_for_low,
            emoji_density_knee: cfg.emoji_density_knee,
            hashtag_density_knee: cfg.hashtag_density_knee,
        }
    }

    /// Map an entropy value to a [0,1] component (RESEARCH §L3 cutoffs).
    ///
    /// LOW side (templated): `H <= entropy_low` → 1.0; a linear shoulder ramps
    /// down to 0.0 at `entropy_low + 1.0` (RESEARCH "ramp to 0 at H=3.0" for
    /// low=2.0). The low-entropy flag ONLY applies when `total_len >=
    /// min_len_for_low` — short posts like "gm" are naturally low-entropy and are
    /// never penalized (FP-averse).
    ///
    /// HIGH side (gibberish): `H >= entropy_high` → 1.0; a linear shoulder ramps
    /// down to 0.0 at `entropy_high - 0.5` (RESEARCH "ramp to 0 at H=5.0" for
    /// high=5.5). Inside the resulting band → 0.0 (normal text).
    fn entropy_component(&self, h: f64, total_len: usize) -> f64 {
        // LOW / templated.
        let low = if total_len < self.min_len_for_low {
            0.0 // short posts exempt
        } else {
            let shoulder = self.entropy_low + 1.0;
            // 1.0 at/below entropy_low, ramping to 0.0 at `shoulder`.
            ((shoulder - h) / (shoulder - self.entropy_low).max(f64::EPSILON)).clamp(0.0, 1.0)
        };
        // HIGH / gibberish.
        let shoulder = (self.entropy_high - 0.5).max(0.0);
        let high =
            ((h - shoulder) / (self.entropy_high - shoulder).max(f64::EPSILON)).clamp(0.0, 1.0);
        low.max(high)
    }
}

impl Layer for ContentEntropyLayer {
    fn name(&self) -> &'static str {
        "L3_content_entropy"
    }

    fn score(&self, events: &[Event], _whitelisted: bool) -> LayerOutput {
        if events.is_empty() {
            return LayerOutput {
                value: 0.0,
                evidence: json!({ "reason": "no_events" }),
            };
        }
        // Concatenate normalized content (trim + lowercase + collapse whitespace),
        // matching L1's fixed normalization for cross-layer consistency.
        let concat: String = events
            .iter()
            .map(|e| {
                e.content
                    .trim()
                    .to_lowercase()
                    .split_whitespace()
                    .collect::<Vec<_>>()
                    .join(" ")
            })
            .collect::<Vec<_>>()
            .join(" ");

        let total_len = concat.chars().count();
        let h = shannon_bits_per_char(&concat);
        let entropy_c = self.entropy_component(h, total_len);

        // Emoji density = emoji-graphemes / graphemes.
        let graphemes: Vec<&str> = concat.graphemes(true).collect();
        let n_graphemes = graphemes.len().max(1);
        let n_emoji = graphemes.iter().filter(|g| is_emoji_grapheme(g)).count();
        let emoji_density = n_emoji as f64 / n_graphemes as f64;
        let emoji_c = if self.emoji_density_knee > 0.0 {
            (emoji_density / self.emoji_density_knee).min(1.0)
        } else {
            0.0
        };

        // Hashtag density = #-prefixed words / words.
        let words: Vec<&str> = concat.unicode_words().collect();
        let n_words = words.len().max(1);
        // unicode_words strips punctuation, so count '#' tokens on whitespace split.
        let n_hashtags = concat.split_whitespace().filter(|w| w.starts_with('#')).count();
        let hashtag_density = n_hashtags as f64 / n_words as f64;
        let hashtag_c = if self.hashtag_density_knee > 0.0 {
            (hashtag_density / self.hashtag_density_knee).min(1.0)
        } else {
            0.0
        };

        let value = entropy_c.max(emoji_c).max(hashtag_c).clamp(0.0, 1.0);
        LayerOutput {
            value,
            evidence: json!({
                "entropy_bits": h,
                "emoji_density": emoji_density,
                "hashtag_density": hashtag_density,
            }),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ev(idx: usize, content: &str) -> Event {
        Event {
            id: format!("e{idx}"),
            pubkey: "pk".into(),
            kind: 1,
            created_at: 1_700_000_000 + idx as i64,
            content: content.to_string(),
            tags: vec![],
        }
    }

    fn layer() -> ContentEntropyLayer {
        ContentEntropyLayer {
            entropy_low: 2.0,
            entropy_high: 5.5,
            min_len_for_low: 200,
            emoji_density_knee: 0.5,
            hashtag_density_knee: 0.3,
        }
    }

    /// Low-entropy / templated: a long, highly-repetitive string (≥ min_len_for_low
    /// chars, very few distinct chars → H well below 2.0) → entropy component ≈ 1.0.
    #[test]
    fn low_entropy_templated_flags() {
        let l = layer();
        // 300 chars of "ab ab ab …" → only a handful of distinct chars → H ≈ 1.x,
        // below entropy_low=2.0 and length ≥ 200 → flag.
        let templated = "ab ".repeat(120); // 360 chars
        let out = l.score(&[ev(0, &templated)], false);
        assert!(
            out.value > 0.8,
            "long templated low-entropy text should flag (value {}, H {})",
            out.value,
            out.evidence["entropy_bits"]
        );
    }

    /// High-entropy / gibberish: long near-random content (H > entropy_high=5.5)
    /// → entropy component ramps toward 1.0.
    #[test]
    fn high_entropy_gibberish_flags() {
        let l = layer();
        // A long string of many distinct chars across a wide range → high entropy.
        let mut gib = String::new();
        // Cycle through a large distinct-char alphabet to push H above 5.5.
        let alphabet: Vec<char> =
            ('!'..='~').chain('À'..='ÿ').chain('Ā'..='ž').collect();
        for i in 0..400 {
            gib.push(alphabet[i % alphabet.len()]);
        }
        let h = shannon_bits_per_char(&gib);
        assert!(h > 5.5, "constructed gibberish must exceed 5.5 bits/char (was {h})");
        let out = l.score(&[ev(0, &gib)], false);
        assert!(
            out.value > 0.0,
            "high-entropy gibberish should flag (value {}, H {})",
            out.value,
            out.evidence["entropy_bits"]
        );
    }

    /// Normal English inside the band → entropy component 0.0; a short "gm" post
    /// is NOT penalized (below min_len_for_low even though it is low-entropy).
    #[test]
    fn normal_and_short_content_not_penalized() {
        let l = layer();
        let prose = "the meeting this morning covered the quarterly budget review \
                     and we discussed several plans for the upcoming product launch \
                     while the team shared honest feedback about recent customer calls";
        let out = l.score(&[ev(0, prose)], false);
        let h = out.evidence["entropy_bits"].as_f64().unwrap();
        assert!(
            (2.0..=5.5).contains(&h),
            "normal English entropy should sit in the band (H {h})"
        );
        assert_eq!(out.value, 0.0, "normal prose must not flag");

        // Short low-entropy post: "gm" — low entropy but well under min_len_for_low.
        let short = l.score(&[ev(0, "gm")], false);
        assert_eq!(short.value, 0.0, "short 'gm' post must not be penalized");
    }

    /// Emoji / hashtag density: content dominated by emoji graphemes or hashtags
    /// ramps the respective component toward 1.0.
    #[test]
    fn emoji_and_hashtag_density_flag() {
        let l = layer();
        // Mostly emoji → emoji density ≥ knee → component → 1.0.
        let emoji = l.score(&[ev(0, "🎉🎊🚀🔥💎🌙⭐🎯🎁🏆")], false);
        assert!(
            emoji.value > 0.8,
            "emoji-dominated content should flag (value {}, density {})",
            emoji.value,
            emoji.evidence["emoji_density"]
        );

        // Many hashtags relative to words → hashtag density ≥ knee.
        let tags = l.score(&[ev(0, "#crypto #airdrop #free #nft #web3 win")], false);
        assert!(
            tags.value > 0.8,
            "hashtag-stuffed content should flag (value {}, density {})",
            tags.value,
            tags.evidence["hashtag_density"]
        );
    }

    /// Bound + max-combine: sub-score = max(components), always in [0,1]; a single
    /// MILD feature (a little emoji, normal entropy) does not stack into a flag.
    #[test]
    fn bound_and_max_combine() {
        let l = layer();
        // Mild: normal prose with one emoji and one hashtag — each component small,
        // and max() does not let them stack.
        let mild = l.score(
            &[ev(
                0,
                "had a great lunch today with friends at the new cafe downtown 🙂 #food",
            )],
            false,
        );
        assert!(
            (0.0..=1.0).contains(&mild.value),
            "value must be bounded (was {})",
            mild.value
        );
        assert!(
            mild.value < 0.5,
            "a single mild feature must not flag (value {})",
            mild.value
        );

        // Arbitrary fixtures stay bounded.
        for c in ["", "x", "🎉#a", &"q".repeat(500), "normal sentence here ok"] {
            let out = l.score(&[ev(0, c)], false);
            assert!(
                (0.0..=1.0).contains(&out.value),
                "content {:?} produced out-of-range {}",
                c,
                out.value
            );
        }
    }
}
