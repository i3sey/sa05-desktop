package update

import (
	"context"
	"os"
	"testing"
)

// TestRealFeed talks to the live GitHub feed. It is opt-in because it depends on what is
// published right now; run with SA05_LIVE_FEED=1.
func TestRealFeed(t *testing.T) {
	if os.Getenv("SA05_LIVE_FEED") == "" {
		t.Skip("нужен SA05_LIVE_FEED=1")
	}
	checker := &Checker{Current: "v0.0.9"}
	update, ok, err := checker.Check(context.Background())
	t.Logf("ok=%v update=%+v err=%v", ok, update, err)
}
