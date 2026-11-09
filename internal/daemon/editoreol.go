package daemon

import (
	"bytes"
	"errors"
	"strings"

	"github.com/alexjsp/hammock/internal/textbuf"
)

// errReadOnly refuses changing or saving over a file you may not write
// (Save As to somewhere else still works).
var errReadOnly = errors.New("the file is read-only: you don't have permission to change it (Save As makes a copy)")

// lineEnds is how a file's lines end. The buffer's text is all "\n"; saving
// puts the file's own endings back. A file with both keeps each line's: a
// line that's unchanged ends as it did, a new or changed one as most did.
type lineEnds struct {
	crlf bool   // most lines end "\r\n" (new lines do too)
	per  []int8 // mixed files only: each line's ending (endLF, endCRLF; 0 for the last line)
}

const (
	endLF   = 1
	endCRLF = 2
)

// normaliseText turns a file's bytes into the buffer's text, "\r\n" lines
// becoming "\n", and how its lines ended.
func normaliseText(b []byte) (textbuf.Text, lineEnds) {
	crlf := bytes.Count(b, []byte("\r\n"))
	if crlf == 0 {
		return textbuf.FromString(string(b)), lineEnds{}
	}
	lf := bytes.Count(b, []byte("\n")) - crlf
	e := lineEnds{crlf: crlf >= lf}
	if lf > 0 {
		e.per = make([]int8, 0, crlf+lf+1)
		for rest := b; ; {
			i := bytes.IndexByte(rest, '\n')
			if i < 0 {
				e.per = append(e.per, 0)
				break
			}
			if i > 0 && rest[i-1] == '\r' {
				e.per = append(e.per, endCRLF)
			} else {
				e.per = append(e.per, endLF)
			}
			rest = rest[i+1:]
		}
	}
	return textbuf.FromString(string(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")))), e
}

// render is text as the file's bytes: disk is the text as last read or
// written, whose lines e describes. It gives the endings written, for next
// time.
func (e lineEnds) render(text, disk textbuf.Text) ([]byte, lineEnds) {
	s := text.String()
	if e.per == nil {
		if e.crlf {
			s = strings.ReplaceAll(s, "\n", "\r\n")
		}
		return []byte(s), e
	}
	lines, old := strings.Split(s, "\n"), textbuf.Lines(disk)
	ends := make([]int8, len(lines))
	i, j := 0, 0
	keep := func(to int) { // unchanged lines up to new line to
		for ; j < to; i, j = i+1, j+1 {
			if i < len(e.per) {
				ends[j] = e.per[i]
			}
		}
	}
	for _, c := range textbuf.DiffLines(old, lines) {
		keep(c.NewFrom)
		i, j = c.OldTo, c.NewTo
	}
	keep(len(lines))
	var out bytes.Buffer
	out.Grow(len(s) + len(lines))
	for k, l := range lines {
		out.WriteString(l)
		if k == len(lines)-1 {
			ends[k] = 0
			break
		}
		if ends[k] == 0 {
			ends[k] = endLF
			if e.crlf {
				ends[k] = endCRLF
			}
		}
		if ends[k] == endCRLF {
			out.WriteString("\r\n")
		} else {
			out.WriteString("\n")
		}
	}
	return out.Bytes(), lineEnds{crlf: e.crlf, per: ends}
}
