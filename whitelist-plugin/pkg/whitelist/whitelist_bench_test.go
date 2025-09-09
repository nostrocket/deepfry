package whitelist

import (
	"encoding/hex"
	"fmt"
	"testing"
)

// genKeys generates n distinct [32]byte keys deterministically.
func genKeys(n int) [][32]byte {
	keys := make([][32]byte, n)
	for i := 0; i < n; i++ {
		var k [32]byte
		// Fill with a deterministic pattern based on i so keys are unique and reproducible.
		v := uint64(i + 1)
		for j := 0; j < 32; j++ {
			k[j] = byte(v >> ((j % 8) * 8))
		}
		keys[i] = k
	}
	return keys
}

// Helper to get hex-encoded strings for a slice of keys.
func keysToHex(keys [][32]byte) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = hex.EncodeToString(k[:])
	}
	return out
}

// Benchmark constructor performance & memory for various sizes including target 500k.
func BenchmarkNewWhiteList(b *testing.B) {
	sizes := []int{1_000, 10_000, 100_000, 500_000}

	for _, n := range sizes {
		n := n
		b.Run(fmt.Sprintf("NewWhiteList/n=%d", n), func(b *testing.B) {
			keys := genKeys(n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				wl := NewWhiteList(keys)
				// keep reference short-lived to emulate typical usage
				_ = wl
			}
		})
	}
}

// Benchmark lookup (IsWhitelisted) for hits and misses across sizes.
func BenchmarkIsWhitelisted(b *testing.B) {
	sizes := []int{1_000, 10_000, 100_000, 500_000}

	for _, n := range sizes {
		n := n
		keys := genKeys(n)
		hexes := keysToHex(keys)
		// construct a miss key (flip a byte of the first key)
		var missKey [32]byte
		copy(missKey[:], keys[0][:])
		missKey[0] ^= 0xFF
		missStr := hex.EncodeToString(missKey[:])

		wl := NewWhiteList(keys) // reuse for both hit/miss benches

		b.Run(fmt.Sprintf("IsWhitelisted_hit/n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = wl.IsWhitelisted(hexes[i%len(hexes)]) // hit
			}
		})

		b.Run(fmt.Sprintf("IsWhitelisted_miss/n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = wl.IsWhitelisted(missStr) // miss
			}
		})
	}
}
