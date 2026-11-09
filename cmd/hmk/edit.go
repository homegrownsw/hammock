package main

// hmk edit: open a file in an editor pane (the daemon owns its buffer),
// point at lines in one, and read one's text, unsaved edits included.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/textbuf"
)

const editUsage = `usage: hmk edit FILE[:LINE[:COL][-LINE[:COL]]] [--line LINE[:COL]] [--to LINE[:COL]]
                [--down | --tab | --split PANE [--down] | --group G] [--large]
       hmk edit --read PANE [--numbers] [--lines FROM-TO] [--json]
       hmk edit --select PANE LINE[:COL][-LINE[:COL]]   (LINE alone: all of it)`

// wholeLine is an end column past any line's end: the daemon clamps it, so
// an end line without a column selects all of that line.
const wholeLine = 1 << 30

// textRange is a selection in 1-based lines and columns (columns in UTF-16
// units; 0: the line's start, or for the end, the line's end).
type textRange struct {
	Line, Col, EndLine, EndCol uint32
}

// parsePos reads LINE[:COL].
func parsePos(s string) (line, col uint32, err error) {
	l, c, hasCol := strings.Cut(s, ":")
	n, err := strconv.ParseUint(l, 10, 32)
	if err != nil || n == 0 {
		return 0, 0, fmt.Errorf("%q isn't LINE[:COL] (lines count from 1)", s)
	}
	if hasCol {
		m, err := strconv.ParseUint(c, 10, 32)
		if err != nil || m == 0 {
			return 0, 0, fmt.Errorf("%q isn't LINE[:COL] (columns count from 1)", s)
		}
		col = uint32(m)
	}
	return uint32(n), col, nil
}

// parseRange reads LINE[:COL][-LINE[:COL]]. An end without a column runs
// to the end of its line.
func parseRange(s string) (textRange, error) {
	from, to, hasTo := strings.Cut(s, "-")
	var r textRange
	var err error
	if r.Line, r.Col, err = parsePos(from); err != nil {
		return r, err
	}
	if hasTo {
		if r.EndLine, r.EndCol, err = parsePos(to); err != nil {
			return r, err
		}
		if r.EndCol == 0 {
			r.EndCol = wholeLine
		}
		if r.EndLine < r.Line || (r.EndLine == r.Line && r.EndCol < r.Col) {
			return r, fmt.Errorf("%q ends before it starts", s)
		}
	}
	return r, nil
}

var posSuffix = regexp.MustCompile(`^(.+?):(\d+(?::\d+)?(?:-\d+(?::\d+)?)?)$`)

// splitPathPos splits FILE:LINE[:COL][-LINE[:COL]] (as compilers and grep
// print them). A file whose name really ends like that wins, if it exists.
func splitPathPos(arg string, exists func(string) bool) (string, textRange, error) {
	m := posSuffix.FindStringSubmatch(arg)
	if m == nil || exists(arg) {
		return arg, textRange{}, nil
	}
	r, err := parseRange(m[2])
	return m[1], r, err
}

func fileExists(p string) bool {
	_, err := os.Stat(expandTilde(p))
	return err == nil
}

// resolvePath is path as the daemon keys its buffers: absolute (against
// base), clean, symlinks followed where it exists.
func resolvePath(path, base string) string {
	path = expandTilde(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(dir, filepath.Base(path))
	}
	return path
}

// editorInTab finds an editor pane in tab tabID already showing path.
func (w *world) editorInTab(tabID, path string) string {
	t := w.tabs[tabID]
	if t == nil {
		return ""
	}
	for _, id := range tabPaneIDs(t) {
		if p := w.panes[id]; p != nil && p.Kind == pb.PaneKind_PANE_KIND_EDITOR && p.GetEditor().GetPath() == path {
			return id
		}
	}
	return ""
}

// editorPane resolves ref to an editor pane (or a tab whose focused pane is one).
func (w *world) editorPane(ref string) (*pb.Pane, error) {
	id, err := w.logPane(ref)
	if err != nil {
		return nil, err
	}
	p := w.panes[id]
	if p == nil || p.Kind != pb.PaneKind_PANE_KIND_EDITOR {
		return nil, fmt.Errorf("%s isn't an editor pane", id)
	}
	return p, nil
}

func selectMsg(paneID string, r textRange) *pb.ClientMessage {
	return &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{
		PaneId: paneID, Action: pb.EditorCommand_ACTION_SELECT, Line: r.Line, Column: r.Col, EndLine: r.EndLine, EndColumn: r.EndCol,
	}}}
}

// largeFileError turns the daemon's "large file: " refusal into what to do.
func largeFileError(err error, how string) error {
	msg, ok := strings.CutPrefix(err.Error(), "large file: ")
	if !ok {
		return err
	}
	msg = strings.TrimSuffix(strings.TrimSuffix(msg, "open it anyway?"), ": ")
	return fmt.Errorf("%s: open it anyway with %s", msg, how)
}

// tabPane is a created id as a pane: a new tab's (focused) pane.
func tabPane(call caller, id string) string {
	if !strings.HasPrefix(id, "tb_") {
		return id
	}
	if s, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}); err == nil {
		for _, t := range s.GetState().GetTabs() {
			if t.Id == id && t.FocusedPaneId != "" {
				return t.FocusedPaneId
			}
		}
	}
	return id
}

func runEdit(w *world, call caller, args []string) error {
	usage := errors.New(editUsage)
	args = args[1:]
	if hasFlag(args, "--read") {
		ref := flagValue(args, "--read")
		if ref == "" || strings.HasPrefix(ref, "-") {
			return usage
		}
		return runEditRead(w, call, ref, dropFlag(args, "--read"))
	}
	if hasFlag(args, "--select") {
		ref, rest := flagValue(args, "--select"), dropFlag(args, "--select")
		if ref == "" || len(rest) != 1 {
			return usage
		}
		p, err := w.editorPane(ref)
		if err != nil {
			return err
		}
		r, err := parseRange(rest[0])
		if err != nil {
			return err
		}
		if r.EndLine == 0 && r.Col == 0 {
			r.EndLine, r.EndCol = r.Line, wholeLine // a line on its own: all of it
		}
		_, err = call(selectMsg(p.Id, r))
		return err
	}

	var file, line, to, split, group string
	var down, newTab, large bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s wants a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--line":
			line, err = value()
		case "--to":
			to, err = value()
		case "--split":
			split, err = value()
		case "--group":
			group, err = value()
		case "--down":
			down = true
		case "--tab":
			newTab = true
		case "--large":
			large = true
		default:
			if strings.HasPrefix(a, "-") || file != "" {
				return usage
			}
			file = a
		}
		if err != nil {
			return err
		}
	}
	if file == "" {
		return usage
	}
	path, r, err := splitPathPos(file, fileExists)
	if err != nil {
		return err
	}
	if line != "" {
		if r.Line, r.Col, err = parsePos(line); err != nil {
			return err
		}
	}
	if to != "" {
		if r.Line == 0 {
			return errors.New("--to needs a start: FILE:LINE or --line")
		}
		if r.EndLine, r.EndCol, err = parsePos(to); err != nil {
			return err
		}
		if r.EndCol == 0 {
			r.EndCol = wholeLine
		}
	}
	wd, _ := os.Getwd()
	path = resolvePath(path, wd)

	// Where: beside this pane (or the one named), else a new tab.
	beside := split
	if beside != "" {
		if beside, err = w.resolve(beside, "pane"); err != nil {
			return err
		}
	} else if !newTab && group == "" {
		if me := paneEnv("PANE_ID"); w.panes[me] != nil {
			beside = me
		}
	}
	if beside != "" {
		if id := w.editorInTab(w.panes[beside].TabId, path); id != "" {
			if r.Line > 0 {
				if _, err := call(selectMsg(id, r)); err != nil {
					return err
				}
			}
			fmt.Println(id)
			return nil
		}
	}
	spec := &pb.PaneSpec{CreatedByPaneId: paneEnv("PANE_ID"), Editor: &pb.EditorOpen{
		Path: path, Line: r.Line, Column: r.Col, EndLine: r.EndLine, EndColumn: r.EndCol, Large: large, Cwd: wd,
	}}
	var m *pb.ClientMessage
	if beside != "" {
		dir := pb.Split_DIRECTION_HORIZONTAL
		if down {
			dir = pb.Split_DIRECTION_VERTICAL
		}
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: beside, Direction: dir, Spec: spec}}}
	} else {
		gid, err := w.browserGroup(group)
		if err != nil {
			return err
		}
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: spec}}}
	}
	res, err := call(m)
	if err != nil {
		return largeFileError(err, "--large")
	}
	fmt.Println(tabPane(call, client.CreatedID(res)))
	return nil
}

// editorView is an editor pane's buffer as hmk edit --read --json and
// editor_read give it.
type editorView struct {
	PaneID    string         `json:"pane_id"`
	Path      string         `json:"path,omitempty"`
	Name      string         `json:"name,omitempty"`
	Dirty     bool           `json:"dirty,omitempty"`
	Conflict  bool           `json:"conflict,omitempty"`
	Missing   bool           `json:"missing,omitempty"`
	ReadOnly  bool           `json:"read_only,omitempty"`
	Lines     int            `json:"lines"`
	Showing   string         `json:"showing,omitempty"` // FROM-TO, when not every line is in text
	Selection *selectionView `json:"selection,omitempty"`
	Text      string         `json:"text"`
}

type selectionView struct {
	From  string `json:"from"` // LINE:COL, the earlier end
	To    string `json:"to"`
	Empty bool   `json:"empty,omitempty"` // just the cursor
	Text  string `json:"text,omitempty"`
}

// describeSelection is a selection's ends as LINE:COL and its text.
func describeSelection(t textbuf.Text, s *pb.EditorSelection) *selectionView {
	if s == nil {
		return nil
	}
	a, b := min(int(s.Anchor), len(t)), min(int(s.Head), len(t))
	if a > b {
		a, b = b, a
	}
	l1, c1 := t.LineCol(a)
	l2, c2 := t.LineCol(b)
	return &selectionView{From: fmt.Sprintf("%d:%d", l1, c1), To: fmt.Sprintf("%d:%d", l2, c2), Empty: a == b, Text: t[a:b].String()}
}

// lineSlice is lines from to to (1-based, inclusive; 0: no bound) of
// text, numbered if asked, and the range shown ("" for all of it).
func lineSlice(text string, from, to int, numbers bool) (out string, total int, showing string) {
	lines := strings.Split(text, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // a final newline isn't a line
	}
	total = len(lines)
	if from < 1 {
		from = 1
	}
	if to < 1 || to > total {
		to = total
	}
	if from > 1 || to < total {
		showing = fmt.Sprintf("%d-%d", from, to)
	}
	if from > to {
		return "", total, showing
	}
	if showing == "" && !numbers {
		return text, total, ""
	}
	var b strings.Builder
	for i := from; i <= to; i++ {
		if numbers {
			fmt.Fprintf(&b, "%6d\t", i)
		}
		b.WriteString(lines[i-1])
		b.WriteByte('\n')
	}
	return b.String(), total, showing
}

// readEditor reads an editor pane's buffer into an editorView.
func readEditor(call caller, p *pb.Pane, from, to int, numbers bool) (*editorView, error) {
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: p.Id}}})
	if err != nil {
		return nil, err
	}
	u := r.GetEditorUpdate()
	ed := p.GetEditor()
	v := &editorView{PaneID: p.Id, Path: ed.GetPath(), Name: ed.GetName(), Dirty: ed.GetDirty(), Conflict: ed.GetConflict(),
		Missing: ed.GetMissing(), ReadOnly: ed.GetReadOnly()}
	v.Selection = describeSelection(textbuf.FromString(u.GetText()), u.GetSelection())
	v.Text, v.Lines, v.Showing = lineSlice(u.GetText(), from, to, numbers)
	return v, nil
}

// parseLines reads FROM-TO, FROM- or a single line.
func parseLines(s string) (from, to int, err error) {
	a, b, isRange := strings.Cut(s, "-")
	if from, err = strconv.Atoi(a); err != nil || from < 1 {
		return 0, 0, fmt.Errorf("--lines wants FROM-TO, not %q", s)
	}
	if !isRange {
		return from, from, nil
	}
	if b == "" {
		return from, 0, nil
	}
	if to, err = strconv.Atoi(b); err != nil || to < from {
		return 0, 0, fmt.Errorf("--lines wants FROM-TO, not %q", s)
	}
	return from, to, nil
}

func runEditRead(w *world, call caller, ref string, args []string) error {
	p, err := w.editorPane(ref)
	if err != nil {
		return err
	}
	from, to := 0, 0
	if l := flagValue(args, "--lines"); l != "" {
		if from, to, err = parseLines(l); err != nil {
			return err
		}
	}
	v, err := readEditor(call, p, from, to, hasFlag(args, "--numbers"))
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return printJSON(v)
	}
	fmt.Print(v.Text)
	return nil
}
