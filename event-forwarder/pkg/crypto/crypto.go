package crypto

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

// DerivePublicKey derives the public key from a secret key (hex or nsec format)
// and returns it in npub format.
func DerivePublicKey(secretKey string) (string, error) {
	var skHex string

	// Check if it's a valid hex private key (64 characters, 32 bytes)
	if len(secretKey) == 64 {
		if _, err := hex.DecodeString(secretKey); err == nil {
			skHex = secretKey
		} else {
			return "", fmt.Errorf("secret key is not a valid hex private key")
		}
	} else {
		// Try to decode as nsec (Bech32)
		prefix, sk, err := nip19.Decode(secretKey)
		if err != nil {
			return "", fmt.Errorf("secret key is invalid: %w", err)
		}
		if prefix != "nsec" {
			return "", errors.New("secret key is not an nsec or valid hex")
		}

		switch v := sk.(type) {
		case string:
			skHex = v
		case []byte:
			skHex = hex.EncodeToString(v)
		default:
			return "", errors.New("secret key is an unexpected nsec payload type")
		}
	}

	// Derive the public key from the hex secret key
	pubHex, err := nostr.GetPublicKey(skHex)
	if err != nil {
		return "", fmt.Errorf("failed to derive public key: %w", err)
	}

	// Encode the public key to npub format
	npub, err := nip19.EncodePublicKey(pubHex)
	if err != nil {
		return "", fmt.Errorf("failed to encode public key: %w", err)
	}

	return npub, nil
}
