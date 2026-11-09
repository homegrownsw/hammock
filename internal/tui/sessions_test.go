package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 15, 0, 0, 0, time.Local)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	for _, tc := range []struct {
		ms   int64
		want string
	}{
		{ago(10 * time.Second), "just now"},
		{ago(12 * time.Minute), "12 min ago"},
		{ago(70 * time.Minute), "1 hour ago"},
		{ago(5 * time.Hour), "5 hours ago"},
		{ago(16 * time.Hour), "yesterday"},
		{ago(3 * 24 * time.Hour), "3 days ago"},
		{time.Date(2026, 3, 12, 9, 0, 0, 0, time.Local).UnixMilli(), "12 Mar"},
		{time.Date(2025, 3, 12, 9, 0, 0, 0, time.Local).UnixMilli(), "12 Mar 2025"},
		{0, ""},
	} {
		if got := relTime(tc.ms, now); got != tc.want {
			t.Errorf("%v: %q, want %q", time.UnixMilli(tc.ms), got, tc.want)
		}
	}
}

func TestTruncLeft(t *testing.T) {
	if got := truncLeft("~/Developer/Fika", 10); !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "/Fika") || textWidth(got) != 10 {
		t.Errorf("%q", got)
	}
	if got := truncLeft("~/x", 10); got != "~/x" {
		t.Errorf("%q", got)
	}
}

func TestAltEnterKey(t *testing.T) {
	ks := keysOf([]byte("\x1b\r"))
	if len(ks) != 1 || ks[0].name != "alt-enter" {
		t.Fatalf("%+v", ks)
	}
	if ks := keysOf([]byte("\x1b")); len(ks) != 1 || ks[0].name != "esc" {
		t.Fatalf("%+v", ks)
	}
	if ks := keysOf([]byte{0x0f}); len(ks) != 1 || ks[0].name != "ctrl-o" {
		t.Fatalf("%+v", ks)
	}
}

// rowText is what row y of the screen reads.
func rowText(s *Screen, y int) string {
	var b strings.Builder
	for x := 0; x < s.W; x++ {
		c := s.Get(x, y)
		if c.Cont {
			continue
		}
		if c.Ch == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Ch)
		}
	}
	return b.String()
}

func screenText(s *Screen) string {
	var lines []string
	for y := 0; y < s.H; y++ {
		lines = append(lines, strings.TrimRight(rowText(s, y), " "))
	}
	return strings.Join(lines, "\n")
}

func TestSessionFinderDraws(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	home, _ := os.UserHomeDir()
	now := time.Now()
	a := &App{w: 120, h: 30, scr: NewScreen(120, 30), th: newTheme(RGB{30, 30, 30}, RGB{220, 220, 220}),
		anim: newSprings(), actions: make(chan func(), 64), tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}}
	f := newSessionFinder("g1")
	a.overlay = f
	f.setList(&pb.SessionList{Indexing: true, Sessions: []*pb.SessionInfo{
		{Harness: "claude", Id: "c1", Title: "Fix the sidebar\nanimation", Cwd: home + "/Developer/Fika",
			UpdatedUnixMs: now.Add(-12 * time.Minute).UnixMilli(), Messages: 34, Snippet: "the «sidebar» springs", OpenPaneId: "p1"},
		{Harness: "codex", Id: "x1", Title: "Port the parser", Cwd: "/tmp/elsewhere",
			UpdatedUnixMs: now.Add(-30 * time.Hour).UnixMilli(), Messages: 1, LastPrompt: "and the tests"},
	}})
	f.draw(a)
	out := screenText(a.scr)
	t.Log("\n" + out)
	for _, want := range []string{"Indexing…", "✻", "Fix the sidebar animation", "~/Developer/Fika", "12 min ago", "34 msgs",
		"● open", "◇", "Port the parser", "1 msg", "the sidebar springs", "↵ Go to Pane", "Continue in Codex"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "«") {
		t.Errorf("snippet markers drawn:\n%s", out)
	}

	// Down selects the next session; its last prompt shows below.
	f.key(a, keysOf([]byte("\x1b[B")))
	a.scr.Clear()
	f.draw(a)
	out = screenText(a.scr)
	for _, want := range []string{"and the tests", "↵ Resume in Codex", "Continue in Claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("after ↓, missing %q in\n%s", want, out)
		}
	}

	// A refresh keeps the selection on the same session.
	f.setList(&pb.SessionList{Sessions: []*pb.SessionInfo{{Harness: "claude", Id: "new"}, f.list[0], f.list[1]}})
	if f.selected().GetId() != "x1" {
		t.Errorf("selection moved to %q", f.selected().GetId())
	}

	// Esc closes.
	f.key(a, keysOf([]byte("\x1b")))
	if a.overlay != nil {
		t.Error("esc didn't close the finder")
	}
}
