package daemon

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The agent view (Phase 7): for each pane someone is watching, the daemon
// tails the agent's session file and reads its screen, and streams the
// conversation as blocks plus the prompt it's showing. Answers are typed into
// the pane as keystrokes, each checked against the screen first.

const (
	agentPoll      = 250 * time.Millisecond
	agentLinger    = 2 * time.Minute // a watch kept after its last viewer leaves
	agentTurns     = 40              // turns sent at first, and per page of history…
	agentBlocks    = 150             // …but no more blocks than this (a long turn can be hundreds of tool calls)…
	agentBytes     = 256 << 10       // …or bytes, so a phone decodes and lays out a page quickly
	agentFileLimit = 30 << 20        // largest file served to a client, or sent (a phone's video, shrunk)
)

type agentWatch struct {
	d *Daemon
	p *pane
	// A subagent's view (its own transcript) rather than the pane's agent;
	// key is its place in d.agentWatches.
	sub, key string
	// The main view's list of the session's subagents, and when each began.
	subs      []*pb.AgentSubagent
	subStarts map[string]int64
	// A background shell's view (its output) rather than the pane's agent,
	// and the output kept for a client that starts watching.
	shell     string
	shellText []byte
	// The main view's list of the session's background shells, and where
	// each one's output goes.
	shells     []*pb.AgentShell
	shellFiles map[string]string
	// The main view's task list and links (pull requests, artifacts) as
	// last sent.
	tasks *pb.AgentTaskList
	links []*pb.AgentLink

	mu       sync.Mutex
	conns    map[*conn]bool
	path     string
	off      int64
	mod      time.Time // a whole-read session's, when last read
	partial  []byte
	parser   agentview.Parser
	harness  string // whose session file path is (claude, codex)
	reread   bool   // a compressed session changed: read again, and replace what clients show
	prompt   *pb.AgentPrompt
	working  bool
	status   string
	draft    string  // what's typed in its input box in the terminal
	ctx      ctxInfo // the model and context last sent
	errText  string
	commands []string
	starts   []string // those of commands that only run at a message's start
	stop     chan struct{}
}

// watchAgent starts (or with before set, pages) c's view of a pane.
func (d *Daemon) watchAgent(c *conn, m *pb.WatchAgent) error {
	d.mu.Lock()
	p := d.panes[m.PaneId]
	if p == nil {
		d.mu.Unlock()
		return fmt.Errorf("no such pane %q", m.PaneId)
	}
	if d.agentWatches == nil {
		d.agentWatches = map[string]*agentWatch{}
	}
	key := m.PaneId
	if m.SubagentId != "" {
		if !validSubagentID(m.SubagentId) {
			d.mu.Unlock()
			return fmt.Errorf("bad subagent id %q", m.SubagentId)
		}
		key += "#" + m.SubagentId
	} else if m.ShellId != "" {
		if !validSubagentID(m.ShellId) {
			d.mu.Unlock()
			return fmt.Errorf("bad shell id %q", m.ShellId)
		}
		key += "$" + m.ShellId
	}
	w := d.agentWatches[key]
	if !m.Watch {
		d.mu.Unlock()
		if w != nil {
			w.drop(c)
		}
		return nil
	}
	fresh := w == nil
	if fresh {
		w = &agentWatch{d: d, p: p, sub: m.SubagentId, shell: m.ShellId, key: key, conns: map[*conn]bool{}, stop: make(chan struct{})}
		d.agentWatches[key] = w
	}
	d.mu.Unlock()

	w.mu.Lock()
	defer w.mu.Unlock()
	if fresh {
		w.refresh() // the first read happens before anyone's told (off the daemon lock: big files take a moment)
		go w.loop()
	}
	if m.BeforeBlockId != "" && w.shell == "" {
		c.send(w.page(m.BeforeBlockId))
		return nil
	}
	w.conns[c] = true
	c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_AgentView{AgentView: w.snapshot()}})
	return nil
}

// drop stops c watching; the watch ends with its last viewer.
func (w *agentWatch) drop(c *conn) {
	w.mu.Lock()
	delete(w.conns, c)
	empty := len(w.conns) == 0
	w.mu.Unlock()
	if !empty {
		return
	}
	// Kept a while (reading on as the agent writes): switching back to the
	// agent view then needn't parse a long session from the start again.
	time.AfterFunc(agentLinger, func() {
		w.mu.Lock()
		still := len(w.conns) == 0
		w.mu.Unlock()
		if !still {
			return
		}
		w.d.mu.Lock()
		if w.d.agentWatches[w.key] == w {
			delete(w.d.agentWatches, w.key)
			close(w.stop)
		}
		w.d.mu.Unlock()
	})
}

// dropAgentWatches ends c's views (it disconnected).
func (d *Daemon) dropAgentWatches(c *conn) {
	d.mu.Lock()
	var ws []*agentWatch
	for _, w := range d.agentWatches {
		ws = append(ws, w)
	}
	d.mu.Unlock()
	for _, w := range ws {
		w.mu.Lock()
		has := w.conns[c]
		w.mu.Unlock()
		if has {
			w.drop(c)
		}
	}
}

func (w *agentWatch) loop() {
	t := time.NewTicker(agentPoll)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.mu.Lock()
			if u := w.refresh(); u != nil {
				for c := range w.conns {
					c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_AgentView{AgentView: u}})
				}
			}
			w.mu.Unlock()
		}
	}
}

// sessionPath finds the pane's Claude session file: as the hooks reported
// it, else by session id, else the newest in its project.
func (w *agentWatch) sessionPath() (string, error) {
	main, err := w.mainPath()
	if err != nil || w.sub == "" {
		return main, err
	}
	if main == "" {
		return "", errors.New("couldn't find this subagent's transcript")
	}
	path := filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents", "agent-"+w.sub+".jsonl")
	if !fileExists(path) {
		return "", errors.New("couldn't find this subagent's transcript")
	}
	return path, nil
}

// mainPath is the pane's agent's own session file.
func (w *agentWatch) mainPath() (string, error) {
	info := w.p.snapshotInfo()
	a := info.Agent
	ag := agents.Get(a.GetHarness())
	if a == nil || ag == nil || ag.NewParser == nil {
		return "", errors.New("no agent session this view can show in this pane")
	}
	if t := safeTranscript(a.TranscriptPath); t != "" {
		return t, nil
	}
	// A session just begun has no file until you send something: show it
	// empty, not the newest other session in the folder.
	fresh := a.SessionId == "" && a.Heuristic && a.State == pb.AgentState_AGENT_STATE_STARTING
	if a.SessionId != "" && ag.Locate != nil {
		if p, ok := ag.Locate(a.SessionId); ok {
			return p, nil
		}
	}
	// Some agents' hooks only fire once you've sent something: until then,
	// a resumed session is the one its command line names.
	if ag.ResumeID != nil && ag.Locate != nil {
		w.p.mu.Lock()
		pid := w.p.pid
		w.p.mu.Unlock()
		if id := resumeID(pid); id != "" {
			if p, ok := ag.Locate(id); ok {
				return p, nil
			}
		}
	}
	if a.SessionId != "" || fresh {
		return "", nil // not written yet
	}
	if ag.Newest != nil {
		if p, ok := ag.Newest(info.Cwd); ok {
			return w.guessed(p), nil
		}
	}
	return "", errors.New("couldn't find this session's file yet")
}

// guessed checks the newest session file in the pane's folder, taken for
// its agent's when its hooks haven't said which is (no session id yet): it
// isn't if another pane's agent has it, or nothing has written it since
// this agent started. Else a session just begun, before its first hook,
// showed another agent's conversation, then went blank. "" when it isn't.
func (w *agentWatch) guessed(path string) string {
	w.p.mu.Lock()
	pid := w.p.pid
	w.p.mu.Unlock()
	if h, apid := findAgentProc(pid); apid != 0 {
		st, err1 := agents.Get(h).StatSession(path)
		started, err2 := procinfo.StartTime(apid)
		if err1 == nil && err2 == nil && st.ModTime().Before(started) {
			return ""
		}
	}
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".zst"), ".jsonl")
	d := w.p.d
	d.mu.Lock()
	others := make([]*pane, 0, len(d.panes))
	for _, o := range d.panes {
		if o != w.p {
			others = append(others, o)
		}
	}
	d.mu.Unlock()
	for _, o := range others {
		a := o.snapshotInfo().Agent
		if a == nil {
			continue
		}
		if a.TranscriptPath == path || a.SessionId != "" && strings.HasSuffix(base, a.SessionId) {
			return ""
		}
	}
	return path
}

// ctxInfo is a session's model, its effort level and how full its context is.
type ctxInfo struct {
	model, effort  string
	tokens, window int64
}

// efforter is a parser that knows the session's effort level (Claude's).
type efforter interface{ Effort() string }

// parserEffort is p's effort level, "" when it doesn't know one.
func parserEffort(p agentview.Parser) string {
	if e, ok := p.(efforter); ok {
		return e.Effort()
	}
	return ""
}

func (w *agentWatch) context() ctxInfo {
	if w.parser == nil {
		return ctxInfo{}
	}
	m, t, win := w.parser.Context()
	return ctxInfo{m, parserEffort(w.parser), t, win}
}

func (v *ctxInfo) set(u *pb.AgentView) {
	u.Model, u.Effort, u.ContextTokens, u.ContextWindow = v.model, v.effort, v.tokens, v.window
}

// refresh reads what's new and returns the update to send (nil: nothing
// changed). Callers hold w.mu.
func (w *agentWatch) refresh() *pb.AgentView {
	if w.shell != "" {
		return w.refreshShell()
	}
	u := &pb.AgentView{PaneId: w.p.id, SubagentId: w.sub}
	path, err := w.sessionPath()
	if err != nil {
		if w.errText == err.Error() {
			return nil
		}
		w.errText = err.Error()
		u.Error = w.errText
		return u
	}
	if w.errText != "" {
		w.errText = ""
		u.Replace = true
	}
	if path != w.path || w.parser == nil {
		// A new session (first look, /clear, a resumed one).
		w.path, w.off, w.mod, w.partial = path, 0, time.Time{}, nil
		w.newParser()
		if ag := agents.Get(w.harness); w.sub == "" && ag != nil && ag.SlashCommands != nil {
			w.commands = ag.SlashCommands(w.p.cwd())
			w.starts = startCommands(w.commands, ag.StartCommands)
		}
		u.Replace = true
	}
	changed := w.readNew()
	if w.reread {
		w.reread = false
		u.Replace = true
	}
	if w.parser.Rewound() {
		u.Replace = true // a branch was dropped
	}
	var sc agentview.Screen
	var prompt *pb.AgentPrompt
	draft := ""
	working := false
	if w.sub == "" {
		sc = agentview.ReadScreen(w.p.text(false))
		if len(sc.Options) == 0 {
			// A menu hides the input box: its ❯ line isn't a draft.
			draft = agentview.InputDraft(w.p.bottomText(20))
		}
		prompt = agentview.Prompt(sc, w.pendingCall())
		working = sc.Working
		if a := w.p.snapshotInfo().Agent; a != nil && a.State == pb.AgentState_AGENT_STATE_WORKING {
			working = true
		}
		if subs := w.listSubagents(path); agents.Get(w.harness).Subagents && !slices.EqualFunc(subs, w.subs, func(a, b *pb.AgentSubagent) bool { return proto.Equal(a, b) }) {
			w.subs = subs
			u.Subagents = subs
		}
		if shells := w.listShells(); !shellsEqual(shells, w.shells) {
			w.shells = shells
			u.Shells = shells
		}
		if t, l, ok := w.extras(); ok {
			if !proto.Equal(t, w.tasks) {
				w.tasks = t
				u.Tasks = cmp.Or(t, &pb.AgentTaskList{})
			}
			if !slices.EqualFunc(l, w.links, func(a, b *pb.AgentLink) bool { return proto.Equal(a, b) }) {
				w.links = l
				u.Links = &pb.AgentLinks{Links: l}
			}
		}
	} else {
		// A subagent: no prompt of its own (the screen is the main agent's);
		// working until its record says it finished.
		working = !w.subFinished()
	}
	ctx := w.context()
	if u.Replace {
		snap := w.snapshot()
		w.prompt, w.working, w.status, w.ctx = prompt, working, sc.Status, ctx
		snap.Prompt, snap.Working, snap.Status = prompt, working, sc.Status
		w.draft, snap.Draft = draft, draft
		return snap
	}
	same := proto.Equal(prompt, w.prompt) && working == w.working && sc.Status == w.status && ctx == w.ctx && draft == w.draft
	if len(changed) == 0 && same && len(u.Subagents) == 0 && len(u.Shells) == 0 && u.Tasks == nil && u.Links == nil {
		return nil
	}
	w.prompt, w.working, w.status, w.ctx = prompt, working, sc.Status, ctx
	u.Blocks, u.Prompt, u.Working, u.Status = changed, prompt, working, sc.Status
	w.draft, u.Draft = draft, draft
	ctx.set(u)
	return u
}

// validSubagentID: Claude's agent ids are short hex; nothing that could
// leave the subagents folder.
func validSubagentID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9') {
			return false
		}
	}
	return true
}

// subagentMeta is Claude's record of a subagent, beside its transcript.
type subagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	Finished    bool   `json:"finished"`
}

// listSubagents reads the session's subagents (<session>/subagents/
// agent-<id>.meta.json), oldest first. Callers hold w.mu.
func (w *agentWatch) listSubagents(sessionPath string) []*pb.AgentSubagent {
	dir := filepath.Join(strings.TrimSuffix(sessionPath, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	if w.subStarts == nil {
		w.subStarts = map[string]int64{}
	}
	var out []*pb.AgentSubagent
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
		var m subagentMeta
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || json.Unmarshal(b, &m) != nil || !validSubagentID(id) {
			continue
		}
		started, ok := w.subStarts[id]
		if !ok {
			started = firstTimestamp(filepath.Join(dir, "agent-"+id+".jsonl"))
			w.subStarts[id] = started
		}
		out = append(out, &pb.AgentSubagent{Id: id, Description: m.Description, AgentType: m.AgentType, Finished: m.Finished, StartedUnixMs: started})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedUnixMs < out[j].StartedUnixMs })
	return out
}

// subFinished: a subagent's record says it's done.
func (w *agentWatch) subFinished() bool {
	main, err := w.mainPath()
	if err != nil {
		return true
	}
	b, err := os.ReadFile(filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents", "agent-"+w.sub+".meta.json"))
	var m subagentMeta
	return err != nil || json.Unmarshal(b, &m) != nil || m.Finished
}

// firstTimestamp is when a transcript began (its first line's timestamp).
func firstTimestamp(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n, _ := f.Read(buf)
	line := buf[:n]
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var l struct {
		Timestamp time.Time `json:"timestamp"`
	}
	if json.Unmarshal(line, &l) != nil {
		return 0
	}
	return l.Timestamp.UnixMilli()
}

// pendingCall is the tool call the screen's prompt is for: the newest
// unanswered call in the transcript, else a question known from its hook.
func (w *agentWatch) pendingCall() *pb.AgentBlock {
	if b := w.parser.Pending(); b != nil {
		return b
	}
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	return w.p.asking
}

// readWhole reads a session whole (a compressed Codex one, or an agent's
// that rewrites its file), again whenever it changes.
func (w *agentWatch) readWhole() []*pb.AgentBlock {
	st, err := agents.Get(w.harness).StatSession(w.path)
	if err != nil || st.Size() == w.off && st.ModTime().Equal(w.mod) {
		return nil
	}
	if w.off != 0 || !w.mod.IsZero() {
		w.newParser()
		w.reread = true
	}
	w.off, w.mod = st.Size(), st.ModTime()
	r, err := agents.Get(w.harness).OpenSession(w.path)
	if err != nil {
		return nil
	}
	defer r.Close()
	var changed []*pb.AgentBlock
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		changed = append(changed, w.parser.Feed(sc.Bytes())...)
	}
	return changed
}

// newParser starts reading w.path afresh, as its agent's sessions read:
// the pane's agent's, if the file is one of its kind, else the agent whose
// folders it's in.
func (w *agentWatch) newParser() {
	w.harness = w.p.snapshotInfo().Agent.GetHarness()
	if ag := agents.Get(w.harness); w.path != "" && (ag == nil || !ownsFile(ag, w.path)) {
		w.harness = cmp.Or(harnessOfPath(w.path), w.harness)
	}
	ag := agents.Get(w.harness)
	if ag == nil || ag.NewParser == nil {
		w.harness, ag = "claude", agents.Claude
	}
	w.parser = ag.NewParser(w.p.cwd(), w.sub != "")
}

// readNew parses lines appended since the last read.
func (w *agentWatch) readNew() []*pb.AgentBlock {
	if strings.HasSuffix(w.path, ".zst") || agents.Get(w.harness).GetWhole() {
		return w.readWhole()
	}
	f, err := os.Open(w.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < w.off {
		w.off, w.partial = 0, nil // rewritten
		w.newParser()
	}
	if st == nil || st.Size() == w.off {
		return nil
	}
	if _, err := f.Seek(w.off, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	w.off += int64(len(data))
	data = append(w.partial, data...)
	w.partial = nil
	var changed []*pb.AgentBlock
	seen := map[string]int{}
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			w.partial = append([]byte(nil), data...)
			break
		}
		for _, b := range w.parser.Feed(data[:i]) {
			if j, ok := seen[b.Id]; ok {
				changed[j] = b
			} else {
				seen[b.Id] = len(changed)
				changed = append(changed, b)
			}
		}
		data = data[i+1:]
	}
	return changed
}

// window is blocks[from:to], from at most agentTurns turns before to.
func windowStart(blocks []*pb.AgentBlock, to int) int {
	turns, size := 0, 0
	for i := to - 1; i >= 0; i-- {
		size += proto.Size(blocks[i])
		if to-i >= agentBlocks || size >= agentBytes {
			return i
		}
		if blocks[i].Kind == pb.AgentBlock_KIND_USER {
			turns++
			if turns == agentTurns {
				return i
			}
		}
	}
	return 0
}

// startCommands is those of commands that are in starts.
func startCommands(commands, starts []string) []string {
	var out []string
	for _, c := range commands {
		if slices.Contains(starts, c) {
			out = append(out, c)
		}
	}
	return out
}

// snapshot is the latest turns, replacing what a client shows. Callers hold w.mu.
func (w *agentWatch) snapshot() *pb.AgentView {
	if w.shell != "" {
		return &pb.AgentView{PaneId: w.p.id, ShellId: w.shell, Replace: true, Working: w.working, Error: w.errText, ShellOutput: string(w.shellText)}
	}
	v := &pb.AgentView{PaneId: w.p.id, SubagentId: w.sub, Replace: true, Prompt: w.prompt, Working: w.working, Status: w.status,
		Commands: w.commands, StartCommands: w.starts, Error: w.errText, Subagents: w.subs, Shells: w.shells, Draft: w.draft}
	if w.parser == nil {
		return v
	}
	blocks := w.parser.Blocks()
	from := windowStart(blocks, len(blocks))
	v.Blocks, v.HasMore = blocks[from:], from > 0
	if t, l, ok := w.extras(); ok {
		w.tasks, w.links = t, l
		v.Tasks = cmp.Or(t, &pb.AgentTaskList{})
		v.Links = &pb.AgentLinks{Links: l}
	}
	ctx := w.context()
	ctx.set(v)
	if a := w.p.snapshotInfo().Agent; a != nil {
		v.SessionId = a.SessionId
	}
	return v
}

// extras is the main view's task list and links (pull requests with their
// titles and states as Claude Code last saw them), if its parser keeps
// them. Callers hold w.mu.
func (w *agentWatch) extras() (*pb.AgentTaskList, []*pb.AgentLink, bool) {
	ex, ok := w.parser.(agentview.Extras)
	if !ok || w.sub != "" || w.shell != "" {
		return nil, nil, false
	}
	links := ex.Links()
	agentview.PRStatus(links)
	return ex.Tasks(), links, true
}

// page is the turns before block id. Callers hold w.mu.
func (w *agentWatch) page(before string) *pb.ServerMessage {
	v := &pb.AgentView{PaneId: w.p.id, SubagentId: w.sub, Older: true}
	if w.parser != nil {
		blocks := w.parser.Blocks()
		to := len(blocks)
		for i, b := range blocks {
			if b.Id == before {
				to = i
				break
			}
		}
		from := windowStart(blocks, to)
		v.Blocks, v.HasMore = blocks[from:to], from > 0
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_AgentView{AgentView: v}}
}

// ---------- answering ----------

// answerAgent types an answer to the prompt on screen, checking before each
// keystroke that the screen still shows what it's for. Replies once done.
func (d *Daemon) answerAgent(c *conn, id uint64, m *pb.AgentAnswer) error {
	if m.Decision != pb.AgentAnswer_DECISION_UNSPECIFIED {
		return d.decideAgent(c, id, m)
	}
	w, p := d.agentWatchFor(m.PaneId)
	if w == nil {
		return errors.New("not watching this pane's agent")
	}
	// Read the screen now, not as of the last poll.
	w.mu.Lock()
	var pending *pb.AgentBlock
	if w.parser != nil {
		pending = w.pendingCall()
	}
	w.mu.Unlock()
	sc := agentview.ReadScreen(p.text(false))
	steps, err := agentview.AnswerSteps(agentview.Prompt(sc, pending), m, sc.Arrows, p.kittyKeys())
	if err != nil {
		d.log.Info("agent answer refused", "pane", p.id, "err", err)
		return err
	}
	d.typeAnswer(c, id, p, steps)
	return errDeferred
}

// decideAgent is Needs You's Allow / Deny for any agent's pane: typed only
// while its screen shows a prompt, so a stale card never types into
// whatever the pane shows now.
func (d *Daemon) decideAgent(c *conn, id uint64, m *pb.AgentAnswer) error {
	d.mu.Lock()
	p := d.panes[m.PaneId]
	d.mu.Unlock()
	if p == nil {
		return fmt.Errorf("no such pane %q", m.PaneId)
	}
	allow := m.Decision == pb.AgentAnswer_DECISION_ALLOW || m.Decision == pb.AgentAnswer_DECISION_ALLOW_SCHEDULE
	screen := p.text(false)
	steps, err := agentview.DecisionSteps(agentview.ReadScreen(screen), allow, p.kittyKeys())
	if err != nil {
		d.log.Info("agent decision refused", "pane", p.id, "err", err)
		return err
	}
	if m.Decision == pb.AgentAnswer_DECISION_ALLOW_SCHEDULE {
		// Only the tool the prompt on screen shows (checked now), and only
		// once its Allow has gone in.
		d.mu.Lock()
		rule, err := d.scheduleToolFor(p, screen)
		d.mu.Unlock()
		if err != nil {
			return err
		}
		d.typeAnswer(c, id, p, steps, func() { d.rememberScheduleTool(p, rule) })
		return errDeferred
	}
	d.typeAnswer(c, id, p, steps)
	return errDeferred
}

// typeAnswer types steps in the background, then replies.
// then, if given, runs (holding d.mu) once every step has gone in.
func (d *Daemon) typeAnswer(c *conn, id uint64, p *pane, steps []agentview.Step, then ...func()) {
	go func() {
		err := d.runSteps(c, p, steps)
		if err != nil {
			d.log.Warn("agent answer stopped", "pane", p.id, "err", err)
			c.replyErr(id, err)
			return
		}
		d.mu.Lock()
		d.agentHub.userInput(p)
		for _, f := range then {
			f()
		}
		d.mu.Unlock()
		c.send(&pb.ServerMessage{ReplyTo: id, Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}})
	}()
}

// keyGap is the least time between an answer's keystrokes: keys that
// arrive together are read as one burst of typing ("1n" is neither a
// number nor n), even when the screen was already ready for the second.
const keyGap = 80 * time.Millisecond

func (d *Daemon) runSteps(c *conn, p *pane, steps []agentview.Step) error {
	var last time.Time
	for _, s := range steps {
		if s.Ready != nil {
			deadline := time.Now().Add(1500 * time.Millisecond)
			for !s.Ready(agentview.ReadScreen(p.text(false))) {
				if time.Now().After(deadline) {
					return fmt.Errorf("%w (waiting to type %s)", agentview.ErrMismatch, s.What)
				}
				time.Sleep(40 * time.Millisecond)
			}
		}
		if wait := keyGap - time.Since(last); wait > 0 {
			time.Sleep(wait)
		}
		if _, err := p.input(c, s.Keys); err != nil {
			return err
		}
		last = time.Now()
	}
	return nil
}

// sendAgent types a message (and attaches images by path, which Claude Code
// turns into [Image #n]), then Enter. Claude queues it if it's working.
func (d *Daemon) sendAgent(c *conn, id uint64, m *pb.AgentSend) error {
	d.mu.Lock()
	p := d.panes[m.PaneId]
	d.mu.Unlock()
	if p == nil {
		return fmt.Errorf("no such pane %q", m.PaneId)
	}
	var paths []string
	for _, a := range m.Attachments {
		if a.Path != "" && !c.isOwnApp() {
			return errors.New("only the Hammock app on this computer can attach a file by its path")
		}
		path, err := saveAttachment(a)
		if err != nil {
			return err
		}
		paths = append(paths, path)
	}
	parts := sendParts(m.Text, paths, m.Inline)
	if len(parts) == 0 {
		return errors.New("nothing to send")
	}
	go func() {
		if err := typeParts(p, c, parts); err != nil {
			c.replyErr(id, err)
			return
		}
		c.send(&pb.ServerMessage{ReplyTo: id, Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}})
	}()
	return errDeferred
}

// isOwnApp is whether c is the Hammock app on this computer, the person
// at it: not a phone, a linked computer or guest, or an agent.
func (c *conn) isOwnApp() bool {
	c.d.mu.Lock()
	peer := c.peer
	c.d.mu.Unlock()
	return !c.remote && peer == "" && c.kind == "mac" && c.personOK.Load()
}

// attachMark is where an attachment goes in an inline AgentSend's text.
const attachMark = "\uFFFC"

// sendPart is one paste into an agent's input: text, or a file's path.
type sendPart struct {
	text, path string
}

// sendParts orders a message's text and attachments' paths as they're
// pasted: inline, each path where its mark is (marks past the last path are
// dropped, and paths past the last mark go at the end); otherwise the paths
// first. The message is trimmed as a whole, not around each path.
func sendParts(text string, paths []string, inline bool) []sendPart {
	var parts []sendPart
	addText := func(t string) {
		if t != "" {
			parts = append(parts, sendPart{text: t})
		}
	}
	if !inline {
		text = strings.ReplaceAll(text, attachMark, "")
		for _, p := range paths {
			parts = append(parts, sendPart{path: p})
		}
		addText(strings.TrimSpace(text))
		return parts
	}
	segs := strings.Split(strings.TrimSpace(text), attachMark)
	for i, seg := range segs {
		if i == 0 {
			seg = strings.TrimLeft(seg, " \t\n")
		}
		addText(seg)
		if i < len(segs)-1 && i < len(paths) {
			parts = append(parts, sendPart{path: paths[i]})
		}
	}
	for _, p := range paths[min(len(paths), len(segs)-1):] {
		parts = append(parts, sendPart{path: p})
	}
	// Only spaces left between the attachments: nothing typed.
	if !slices.ContainsFunc(parts, func(p sendPart) bool { return p.path != "" || strings.TrimSpace(p.text) != "" }) {
		return nil
	}
	return parts
}

// typeToAgent types a message (and files' paths, first) into an agent's
// pane as the composer does, and presses Enter. It takes a moment: not
// under d.mu. c is who's typing (nil: Hammock itself, as for review
// comments).
func typeToAgent(p *pane, c *conn, text string, paths []string) error {
	return typeParts(p, c, sendParts(text, paths, false))
}

// typeParts pastes each part in turn into an agent's input, and presses
// Enter.
func typeParts(p *pane, c *conn, parts []sendPart) error {
	// As pastes (bracketed when the program asked, which Claude Code
	// does): a pasted image path becomes [Image #n], and newlines stay in
	// the message instead of sending it line by line.
	paste := func(s string) []byte { return firstPromptPaste(s, p.bracketedPaste()) }
	var steps []agentview.Step
	// Whatever was typed into its input box in the terminal goes first:
	// the message goes in alone.
	steps = append(steps, agentview.Step{Keys: agentview.ClearInput(agentview.InputLines(p.text(false))), What: "clear"})
	// Each separately, so each pasted path is taken as a file.
	for _, part := range parts {
		if part.path != "" {
			steps = append(steps, agentview.Step{Keys: paste(part.path + " "), What: "attachment"})
		} else {
			steps = append(steps, agentview.Step{Keys: paste(part.text), What: "message"})
		}
	}
	steps = append(steps, agentview.Step{Keys: agentview.Enter(p.kittyKeys()), What: "enter"})
	for i, s := range steps {
		if i > 0 {
			time.Sleep(150 * time.Millisecond) // separate bursts, or it's one paste
		}
		if _, err := p.input(c, s.Keys); err != nil {
			return err
		}
	}
	return nil
}

// saveAttachment writes an attachment where its path needs no quoting (the
// state dir has a space in it), for pasting as a path.
// One given by its path (a file on this computer) is cloned there, as it is.
func saveAttachment(a *pb.AgentAttachment) (string, error) {
	if a.Path != "" {
		fi, err := os.Stat(a.Path)
		if err != nil {
			return "", err
		}
		if !fi.Mode().IsRegular() || fi.Size() == 0 {
			return "", fmt.Errorf("%s isn't a file to attach", filepath.Base(a.Path))
		}
	} else if len(a.Data) == 0 || len(a.Data) > agentFileLimit {
		return "", errors.New("attachment is empty or too large")
	}
	ext := strings.ToLower(filepath.Ext(a.Name))
	if ext == "" {
		ext = ".png"
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	sub, err := attachmentsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", err
	}
	pruneAttachments(sub, time.Now())
	path := filepath.Join(sub, time.Now().Format(attachmentStamp)+"-"+hex.EncodeToString(b)+ext)
	if a.Path != "" {
		if cloneFile(a.Path, path) {
			return path, nil
		}
		return path, copyFile(a.Path, path)
	}
	return path, os.WriteFile(path, a.Data, 0o600)
}

// attachmentStamp starts an attachment's name: when it was sent (its
// modification time is the original's, for a clone).
const attachmentStamp = "20060102-150405"

// attachmentKeep is how long an attachment is kept: a sent video's tile and
// the agent's reading it again need it (a picture's tile comes from the
// session itself), but not for long.
const attachmentKeep = 7 * 24 * time.Hour

func attachmentsDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "hammock", "attachments"), nil
}

var attachmentsPruned struct {
	sync.Mutex
	at time.Time
}

// pruneAttachments removes attachments sent more than attachmentKeep ago
// (by their names), at most once an hour.
func pruneAttachments(dir string, now time.Time) {
	attachmentsPruned.Lock()
	if now.Sub(attachmentsPruned.at) < time.Hour {
		attachmentsPruned.Unlock()
		return
	}
	attachmentsPruned.at = now
	attachmentsPruned.Unlock()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if len(e.Name()) < len(attachmentStamp) || !e.Type().IsRegular() {
			continue
		}
		t, err := time.ParseInLocation(attachmentStamp, e.Name()[:len(attachmentStamp)], time.Local)
		if err == nil && now.Sub(t) > attachmentKeep {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// copyFile copies src to a new file dst (removed again if it fails).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Close()
	} else {
		out.Close()
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}

// readAgentFile serves an image from the session, or a file the session
// referenced. A guest must be watching the agent itself, and one who can
// only view gets only images (by their content).
// errViewerFile is what a viewer sees for a file that isn't an image;
// clients show it as is.
var errViewerFile = errors.New("Only people who can drive can open files.")

func (d *Daemon) readAgentFile(c *conn, m *pb.ReadAgentFile) (*pb.ServerMessage, error) {
	w, _ := d.agentWatchFor(m.PaneId)
	if w == nil {
		return nil, errors.New("not watching this pane's agent")
	}
	d.mu.Lock()
	peer := c.peer
	role := d.scope(peer)[d.groupOfPane(m.PaneId)]
	viewer := peer != "" && role != pb.Role_ROLE_DRIVER && role != pb.Role_ROLE_OWNER
	d.mu.Unlock()
	w.mu.Lock()
	if peer != "" && !w.conns[c] {
		w.mu.Unlock()
		return nil, errors.New("not watching this pane's agent")
	}
	parser := w.parser
	var img agentview.Image
	var ok bool
	if parser != nil {
		img, ok = parser.Image(m.Ref)
	}
	referenced := false
	path, isFile := strings.CutPrefix(m.Ref, "file:")
	if isFile && parser != nil {
		path = filepath.Clean(path)
		referenced = parser.Referenced(path)
	}
	w.mu.Unlock()
	reply := func(mt string, data []byte) *pb.ServerMessage {
		return &pb.ServerMessage{Msg: &pb.ServerMessage_AgentFile{AgentFile: &pb.AgentFile{Ref: m.Ref, MediaType: mt, Data: data}}}
	}
	if ok {
		if data := pastedOriginal(img); data != nil {
			return reply("image/png", data), nil
		}
		return reply(img.MediaType, img.Data), nil
	}
	if !isFile || !referenced {
		return nil, errors.New("not something this session referenced")
	}
	mt, data, err := readReferencedFile(path, viewer)
	if err != nil {
		return nil, err
	}
	return reply(mt, data), nil
}

// pastedOriginal reads the PNG a pasted image came from, if it's still
// there (Claude Code keeps it in a temporary folder), is the same paste
// (not written after the message: paste numbers are reused) and has
// transparency (else the JPEG, smaller, looks the same): nil otherwise.
func pastedOriginal(img agentview.Image) []byte {
	if img.Original == "" || img.Sent.IsZero() {
		return nil
	}
	st, err := os.Lstat(img.Original)
	if err != nil || !st.Mode().IsRegular() || st.Size() > agentFileLimit || st.ModTime().After(img.Sent.Add(5*time.Second)) {
		return nil
	}
	data, err := os.ReadFile(img.Original)
	if err != nil || !pngHasAlpha(data) {
		return nil
	}
	return data
}

// pngHasAlpha reports whether data is a PNG with an alpha channel or a
// transparent colour.
func pngHasAlpha(data []byte) bool {
	if len(data) < 33 || !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return false
	}
	if t := data[25]; t == 4 || t == 6 { // grey or RGB, with alpha
		return true
	}
	idat := bytes.Index(data, []byte("IDAT"))
	trns := bytes.Index(data, []byte("tRNS"))
	return trns > 0 && (idat < 0 || trns < idat)
}

// readReferencedFile reads a file an agent's session referenced, for the
// agent view. A viewer gets it only if it's an image, by its content.
func readReferencedFile(path string, viewer bool) (mediaType string, data []byte, err error) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() > agentFileLimit {
		return "", nil, errors.New("can't read that file")
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	sniffed := http.DetectContentType(data)
	if viewer {
		if !strings.HasPrefix(sniffed, "image/") {
			return "", nil, errViewerFile
		}
		return sniffed, data, nil
	}
	mt := mime.TypeByExtension(filepath.Ext(path))
	if mt == "" {
		mt = "text/plain"
	}
	return mt, data, nil
}

func (d *Daemon) agentWatchFor(paneID string) (*agentWatch, *pane) {
	d.mu.Lock()
	defer d.mu.Unlock()
	w := d.agentWatches[paneID]
	if w == nil {
		return nil, nil
	}
	return w, w.p
}

// bracketedPaste: the program turned on bracketed paste (mode 2004).
func (p *pane) bracketedPaste() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mirror != nil && p.mirror.BracketedPaste()
}

// kittyKeys: the program turned on the kitty keyboard protocol.
func (p *pane) kittyKeys() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mirror != nil && p.mirror.KittyKeyboard()
}
