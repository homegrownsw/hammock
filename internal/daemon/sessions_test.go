package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// The session finder lists a Claude session and resumes it in a new tab in
// the Group already in its folder; asked again, it finds that pane.
func TestSessionFinderListsAndResumes(t *testing.T) {
	base := t.TempDir()
	setAgentRoots(t, map[string][]string{"claude": {filepath.Join(base, "claude", "projects")}, "codex": {filepath.Join(base, "codex", "sessions")}})
	work := t.TempDir()
	proj := filepath.Join(base, "claude", "projects", "-work")
	os.MkdirAll(proj, 0o755)
	os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(
		`{"type":"user","sessionId":"s1","cwd":"`+work+`","timestamp":"2026-11-01T08:00:00Z","message":{"role":"user","content":"tidy the zeppelin hangar"}}`+"\n"+
			`{"type":"custom-title","customTitle":"Hangar tidy","sessionId":"s1"}`+"\n"), 0o600)
	// A fake claude, so resuming runs nothing real; a plain shell for panes.
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho RESUMED \"$@\"\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin") // nothing real called claude
	plainShell(t)
	t.Setenv("ENV", "")

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	other := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Other"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: other, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	here := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: here, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, Cwd: work}}}})
	// Its cwd as the daemon sees it (a command's own).
	d.mu.Lock()
	for _, p := range d.panes {
		if t := d.tabs[p.tabID()]; t != nil && t.GroupId == here {
			p.mu.Lock()
			p.info.Cwd = work
			p.mu.Unlock()
		}
	}
	d.mu.Unlock()

	var list *pb.SessionList
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		list = call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{Query: "zepp hangar"}}}).GetSessionList()
		if !list.Indexing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still indexing")
		}
	}
	if len(list.Sessions) != 1 || list.Sessions[0].Title != "Hangar tidy" || list.Sessions[0].Id != "s1" || list.Sessions[0].Cwd != work {
		t.Fatalf("list: %v", list)
	}

	// Read before resuming: the agent view's blocks, no pane.
	v := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadSession{ReadSession: &pb.ReadSession{Id: "s1"}}}).GetAgentView()
	if v == nil || v.SessionId != "s1" || len(v.Blocks) == 0 || v.Blocks[0].Text != "tidy the zeppelin hangar" || v.PaneId != "" {
		t.Fatalf("read: %v", v)
	}

	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ResumeSession{ResumeSession: &pb.ResumeSession{Id: "s1", GroupId: other}}}).GetSessionResumed()
	if r == nil || r.PaneId == "" || r.AlreadyOpen {
		t.Fatalf("resumed: %v", r)
	}
	d.mu.Lock()
	gid := d.tabs[r.TabId].GroupId
	d.mu.Unlock()
	if gid != here {
		t.Fatalf("opened in %s, not the Group in its folder (%s)", gid, here)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: r.PaneId}}})
	waitOutput(t, c, r.PaneId, "RESUMED --resume s1")

	// Listed as open there, and resuming it again goes to that pane.
	d.mu.Lock()
	rp := d.panes[r.PaneId]
	d.mu.Unlock()
	rp.mu.Lock()
	rp.info.Agent.SessionId = "s1" // as its hooks would say
	rp.mu.Unlock()
	list = call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{}}}).GetSessionList()
	if len(list.Sessions) != 1 || list.Sessions[0].OpenPaneId != r.PaneId {
		t.Fatalf("not shown open: %v", list)
	}
	again := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ResumeSession{ResumeSession: &pb.ResumeSession{Id: "s1"}}}).GetSessionResumed()
	if !again.AlreadyOpen || again.PaneId != r.PaneId {
		t.Fatalf("again: %v", again)
	}
}

// Only on this Mac: agents see only their own project's sessions, and
// can't open one in the agent view's reader.
func TestSessionFinderIsLocal(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	pane := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	agent := dialAs(t, dir, &caller{pane: pane, agent: true}, "mcp")
	if _, err := agent.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{Everywhere: true}}}); err == nil {
		t.Fatal("an agent listed every folder's sessions without asking")
	}
	if _, err := agent.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_ReadSession{ReadSession: &pb.ReadSession{Id: "x"}}}); err == nil {
		t.Fatal("an agent read a session")
	}
}

// An agent searches and reads its own project's sessions; other folders'
// need a "sessions" grant.
func TestAgentsReadTheirProjectsSessions(t *testing.T) {
	base := t.TempDir()
	setAgentRoots(t, map[string][]string{"claude": {filepath.Join(base, "claude", "projects")}, "codex": {filepath.Join(base, "codex", "sessions")}})
	work, elsewhere := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(work, ".git"), 0o755)
	os.MkdirAll(filepath.Join(work, "web"), 0o755)
	line := func(sid, cwd, typ, text string) string {
		return `{"type":"` + typ + `","sessionId":"` + sid + `","cwd":"` + cwd + `","timestamp":"2026-11-01T08:00:00Z","message":{"role":"` + typ + `","content":"` + text + `"}}` + "\n"
	}
	for _, s := range []struct{ sid, cwd string }{{"mine", filepath.Join(work, "web")}, {"theirs", elsewhere}} {
		proj := filepath.Join(base, "claude", "projects", strings.ReplaceAll(s.cwd, "/", "-"))
		os.MkdirAll(proj, 0o755)
		os.WriteFile(filepath.Join(proj, s.sid+".jsonl"), []byte(
			line(s.sid, s.cwd, "user", "what cache policy did we pick")+
				line(s.sid, s.cwd, "assistant", "Two layers: memory then disk, a five minute expiry.")+
				line(s.sid, s.cwd, "user", "and the zeppelin?")), 0o600)
	}

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	// The agent: a stand-in harness (pane-holder copied as "claude"), started
	// in the project, below its root. Its folder is the project, not the
	// pane's (which a program can say is anywhere: OSC 7).
	bin := t.TempDir()
	claude := filepath.Join(bin, "claude")
	if out, err := exec.Command("cp", holderBin, claude).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{
		Command: []string{"/bin/sh", "-c", claude + " --idle-exit 1s --socket " + filepath.Join(shortDir(t), "h.sock") + " -- /bin/cat & wait"}, Cwd: filepath.Join(work, "web"),
	}}}}))
	d.mu.Lock()
	pane := d.tabs[tid].FocusedPaneId
	p := d.panes[pane]
	d.mu.Unlock()
	home, _ := os.UserHomeDir()
	p.mu.Lock()
	p.info.Cwd = home // as an OSC 7 says
	p.mu.Unlock()
	waitFor(t, "the harness", func() bool {
		p.mu.Lock()
		shell := p.pid
		p.mu.Unlock()
		if shell <= 0 {
			return false
		}
		_, pid := findAgentProc(shell)
		return pid > 0
	})
	agent := dialAs(t, dir, &caller{pane: pane, agent: true}, "mcp")
	ask := func(m *pb.ClientMessage) (*pb.ServerMessage, error) { return agent.Call(context.Background(), m) }

	var list *pb.SessionList
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		r, err := ask(&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{Query: "cache policy"}}})
		if err != nil {
			t.Fatal(err)
		}
		if list = r.GetSessionList(); !list.Indexing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still indexing")
		}
	}
	if len(list.Sessions) != 1 || list.Sessions[0].Id != "mine" {
		t.Fatalf("its project's: %v", list)
	}
	if _, err := ask(&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{Folder: elsewhere}}}); err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("another folder without asking: %v", err)
	}
	if _, err := ask(&pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: &pb.SessionExcerpts{Id: "theirs"}}}); err == nil {
		t.Fatal("read another folder's session without asking")
	}

	// A Group's sessions (its "Resume a Session…"): its panes' projects,
	// not elsewhere; none for a Group with no terminals; agents must ask.
	// The pane, for now, is below the session's folder: found by the git root.
	p.mu.Lock()
	p.info.Cwd = filepath.Join(work, "web", "deep")
	p.mu.Unlock()
	mine := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{GroupId: gid}}}).GetSessionList()
	if len(mine.Sessions) != 1 || mine.Sessions[0].Id != "mine" {
		t.Fatalf("the Group's: %v", mine)
	}
	p.mu.Lock()
	p.info.Cwd = filepath.Join(work, "web")
	p.mu.Unlock()
	away := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Away"}}}))
	atid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: away, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, Cwd: elsewhere}}}}))
	d.mu.Lock()
	ap := d.panes[d.tabs[atid].FocusedPaneId]
	d.mu.Unlock()
	ap.mu.Lock()
	ap.info.Cwd = elsewhere
	ap.mu.Unlock()
	if l := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{GroupId: away}}}).GetSessionList(); len(l.Sessions) != 1 || l.Sessions[0].Id != "theirs" {
		t.Fatalf("another Group's: %v", l)
	}
	empty := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Empty"}}}))
	if l := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{GroupId: empty}}}).GetSessionList(); len(l.Sessions) != 0 {
		t.Fatalf("a Group with no terminals: %v", l)
	}
	if _, err := c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{GroupId: "nope"}}}); err == nil {
		t.Fatal("listed a Group that doesn't exist")
	}
	if _, err := ask(&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{GroupId: gid}}}); err == nil || !strings.Contains(err.Error(), "out of scope") {
		t.Fatalf("an agent listed a Group's sessions without asking: %v", err)
	}

	// Excerpts: the matching message and its neighbours, not tool noise.
	r, err := ask(&pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: &pb.SessionExcerpts{Id: "mine", Query: "expiry"}}})
	if err != nil {
		t.Fatal(err)
	}
	ex := r.GetSessionExcerpt()
	if ex.Matched != 1 || len(ex.Messages) != 3 || ex.Messages[1].Role != "assistant" || !strings.Contains(ex.Messages[1].Text, "five minute") {
		t.Fatalf("excerpt: %v", ex)
	}
	// The first prompt alone, for Schedule This.
	r, err = ask(&pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: &pb.SessionExcerpts{Id: "mine", FirstPrompt: true}}})
	if m := r.GetSessionExcerpt().GetMessages(); err != nil || len(m) != 1 || m[0].Text != "what cache policy did we pick" {
		t.Fatalf("first prompt: %v %v", r, err)
	}

	// Allowed once: everywhere.
	d.mu.Lock()
	d.noteOnceGrant(&pb.GrantRequest{RequesterPaneId: pane, Action: "sessions", TargetPaneId: pane})
	d.mu.Unlock()
	r, err = ask(&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{Query: "cache", Everywhere: true}}})
	if err != nil || len(r.GetSessionList().Sessions) != 2 {
		t.Fatalf("everywhere, allowed: %v %v", r, err)
	}
	// Allowed, another Group's: all of its sessions, not just its project's share.
	r, err = ask(&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{GroupId: away}}})
	if err != nil || len(r.GetSessionList().Sessions) != 1 || r.GetSessionList().Sessions[0].Id != "theirs" {
		t.Fatalf("another Group's, allowed: %v %v", r, err)
	}
	if _, err := ask(&pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: &pb.SessionExcerpts{Id: "theirs"}}}); err != nil {
		t.Fatalf("another folder's, allowed: %v", err)
	}
}

func TestExcerptPicksMatchesAndNeighbours(t *testing.T) {
	var msgs []excerptMessage
	for i, s := range []string{"a", "b", "the Cache key", "c", "d", "cache again", "e"} {
		msgs = append(msgs, excerptMessage{role: "user", text: s, index: i})
	}
	out, matched := excerpt(msgs, "cache", 0)
	var got []string
	for _, m := range out {
		got = append(got, m.text)
	}
	if matched != 2 || strings.Join(got, ",") != "b,the Cache key,c,d,cache again,e" {
		t.Fatalf("got %v (%d)", got, matched)
	}
	if out, _ := excerpt(msgs, "", 2); len(out) != 2 || out[1].text != "e" {
		t.Fatalf("no query: the last ones: %v", out)
	}
	long := strings.Repeat("x", 5000) + " needle " + strings.Repeat("y", 5000)
	if c := clipAround(long, "needle", 1500); !strings.Contains(c, "needle") || !strings.HasPrefix(c, "…") || len([]rune(c)) > 1502 {
		t.Fatalf("clip: %d runes, has needle %v", len([]rune(c)), strings.Contains(c, "needle"))
	}
}

// An agent's project is never so wide it's every project.
func TestProjectNeverHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, dir := range []string{"/", home, filepath.Dir(home), ".", "rel/ative"} {
		if !tooWide(dir) {
			t.Errorf("%q is a project", dir)
		}
	}
	if tooWide(filepath.Join(home, "src", "x")) {
		t.Error("a folder in home is too wide")
	}
}
