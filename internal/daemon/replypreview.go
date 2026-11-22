package daemon

import (
	"regexp"
	"strings"
	"unicode"
)

// replyPreviewMax is about how long a finished agent's summary runs, in
// runes: the start of its reply, for its notification ("Claude: I've
// fixed…") and its card.
const replyPreviewMax = 200

var (
	// What starts a line: quotes, headings, list bullets (and their task
	// boxes), any mix of them ("> - ").
	mdLineMark = regexp.MustCompile(`^(?:>\s*|#{1,6}(?:\s+|$)|[-*+]\s+(?:\[[ xX]\]\s+)?)`)
	// Lines that are only marks: rules, setext underlines, a table's
	// header line.
	mdRuleLine = regexp.MustCompile(`^(?:[-*_=]\s*){3,}$|^\|?(?:\s*:?-+:?\s*\|)+\s*:?-*:?\s*\|?$`)
	mdCode     = regexp.MustCompile("`+([^`]+?)`+")
	mdImage    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	mdAutolink = regexp.MustCompile(`<((?:https?|mailto):[^>\s]+)>`)
	mdStrong   = regexp.MustCompile(`\*\*([^\s*](?:[^*]*?[^\s*])?)\*\*`)
	mdEm       = regexp.MustCompile(`\*([^\s*](?:[^*]*?[^\s*])?)\*`)
	mdStrike   = regexp.MustCompile(`~~([^\s~](?:[^~]*?[^\s~])?)~~`)
	// Underscores only mark emphasis at a word's edge (snake_case isn't).
	mdUnder2 = regexp.MustCompile(`(^|[^\p{L}\p{N}_])__([^\s_](?:[^_]*?[^\s_])?)__([^\p{L}\p{N}_]|$)`)
	mdUnder  = regexp.MustCompile(`(^|[^\p{L}\p{N}_])_([^\s_](?:[^_]*?[^\s_])?)_([^\p{L}\p{N}_]|$)`)
	mdEscape = regexp.MustCompile("\\\\([\\\\`*_{}\\[\\]()#+\\-.!>~|])")
)

// replyPreview is the start of an agent's reply, as a finished agent's
// summary: its markdown taken out (code blocks dropped; headings, quotes,
// bullets, emphasis, code and link marks off, a link's text kept), on one
// line, cut on a word at about replyPreviewMax runes ("…" when it is). ""
// for a reply with nothing left.
func replyPreview(s string) string {
	var words []string
	fence := ""
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if fence != "" { // in a code block
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		heading := false
		for i := 0; i < 4; i++ { // "> - [ ] x"
			u := mdLineMark.ReplaceAllString(t, "")
			if u == t {
				break
			}
			heading = heading || strings.HasPrefix(t, "#")
			t = strings.TrimSpace(u)
		}
		if t == "" || mdRuleLine.MatchString(t) {
			continue
		}
		// A heading reads into what follows it: "Done: I renamed…".
		if heading && !strings.ContainsAny(t[len(t)-1:], ".:!?") {
			t += ":"
		}
		words = append(words, t)
	}
	s = strings.Join(strings.Fields(mdInline(strings.Join(words, " "))), " ")
	return cutWords(s, replyPreviewMax)
}

// mdInline takes markdown's inline marks out of s, leaving inline code's
// text as it is.
func mdInline(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range mdCode.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(mdMarks(s[last:m[0]]))
		b.WriteString(strings.TrimSpace(s[m[2]:m[3]]))
		last = m[1]
	}
	b.WriteString(mdMarks(s[last:]))
	return b.String()
}

// mdMarks takes links, images and emphasis marks out of s (not code).
func mdMarks(s string) string {
	// Escaped marks are text: kept out of the way meanwhile, as runes of
	// a private use plane.
	s = mdEscape.ReplaceAllStringFunc(s, func(m string) string { return string(rune(escapedBase + int(m[1]))) })
	s = mdImage.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdAutolink.ReplaceAllString(s, "$1")
	s = mdStrong.ReplaceAllString(s, "$1")
	s = mdEm.ReplaceAllString(s, "$1")
	s = mdStrike.ReplaceAllString(s, "$1")
	// Each match takes the character after it, which may start the next:
	// again until none's left ("_a_ _b_").
	for i := 0; i < 4; i++ {
		u := mdUnder.ReplaceAllString(mdUnder2.ReplaceAllString(s, "${1}${2}${3}"), "${1}${2}${3}")
		if u == s {
			break
		}
		s = u
	}
	return strings.Map(func(r rune) rune {
		if r >= escapedBase && r < escapedBase+0x80 {
			return r - escapedBase
		}
		return r
	}, s)
}

// escapedBase is where mdMarks keeps escaped ASCII marks.
const escapedBase = 0xF0000

// cutWords cuts s to at most max runes and "…", on a space when there's
// one in its last half.
func cutWords(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := max - 1 // no space to cut on: in the word, leaving room for "…"
	for i := max; i >= max/2; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimRightFunc(string(r[:cut]), func(c rune) bool {
		return unicode.IsSpace(c) || strings.ContainsRune(",;:-–—", c)
	}) + "…"
}
