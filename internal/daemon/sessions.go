package daemon

// The session finder (Phase 5's thread history browser): every Claude Code
// and Codex session on this Mac, from internal/sessions' index, and resuming
// one in a new tab.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/holder"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/sessions"
	"github.com/homegrownsw/hammock/internal/thread"
)

// sessionFinder opens the index when it's first wanted (most daemons, and
// every test's, never are).
type sessionFinder struct {
	d        *Daemon
	mu       sync.Mutex
	index    *sessions.Index
	err      error
	indexed  bool      // a refresh has finished: results are complete
	running  bool      // a refresh is under way
	refreshd time.Time // when the last one finished
}

// sessionsFresh: how old the index may be when asked before it's refreshed
// first (a refresh that finds nothing new takes milliseconds).
const sessionsFresh = 3 * time.Second

// sessionSources are what the index reads: each agent's sessions, in its
// own folders and any others agents were started with.
func sessionSources() sessions.Sources {
	var out sessions.Sources
	for _, a := range agents.All {
		if a.Sessions == nil || a.Index == nil {
			continue
		}
		src := sessions.Source{
			Harness: a.ID, Appends: a.Appends, Read: a.Index, Open: a.Open, Stat: a.Stat,
			Files: func() []string { return a.Sessions(agentRoots(a.ID)) },
		}
		if a.Names != nil {
			src.Names = func() map[string]string { return a.Names(agentRoots(a.ID)) }
		}
		out = append(out, src)
	}
	return out
}

// ready returns the index, refreshed if it's stale; complete is false while
// the first index is still being built (in the background).
func (f *sessionFinder) ready() (x *sessions.Index, complete bool, err error) {
	f.mu.Lock()
	if f.index == nil && f.err == nil {
		f.index, f.err = sessions.Open(filepath.Join(f.d.cfg.Dir, "sessions.db"), sessionSources())
		if f.err != nil {
			f.d.log.Warn("session index", "err", f.err)
		}
	}
	x, err = f.index, f.err
	if err != nil {
		f.mu.Unlock()
		return nil, false, errors.New("the session index isn't available: " + err.Error())
	}
	stale := time.Since(f.refreshd) > sessionsFresh
	switch {
	case f.running:
		complete = f.indexed
		f.mu.Unlock()
	case !f.indexed:
		// The first time can take seconds: in the background.
		f.running = true
		f.mu.Unlock()
		go f.refresh()
	case stale:
		f.running = true
		f.mu.Unlock()
		f.refresh()
		complete = true
	default:
		complete = true
		f.mu.Unlock()
	}
	return x, complete, nil
}

func (f *sessionFinder) refresh() {
	start := time.Now()
	n, err := f.index.Refresh()
	if err != nil {
		f.d.log.Warn("session index refresh", "err", err)
	} else if n > 0 {
		f.d.log.Info("session index refreshed", "read", n, "ms", time.Since(start).Milliseconds())
	}
	f.mu.Lock()
	f.running, f.indexed, f.refreshd = false, true, time.Now()
	f.mu.Unlock()
}

func (f *sessionFinder) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.index != nil && !f.running {
		f.index.Close()
	}
}

// openSessions maps sessions running in panes now to their panes.
// Callers don't hold d.mu.
func (d *Daemon) openSessions() map[string]string {
	d.mu.Lock()
	ps := make([]*pane, 0, len(d.panes))
	for _, p := range d.panes {
		ps = append(ps, p)
	}
	d.mu.Unlock()
	out := map[string]string{}
	for _, p := range ps {
		p.mu.Lock()
		if a := p.info.Agent; a != nil && a.SessionId != "" && !p.info.Exited {
			out[a.Harness+":"+a.SessionId] = p.id
		}
		p.mu.Unlock()
	}
	return out
}

func (d *Daemon) listSessions(c *conn, m *pb.ListSessions) (*pb.ServerMessage, error) {
	folder, err := d.sessionFolder(c, m)
	if err != nil {
		return nil, err
	}
	var among []string
	if m.GroupId != "" {
		if among, err = d.groupFolders(m.GroupId); err != nil {
			return nil, err
		}
	}
	x, complete, err := d.sessions.ready()
	if err != nil {
		return nil, err
	}
	found, err := x.SearchIn(m.Query, folder, among, int(m.Limit))
	if err != nil {
		return nil, err
	}
	open := d.openSessions()
	out := &pb.SessionList{Indexing: !complete}
	for _, s := range found {
		out.Sessions = append(out.Sessions, &pb.SessionInfo{
			Harness: s.Harness, Id: s.ID, Title: s.Title, Cwd: s.Cwd, FirstPrompt: s.FirstPrompt, LastPrompt: s.LastPrompt,
			CreatedUnixMs: s.Created.UnixMilli(), UpdatedUnixMs: s.Updated.UnixMilli(), Messages: int32(s.Messages),
			Snippet: s.Snippet, OpenPaneId: open[s.Harness+":"+s.ID],
		})
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_SessionList{SessionList: out}}, nil
}

// groupFolders are the folders open in a Group: its terminals' folders,
// each widened to its git root, so a pane in a project's subfolder finds
// the project's sessions. Not the home folder or /, which would be every
// session. Callers don't hold d.mu.
func (d *Daemon) groupFolders(id string) ([]string, error) {
	d.mu.Lock()
	g := d.groups[id]
	if g == nil {
		d.mu.Unlock()
		return nil, fmt.Errorf("no Group %q", id)
	}
	// The folder it keeps: an empty Group's sessions are still its own.
	cwds := []string{g.Folder}
	for _, tid := range g.TabIds {
		t := d.tabs[tid]
		if t == nil {
			continue
		}
		for _, pid := range tabPanes(t) {
			if p := d.panes[pid]; p != nil && p.isTerminal() {
				cwds = append(cwds, p.cwd())
			}
		}
	}
	d.mu.Unlock()
	home, _ := os.UserHomeDir()
	folders := []string{}
	for _, cwd := range cwds {
		if cwd == "" {
			continue
		}
		f := projectRoot(cwd) // stats the disk: not under d.mu
		if f != "/" && f != filepath.Clean(home) && !slices.Contains(folders, f) {
			folders = append(folders, f)
		}
	}
	return folders, nil
}

// resumeSession opens a session in a new tab (or finds the pane it's in).
// Callers don't hold d.mu.
func (d *Daemon) resumeSession(m *pb.ResumeSession) (*pb.SessionResumed, error) {
	x, _, err := d.sessions.ready()
	if err != nil {
		return nil, err
	}
	s, err := findSession(x, m.Harness, m.Id)
	if err != nil {
		return nil, err
	}
	target := m.TargetHarness
	if target == "" {
		target = s.Harness
	}
	// Its own agent resumes it with its own command; another needs a codec
	// to convert it.
	own := agents.Get(target)
	if _, ok := thread.CodecFor(target); !ok && (target != s.Harness || own == nil || own.Resume == nil) {
		return nil, fmt.Errorf("resuming in %s isn't supported", target)
	}
	if _, ok := thread.CodecFor(s.Harness); !ok && target != s.Harness {
		return nil, fmt.Errorf("continuing %s sessions in another agent isn't supported", s.Harness)
	}
	if target == s.Harness {
		if pid := d.openSessions()[s.Harness+":"+s.ID]; pid != "" {
			d.mu.Lock()
			tab := ""
			if p := d.panes[pid]; p != nil {
				tab = p.tabID()
			}
			d.mu.Unlock()
			return &pb.SessionResumed{TabId: tab, PaneId: pid, AlreadyOpen: true}, nil
		}
	}

	// Its folder, if it's still there.
	cwd := s.Cwd
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		cwd, _ = os.UserHomeDir()
	}

	// The command: its own resume, or a conversion into target.
	var cmd, report, transcript, sessionID string
	if target == s.Harness {
		if src, ok := thread.CodecFor(s.Harness); ok {
			cmd = shellJoin(src.ResumeCommand(s.ID))
		} else {
			cmd = own.Resume(s.ID)
		}
		sessionID, transcript = s.ID, s.Path
	} else {
		src, _ := thread.CodecFor(s.Harness)
		var th thread.Thread
		var recs []thread.Record
		if err := readSession(src, s.Path, &th, &recs); err != nil {
			return nil, fmt.Errorf("can't read the session: %w", err)
		}
		th.Cwd = cwd
		if existing, found, _ := d.store.ThreadByNative(s.Harness, th.NativeID); found {
			th.ID = existing.ID
		} else if th.ID == "" {
			th.ID = thread.NewID()
		}
		thread.Seal(recs)
		d.persist(d.store.PutThread(th, recs))
		var nt thread.Thread
		var c carryOver
		cmd, nt, transcript, c = d.convert(th, recs, target, cwd, "resume-"+s.ID)
		sessionID, report = nt.NativeID, c.String(target)
	}

	d.mu.Lock()
	gid := d.groupForDir(s.Cwd, m.GroupId)
	if gid == "" {
		d.mu.Unlock()
		return nil, errors.New("no Group to open it in")
	}
	t, err := d.createTab(gid, "", &pb.PaneSpec{Cwd: cwd}, "", "")
	if err != nil {
		d.mu.Unlock()
		return nil, err
	}
	p := d.panes[t.FocusedPaneId]
	d.mu.Unlock()
	if p == nil {
		return nil, errors.New("the new tab has no pane")
	}
	p.mu.Lock()
	// Known before its hooks say so (as after a switch).
	p.info.Agent = &pb.Agent{Harness: target, State: pb.AgentState_AGENT_STATE_STARTING, StateSinceUnixMs: time.Now().UnixMilli(),
		SessionId: sessionID, TranscriptPath: safeTranscript(transcript), Heuristic: true}
	ok := p.postLocked(func(hc *holder.Client) { _ = hc.Input([]byte(cmd + "\r")) })
	p.mu.Unlock()
	if !ok {
		return nil, errors.New("the new tab's shell isn't running")
	}
	d.mu.Lock()
	d.paneChangedIfLive(p)
	d.mu.Unlock()
	return &pb.SessionResumed{TabId: t.Id, PaneId: p.id, Report: report}, nil
}

// groupForDir picks the Group a session from dir opens in: the folder's
// (groupUsingDir), else fallback if it exists, else the first. Callers
// hold d.mu.
func (d *Daemon) groupForDir(dir, fallback string) string {
	if g := d.groupUsingDir(dir); g != "" {
		return g
	}
	if _, ok := d.groups[fallback]; ok {
		return fallback
	}
	gs := d.groupsInOrder()
	if len(gs) > 0 {
		return gs[0].Id
	}
	return ""
}

func (d *Daemon) groupsInOrder() []*pb.Group {
	gs := make([]*pb.Group, 0, len(d.groups))
	for _, g := range d.groups {
		gs = append(gs, g)
	}
	slices.SortFunc(gs, func(a, b *pb.Group) int { return int(a.Order - b.Order) })
	return gs
}

// groupUsingDir is the folder's Group: the first (in sidebar order) whose
// most recently focused terminal is in dir, else one with any terminal
// there; "" if none. Callers hold d.mu.
func (d *Daemon) groupUsingDir(dir string) string {
	gs := d.groupsInOrder()
	clean := filepath.Clean(dir)
	inDir := func(id string) bool {
		p := d.panes[id]
		if p == nil || !p.isTerminal() {
			return false
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.info.Cwd != "" && filepath.Clean(p.info.Cwd) == clean
	}
	any := ""
	for _, g := range gs {
		if inDir(g.LastFocusedPaneId) {
			return g.Id
		}
		if any != "" {
			continue
		}
		for _, tid := range g.TabIds {
			if t := d.tabs[tid]; t != nil && slices.ContainsFunc(tabPanes(t), inDir) {
				any = g.Id
				break
			}
		}
	}
	return any
}

// readSession parses a session's transcript as the agent view does, for a
// read-only look before resuming it: the newest turns, or those before
// m.BeforeBlockId.
func (d *Daemon) readSession(m *pb.ReadSession) (*pb.AgentView, error) {
	x, _, err := d.sessions.ready()
	if err != nil {
		return nil, err
	}
	s, err := findSession(x, m.Harness, m.Id)
	if err != nil {
		return nil, err
	}
	ag := agents.Get(s.Harness)
	if ag == nil || ag.NewParser == nil {
		return nil, fmt.Errorf("can't show %s sessions", s.Harness)
	}
	parser := ag.NewParser(s.Cwd, false)
	r, err := ag.OpenSession(s.Path)
	if err != nil {
		return nil, fmt.Errorf("can't read the session: %w", err)
	}
	defer r.Close()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		parser.Feed(sc.Bytes())
	}
	blocks := parser.Blocks()
	to := len(blocks)
	if m.BeforeBlockId != "" {
		for i, b := range blocks {
			if b.Id == m.BeforeBlockId {
				to = i
				break
			}
		}
	}
	from := windowStart(blocks, to)
	v := &pb.AgentView{SessionId: s.ID, Blocks: blocks[from:to], HasMore: from > 0, Older: m.BeforeBlockId != "", Replace: m.BeforeBlockId == ""}
	v.Model, v.ContextTokens, v.ContextWindow = parser.Context()
	return v, nil
}

// ---------- Agents and past sessions ----------
//
// An agent may search the sessions started in its own project (its pane's
// git root, else its folder) and read excerpts of them; other folders take
// the person's permission (a "sessions" grant, asked for the agent's own
// pane, so "always in this Group" works as for panes).

// projectRoot is dir's git root, short of the home folder, else dir.
func projectRoot(dir string) string {
	home, _ := os.UserHomeDir()
	for d := filepath.Clean(dir); d != "/" && d != home && d != "."; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
	}
	return filepath.Clean(dir)
}

// agentRoot is projectRoot for an agent's harness started at started: a
// git root only if it's a real repository (HEAD and objects, or a
// worktree's .git file pointing at one) made before the harness started.
// Else an agent in a folder with no repository (~/Developer/notes) could
// `mkdir ~/Developer/.git` and have every project in ~/Developer for its
// own. Short of one, the harness's own folder.
func agentRoot(dir string, started time.Time) string {
	home, _ := os.UserHomeDir()
	for d := filepath.Clean(dir); d != "/" && d != home && d != "."; d = filepath.Dir(d) {
		git := filepath.Join(d, ".git")
		if _, err := os.Lstat(git); err != nil {
			continue
		}
		born, ok := fileBorn(git)
		if gitRepo(git) && ok && !started.IsZero() && born.Before(started) {
			return d
		}
		break // the nearest .git is the one git would use: no further up
	}
	return filepath.Clean(dir)
}

// gitRepo reports whether git (a folder's .git) is a repository: a
// directory with HEAD and objects/, or a worktree's file naming one.
func gitRepo(git string) bool {
	fi, err := os.Lstat(git)
	if err != nil {
		return false
	}
	if fi.Mode().IsRegular() {
		b, err := os.ReadFile(git)
		target, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
		if err != nil || !ok || fi.Size() > 4096 {
			return false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(git), target)
		}
		_, err = os.Stat(filepath.Join(target, "HEAD"))
		return err == nil
	}
	if !fi.IsDir() {
		return false
	}
	head, err1 := os.Stat(filepath.Join(git, "HEAD"))
	objects, err2 := os.Stat(filepath.Join(git, "objects"))
	return err1 == nil && err2 == nil && head.Mode().IsRegular() && objects.IsDir()
}

// inFolder: path is folder or inside it.
func inFolder(path, folder string) bool {
	path, folder = filepath.Clean(path), filepath.Clean(folder)
	return path == folder || strings.HasPrefix(path, folder+string(filepath.Separator))
}

// sessionScope is, for an agent's connection, its project folder and
// whether it may look everywhere (allowed by the person); for anyone else,
// "" and true. Callers don't hold d.mu.
func (d *Daemon) sessionScope(c *conn) (project string, everywhere bool) {
	cl := c.caller
	if !cl.isAgent() {
		return "", true
	}
	project = d.agentProject(cl.pane)
	d.mu.Lock()
	defer d.mu.Unlock()
	return project, d.granted(cl.pane, "sessions", cl.pane)
}

// agentProject is the project of the agent a pane runs (or, for a pane an
// agent opened, the agent that opened it): the git root of the folder its
// harness runs in, as the system has it. Not the pane's own folder: a
// program prints that (OSC 7), and `cd` changes it. Never your home
// folder or above, which would be every project: "" then (ask). Callers
// don't hold d.mu.
func (d *Daemon) agentProject(pane string) string {
	d.mu.Lock()
	var shells []int
	ids := []string{pane} // its own first
	for id := range d.owners(pane) {
		if id != pane {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if p := d.panes[id]; p != nil {
			p.mu.Lock()
			if p.pid > 0 {
				shells = append(shells, p.pid)
			}
			p.mu.Unlock()
		}
	}
	d.mu.Unlock()
	for _, shell := range shells {
		agent := shell
		if !isAgentProcess(shell) {
			_, agent = findAgentProc(shell)
		}
		if agent <= 0 {
			continue
		}
		cwd, err := procinfo.Cwd(agent)
		if err != nil {
			continue
		}
		cwd = logicalDir(cwd, procinfo.Env(agent))
		started, _ := procinfo.StartTime(agent)
		if root := agentRoot(cwd, started); !tooWide(root) {
			return root
		}
		return ""
	}
	return ""
}

// tooWide: dir is /, your home folder or above it, where every project is.
func tooWide(dir string) bool {
	home, _ := os.UserHomeDir()
	dir = filepath.Clean(dir)
	return dir == "/" || dir == "." || !filepath.IsAbs(dir) || home != "" && inFolder(home, dir)
}

// sessionFolder is the folder a search looks in, as the caller may.
func (d *Daemon) sessionFolder(c *conn, m *pb.ListSessions) (string, error) {
	project, everywhere := d.sessionScope(c)
	switch {
	case project == "" && everywhere: // a person
		return m.Folder, nil
	case m.Everywhere:
		if !everywhere {
			return "", errAgentScope
		}
		return "", nil
	case m.GroupId != "":
		// A Group's folders are anywhere: as everywhere, within folder if given.
		if !everywhere {
			return "", errAgentScope
		}
		return m.Folder, nil
	case m.Folder != "":
		if !everywhere && (project == "" || !inFolder(m.Folder, project)) {
			return "", errAgentScope
		}
		return m.Folder, nil
	case project == "":
		return "", errAgentScope
	}
	return project, nil
}

// excerptMessage is one of a session's messages, as excerpts show it.
type excerptMessage struct {
	role  string
	at    time.Time
	text  string
	index int
}

// sessionMessages are a session's prompts and replies, in order: no tool
// calls or results, no command echoes or system notes.
func sessionMessages(s sessions.Session) ([]excerptMessage, error) {
	recs, err := sessionRecords(s)
	if err != nil {
		return nil, err
	}
	var out []excerptMessage
	for _, r := range recs {
		role := map[thread.Kind]string{thread.KindUser: "user", thread.KindAssistant: "assistant"}[r.Kind]
		t := strings.TrimSpace(thread.UnwrapPasted(r.Text))
		if role == "" || t == "" || (role == "user" && strings.HasPrefix(t, "<")) {
			continue
		}
		out = append(out, excerptMessage{role: role, at: r.Time, text: t, index: len(out)})
	}
	return out, nil
}

// sessionRecords reads a session whole: with its agent's codec where it
// has one (the main chain), else as the session finder reads it.
func sessionRecords(s sessions.Session) ([]thread.Record, error) {
	ag := agents.Get(s.Harness)
	if ag == nil {
		return nil, fmt.Errorf("reading %s sessions isn't supported", s.Harness)
	}
	f, err := ag.OpenSession(s.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if codec := ag.Codec(); codec != nil {
		_, recs, err := codec.Decode(bufio.NewReaderSize(f, 1<<20))
		return recs, err
	}
	if ag.Index == nil {
		return nil, fmt.Errorf("reading %s sessions isn't supported", s.Harness)
	}
	var recs []thread.Record
	var t thread.Thread
	_, err = ag.Index(f, &t, func(r thread.Record) { recs = append(recs, r) })
	return recs, err
}

// findSession is session id of harness ("": any agent's) in the index.
func findSession(x *sessions.Index, harness, id string) (sessions.Session, error) {
	err := fmt.Errorf("no session %q", id)
	for _, a := range agents.All {
		if harness != "" && harness != a.ID {
			continue
		}
		s, e := x.Get(a.ID, id)
		if e == nil {
			return s, nil
		}
		err = e
	}
	return sessions.Session{}, err
}

// excerpt picks what to show of a session's messages: with a query, those
// holding every word (else any), each with the message before and after;
// without, the last limit. Each is clipped, around the first match.
func excerpt(msgs []excerptMessage, query string, limit int) (out []excerptMessage, matched int) {
	if limit <= 0 {
		limit = 12
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		out = append(out, msgs[max(0, len(msgs)-limit):]...)
		for i := range out {
			out[i].text = clipAround(out[i].text, "", 1500)
		}
		return out, 0
	}
	has := func(m excerptMessage, all bool) bool {
		l := strings.ToLower(m.text)
		for _, w := range words {
			if strings.Contains(l, w) != all {
				return !all
			}
		}
		return all
	}
	var hits []int
	for _, all := range []bool{true, false} {
		for i, m := range msgs {
			if has(m, all) {
				hits = append(hits, i)
			}
		}
		if len(hits) > 0 {
			break
		}
	}
	matched = len(hits)
	keep := map[int]bool{}
	for _, i := range hits {
		for _, j := range []int{i - 1, i, i + 1} {
			if j >= 0 && j < len(msgs) && len(keep) < limit {
				keep[j] = true
			}
		}
		if len(keep) >= limit {
			break
		}
	}
	for i, m := range msgs {
		if keep[i] {
			first := ""
			for _, w := range words {
				if strings.Contains(strings.ToLower(m.text), w) {
					first = w
					break
				}
			}
			m.text = clipAround(m.text, first, 1500)
			out = append(out, m)
		}
	}
	return out, matched
}

// clipAround cuts s to about n runes, around the first occurrence of word
// (case-insensitive) if given, else from the start, marking cuts with "…".
func clipAround(s, word string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	start := 0
	if word != "" {
		if i := strings.Index(strings.ToLower(s), word); i >= 0 {
			start = max(0, len([]rune(s[:i]))-n/3)
		}
	}
	end := min(len(r), start+n)
	start = max(0, end-n)
	out := string(r[start:end])
	if start > 0 {
		out = "…" + out
	}
	if end < len(r) {
		out += "…"
	}
	return out
}

// sessionExcerpts reads the parts of a session that matter. Callers don't hold d.mu.
func (d *Daemon) sessionExcerpts(c *conn, m *pb.SessionExcerpts) (*pb.SessionExcerpt, error) {
	x, _, err := d.sessions.ready()
	if err != nil {
		return nil, err
	}
	s, err := findSession(x, m.Harness, m.Id)
	if err != nil {
		return nil, err
	}
	if project, everywhere := d.sessionScope(c); !everywhere && (project == "" || !inFolder(s.Cwd, project)) {
		return nil, errAgentScope
	}
	msgs, err := sessionMessages(s)
	if err != nil {
		return nil, err
	}
	var shown []excerptMessage
	var matched int
	if m.FirstPrompt {
		for _, e := range msgs {
			if e.role == "user" {
				shown = []excerptMessage{e}
				break
			}
		}
	} else {
		shown, matched = excerpt(msgs, m.Query, int(m.Limit))
	}
	out := &pb.SessionExcerpt{Matched: int32(matched), Session: &pb.SessionInfo{
		Harness: s.Harness, Id: s.ID, Title: s.Title, Cwd: s.Cwd, FirstPrompt: s.FirstPrompt, LastPrompt: s.LastPrompt,
		CreatedUnixMs: s.Created.UnixMilli(), UpdatedUnixMs: s.Updated.UnixMilli(), Messages: int32(s.Messages),
		OpenPaneId: d.openSessions()[s.Harness+":"+s.ID],
	}}
	for _, e := range shown {
		out.Messages = append(out.Messages, &pb.SessionMessage{Role: e.role, TimeUnixMs: e.at.UnixMilli(), Text: e.text, Index: int32(e.index)})
	}
	return out, nil
}

// logicalDir is dir as the program was started in it ($PWD, through any
// symbolic links: as its sessions record it) when that is the same folder.
func logicalDir(dir string, env []string) string {
	for _, kv := range env {
		if pwd, ok := strings.CutPrefix(kv, "PWD="); ok && filepath.IsAbs(pwd) {
			if r, err := filepath.EvalSymlinks(pwd); err == nil && r == dir {
				return pwd
			}
		}
	}
	return dir
}
