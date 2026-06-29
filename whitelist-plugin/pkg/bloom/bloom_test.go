package bloom

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// makeKey returns a deterministic [32]byte keyed on seed, mirroring whitelist_test.go.
func makeKey(seed byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// genKeys returns n deterministic [32]byte keys via index-based encoding.
// Mirror of whitelist_bench_test.go genKeys.
func genKeys(n int) [][32]byte {
	keys := make([][32]byte, n)
	for i := 0; i < n; i++ {
		var k [32]byte
		v := uint64(i + 1)
		for j := 0; j < 32; j++ {
			k[j] = byte(v >> ((j % 8) * 8))
		}
		keys[i] = k
	}
	return keys
}

// make64CharsWithChar returns a 64-char string filled with ch (mirrors whitelist_test.go).
func make64CharsWithChar(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

// TestRoundTrip verifies that a filter serialized via WriteTo can be deserialized via
// ReadFilter and returns identical membership, generation marker, and FP rate.
func TestRoundTrip(t *testing.T) {
	const memberCount = 1000
	members := genKeys(memberCount)

	b := NewBuilder(uint(memberCount), 1e-6)
	for _, k := range members {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Serialize
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	// Deserialize
	f2, err := ReadFilter(&buf)
	if err != nil {
		t.Fatalf("ReadFilter: %v", err)
	}

	// Generation must match
	if f.Generation() != f2.Generation() {
		t.Errorf("Generation mismatch: %x vs %x", f.Generation(), f2.Generation())
	}

	// FalsePositiveRate must match
	if f.FalsePositiveRate() != f2.FalsePositiveRate() {
		t.Errorf("FalsePositiveRate mismatch: %v vs %v", f.FalsePositiveRate(), f2.FalsePositiveRate())
	}

	// All members must be possibly-present in the reloaded filter
	for i, k := range members {
		if !f2.Contains(k) {
			t.Errorf("member %d lost across round-trip", i)
		}
	}
}

// TestDeterministicGeneration verifies that two independent Builders fed the same keyset
// produce the same Generation() (D-03 content-hash determinism).
func TestDeterministicGeneration(t *testing.T) {
	const n = 500
	keys := genKeys(n)

	build := func() *Filter {
		b := NewBuilder(uint(n), 1e-6)
		for _, k := range keys {
			b.Add(k)
		}
		f, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return f
	}

	f1 := build()
	f2 := build()

	if f1.Generation() != f2.Generation() {
		t.Errorf("identical keysets produced different generation markers:\n  %x\n  %x",
			f1.Generation(), f2.Generation())
	}

	// Different keyset must produce different marker.
	other := NewBuilder(uint(n), 1e-6)
	other.Add(makeKey(0xAB)) // a single different key
	fOther, err := other.Build()
	if err != nil {
		t.Fatalf("Build other: %v", err)
	}
	if f1.Generation() == fOther.Generation() {
		t.Errorf("different keysets unexpectedly produced the same generation marker")
	}
}

// TestZeroFalseNegatives verifies that every key added to the filter is possibly-present
// (BLOOM-03 — no false negatives are possible in a correct bloom filter).
func TestZeroFalseNegatives(t *testing.T) {
	const n = 2000
	keys := genKeys(n)

	b := NewBuilder(uint(n), 1e-6)
	for _, k := range keys {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for i, k := range keys {
		if !f.Contains(k) {
			t.Errorf("false negative for member %d", i)
		}
	}
}

// TestMeasuredFPRate builds a filter at the default 1e-6 target over a member set and
// queries a large sample of known non-members, asserting the measured FP rate is at or
// below the target (BLOOM-01, success criterion 1). Non-members are offset well beyond
// the member index range to guarantee disjointness.
func TestMeasuredFPRate(t *testing.T) {
	const memberCount = 10_000
	const sampleSize = 10_000_000 // 1e7
	const targetFP = 1e-6
	const toleranceFactor = 5.0 // allow up to 5x target to absorb sampling variance

	members := genKeys(memberCount)
	b := NewBuilder(uint(memberCount), targetFP)
	for _, k := range members {
		b.Add(k)
	}
	f, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Generate non-members: offset by memberCount + 1 so they cannot coincide.
	falsePositives := 0
	for i := 0; i < sampleSize; i++ {
		var k [32]byte
		v := uint64(memberCount + 1 + i)
		for j := 0; j < 32; j++ {
			k[j] = byte(v >> ((j % 8) * 8))
		}
		if f.Contains(k) {
			falsePositives++
		}
	}

	measured := float64(falsePositives) / float64(sampleSize)
	threshold := targetFP * toleranceFactor
	if measured > threshold {
		t.Errorf("measured FP rate %.2e exceeds threshold %.2e (target=%.2e, FPs=%d/%d)",
			measured, threshold, targetFP, falsePositives, sampleSize)
	}
	t.Logf("measured FP rate: %.4e (target=%.2e, FPs=%d/%d)", measured, targetFP, falsePositives, sampleSize)
}

// TestReadFilterRejectsBadMagic verifies that ReadFilter on bytes with a wrong 4-byte
// magic returns an error satisfying errors.Is(err, ErrBadFormat).
func TestReadFilterRejectsBadMagic(t *testing.T) {
	// Build a valid filter and serialize it, then overwrite the magic.
	f := buildSmall(t)
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Corrupt the first 4 bytes (magic field).
	copy(data[:4], "XXXX")

	_, readErr := ReadFilter(bytes.NewReader(data))
	if readErr == nil {
		t.Fatal("expected error for bad magic, got nil")
	}
	if !isBadFormat(readErr) {
		t.Errorf("expected ErrBadFormat chain, got: %v", readErr)
	}
}

// TestReadFilterRejectsTruncated verifies that ReadFilter on a valid header with a
// payloadLen larger than the available bytes returns an error satisfying
// errors.Is(err, ErrTruncated) (D-07).
func TestReadFilterRejectsTruncated(t *testing.T) {
	f := buildSmall(t)
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// The payloadLen field starts at byte offset: 4 (magic) + 1 (version) + 8 (fpRate) + 32 (gen) = 45.
	const payloadLenOffset = 45
	// Set payloadLen to an absurdly large value so the stream ends before we can read it.
	binary.BigEndian.PutUint64(data[payloadLenOffset:payloadLenOffset+8], 999_999_999)

	_, readErr := ReadFilter(bytes.NewReader(data))
	if readErr == nil {
		t.Fatal("expected error for truncated payload, got nil")
	}
	if !isTruncated(readErr) {
		t.Errorf("expected ErrTruncated chain, got: %v", readErr)
	}
}

// TestAddHexStrict verifies that AddHex returns an error for non-hex or wrong-length
// strings rather than silently ignoring them (D-10 strict boundary contract).
func TestAddHexStrict(t *testing.T) {
	k1 := makeKey(1)
	k2 := makeKey(2)
	k3 := makeKey(3)
	cases := []struct {
		name  string
		input string
	}{
		{"too_short", hex.EncodeToString(k1[:16])},     // 32 chars
		{"too_long", hex.EncodeToString(k2[:]) + "00"}, // 66 chars
		{"non_hex_64", make64CharsWithChar('z')},        // 64 z's
		{"empty", ""},
	}

	b := NewBuilder(10, 1e-3)
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := b.AddHex(tc.input); err == nil {
				t.Errorf("AddHex(%q): expected error, got nil", tc.input)
			}
		})
	}

	// Valid hex must succeed.
	validHex := hex.EncodeToString(k3[:])
	if err := b.AddHex(validHex); err != nil {
		t.Errorf("AddHex(valid): unexpected error: %v", err)
	}
	// Uppercase valid hex must also succeed.
	upper := ""
	for _, c := range validHex {
		if c >= 'a' && c <= 'f' {
			upper += string(rune(c - 32))
		} else {
			upper += string(c)
		}
	}
	if err := b.AddHex(upper); err != nil {
		t.Errorf("AddHex(uppercase valid): unexpected error: %v", err)
	}
}

// TestContainsHexLenient verifies that ContainsHex returns (false, nil) for invalid or
// wrong-length strings without panicking (D-10 lenient query-side contract).
func TestContainsHexLenient(t *testing.T) {
	k := makeKey(0xAA)
	b := NewBuilder(10, 1e-3)
	b.Add(k)
	f, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	badInputs := []struct {
		name  string
		input string
	}{
		{"too_short", hex.EncodeToString(k[:16])},
		{"too_long", hex.EncodeToString(k[:]) + "00"},
		{"non_hex_64", make64CharsWithChar('z')},
		{"empty", ""},
	}

	for _, tc := range badInputs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ok, err := f.ContainsHex(tc.input)
			if err != nil {
				t.Errorf("ContainsHex(%q): expected nil error, got: %v", tc.input, err)
			}
			if ok {
				t.Errorf("ContainsHex(%q): expected false for invalid input, got true", tc.input)
			}
		})
	}

	// Valid hex for a member must be found.
	validHex := hex.EncodeToString(k[:])
	ok, err := f.ContainsHex(validHex)
	if err != nil {
		t.Errorf("ContainsHex(valid member): unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("ContainsHex(valid member): expected true, got false")
	}
}

// buildSmall builds a small filter for use in error-path tests.
func buildSmall(t *testing.T) *Filter {
	t.Helper()
	b := NewBuilder(10, 1e-3)
	b.Add(makeKey(1))
	b.Add(makeKey(2))
	f, err := b.Build()
	if err != nil {
		t.Fatalf("buildSmall: Build: %v", err)
	}
	return f
}

// isBadFormat reports whether err unwraps to ErrBadFormat.
func isBadFormat(err error) bool {
	return isErr(err, ErrBadFormat)
}

// isTruncated reports whether err unwraps to ErrTruncated.
func isTruncated(err error) bool {
	return isErr(err, ErrTruncated)
}

func isErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			return false
		}
	}
	return false
}
