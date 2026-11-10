package tui

// editor-tui-external: an editor pane focused in the TUI opens in $VISUAL
// or $EDITOR (vi without either), the way git commit does. The TUI steps
// aside (the terminal back to normal, its own reading of the keys paused),
// writes the buffer to a temporary file and runs the editor on it. Each
// time the editor writes the file, what changed is pushed like any edit,
// rebased over edits from elsewhere since, and saved. Edits from elsewhere
// reach the file only while it's unchanged since Hammock last wrote it (the
// editor sees them if it reloads). The TUI comes back when the editor exits.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/homegrownsw/hammock/internal/textbuf"
)

// Terminal modes the TUI turns on (Run) and off again: the alternate
// screen, a hidden cursor, the mouse (buttons, drags and motion, SGR
// coordinates), focus reports and bracketed paste.
const (
	ttyEnter = "\x1b[?1049h\x1b[?25l\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h\x1b[?1004h\x1b[?2004h"
	ttyLeave = "\x1b]22;default\x1b\\\x1b[?2004l\x1b[?1004l\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l"
)

// ttyReader reads the terminal for the TUI, and can stop for a while so
// another program (an external editor) gets the keys: it waits for input
// with select, which works on terminals everywhere (macOS's poll and
// kqueue don't), alongside a pipe that wakes it to pause.
type ttyReader struct {
	fd           int
	out          chan []byte
	wakeR, wakeW *os.File
	pauseReq     chan chan struct{}
	resume       chan struct{}
}

func newTTYReader(in *os.File, out chan []byte) (*ttyReader, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	t := &ttyReader{fd: int(in.Fd()), out: out, wakeR: r, wakeW: w, pauseReq: make(chan chan struct{}, 1), resume: make(chan struct{})}
	go t.run()
	return t, nil
}

func (t *ttyReader) run() {
	defer close(t.out)
	buf := make([]byte, 16384)
	wake := int(t.wakeR.Fd())
	for {
		var set unix.FdSet
		set.Zero()
		set.Set(t.fd)
		set.Set(wake)
		if _, err := unix.Select(max(t.fd, wake)+1, &set, nil, nil, nil); err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if set.IsSet(t.fd) {
			n, err := unix.Read(t.fd, buf)
			if err == unix.EINTR || err == unix.EAGAIN {
				continue
			}
			if err != nil || n == 0 {
				return
			}
			t.out <- append([]byte(nil), buf[:n]...)
		}
		if set.IsSet(wake) {
			var b [16]byte
			unix.Read(wake, b[:])
			select {
			case ack := <-t.pauseReq:
				close(ack)
				<-t.resume
			default:
			}
		}
	}
}

// pause stops reading until unpause; what was typed but not yet handled is
// dropped.
func (t *ttyReader) pause() {
	ack := make(chan struct{})
	t.pauseReq <- ack
	t.wakeW.Write([]byte{0})
	for {
		select {
		case <-ack:
			return
		case _, ok := <-t.out:
			if !ok {
				return
			}
		}
	}
}

func (t *ttyReader) unpause() {
	select {
	case t.resume <- struct{}{}:
	case <-time.After(time.Second): // it had stopped (end of input)
	}
}

// suspend hands the terminal back as it was before the TUI.
func (a *App) suspend() {
	if a.tty == nil {
		return
	}
	a.dropLive()
	a.tty.pause()
	io.WriteString(a.out, ttyLeave)
	if a.rawOld != nil {
		term.Restore(a.fd, a.rawOld)
	}
	a.suspended = true
}

// resumeTTY takes the terminal again and redraws everything.
func (a *App) resumeTTY() {
	if !a.suspended {
		return
	}
	term.MakeRaw(a.fd)
	io.WriteString(a.out, ttyEnter)
	a.tty.unpause()
	a.suspended = false
	a.pointer = ""
	if w, h, err := term.GetSize(a.fd); err == nil {
		a.w, a.h = w, h
	}
	a.scr = NewScreen(a.w, a.h)
	a.syncPanes()
	a.dirty = true
}

// extSession is an editor pane open in an external editor.
type extSession struct {
	pane string
	dir  string // the temporary folder holding the file
	path string
	// base is the file as Hammock last wrote or read it; since, the changes
	// made to the buffer from elsewhere after that, in order.
	base  textbuf.Text
	since []textbuf.ChangeSet
}

// extEditor is what to run: $VISUAL, else $EDITOR, else vi.
func extEditor() string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return "vi"
}

// checkExternal opens the focused editor pane in the external editor, when
// that's the setting and it hasn't just come back from there.
func (a *App) checkExternal() {
	v := a.focusedEditor()
	if v == nil {
		a.extShown = ""
		return
	}
	if !editorPrefs.TUIExternal || a.ext != nil || a.overlay != nil || a.tty == nil || a.extShown == v.id || !v.loaded {
		return
	}
	a.extShown = v.id
	if !a.editable(v, true) {
		return
	}
	a.startExternal(v)
}

func (a *App) startExternal(v *editorView) {
	name := a.panes[v.id].GetEditor().GetName()
	if name == "" || name == "Untitled" {
		name = "untitled.txt"
	}
	dir, err := os.MkdirTemp("", "hammock-edit-")
	if err != nil {
		a.flash("⚠ " + err.Error())
		return
	}
	s := &extSession{pane: v.id, dir: dir, path: filepath.Join(dir, filepath.Base(name)), base: slices.Clone(v.doc.text)}
	if err := os.WriteFile(s.path, []byte(s.base.String()), 0o600); err != nil {
		os.RemoveAll(dir)
		a.flash("⚠ " + err.Error())
		return
	}
	out := os.Stdout
	if f, ok := a.out.(*os.File); ok {
		out = f
	}
	// Through the shell, so $EDITOR can carry arguments ("code -w").
	cmd := exec.Command("/bin/sh", "-c", extEditor()+` "$1"`, "sh", s.path)
	cmd.Stdout, cmd.Stderr = out, out
	if a.in != nil {
		cmd.Stdin = a.in
	}
	a.suspend()
	if err := cmd.Start(); err != nil {
		a.resumeTTY()
		os.RemoveAll(dir)
		a.flash("⚠ " + err.Error())
		return
	}
	a.ext = s
	v.onText = func(applied []textbuf.ChangeSet) { a.extRemote(s, applied) }
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()
	go a.watchExternal(s, done)
}

// watchExternal passes on the file's contents each time it changes, until
// the editor exits.
func (a *App) watchExternal(s *extSession, done chan struct{}) {
	var last []byte
	look := func() {
		data, err := os.ReadFile(s.path)
		if err != nil || bytes.Equal(data, last) {
			return
		}
		last = data
		a.actions <- func() { a.extWritten(s, data) }
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			look()
		case <-done:
			look()
			a.actions <- func() { a.endExternal(s) }
			return
		}
	}
}

// extWritten takes the file as the editor wrote it: pushed, then saved.
func (a *App) extWritten(s *extSession, data []byte) {
	v := a.editors[s.pane]
	if v == nil || a.ext != s {
		return
	}
	if !extApply(s, v.doc, string(data)) {
		return
	}
	a.editorChanged(v)
	if a.panes[s.pane].GetEditor().GetPath() != "" {
		v.save = &pendingSave{}
		a.editorSaveIfDue(v)
	}
}

// extApply makes the buffer what the editor wrote to the file, keeping the
// changes made to it from elsewhere since the file was last in step: what
// changed in the file is rebased over them. False: nothing changed.
func extApply(s *extSession, d *editDoc, file string) bool {
	t := textbuf.FromString(strings.ReplaceAll(file, "\r\n", "\n"))
	if slices.Equal(t, s.base) {
		return false
	}
	since, cs := rebase(s.since, textbuf.Diff(s.base, t))
	s.base, s.since = t, since
	if cs.Empty() {
		return false
	}
	return d.change(cs, "edit")
}

// extRemote notes the buffer changing from elsewhere (applied nil: started
// again from a snapshot) and, if the file is as Hammock left it, rewrites
// it, so an editor that reloads shows the change.
func (a *App) extRemote(s *extSession, applied []textbuf.ChangeSet) {
	v := a.editors[s.pane]
	if v == nil || a.ext != s {
		return
	}
	if applied == nil {
		s.since = []textbuf.ChangeSet{textbuf.Diff(s.base, v.doc.text)}
	} else {
		s.since = append(s.since, applied...)
	}
	data, err := os.ReadFile(s.path)
	if err != nil || !slices.Equal(textbuf.FromString(strings.ReplaceAll(string(data), "\r\n", "\n")), s.base) {
		return // changed in the editor: it comes back when it's written
	}
	if os.WriteFile(s.path, []byte(v.doc.text.String()), 0o600) == nil {
		s.base, s.since = slices.Clone(v.doc.text), nil
	}
}

// endExternal brings the TUI back once the editor has exited.
func (a *App) endExternal(s *extSession) {
	if a.ext != s {
		return
	}
	a.ext = nil
	if v := a.editors[s.pane]; v != nil {
		v.onText = nil
	}
	os.RemoveAll(s.dir)
	a.resumeTTY()
}
