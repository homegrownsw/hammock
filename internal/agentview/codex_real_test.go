package agentview

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/thread"
)

// HMK_AGENTVIEW_CODEX=1 reads every Codex session on this Mac (or the one
// file it names) through the Codex parser, and reports what came out: for
// trying it on real sessions without running Codex. Not in a normal run.
func TestRealCodexSessions(t *testing.T) {
	arg := os.Getenv("HMK_AGENTVIEW_CODEX")
	if arg == "" {
		t.Skip("set HMK_AGENTVIEW_CODEX=1 (or a rollout path)")
	}
	var files []string
	if arg != "1" {
		files = []string{arg}
	} else {
		home, _ := os.UserHomeDir()
		filepath.WalkDir(filepath.Join(home, ".codex", "sessions"), func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && strings.HasPrefix(e.Name(), "rollout-") {
				files = append(files, p)
			}
			return nil
		})
	}
	kinds, titles := map[string]int{}, map[string]int{}
	pending, blocks := 0, 0
	for _, f := range files {
		r, err := thread.OpenTranscript(f)
		if err != nil {
			t.Fatal(err)
		}
		c := NewCodex("")
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			c.Feed(sc.Bytes())
		}
		r.Close()
		if err := sc.Err(); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		for i, b := range c.Blocks() {
			blocks++
			kinds[strings.TrimPrefix(b.Kind.String(), "KIND_")]++
			if b.Kind == pb.AgentBlock_KIND_TOOL {
				w := strings.Fields(b.Title)
				if len(w) > 0 {
					titles[w[0]+" ("+b.Tool+")"]++
				}
				if b.Pending && i < len(c.Blocks())-3 {
					pending++ // a call that never got its result, not at the end
				}
			}
		}
		if arg != "1" {
			for _, b := range c.Blocks() {
				t.Logf("%-8s %-14s %s | in=%q out=%q diff=%d+%d err=%v", strings.TrimPrefix(b.Kind.String(), "KIND_"), b.Tool, oneLine(firstOf(b.Title, b.Text, 90)),
					clipTo(b.Input, 60), clipTo(b.Output, 60), b.Added, b.Removed, b.Error)
			}
			m, tok, win := c.Context()
			t.Logf("model %s, context %d of %d", m, tok, win)
		}
	}
	t.Logf("%d sessions, %d blocks: %v", len(files), blocks, kinds)
	var ts []string
	for k, n := range titles {
		ts = append(ts, k+" ×"+itoa(n))
	}
	sort.Strings(ts)
	t.Logf("tool titles: %s", strings.Join(ts, ", "))
	t.Logf("calls with no result (not at a session's end): %d", pending)
}

func firstOf(a, b string, n int) string {
	if a != "" {
		return clipTo(a, n)
	}
	return clipTo(b, n)
}

func clipTo(s string, n int) string {
	s = oneLine(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func itoa(n int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(n)) }

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}
