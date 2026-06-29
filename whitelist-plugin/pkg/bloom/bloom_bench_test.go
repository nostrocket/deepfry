package bloom

import (
	"fmt"
	"testing"
)

// BenchmarkContains measures Filter.Contains performance on hit and miss paths,
// size-swept over {1k, 10k, 100k, 500k} members, with ReportAllocs to validate
// the D-08 alloc-free k[:] claim (0 allocs/op on the plugin hot path).
func BenchmarkContains(b *testing.B) {
	sizes := []int{1_000, 10_000, 100_000, 500_000}

	for _, n := range sizes {
		n := n
		keys := genKeys(n)

		// Build the filter once outside the timing loop.
		bld := NewBuilder(uint(n), 1e-6)
		for _, k := range keys {
			bld.Add(k)
		}
		f, err := bld.Build()
		if err != nil {
			b.Fatalf("Build n=%d: %v", n, err)
		}

		// Hit key: the first member — provably in the filter.
		hitKey := keys[0]

		// Miss key: offset well beyond member range so it is provably not a member.
		var missKey [32]byte
		v := uint64(n + 1_000_000)
		for j := 0; j < 32; j++ {
			missKey[j] = byte(v >> ((j % 8) * 8))
		}

		b.Run(fmt.Sprintf("hit/n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = f.Contains(hitKey)
			}
		})

		b.Run(fmt.Sprintf("miss/n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = f.Contains(missKey)
			}
		})
	}
}

// BenchmarkBuild measures Builder.Build() cost at the production-sized 500k member set.
// Mirrors BenchmarkNewWhiteList from pkg/whitelist/whitelist_bench_test.go.
func BenchmarkBuild(b *testing.B) {
	const n = 500_000
	keys := genKeys(n)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bld := NewBuilder(n, 1e-6)
		for _, k := range keys {
			bld.Add(k)
		}
		f, err := bld.Build()
		if err != nil {
			b.Fatalf("Build: %v", err)
		}
		_ = f
	}
}

// TestContainsZeroAllocs is a hard CI gate that enforces the D-08 alloc-free claim:
// Filter.Contains([32]byte) must allocate zero heap bytes per call. It uses
// testing.AllocsPerRun so this invariant is checked under the normal `go test` gate
// and does not require manual inspection of -benchmem output.
func TestContainsZeroAllocs(t *testing.T) {
	const n = 1_000
	keys := genKeys(n)

	bld := NewBuilder(uint(n), 1e-6)
	for _, k := range keys {
		bld.Add(k)
	}
	f, err := bld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Use the first member as the stable query key — provably in the filter.
	k := keys[0]

	avg := testing.AllocsPerRun(100, func() {
		_ = f.Contains(k)
	})

	if avg != 0 {
		t.Fatalf("Filter.Contains allocates %.1f heap object(s) per call (D-08 requires 0); "+
			"k[:] may have escaped to the heap", avg)
	}
}
