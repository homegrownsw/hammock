package daemon

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/agentview"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Background shells in the agent view: the main view lists the session's
// (from its transcript), and a shell's own view follows the file Claude
// Code writes its output to.

const (
	shellTail = 128 << 10 // output kept, and sent to a client that starts watching
)

// listShells is the session's background shells as clients see them: one
// still running only while the agent that started it is (Claude Code
// stops them when it exits). Callers hold w.mu.
func (w *agentWatch) listShells() []*pb.AgentShell {
	cc, ok := w.parser.(*agentview.Claude)
	if !ok {
		return nil
	}
	w.p.mu.Lock()
	start, exited := w.p.agentStart, w.p.info.Exited
	n := int32(0)
	if a := w.p.info.Agent; a != nil {
		n = a.Shells
	}
	w.p.mu.Unlock()
	shells := cc.Shells()
	if w.shellFiles == nil {
		w.shellFiles = map[string]string{}
	}
	out := make([]*pb.AgentShell, 0, len(shells))
	for _, s := range shells {
		w.shellFiles[s.Id] = s.Output
		alive := !exited && n > 0 && !start.IsZero() && s.StartedUnixMs >= start.UnixMilli()
		if !s.Finished && !alive {
			s.Finished = true
		}
		out = append(out, s.AgentShell)
	}
	return out
}

// shellFile is where a shell's output is: as its session's main view
// read it, else from the transcript. Only a file named for it in a tasks
// folder, and not a link (a subagent's is one, to its transcript).
func (w *agentWatch) shellFile() (string, error) {
	path := ""
	w.d.mu.Lock()
	main := w.d.agentWatches[w.p.id]
	w.d.mu.Unlock()
	if main != nil {
		main.mu.Lock()
		path = main.shellFiles[w.shell]
		main.mu.Unlock()
	}
	if path == "" {
		session, err := w.mainPath()
		if err != nil {
			return "", err
		}
		path = findShellFile(session, w.shell)
	}
	if path == "" || filepath.Base(path) != w.shell+".output" || filepath.Base(filepath.Dir(path)) != "tasks" {
		return "", errors.New("couldn't find this command's output")
	}
	if st, err := os.Lstat(path); err != nil || !st.Mode().IsRegular() {
		return "", errors.New("couldn't find this command's output")
	}
	return path, nil
}

// findShellFile reads a session's transcript for where a background
// shell's output goes.
func findShellFile(session, id string) string {
	f, err := os.Open(session)
	if err != nil {
		return ""
	}
	defer f.Close()
	key := []byte(`"backgroundTaskId":"` + id + `"`)
	re := regexp.MustCompile(`Output is being written to: (\S+/` + regexp.QuoteMeta(id) + `\.output)`)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if bytes.Contains(sc.Bytes(), key) {
			if m := re.FindSubmatch(sc.Bytes()); m != nil {
				return string(m[1])
			}
		}
	}
	return ""
}

// refreshShell reads what the shell wrote since the last look. Callers
// hold w.mu.
func (w *agentWatch) refreshShell() *pb.AgentView {
	u := &pb.AgentView{PaneId: w.p.id, ShellId: w.shell}
	path, err := w.shellFile()
	if err != nil {
		if w.errText == err.Error() {
			return nil
		}
		w.errText, u.Error, u.Replace = err.Error(), err.Error(), true
		return u
	}
	if w.errText != "" {
		w.errText = ""
		u.Replace = true
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	if path != w.path || st.Size() < w.off {
		// First look, or it was rewritten: its last part.
		w.path, w.off, w.partial, w.shellText = path, max(0, st.Size()-shellTail), nil, nil
		u.Replace = true
	}
	running := w.shellRunning()
	if st.Size() == w.off && !u.Replace && running == w.working {
		return nil
	}
	w.working = running
	var text string
	if st.Size() > w.off {
		if _, err := f.Seek(w.off, io.SeekStart); err != nil {
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(f, 4<<20))
		if err != nil {
			return nil
		}
		if u.Replace && w.off > 0 {
			// Begun partway: from its next line.
			if i := bytes.IndexByte(data, '\n'); i >= 0 {
				w.off += int64(i + 1)
				data = data[i+1:]
			}
		}
		w.off += int64(len(data))
		data = append(w.partial, data...)
		data, w.partial = splitHeld(data)
		text = cleanOutput(data)
		w.shellText = append(w.shellText, text...)
		if over := len(w.shellText) - shellTail; over > 0 {
			for over < len(w.shellText) && !utf8.RuneStart(w.shellText[over]) {
				over++
			}
			w.shellText = w.shellText[over:]
		}
	}
	if u.Replace {
		return w.snapshot()
	}
	u.ShellOutput, u.Working = text, running
	return u
}

// shellRunning: the session's main view lists the shell as running.
func (w *agentWatch) shellRunning() bool {
	w.d.mu.Lock()
	main := w.d.agentWatches[w.p.id]
	w.d.mu.Unlock()
	if main == nil {
		return false
	}
	main.mu.Lock()
	defer main.mu.Unlock()
	i := slices.IndexFunc(main.shells, func(s *pb.AgentShell) bool { return s.Id == w.shell })
	return i >= 0 && !main.shells[i].Finished
}

// splitHeld holds back what may continue in the next read: an incomplete
// escape sequence or character at the end.
func splitHeld(data []byte) (ready, held []byte) {
	if i := bytes.LastIndexByte(data, 0x1b); i >= 0 && len(data)-i < 64 && !escapeEnds(data[i:]) {
		return data[:i], slices.Clone(data[i:])
	}
	for n := 1; n <= 3 && n <= len(data); n++ {
		if b := data[len(data)-n]; utf8.RuneStart(b) {
			if !utf8.FullRune(data[len(data)-n:]) {
				return data[:len(data)-n], slices.Clone(data[len(data)-n:])
			}
			break
		}
	}
	return data, nil
}

// escapeEnds: an escape sequence (from ESC) is complete.
func escapeEnds(s []byte) bool {
	if len(s) < 2 {
		return false
	}
	switch s[1] {
	case '[':
		for _, b := range s[2:] {
			if b >= 0x40 && b <= 0x7e {
				return true
			}
		}
		return false
	case ']':
		return bytes.IndexByte(s, 0x07) >= 0 || bytes.Contains(s, []byte("\x1b\\"))
	}
	return true
}

var (
	csiSeq = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	oscSeq = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	escSeq = regexp.MustCompile(`\x1b[()*+][0-9A-Za-z]|\x1b[^\[\]]`)
	ctlSeq = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

// cleanOutput is a command's output as text: escape sequences and control
// characters dropped, but for newlines, tabs and carriage returns (a
// progress line redrawn: clients keep its last).
func cleanOutput(b []byte) string {
	b = csiSeq.ReplaceAll(b, nil)
	b = oscSeq.ReplaceAll(b, nil)
	b = escSeq.ReplaceAll(b, nil)
	b = ctlSeq.ReplaceAll(b, nil)
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return string(bytes.ToValidUTF8(b, []byte("�")))
}

func shellsEqual(a, b []*pb.AgentShell) bool {
	return slices.EqualFunc(a, b, func(x, y *pb.AgentShell) bool { return proto.Equal(x, y) })
}
