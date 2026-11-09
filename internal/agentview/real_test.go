package agentview

import (
	"os"
	"strings"
	"testing"
	"time"
)

// HMK_AGENTVIEW_REAL=path parses a real session file (not committed).
func TestRealSession(t *testing.T) {
	path := os.Getenv("HMK_AGENTVIEW_REAL")
	if path == "" {
		t.Skip("set HMK_AGENTVIEW_REAL")
	}
	start := time.Now()
	c := feedFile(t, path)
	counts := map[string]int{}
	for _, b := range c.Blocks() {
		counts[strings.TrimPrefix(b.Kind.String(), "KIND_")]++
	}
	t.Logf("%d blocks in %v: %v, %d images", len(c.Blocks()), time.Since(start), counts, len(c.images))
}
