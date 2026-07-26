package splithttp

import (
	"regexp"
	"testing"
)

// Beeline CDN rejects the stock 36-char UUID XHTTP session ID with HTTP 403.
// The patched dialer must emit a 12-char Base64URL id from the [A-Za-z0-9_-]
// alphabet. This guards §10.4 / §10.9 of the integration spec: the short
// session ID must survive any upstream Xray update.
func TestNewBeelineSessionID(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9_-]{12}$`)
	seen := make(map[string]struct{})
	const iterations = 10000
	for i := 0; i < iterations; i++ {
		id := newBeelineSessionID()
		if !re.MatchString(id) {
			t.Fatalf("session id %q does not match ^[A-Za-z0-9_-]{12}$", id)
		}
		seen[id] = struct{}{}
	}
	// 72 bits of entropy: collisions across 10k samples are astronomically
	// unlikely, so a large duplicate rate would signal a broken generator.
	if len(seen) < iterations-1 {
		t.Fatalf("session ids not sufficiently unique: %d distinct of %d", len(seen), iterations)
	}
}
