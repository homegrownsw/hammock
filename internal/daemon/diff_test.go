package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Sam", "GIT_AUTHOR_EMAIL=sam@example.com",
		"GIT_COMMITTER_NAME=Sam", "GIT_COMMITTER_EMAIL=sam@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func paneDiff(d *Daemon, id string) *pb.DiffInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.panes[id]; p != nil {
		return p.snapshotInfo().Diff
	}
	return nil
}

func TestDiffPane(t *testing.T) {
	dir := shortDir(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "First")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n2\n"), 0o644)

	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	first := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Cwd: repo},
	}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Cwd: repo}}}})
	// Opened from the first tab: the diff's tab goes just after it.
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, AfterTabId: first, Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: filepath.Join(repo, ".")}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	order := slices.Clone(d.groups[g].TabIds)
	name := d.tabs[tabID].Name
	d.mu.Unlock()
	if order[1] != tabID || name != "Review" {
		t.Fatalf("tabs %v (diff %s, named %q)", order, tabID, name)
	}
	di := paneDiff(d, paneID)
	if di.Source.Kind != pb.DiffSource_KIND_UNCOMMITTED || di.Title != "Uncommitted changes" || di.Repo != repo {
		t.Fatalf("info %v", di)
	}

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: paneID}}}).GetDiffContent()
	if len(r.Files) != 1 || r.Files[0].Path != "a.txt" || r.Generation != 1 || len(r.Commits) != 1 || r.DefaultBranch != "main" {
		t.Fatalf("content %v", r)
	}
	if di := paneDiff(d, paneID); di.Files != 1 || di.Added != 1 || di.Removed != 1 || di.Generation != 1 {
		t.Fatalf("counts %v", di)
	}
	ft := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiffFile{ReadDiffFile: &pb.ReadDiffFile{PaneId: paneID, Path: "a.txt", Generation: 1}}}).GetDiffFileText()
	if ft.OldText != "one\ntwo\n" || ft.NewText != "one\n2\n" {
		t.Fatalf("sides %q %q", ft.OldText, ft.NewText)
	}

	// The working tree moves on: the pane says so, and a refresh catches up.
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("new\n"), 0o644)
	waitFor(t, "the diff to go stale", func() bool { return paneDiff(d, paneID).GetStale() })
	r = call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: paneID, Refresh: true}}}).GetDiffContent()
	if len(r.Files) != 2 || r.Generation != 2 {
		t.Fatalf("refreshed %v", r)
	}
	if di := paneDiff(d, paneID); di.Stale || di.Files != 2 {
		t.Fatalf("after refresh %v", di)
	}
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiffFile{ReadDiffFile: &pb.ReadDiffFile{PaneId: paneID, Path: "a.txt", Generation: 1}}}); err == nil {
		t.Error("reading a file of an earlier generation")
	}

	// Another source: the commit itself.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_DiffCommand{DiffCommand: &pb.DiffCommand{
		PaneId: paneID, Action: pb.DiffCommand_ACTION_SET_SOURCE, Source: &pb.DiffSource{Kind: pb.DiffSource_KIND_COMMIT, Rev: "HEAD"},
	}}})
	if di := paneDiff(d, paneID); di.Source.Kind != pb.DiffSource_KIND_COMMIT || di.Title[8:] != "First" || di.Files != 1 || di.Added != 2 {
		t.Fatalf("commit %v", di)
	}
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_DiffCommand{DiffCommand: &pb.DiffCommand{
		PaneId: paneID, Action: pb.DiffCommand_ACTION_SET_SOURCE, Source: &pb.DiffSource{Kind: pb.DiffSource_KIND_COMMIT, Rev: "nope"},
	}}}); err == nil {
		t.Error("a commit that isn't there was taken")
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_DiffCommand{DiffCommand: &pb.DiffCommand{
		PaneId: paneID, Action: pb.DiffCommand_ACTION_SET_VIEW, View: &pb.DiffView{Style: "unified", Whitespace: "sideways"},
	}}})
	if v := paneDiff(d, paneID).View; v.Style != "unified" || v.Whitespace != "" {
		t.Fatalf("view %v", v)
	}

	// Not a repository: refused when opening.
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: t.TempDir()}},
	}}}); err == nil {
		t.Error("a diff pane outside a repository")
	}
}

// ReadRepo: what a diff could compare, before one's open: the branch it's
// on, the branches to compare with (the default first) and its commits;
// the Group's folder when no path is given; why not, outside a repository.
func TestReadRepo(t *testing.T) {
	dir := shortDir(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "First")
	gitIn(t, repo, "checkout", "-q", "-b", "feature")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("two\n"), 0o644)
	gitIn(t, repo, "commit", "-q", "-am", "Second")
	os.MkdirAll(filepath.Join(repo, "sub"), 0o755)

	startDaemon(t, dir)
	c := dial(t, dir)
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadRepo{ReadRepo: &pb.ReadRepo{Path: filepath.Join(repo, "sub")}}}).GetRepoInfo()
	if r.Repo != repo || r.Branch != "feature" || r.DefaultBranch != "main" || !slices.Equal(r.Branches, []string{"main", "feature"}) ||
		len(r.Commits) != 2 || r.Commits[0].Subject != "Second" || r.Error != "" {
		t.Fatalf("repo %v", r)
	}

	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: g, Folder: repo}}})
	if r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadRepo{ReadRepo: &pb.ReadRepo{GroupId: g}}}).GetRepoInfo(); r.Repo != repo {
		t.Fatalf("the Group's folder: %v", r)
	}
	if r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadRepo{ReadRepo: &pb.ReadRepo{Path: t.TempDir()}}}).GetRepoInfo(); r.Repo != "" || !strings.Contains(r.Error, "isn't in a git repository") {
		t.Fatalf("not a repository: %v", r)
	}
}

func TestReviewThreads(t *testing.T) {
	dir := shortDir(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "First")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\nTHREE\nfour\nfive\n"), 0o644)

	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	agentTab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Cwd: repo}}}}))
	d.mu.Lock()
	agentPane := d.tabs[agentTab].FocusedPaneId
	ap := d.panes[agentPane]
	d.mu.Unlock()
	// It's an agent waiting on you: comments sent now are held.
	ap.mu.Lock()
	ap.info.Agent = &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_NEEDS_INPUT}
	ap.mu.Unlock()

	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: repo, TargetPaneId: agentPane}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: paneID}}})
	cmd := func(m *pb.ReviewCommand) (*pb.ReviewReply, error) {
		m.PaneId = paneID
		r, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReviewCommand{ReviewCommand: m}})
		return r.GetReviewReply(), err
	}

	// A comment on line 3, sent at once: held, the agent is asking you something.
	r, err := cmd(&pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_ADD_THREAD, Path: "a.txt", Line: 3, Text: "Why shout?", Send: true})
	if err != nil {
		t.Fatal(err)
	}
	th := r.Review.Threads[0]
	if th.Quote != "THREE" || th.Before != "one\ntwo" || th.After != "four\nfive" || r.Queued != 1 {
		t.Fatalf("thread %v, queued %d", th, r.Queued)
	}
	if di := paneDiff(d, paneID); di.ReviewId == "" || di.Queued != 1 || di.OpenThreads != 1 || di.TargetPaneId != agentPane {
		t.Fatalf("info %v", di)
	}
	// One kept back, a note to yourself.
	r, _ = cmd(&pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_ADD_THREAD, Path: "a.txt", Line: 1, EndLine: 2, Text: "fine"})
	note := r.CommentId
	if di := paneDiff(d, paneID); di.Unsent != 1 || di.OpenThreads != 2 {
		t.Fatalf("info %v", di)
	}
	if _, err := cmd(&pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_ADD_THREAD, Path: "nope.txt", Line: 1, Text: "x"}); err == nil {
		t.Error("a thread on a file not in the diff")
	}

	// The agent answers, and marks it addressed; it can't resolve it.
	who := reviewer{name: "claude", agent: true, pane: agentPane}
	if _, err := d.diffs.reviewCommand(&pb.ReviewCommand{PaneId: paneID, Action: pb.ReviewCommand_ACTION_SET_STATE, ThreadId: th.Id,
		State: pb.ReviewThread_STATE_RESOLVED}, who, g); err == nil {
		t.Error("an agent resolved a thread")
	}
	if _, err := d.diffs.reviewCommand(&pb.ReviewCommand{PaneId: paneID, Action: pb.ReviewCommand_ACTION_SEND}, who, g); err == nil {
		t.Error("an agent sent comments")
	}
	if _, err := d.diffs.reviewCommand(&pb.ReviewCommand{PaneId: paneID, Action: pb.ReviewCommand_ACTION_SET_STATE, ThreadId: th.Id,
		State: pb.ReviewThread_STATE_ADDRESSED, Text: "Lowered it."}, who, g); err != nil {
		t.Fatal(err)
	}
	if di := paneDiff(d, paneID); di.Unread != 1 {
		t.Fatalf("the agent's reply is unread: %v", di)
	}
	rv := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadReview{ReadReview: &pb.ReadReview{PaneId: paneID}}}).GetReview()
	if got := findThread(rv, th.Id); got.State != pb.ReviewThread_STATE_ADDRESSED || len(got.Comments) != 2 || !got.Comments[1].Agent || got.Comments[1].Author != "claude" {
		t.Fatalf("thread after the agent: %v", got)
	}
	cmd(&pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_MARK_READ})
	if di := paneDiff(d, paneID); di.Unread != 0 {
		t.Fatalf("read: %v", di)
	}
	// The message the agent gets.
	msg := reviewMessage(rv, []string{th.Comments[0].Id})
	if !strings.Contains(msg, "a.txt:3 [thread "+th.Id+"]") || !strings.Contains(msg, "> THREE") || !strings.Contains(msg, "Why shout?") {
		t.Errorf("message:\n%s", msg)
	}
	// A minified line is quoted only so far.
	long := proto.CloneOf(rv)
	findThread(long, th.Id).Quote = strings.Repeat("x", 5000)
	if msg := reviewMessage(long, []string{th.Comments[0].Id}); strings.Count(msg, "x") > reviewQuoteWidth+10 || !strings.Contains(msg, "x…") {
		t.Errorf("a long quoted line isn't cut short: %d characters", len(msg))
	}

	// The code moves on: line 3 is now line 4; lines 1-2 changed (outdated).
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("zero\nONE\nTWO\nTHREE\nfour\nfive\n"), 0o644)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: paneID, Refresh: true}}})
	rv = call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadReview{ReadReview: &pb.ReadReview{PaneId: paneID}}}).GetReview()
	if got := findThread(rv, th.Id); got.Line != 4 || got.Outdated {
		t.Errorf("moved thread: line %d, outdated %v", got.Line, got.Outdated)
	}
	if got := rv.Threads[1]; !got.Outdated {
		t.Errorf("changed lines' thread isn't outdated: %v", got)
	}

	// Your note can go; edited first.
	if _, err := cmd(&pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_EDIT_COMMENT, CommentId: note, Text: "fine really"}); err != nil {
		t.Fatal(err)
	}
	cmd(&pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_DELETE_COMMENT, CommentId: note})
	if di := paneDiff(d, paneID); di.OpenThreads != 1 || di.Unsent != 0 {
		t.Fatalf("after deleting: %v", di)
	}

	// Closing the pane with a thread open keeps the review; reopening the
	// same diff brings it back.
	reviewID := paneDiff(d, paneID).ReviewId
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: paneID}}})
	if d.diffs.reviewGroup(reviewID) != g {
		t.Fatal("the review went with its pane, threads still open")
	}
	tab2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: repo}},
	}}}))
	d.mu.Lock()
	pane2 := d.tabs[tab2].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: pane2}}})
	if di := paneDiff(d, pane2); di.ReviewId != reviewID || di.TargetPaneId != agentPane {
		t.Fatalf("reopened: %v", di)
	}
	// Resolved, and closed: it goes.
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReviewCommand{ReviewCommand: &pb.ReviewCommand{PaneId: pane2,
		Action: pb.ReviewCommand_ACTION_SET_STATE, ThreadId: th.Id, State: pb.ReviewThread_STATE_RESOLVED}}}); err != nil {
		t.Fatal(err)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: pane2}}})
	if d.diffs.reviewGroup(reviewID) != "" {
		t.Fatal("a review with every thread resolved outlived its pane")
	}
}

func TestReviewSharedAndHeld(t *testing.T) {
	dir := shortDir(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(repo, "docs"), 0o755)
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "First")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n2\n"), 0o644)

	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	agentTab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Cwd: repo}}}}))
	d.mu.Lock()
	agentPane := d.tabs[agentTab].FocusedPaneId
	ap := d.panes[agentPane]
	d.mu.Unlock()
	ap.mu.Lock()
	ap.info.Agent = &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_NEEDS_INPUT}
	ap.mu.Unlock()

	open := func() string {
		tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
			GroupId: g, Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: repo, TargetPaneId: agentPane}},
		}}}))
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.tabs[tab].FocusedPaneId
	}
	one, two := open(), open()
	for _, p := range []string{one, two} {
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: p}}})
	}
	// A comment from the first pane, with the lines as the page showed them
	// (the file has moved on since, say): the thread quotes those.
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReviewCommand{ReviewCommand: &pb.ReviewCommand{
		PaneId: one, Action: pb.ReviewCommand_ACTION_ADD_THREAD, Path: "a.txt", Line: 2, Text: "Spell it out", Send: true,
		Quote: "two", Before: "one",
	}}}).GetReviewReply()
	if q := r.Review.Threads[0].Quote; q != "two" || r.Queued != 1 {
		t.Fatalf("quote %q, queued %d", q, r.Queued)
	}
	// The second pane shows the same review, its counts too.
	waitFor(t, "the other pane's counts", func() bool {
		return paneDiff(d, two).Queued == 1 && paneDiff(d, two).ReviewId == paneDiff(d, one).ReviewId
	})

	// Its agent has gone: held comments are yours to send again, not stuck.
	ap.mu.Lock()
	ap.info.Agent.State = pb.AgentState_AGENT_STATE_EXITED
	ap.mu.Unlock()
	d.diffs.flushQueued()
	waitFor(t, "held comments back to unsent", func() bool { return paneDiff(d, one).Unsent == 1 && paneDiff(d, two).Unsent == 1 })

	// A guest can't change your comments (nor you a guest's).
	commentID := r.CommentId
	if _, err := d.diffs.reviewCommand(&pb.ReviewCommand{PaneId: one, Action: pb.ReviewCommand_ACTION_EDIT_COMMENT, CommentId: commentID, Text: "mine now"},
		reviewer{name: "Sam", guest: true}, g); err == nil {
		t.Error("a guest edited the owner's comment")
	}

	// Closing one pane leaves the review with the other.
	reviewID := paneDiff(d, one).ReviewId
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: one}}})
	if rv, _ := d.diffs.readReview(&pb.ReadReview{ReviewId: reviewID}); rv.GetPaneId() != two {
		t.Fatalf("the review's pane after closing the other: %q", rv.GetPaneId())
	}
	// With none open, sending by its id says to open it.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: two}}})
	if _, err := d.diffs.reviewCommand(&pb.ReviewCommand{ReviewId: reviewID, Action: pb.ReviewCommand_ACTION_SEND}, reviewer{}, g); err == nil ||
		!strings.Contains(err.Error(), "open the diff") {
		t.Errorf("sending with the diff closed: %v", err)
	}

	// Pruning: a repository that isn't there now (a disk not mounted) keeps
	// it; a commit that's gone doesn't.
	d.diffs.mu.Lock()
	d.diffs.reviews[reviewID].Repo = filepath.Join(repo, "not-mounted")
	d.diffs.mu.Unlock()
	d.diffs.pruneReviews()
	if d.diffs.reviewGroup(reviewID) == "" {
		t.Fatal("pruned for a folder that isn't there")
	}
	d.diffs.mu.Lock()
	d.diffs.reviews[reviewID].Repo = repo
	d.diffs.reviews[reviewID].Source = &pb.DiffSource{Kind: pb.DiffSource_KIND_COMMIT, Rev: "0123456789abcdef0123456789abcdef01234567"}
	d.diffs.mu.Unlock()
	d.diffs.pruneReviews()
	if d.diffs.reviewGroup(reviewID) != "" {
		t.Fatal("kept a review whose commit is gone")
	}

	// A guest in a Group whose folder is inside the repository can't diff all of it.
	d.mu.Lock()
	d.groups[g].Folder = filepath.Join(repo, "docs")
	err := d.guestEditorPath(&pb.PaneSpec{Diff: &pb.DiffOpen{}}, g)
	d.mu.Unlock()
	if err == nil {
		t.Error("a guest's diff of the repository around the Group's folder")
	}
}
