package daemon

// Diff panes (PANE_KIND_DIFF): a git diff to review, worked out here, on
// the repository's computer (internal/gitdiff), and read by clients a file
// at a time. The pane keeps what it compares (DiffInfo.source); the diff
// itself is worked out when a client first reads it, and again when one
// asks to refresh. While a client is attached, the repository is watched:
// when what the diff compares changes, DiffInfo.stale is set, and clients
// offer to refresh rather than changing what's on screen under you.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/gitdiff"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// diffPollEvery is how often a watched diff checks its repository.
const diffPollEvery = 2 * time.Second

type diffHub struct {
	d    *Daemon
	mu   sync.Mutex
	st   map[string]*diffState // by pane
	once sync.Once
	stop chan struct{}

	// Reviews (review.go), by id, and what each is of (reviewKey; worked
	// out as panes find theirs).
	reviews    map[string]*pb.Review
	reviewKeys map[string]string
	sending    map[string]bool // reviews whose comments are being typed to their agent
	prepared   map[*pb.DiffOpen]preparedDiff
}

type diffState struct {
	paneID string
	// work runs one computation at a time (git can be slow; a second
	// refresh waits for the first, then finds it done).
	work sync.Mutex

	// Guarded by diffHub.mu:
	repo        string
	source      *pb.DiffSource
	resolved    *gitdiff.Resolved
	files       []*pb.DiffFile
	fingerprint string
	computed    bool
	generation  uint64
	watchers    map[*conn]bool
	stale       bool
	checking    bool
	reviewID    string
	target      string    // the agent comments go to
	nextCheck   time.Time // a big repository is checked less often (check)
}

func newDiffHub(d *Daemon) *diffHub {
	h := &diffHub{d: d, st: map[string]*diffState{}, stop: make(chan struct{}),
		reviews: map[string]*pb.Review{}, reviewKeys: map[string]string{}, sending: map[string]bool{},
		prepared: map[*pb.DiffOpen]preparedDiff{}}
	h.loadReviews()
	return h
}

func (h *diffHub) start() { h.once.Do(func() { go h.run() }) }

func (h *diffHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
}

// preparedDiff is a diff worked out for a pane about to be made.
type preparedDiff struct {
	r   *gitdiff.Resolved
	err error
}

// prepareDiff works out the diff a message makes a pane for (CreateTab,
// CreatePane, ReplacePane), before d.mu is taken to make it: startDiff
// takes it from there. Not under d.mu.
func (d *Daemon) prepareDiff(msg *pb.ClientMessage) *pb.DiffOpen {
	var spec *pb.PaneSpec
	var paneRef, groupID string
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_CreateTab:
		spec, groupID = m.CreateTab.Pane, m.CreateTab.GroupId
	case *pb.ClientMessage_CreatePane:
		spec, paneRef = m.CreatePane.Spec, m.CreatePane.SplitPaneId
	case *pb.ClientMessage_ReplacePane:
		spec, paneRef = m.ReplacePane.Spec, m.ReplacePane.PaneId
	}
	open := spec.GetDiff()
	if open == nil {
		return nil
	}
	d.mu.Lock()
	dir := open.Repo
	if dir == "" {
		dir = spec.Cwd
	}
	if p := d.panes[paneRef]; dir == "" && p != nil {
		dir = p.cwd() // a split's, as splitPane gives it
	}
	if dir == "" {
		if groupID == "" {
			groupID = d.groupOfPane(paneRef)
		}
		if g := d.groups[groupID]; g != nil {
			dir = d.groupFolder(g)
		}
	}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := gitdiff.Resolve(ctx, expandHome(dir), open.Source)
	h := d.diffs
	h.mu.Lock()
	h.prepared[open] = preparedDiff{r, err}
	h.mu.Unlock()
	return open
}

// takePrepared is what prepareDiff worked out for open, if it did.
func (h *diffHub) takePrepared(open *pb.DiffOpen) (*gitdiff.Resolved, error, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	x, ok := h.prepared[open]
	delete(h.prepared, open)
	return x.r, x.err, ok
}

// startDiff creates a diff pane for tab t (not yet placed in its layout).
// Callers hold d.mu.
func (d *Daemon) startDiff(t *pb.Tab, spec *pb.PaneSpec) (*pane, error) {
	open := spec.Diff
	dir := open.Repo
	if dir == "" {
		dir = spec.Cwd
	}
	if dir == "" {
		if g := d.groups[t.GroupId]; g != nil {
			dir = d.groupFolder(g)
		}
	}
	// Worked out before d.mu was taken (prepareDiff), as a rule: git can
	// take a while in a big repository, and everything waits on d.mu.
	r, err, ok := d.diffs.takePrepared(open)
	if !ok {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err = gitdiff.Resolve(ctx, expandHome(dir), open.Source)
	}
	if err != nil {
		return nil, err
	}
	id := newID("pn")
	// Comments go to the agent it's opened for, or the agent that opened it.
	target := open.TargetPaneId
	if target == "" {
		if c := d.panes[spec.CreatedByPaneId]; c != nil && c.snapshotInfo().Agent != nil {
			target = spec.CreatedByPaneId
		}
	}
	info := &pb.Pane{
		Id: id, TabId: t.Id, Kind: pb.PaneKind_PANE_KIND_DIFF, Title: r.Title, Cwd: r.Repo,
		CreatedByPaneId: spec.CreatedByPaneId,
		Diff: &pb.DiffInfo{
			Repo: r.Repo, Source: proto.CloneOf(r.Source), Title: r.Title, BaseRev: r.Base, HeadRev: r.Head, Branch: r.Branch,
			View: cleanDiffView(open.View), TargetPaneId: target,
		},
	}
	h := d.diffs
	h.mu.Lock()
	h.st[id] = &diffState{paneID: id, repo: r.Repo, source: proto.CloneOf(r.Source), resolved: r, watchers: map[*conn]bool{}, target: target}
	h.mu.Unlock()
	h.start()
	p := newPane(d, info)
	d.panes[id] = p
	d.persist(d.store.PutPane(info))
	return p, nil
}

// restore takes a diff pane back after the daemon restarts: its diff is
// worked out again when it's next read. Callers hold d.mu.
func (h *diffHub) restore(p *pane) {
	info := p.info
	if info.Diff == nil {
		info.Diff = &pb.DiffInfo{Error: "This pane lost what it compared"}
	}
	info.Diff.Stale = false
	h.mu.Lock()
	h.st[p.id] = &diffState{paneID: p.id, repo: info.Diff.Repo, source: proto.CloneOf(info.Diff.Source),
		generation: info.Diff.Generation, watchers: map[*conn]bool{}, target: info.Diff.TargetPaneId}
	if rv := h.reviews[info.Diff.ReviewId]; rv != nil {
		rv.PaneId = p.id
		h.st[p.id].reviewID = rv.Id
	}
	h.mu.Unlock()
	h.start()
}

func (h *diffHub) paneClosed(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.st[id]; s != nil && s.reviewID != "" {
		h.releaseReviewLocked(s.reviewID, s)
	}
	delete(h.st, id)
}

func (h *diffHub) attach(c *conn, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.st[id]; s != nil {
		s.watchers[c] = true
	}
}

func (h *diffHub) detach(c *conn, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.st[id]; s != nil {
		delete(s.watchers, c)
	}
}

func (h *diffHub) state(id string) (*diffState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.st[id]
	if s == nil {
		return nil, fmt.Errorf("%s isn't a diff pane", id)
	}
	return s, nil
}

// compute works the diff out afresh (refresh), or if it never was, and
// updates the pane's DiffInfo.
func (h *diffHub) compute(s *diffState, refresh bool) error {
	s.work.Lock()
	defer s.work.Unlock()
	h.mu.Lock()
	if s.computed && !refresh {
		h.mu.Unlock()
		return nil
	}
	repo, src := s.repo, proto.CloneOf(s.source)
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r, err := gitdiff.Resolve(ctx, repo, src)
	var files []*pb.DiffFile
	fp := ""
	if err == nil {
		// The fingerprint first: a change while the files are listed then
		// shows as one to refresh for, rather than going unnoticed.
		fp, _ = gitdiff.Fingerprint(ctx, r)
		files, err = gitdiff.Files(ctx, r)
	}

	h.mu.Lock()
	if h.st[s.paneID] != s {
		h.mu.Unlock()
		return errors.New("the pane has closed")
	}
	s.generation++
	gen := s.generation
	s.computed, s.stale = true, false
	if err == nil {
		s.resolved, s.files, s.fingerprint = r, files, fp
		h.attachReviewLocked(s)
	} else {
		s.files, s.fingerprint = nil, ""
	}
	h.mu.Unlock()
	if err == nil {
		// Its threads, found again in the diff as it is now.
		defer func() {
			h.reanchor(s)
			h.publish(s)
		}()
	}

	d := h.d
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.panes[s.paneID]
	if p == nil {
		return nil
	}
	p.mu.Lock()
	di := proto.CloneOf(p.info.Diff)
	if di == nil {
		di = &pb.DiffInfo{}
	}
	di.Generation, di.Stale = gen, false
	if err != nil {
		di.Error, di.Files, di.Added, di.Removed = err.Error(), 0, 0, 0
	} else {
		added, removed := gitdiff.Totals(files)
		di.Error = ""
		di.Repo, di.Source, di.Title, di.BaseRev, di.HeadRev, di.Branch = r.Repo, proto.CloneOf(r.Source), r.Title, r.Base, r.Head, r.Branch
		di.Files, di.Added, di.Removed = uint32(len(files)), added, removed
		p.info.Title = r.Title
	}
	p.info.Diff = di
	p.mu.Unlock()
	d.paneChangedIfLive(p)
	return nil
}

// read answers ReadDiff.
func (h *diffHub) read(m *pb.ReadDiff) (*pb.DiffContent, error) {
	s, err := h.state(m.PaneId)
	if err != nil {
		return nil, err
	}
	if err := h.compute(s, m.Refresh); err != nil {
		return nil, err
	}
	h.mu.Lock()
	out := &pb.DiffContent{PaneId: m.PaneId, Generation: s.generation}
	for _, f := range s.files {
		out.Files = append(out.Files, proto.CloneOf(f))
	}
	repo := s.repo
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out.Commits = gitdiff.Commits(ctx, repo, 60)
	out.DefaultBranch = gitdiff.DefaultBranch(ctx, repo)
	out.Branches = gitdiff.Branches(ctx, repo, out.DefaultBranch)
	return out, nil
}

// readRepo answers ReadRepo: what a diff of the repository m.Path is in
// could compare, before a pane is opened on it. Not under d.mu.
func (d *Daemon) readRepo(m *pb.ReadRepo) *pb.RepoInfo {
	dir := m.Path
	if dir == "" {
		d.mu.Lock()
		if g := d.groups[m.GroupId]; g != nil {
			dir = d.groupFolder(g)
		}
		d.mu.Unlock()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, err := gitdiff.TopLevel(ctx, expandHome(dir))
	if err != nil {
		return &pb.RepoInfo{Error: err.Error()}
	}
	out := &pb.RepoInfo{Repo: repo, Branch: gitdiff.CurrentBranch(ctx, repo), DefaultBranch: gitdiff.DefaultBranch(ctx, repo)}
	out.Branches = gitdiff.Branches(ctx, repo, out.DefaultBranch)
	out.Commits = gitdiff.Commits(ctx, repo, 60)
	return out
}

// readFile answers ReadDiffFile.
func (h *diffHub) readFile(m *pb.ReadDiffFile) (*pb.DiffFileText, error) {
	s, err := h.state(m.PaneId)
	if err != nil {
		return nil, err
	}
	if err := h.compute(s, false); err != nil {
		return nil, err
	}
	h.mu.Lock()
	r := s.resolved
	var f *pb.DiffFile
	for _, x := range s.files {
		if x.Path == m.Path {
			f = proto.CloneOf(x)
			break
		}
	}
	gen := s.generation
	h.mu.Unlock()
	if f == nil || r == nil {
		return nil, fmt.Errorf("%s isn't in this diff", m.Path)
	}
	if m.Generation != 0 && m.Generation != gen {
		return nil, errors.New("the diff has been refreshed since: read it again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t, err := gitdiff.FileText(ctx, r, f)
	if err != nil {
		return nil, err
	}
	t.PaneId = m.PaneId
	return t, nil
}

// command answers DiffCommand.
func (h *diffHub) command(m *pb.DiffCommand) error {
	s, err := h.state(m.PaneId)
	if err != nil {
		return err
	}
	switch m.Action {
	case pb.DiffCommand_ACTION_SET_SOURCE:
		if m.Source == nil {
			return errors.New("no source to compare")
		}
		h.mu.Lock()
		repo := s.repo
		h.mu.Unlock()
		// Checked before it's taken, so a commit that isn't there is
		// refused rather than leaving the pane with nothing to show.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		r, err := gitdiff.Resolve(ctx, repo, m.Source)
		cancel()
		if err != nil {
			return err
		}
		h.mu.Lock()
		s.source = proto.CloneOf(r.Source)
		h.mu.Unlock()
		return h.compute(s, true)
	case pb.DiffCommand_ACTION_SET_VIEW:
		d := h.d
		d.mu.Lock()
		defer d.mu.Unlock()
		p := d.panes[m.PaneId]
		if p == nil {
			return fmt.Errorf("no pane %s", m.PaneId)
		}
		p.mu.Lock()
		di := proto.CloneOf(p.info.Diff)
		if di == nil {
			di = &pb.DiffInfo{}
		}
		v := cleanDiffView(m.View)
		changed := !proto.Equal(di.View, v)
		di.View = v
		p.info.Diff = di
		p.mu.Unlock()
		if changed {
			d.paneChangedIfLive(p)
		}
		return nil
	}
	return fmt.Errorf("unknown diff command %v", m.Action)
}

// cleanDiffView keeps only the values each choice can take.
func cleanDiffView(v *pb.DiffView) *pb.DiffView {
	out := &pb.DiffView{}
	if v == nil {
		return out
	}
	pick := func(s string, ok ...string) string {
		for _, o := range ok {
			if s == o {
				return s
			}
		}
		return ""
	}
	out.Style = pick(v.Style, "split", "unified")
	out.Whitespace = pick(v.Whitespace, "show", "ignore")
	out.Wrap = pick(v.Wrap, "on", "off")
	return out
}

// run watches the diffs someone is looking at, marking them stale when
// their repository moves on.
func (h *diffHub) run() {
	tick := time.NewTicker(diffPollEvery)
	defer tick.Stop()
	lastPrune := time.Time{}
	for {
		select {
		case <-h.stop:
			return
		case <-tick.C:
		}
		h.flushQueued()
		if time.Since(lastPrune) > time.Hour {
			lastPrune = time.Now()
			go h.pruneReviews()
		}
		h.mu.Lock()
		var due []*diffState
		for _, s := range h.st {
			if len(s.watchers) > 0 && s.computed && !s.stale && !s.checking && s.resolved != nil && s.resolved.Live() && time.Now().After(s.nextCheck) {
				s.checking = true
				due = append(due, s)
			}
		}
		h.mu.Unlock()
		for _, s := range due {
			go h.check(s)
		}
	}
}

// check marks s stale if its repository has changed since it was worked out.
func (h *diffHub) check(s *diffState) {
	h.mu.Lock()
	r, was := s.resolved, s.fingerprint
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	start := time.Now()
	fp, err := gitdiff.Fingerprint(ctx, r)
	took := time.Since(start)
	cancel()
	h.mu.Lock()
	s.checking = false
	// git status taking a while (a big repository): less often, so the
	// diff isn't most of what the Mac is doing.
	if took > 300*time.Millisecond {
		s.nextCheck = time.Now().Add(min(took*5, 30*time.Second))
	}
	moved := err == nil && fp != was && h.st[s.paneID] == s && s.fingerprint == was && !s.stale
	if moved {
		s.stale = true
	}
	h.mu.Unlock()
	if !moved {
		return
	}
	d := h.d
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.panes[s.paneID]
	if p == nil {
		return
	}
	p.mu.Lock()
	di := proto.CloneOf(p.info.Diff)
	if di == nil {
		di = &pb.DiffInfo{}
	}
	di.Stale = true
	p.info.Diff = di
	p.mu.Unlock()
	d.paneChangedIfLive(p)
}
