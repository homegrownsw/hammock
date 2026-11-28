package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestQuickReplies(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	get := func(t *testing.T, m *pb.QuickReplyCommand) []string {
		t.Helper()
		r, err := quickReplies(m)
		if err != nil {
			t.Fatal(err)
		}
		return r.GetQuickReplies().Replies
	}

	if got := get(t, &pb.QuickReplyCommand{}); !slices.Equal(got, defaultQuickReplies) {
		t.Fatalf("unset: got %q, want the defaults", got)
	}

	// Set: one line each, blanks dropped, whitespace (newlines too) folded.
	got := get(t, &pb.QuickReplyCommand{Set: true, Replies: []string{" Ship it ", "", "Open a\nPR"}})
	if want := []string{"Ship it", "Open a PR"}; !slices.Equal(got, want) {
		t.Fatalf("set: got %q, want %q", got, want)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "quick-reply = Ship it\nquick-reply = Open a PR") {
		t.Fatalf("config file:\n%s", data)
	}

	// Cleared: the defaults again.
	if got := get(t, &pb.QuickReplyCommand{Set: true}); !slices.Equal(got, defaultQuickReplies) {
		t.Fatalf("cleared: got %q, want the defaults", got)
	}

	// Too many, or too long: refused, and nothing written.
	many := make([]string, maxQuickReplies+1)
	for i := range many {
		many[i] = "x"
	}
	if _, err := quickReplies(&pb.QuickReplyCommand{Set: true, Replies: many}); err == nil {
		t.Fatal("too many: no error")
	}
	if _, err := quickReplies(&pb.QuickReplyCommand{Set: true, Replies: []string{strings.Repeat("x", maxQuickReplyLen+1)}}); err == nil {
		t.Fatal("too long: no error")
	}
}
