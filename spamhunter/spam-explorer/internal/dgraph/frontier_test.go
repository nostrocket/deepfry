package dgraph

import (
	"strings"
	"testing"
)

// TestExpandFrontierQueryShape asserts the frontier query string SHAPE without a
// live Dgraph (mirrors web-of-trust's package-constant query-assertion pattern).
// The query must root on `func: uid(<csv>)` over the joined UID set and select a
// nested `follows { uid pubkey }` block carrying BOTH uid and pubkey (Pitfall 2:
// BFS keys on UID, output emits pubkey).
func TestExpandFrontierQueryShape(t *testing.T) {
	uids := []string{"0x1", "0x2", "0x3"}
	q := frontierQuery(uids)

	if !strings.Contains(q, "func: uid(") {
		t.Errorf("frontier query missing `func: uid(` root:\n%s", q)
	}

	joined := strings.Join(uids, ", ")
	if !strings.Contains(q, joined) {
		t.Errorf("frontier query missing joined UID set %q:\n%s", joined, q)
	}

	// The nested follows block must request BOTH uid and pubkey.
	if !strings.Contains(q, "follows") {
		t.Errorf("frontier query missing nested follows block:\n%s", q)
	}
	// Strip whitespace to assert the follows block fetches both fields regardless
	// of formatting.
	flat := strings.Join(strings.Fields(q), " ")
	if !strings.Contains(flat, "follows { uid pubkey }") {
		t.Errorf("frontier query follows block must fetch both uid and pubkey, got:\n%s", flat)
	}
}

// TestResolveSeedQueryShape asserts the seed-resolution query interpolates the
// pubkey with %q (quoted/escaped — DQL-injection mitigation, T-01-02) and never
// raw, and roots on eq(pubkey, ...).
func TestResolveSeedQueryShape(t *testing.T) {
	seed := "deadbeef"
	q := resolveSeedQuery(seed)

	if !strings.Contains(q, `eq(pubkey, "deadbeef")`) {
		t.Errorf("resolve query must quote the seed as eq(pubkey, %q), got:\n%s", seed, q)
	}
	if !strings.Contains(q, "uid") || !strings.Contains(q, "pubkey") {
		t.Errorf("resolve query must select uid and pubkey:\n%s", q)
	}
}

// TestResolveSeedQuotesAdversarialSeed proves the %q quoting neutralizes an
// injection-shaped seed (embedded quotes/parens are escaped, not interpreted).
func TestResolveSeedQuotesAdversarialSeed(t *testing.T) {
	adversarial := `x") { uid } injected(func: has(pubkey`
	q := resolveSeedQuery(adversarial)

	// The injected `) {` must not appear unescaped as a query break-out. %q escapes
	// the embedded double-quote, so the literal closing-quote-then-brace from the
	// payload cannot terminate the eq() argument prematurely.
	if strings.Contains(q, `eq(pubkey, "`+adversarial+`")`) {
		t.Errorf("adversarial seed was interpolated raw, not escaped:\n%s", q)
	}
	if !strings.Contains(q, `\"`) {
		t.Errorf("expected the embedded quote to be backslash-escaped by %%q:\n%s", q)
	}
}
