package crypto

import (
	"testing"

	"event-forwarder/pkg/testutil"
)

func TestDeriveKeyPair_ValidNsec(t *testing.T) {
	keyPair, err := DeriveKeyPair(testutil.TestSK)
	if err != nil {
		t.Fatalf("expected no error for valid nsec key, got %v", err)
	}
	if keyPair.PrivateKeyHex == "" {
		t.Error("expected private key hex to be derived")
	}
	if keyPair.PrivateKeyBech32 != testutil.TestSK {
		t.Errorf("expected private key bech32 %s, got %s", testutil.TestSK, keyPair.PrivateKeyBech32)
	}
	if keyPair.PublicKeyHex == "" {
		t.Error("expected public key hex to be derived")
	}
	if keyPair.PublicKeyBech32 != testutil.TestPK {
		t.Errorf("expected public key bech32 %s, got %s", testutil.TestPK, keyPair.PublicKeyBech32)
	}
}

func TestDeriveKeyPair_ValidHex(t *testing.T) {
	keyPair, err := DeriveKeyPair(testutil.TestSKHex)
	if err != nil {
		t.Fatalf("expected no error for valid hex key, got %v", err)
	}
	if keyPair.PrivateKeyHex != testutil.TestSKHex {
		t.Errorf("expected private key hex %s, got %s", testutil.TestSKHex, keyPair.PrivateKeyHex)
	}
	if keyPair.PrivateKeyBech32 == "" {
		t.Error("expected private key bech32 to be derived")
	}
	if keyPair.PublicKeyHex != testutil.TestPKHex {
		t.Errorf("expected public key hex %s, got %s", testutil.TestPKHex, keyPair.PublicKeyHex)
	}
	if keyPair.PublicKeyBech32 == "" {
		t.Error("expected public key bech32 to be derived")
	}
}

func TestDeriveKeyPair_Invalid(t *testing.T) {
	_, err := DeriveKeyPair("invalid")
	if err == nil {
		t.Fatal("expected error for invalid key, got nil")
	}
}

func TestDeriveKeyPair_InvalidHex(t *testing.T) {
	_, err := DeriveKeyPair("invalidhexstring")
	if err == nil {
		t.Fatal("expected error for invalid hex key, got nil")
	}
}

func TestDeriveKeyPair_WrongPrefix(t *testing.T) {
	// Use a valid npub as nsec to trigger wrong prefix
	_, err := DeriveKeyPair(testutil.TestPK) // npub instead of nsec
	if err == nil {
		t.Fatal("expected error for wrong prefix, got nil")
	}
}

func TestDerivePublicKey_ValidNsec(t *testing.T) {
	pubKey, err := DerivePublicKey(testutil.TestSK)
	if err != nil {
		t.Fatalf("expected no error for valid nsec key, got %v", err)
	}
	if pubKey == "" {
		t.Error("expected public key to be derived")
	}
	if pubKey != testutil.TestPKHex {
		t.Errorf("expected public key %s, got %s", testutil.TestPKHex, pubKey)
	}
}

func TestDerivePublicKey_ValidHex(t *testing.T) {
	pubKey, err := DerivePublicKey(testutil.TestSKHex)
	if err != nil {
		t.Fatalf("expected no error for valid hex key, got %v", err)
	}
	if pubKey == "" {
		t.Error("expected public key to be derived")
	}
	if pubKey != testutil.TestPKHex {
		t.Errorf("expected public key %s, got %s", testutil.TestPKHex, pubKey)
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
	_, err := DerivePublicKey(testutil.TestPK) // npub instead of nsec
	if err == nil {
		t.Fatal("expected error for wrong prefix, got nil")
	}
}
