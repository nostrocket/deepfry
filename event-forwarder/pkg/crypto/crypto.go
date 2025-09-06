package crypto

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// KeyPair holds all forms of the private and public keys
type KeyPair struct {
	PrivateKeyHex    string // Hex-encoded private key
	PrivateKeyBech32 string // Bech32-encoded private key (nsec)
	PublicKeyHex     string // Hex-encoded public key
	PublicKeyBech32  string // Bech32-encoded public key (npub)
}

// DeriveKeyPair derives all forms of the keys from a private key (hex or nsec format)
// and returns a KeyPair struct.
func DeriveKeyPair(secretKey string) (*KeyPair, error) {
	var skHex string

	// Check if it's a valid hex private key (64 characters, 32 bytes)
	if len(secretKey) == 64 {
		if _, err := hex.DecodeString(secretKey); err == nil {
			skHex = secretKey
		} else {
			return nil, fmt.Errorf("secret key is not a valid hex private key")
		}
	} else {
		// Try to decode as nsec (Bech32)
		prefix, sk, err := nip19.Decode(secretKey)
		if err != nil {
			return nil, fmt.Errorf("secret key is invalid: %w", err)
		}
		if prefix != "nsec" {
			return nil, errors.New("secret key is not an nsec or valid hex")
		}

		switch v := sk.(type) {
		case string:
			skHex = v
		case []byte:
			skHex = hex.EncodeToString(v)
		default:
			return nil, errors.New("secret key is an unexpected nsec payload type")
		}
	}

	// Derive the public key from the hex secret key
	pubHex, err := nostr.GetPublicKey(skHex)
	if err != nil {
		return nil, fmt.Errorf("failed to derive public key: %w", err)
	}

	// Encode the private key to nsec format
	nsec, err := nip19.EncodePrivateKey(skHex)
	if err != nil {
		return nil, fmt.Errorf("failed to encode private key: %w", err)
	}

	// Encode the public key to npub format
	npub, err := nip19.EncodePublicKey(pubHex)
	if err != nil {
		return nil, fmt.Errorf("failed to encode public key: %w", err)
	}

	return &KeyPair{
		PrivateKeyHex:    skHex,
		PrivateKeyBech32: nsec,
		PublicKeyHex:     pubHex,
		PublicKeyBech32:  npub,
	}, nil
}

// DerivePublicKey is kept for backward compatibility, but now returns the hex public key
func DerivePublicKey(secretKey string) (string, error) {
	keyPair, err := DeriveKeyPair(secretKey)
	if err != nil {
		return "", err
	}
	return keyPair.PublicKeyHex, nil
}
