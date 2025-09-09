package whitelist

import (
	"encoding/hex"
	"testing"
)

func makeKey(seed byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

func TestNewWhiteList_IsWhitelisted_and_case_insensitivity(t *testing.T) {
	k := makeKey(0x01)
	hexStr := hex.EncodeToString(k[:])

	wl := NewWhiteList([][32]byte{k})

	// exact lowercase hex -> should be whitelisted
	if !wl.IsWhitelisted(hexStr) {
		t.Fatalf("expected key %s to be whitelisted", hexStr)
	}

	// uppercase hex should also be accepted (IsWhitelisted lowercases input)
	upper := hex.EncodeToString(k[:])
	upper = string([]byte(upper)) // keep same but treat as test of case-insensitivity
	upper = string([]byte(upper))
	upper = hex.EncodeToString(k[:])
	upper = stringsToUpper(upper)
	if !wl.IsWhitelisted(upper) {
		t.Fatalf("expected uppercase key %s to be whitelisted", upper)
	}
}

func TestIsWhitelisted_invalid_length_and_nonhex(t *testing.T) {
	k := makeKey(0x02)
	hexStr := hex.EncodeToString(k[:])

	wl := NewWhiteList([][32]byte{k})

	// too short
	if wl.IsWhitelisted(hexStr[:10]) {
		t.Fatalf("short key unexpectedly considered whitelisted")
	}
	// too long
	if wl.IsWhitelisted(hexStr + "00") {
		t.Fatalf("long key unexpectedly considered whitelisted")
	}

	// invalid hex characters (z is not hex)
	invalid := make64CharsWithChar('z')
	if wl.IsWhitelisted(invalid) {
		t.Fatalf("non-hex key unexpectedly considered whitelisted")
	}
}

func TestIsWhitelisted_empty_and_nil_store(t *testing.T) {
	// NewWhiteList with no keys -> none whitelisted
	wl := NewWhiteList(nil)
	k := makeKey(0x03)
	hexStr := hex.EncodeToString(k[:])
	if wl.IsWhitelisted(hexStr) {
		t.Fatalf("expected no key to be whitelisted for empty whitelist")
	}

	// zero value Whitelist (list pointer nil) should be safe and return false
	var zero Whitelist
	if zero.IsWhitelisted(hexStr) {
		t.Fatalf("expected zero-value Whitelist to return false")
	}
}

func TestNewWhiteList_immutability_of_input_slice(t *testing.T) {
	k := makeKey(0x04)
	keys := [][32]byte{k}

	wl := NewWhiteList(keys)

	// modify original slice element after creating whitelist
	for i := range keys[0] {
		keys[0][i] = 0xFF
	}

	// original hex (before modification) should still be whitelisted
	origHex := hex.EncodeToString(k[:])
	if !wl.IsWhitelisted(origHex) {
		t.Fatalf("expected original key %s to remain whitelisted after mutating input slice", origHex)
	}

	// mutated key should not be whitelisted
	mutHex := hex.EncodeToString(keys[0][:])
	if wl.IsWhitelisted(mutHex) {
		t.Fatalf("mutated key unexpectedly considered whitelisted")
	}
}

// helpers

// make64CharsWithChar returns a 64-char string filled with the provided rune (as a byte).
func make64CharsWithChar(ch byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = ch
	}
	return string(b)
}

// stringsToUpper is a tiny inline helper to avoid importing strings in many tests.
// It upper-cases ASCII hex letters only.
func stringsToUpper(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'f' {
			b[i] = b[i] - 32
		}
	}
	return string(b)
}
