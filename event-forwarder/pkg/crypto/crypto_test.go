package crypto

import (
	"testing"
)

const testSK = "nsec19nzdqz0awf73vmhtptexj32fyjjufrt62whzfa9mfakcaml5vckqukyjyp"
const testPK = "npub1u4kr6t7cuqcfye89tqcf4ej7xyeglc9zu8lzdn6qwj5078053lpq2qwka7"
const testSK_hex = "2cc4d009fd727d166eeb0af269454924a5c48d7a53ae24f4bb4f6d8eeff4662c"
const testPK_hex = "e56c3d2fd8e0309264e558309ae65e31328fe0a2e1fe26cf4074a8ff1df48fc2"

func TestDerivePublicKey_ValidNsec(t *testing.T) {
	pubKey, err := DerivePublicKey(testSK)
	if err != nil {
		t.Fatalf("expected no error for valid nsec key, got %v", err)
	}
	if pubKey == "" {
		t.Error("expected public key to be derived")
	}
	if pubKey != testPK {
		t.Errorf("expected public key %s, got %s", testPK, pubKey)
	}
}

func TestDerivePublicKey_ValidHex(t *testing.T) {
	pubKey, err := DerivePublicKey(testSK_hex)
	if err != nil {
		t.Fatalf("expected no error for valid hex key, got %v", err)
	}
	if pubKey == "" {
		t.Error("expected public key to be derived")
	}
	if pubKey != testPK {
		t.Errorf("expected public key %s, got %s", testPK, pubKey)
	}
}

func TestDerivePublicKey_Invalid(t *testing.T) {
	_, err := DerivePublicKey("invalid")
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
}

func TestDerivePublicKey_InvalidHex(t *testing.T) {
	_, err := DerivePublicKey("invalidhexstring")
	if err == nil {
		t.Fatal("expected error for invalid hex key, got nil")
	}
}

func TestDerivePublicKey_WrongPrefix(t *testing.T) {
	// Use a valid npub as nsec to trigger wrong prefix
	_, err := DerivePublicKey(testPK) // npub instead of nsec
	if err == nil {
		t.Fatal("expected error for wrong prefix, got nil")
	}
}
