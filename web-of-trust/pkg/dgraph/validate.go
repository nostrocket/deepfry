package dgraph

import (
	"fmt"
	"regexp"
)

// validHexPubkeyRe matches a valid Nostr pubkey: exactly 64 lowercase hex
// characters. This is the single source of truth for pubkey validation across
// the module (promoted from cmd/healthcheck/main.go).
var validHexPubkeyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidatePubkey returns an error if pubkey is not a valid 64-char lowercase
// hex Nostr pubkey, and nil otherwise.
//
// Phase 4 SEC-02's RemoveFollower MUST reuse this helper (or isValidHexPubkey)
// rather than rolling its own validator, so that every pubkey-add/remove site
// shares one definition of "valid pubkey".
func ValidatePubkey(pubkey string) error {
	if !validHexPubkeyRe.MatchString(pubkey) {
		return fmt.Errorf("pubkey %q is not a valid 64-char hex Nostr pubkey", pubkey)
	}
	return nil
}

// isValidHexPubkey is the package-internal fast-path used in hot loops where a
// bool is more ergonomic than an error. It is backed by the same regex as
// ValidatePubkey.
func isValidHexPubkey(pubkey string) bool {
	return validHexPubkeyRe.MatchString(pubkey)
}
