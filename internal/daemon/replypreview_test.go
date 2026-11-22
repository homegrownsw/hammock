package daemon

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReplyPreview(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  \n\n ", ""},
		{"Done.", "Done."},
		{"First line.\n\nSecond   line,\tand a third.", "First line. Second line, and a third."},
		{"# Summary\n\n## What changed?\nIt works.\n#\n#hashtag", "Summary: What changed? It works. #hashtag"},
		{"**Bold**, *em*, __strong__, _under_ and ~~gone~~.", "Bold, em, strong, under and gone."},
		{"Kept snake_case_name, 2 * 3 * 4 and a*b.", "Kept snake_case_name, 2 * 3 * 4 and a*b."},
		{"_a_ _b_", "a b"},
		{"Ran `go test ./...` and `*args`.", "Ran go test ./... and *args."},
		{"See [the docs](https://example.com/docs) and ![a chart](chart.png) at <https://example.com>.", "See the docs and a chart at https://example.com."},
		{"Steps:\n- one\n* two\n+ three\n- [x] done\n1. first", "Steps: one two three done 1. first"},
		{"> Quoted\n> > twice\n> - in a list", "Quoted twice in a list"},
		{"Before\n```go\nfunc main() {}\n```\nAfter", "Before After"},
		{"~~~\ncode\n~~~", ""},
		{"```\nnever closed", ""},
		{"Above\n\n---\n\n| a | b |\n|---|:-:|\n| 1 | 2 |", "Above | a | b | | 1 | 2 |"},
		{`An \*escaped\* star`, "An *escaped* star"},
	} {
		if got := replyPreview(c.in); got != c.want {
			t.Errorf("replyPreview(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReplyPreviewCuts(t *testing.T) {
	long := strings.Repeat("word ", 100)
	got := replyPreview(long)
	if !strings.HasSuffix(got, "word…") || utf8.RuneCountInString(got) > replyPreviewMax+1 {
		t.Errorf("cut to %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	// On the space before the word that runs past the end, without the
	// comma before it.
	in := strings.Repeat("a", replyPreviewMax-10) + ", overflowing words"
	if got, want := replyPreview(in), strings.Repeat("a", replyPreviewMax-10)+"…"; got != want {
		t.Errorf("cut to %q, want %q", got, want)
	}
	// A word that ends right at the limit is kept whole.
	in = strings.Repeat("b", replyPreviewMax) + " more"
	if got, want := replyPreview(in), strings.Repeat("b", replyPreviewMax)+"…"; got != want {
		t.Errorf("cut to %q, want %q", got, want)
	}
	// No space to cut on: in the word, max runes in all.
	in = strings.Repeat("é", replyPreviewMax*2)
	if got := replyPreview(in); utf8.RuneCountInString(got) != replyPreviewMax || !strings.HasSuffix(got, "…") {
		t.Errorf("cut to %d runes: %q", utf8.RuneCountInString(got), got)
	}
	// Just short enough: untouched.
	in = strings.Repeat("c", replyPreviewMax)
	if got := replyPreview(in); got != in {
		t.Errorf("cut %q", got)
	}
}
