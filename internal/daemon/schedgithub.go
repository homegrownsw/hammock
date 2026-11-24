package daemon

// GitHub triggers (schedtriggers.go): the repository of a task's folder is
// asked about with gh (GraphQL, as you're signed in to it) every couple of
// minutes while any task has one, one question per repository for every
// task watching it. Each trigger keeps what was so at its last look (its
// trigState, kept across restarts): what's new since fires, and its first
// look only notes what's there, so a backlog never does.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/homegrownsw/hammock/internal/gitcmd"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// ghPoll is how often GitHub is asked; ghMinGap the least time between
// two looks however often triggers change.
var (
	ghPoll   = 90 * time.Second
	ghMinGap = 10 * time.Second
)

// ghRun runs gh with args, returning what it printed (tests replace it).
// Its error says why, in gh's words, when it ran.
var ghRun = func(ctx context.Context, args ...string) ([]byte, error) {
	gh := ghPath()
	if gh == "" {
		return nil, errNoGH
	}
	cmd := exec.CommandContext(ctx, gh, args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		switch {
		case strings.Contains(msg, "gh auth login"):
			return nil, errGHAuth
		case msg != "":
			return nil, errors.New("gh: " + firstLine(msg))
		}
		return nil, fmt.Errorf("gh: %w", err)
	}
	return out, nil
}

// ghBackoff is when GitHub may next be asked, after it said to slow down
// (a rate limit); ghBackoffFor how long that is; ghSpread the gap between
// repositories in one look.
var (
	ghBackoff    atomic.Value // time.Time
	ghBackoffFor = 10 * time.Minute
	ghSpread     = 2 * time.Second
)

func init() { ghBackoff.Store(time.Time{}) }

// ghRateLimited: gh's error is GitHub saying to slow down.
func ghRateLimited(err error) bool {
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "rate limit") || strings.Contains(m, "retry-after") || strings.Contains(m, "abuse")
}

var (
	errNoGH   = errors.New("GitHub triggers need the GitHub CLI: install gh, then run gh auth login")
	errGHAuth = errors.New("gh isn't signed in to GitHub: run gh auth login")
)


// gitRun runs git in dir (tests replace it).
var gitRun = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return gitcmd.Output(ctx, dir, args...)
}

// ghDefaultRepo is the repository gh uses in dir (gh repo set-default),
// "" for none (tests replace it).
var ghDefaultRepo = func(dir string) string {
	gh := ghPath()
	if gh == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "repo", "set-default", "--view")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1")
	out, err := cmd.Output()
	v := strings.TrimSpace(string(out))
	if err != nil || strings.Count(v, "/") != 1 || strings.ContainsAny(v, " \t\n") {
		return ""
	}
	return v
}

// githubRepoOf is the GitHub repository ("owner/name") of the folder dir,
// as gh would take it: gh's default for it (set-default), else its
// upstream remote's (a fork's original), else origin's, else its first
// GitHub remote's.
func githubRepoOf(dir string) (string, error) {
	if repo := ghDefaultRepo(dir); repo != "" {
		return repo, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := gitRun(ctx, dir, "remote", "-v")
	if err != nil {
		return "", fmt.Errorf("%s isn't a git repository", dir)
	}
	byName := map[string]string{}
	var first string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		repo := githubRepoFromURL(f[1])
		if repo == "" {
			continue
		}
		if byName[f[0]] == "" {
			byName[f[0]] = repo
		}
		if first == "" {
			first = repo
		}
	}
	if repo := cmpOr(byName["upstream"], byName["origin"], first); repo != "" {
		return repo, nil
	}
	return "", fmt.Errorf("%s has no GitHub remote", dir)
}

// githubRepoFromURL is "owner/name" from a github.com remote's URL (https,
// ssh or scp-like), else "".
func githubRepoFromURL(u string) string {
	var path string
	switch {
	case strings.HasPrefix(u, "git@github.com:"):
		path = strings.TrimPrefix(u, "git@github.com:")
	default:
		pu, err := url.Parse(u)
		if err != nil || !strings.EqualFold(pu.Hostname(), "github.com") {
			return ""
		}
		path = strings.TrimPrefix(pu.Path, "/")
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// gitBranchOf is the branch dir has checked out ("" when detached).
func gitBranchOf(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := gitRun(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// watchGitHub asks GitHub about the repositories GitHub triggers watch,
// until the daemon stops.
func (d *Daemon) watchGitHub() {
	wake := make(chan struct{}, 1)
	d.mu.Lock()
	d.sched.ghWake = wake
	d.mu.Unlock()
	every, gap := ghPoll, ghMinGap // (as they are now: tests change them)
	t := time.NewTimer(10 * time.Second)
	defer t.Stop()
	var last time.Time
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		case <-wake:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
			if wait := gap - time.Since(last); wait > 0 {
				t.Reset(wait)
				continue
			}
		}
		last = time.Now()
		d.pollGitHub()
		t.Reset(every)
	}
}

// ghJob is one task folder's GitHub triggers, for a look.
type ghJob struct {
	schedule string
	dir      string
	triggers []*pb.EventTrigger // copies
	repo     string
	branch   string
}

// pollGitHub has one look at GitHub for every GitHub trigger.
func (d *Daemon) pollGitHub() {
	d.mu.Lock()
	var jobs []*ghJob
	for _, s := range d.sched.list {
		var ts []*pb.EventTrigger
		for _, t := range d.eventsOf(s) {
			if t.GetGithub() != nil {
				ts = append(ts, cloneTrigger(t))
			}
		}
		if len(ts) > 0 {
			jobs = append(jobs, &ghJob{schedule: s.Id, dir: s.Cwd, triggers: ts})
		}
	}
	d.mu.Unlock()
	if len(jobs) == 0 {
		return
	}
	fail := func(js []*ghJob, err error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, j := range js {
			d.ghTriggerErrors(j, err.Error())
		}
	}
	if wait := time.Until(ghBackoff.Load().(time.Time)); wait > 0 {
		fail(jobs, fmt.Errorf("GitHub asked Hammock to slow down: it looks again at %s", time.Now().Add(wait).Format("15:04")))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	login, err := ghLogin(ctx)
	cancel()
	if err != nil {
		fail(jobs, err)
		return
	}
	byRepo := map[string][]*ghJob{}
	for _, j := range jobs {
		repo, err := githubRepoOf(j.dir)
		if err != nil {
			fail([]*ghJob{j}, err)
			continue
		}
		j.repo, j.branch = repo, gitBranchOf(j.dir)
		byRepo[repo] = append(byRepo[repo], j)
	}
	n := 0
	for repo, js := range byRepo {
		if n++; n > 1 {
			time.Sleep(ghSpread) // (not all at once)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := ghAsk(ctx, repo, login, js)
		cancel()
		if err != nil {
			if ghRateLimited(err) {
				ghBackoff.Store(time.Now().Add(ghBackoffFor))
			}
			fail(js, err)
			continue
		}
		d.mu.Lock()
		for _, j := range js {
			d.ghTriggerErrors(j, "")
			d.ghFire(j, res)
		}
		d.mu.Unlock()
	}
}

func cloneTrigger(t *pb.EventTrigger) *pb.EventTrigger {
	return &pb.EventTrigger{Id: t.Id, Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: t.GetGithub().GetEvent(), Label: t.GetGithub().GetLabel()}}}
}

// ghTriggerErrors notes (or clears) why j's triggers can't fire, and the
// repository they watch. Callers hold d.mu.
func (d *Daemon) ghTriggerErrors(j *ghJob, msg string) {
	s := d.sched.list[j.schedule]
	if s == nil {
		return
	}
	changed := false
	for _, t := range s.Events {
		g := t.GetGithub()
		if g == nil {
			continue
		}
		if t.Error != msg {
			t.Error, changed = msg, true
		}
		if j.repo != "" && g.Repo != j.repo {
			g.Repo, changed = j.repo, true
		}
	}
	if changed {
		d.saveSchedule(s)
	}
}

var ghLoginCache struct {
	sync.Mutex
	login string
	at    time.Time
}

// ghLogin is who gh is signed in as (asked again every hour).
func ghLogin(ctx context.Context) (string, error) {
	ghLoginCache.Lock()
	defer ghLoginCache.Unlock()
	if ghLoginCache.login != "" && time.Since(ghLoginCache.at) < time.Hour {
		return ghLoginCache.login, nil
	}
	out, err := ghRun(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	login := strings.TrimSpace(string(out))
	if login == "" {
		return "", errGHAuth
	}
	ghLoginCache.login, ghLoginCache.at = login, time.Now()
	return login, nil
}

// ---------- the question ----------

type ghItem struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	Typename    string `json:"__typename"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	Commits struct {
		Nodes []struct {
			Commit ghCommit `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Reviews  ghNotes `json:"reviews"`
	Comments ghNotes `json:"comments"`
}

type ghCommit struct {
	Oid    string `json:"oid"`
	Rollup *struct {
		State    string `json:"state"`
		Contexts struct {
			Nodes []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
				Context    string `json:"context"`
				State      string `json:"state"`
			} `json:"nodes"`
		} `json:"contexts"`
	} `json:"statusCheckRollup"`
}

type ghNotes struct {
	Nodes []struct {
		ID     string `json:"id"`
		State  string `json:"state"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	} `json:"nodes"`
}

// ghResult is what one look found in a repository.
type ghResult struct {
	repo, login   string
	defaultBranch string
	mine          []ghItem              // your open pull requests
	branches      map[string]ghCommit // a task folder's branch → its head
	reviewReq     []ghItem
	assigned      []ghItem
	labeled       map[string][]ghItem // label →
	merged        []ghItem
	asked         map[string]bool // which of these were asked for
}

const ghRollup = `statusCheckRollup { state contexts(first: 50) { nodes { ... on CheckRun { name conclusion } ... on StatusContext { context state } } } }`

// ghAsk asks GitHub what's so in repo for js's triggers, in one GraphQL query.
func ghAsk(ctx context.Context, repo, login string, js []*ghJob) (*ghResult, error) {
	owner, name, _ := strings.Cut(repo, "/")
	need := map[pb.GitHubTrigger_Event]bool{}
	labels := map[string]bool{}
	branches := map[string]bool{}
	for _, j := range js {
		for _, t := range j.triggers {
			g := t.GetGithub()
			need[g.Event] = true
			if g.Event == pb.GitHubTrigger_EVENT_LABELED {
				labels[g.Label] = true
			}
			if g.Event == pb.GitHubTrigger_EVENT_CI_FAILED && j.branch != "" {
				branches[j.branch] = true
			}
		}
	}
	var vars []string
	var parts []string
	args := []string{"api", "graphql"}
	addVar := func(name, val string) {
		vars = append(vars, "$"+name+": String!")
		args = append(args, "-f", name+"="+val)
	}
	search := func(alias, q string, first int, fields string) {
		addVar(alias+"Q", q)
		parts = append(parts, fmt.Sprintf("%s: search(query: $%sQ, type: ISSUE, first: %d) { nodes { __typename ... on PullRequest { %s } ... on Issue { number title url author { login } } } }", alias, alias, first, fields))
	}
	r := &ghResult{repo: repo, login: login, labeled: map[string][]ghItem{}, branches: map[string]ghCommit{}, asked: map[string]bool{}}
	base := "repo:" + repo + " "
	if need[pb.GitHubTrigger_EVENT_CI_FAILED] || need[pb.GitHubTrigger_EVENT_REVIEWED] {
		r.asked["mine"] = true
		search("mine", base+"is:pr is:open author:"+login, 30,
			"number title url headRefName author { login } commits(last: 1) { nodes { commit { oid "+ghRollup+" } } } reviews(last: 10) { nodes { id state body url author { login } } } comments(last: 10) { nodes { id body url author { login } } }")
	}
	if need[pb.GitHubTrigger_EVENT_REVIEW_REQUESTED] {
		r.asked["reviewReq"] = true
		search("reviewReq", base+"is:pr is:open review-requested:"+login, 30, "number title url author { login }")
	}
	if need[pb.GitHubTrigger_EVENT_ASSIGNED] {
		r.asked["assigned"] = true
		search("assigned", base+"is:open assignee:"+login, 30, "number title url author { login }")
	}
	if need[pb.GitHubTrigger_EVENT_MERGED] {
		r.asked["merged"] = true
		search("merged", base+"is:pr is:merged author:"+login+" sort:updated-desc", 15, "number title url headRefName author { login }")
	}
	labelAlias := map[string]string{}
	for i, l := range sortedKeys(labels) {
		a := fmt.Sprintf("label%d", i)
		labelAlias[a] = l
		search(a, base+"is:open label:"+strconvQuote(l), 30, "number title url author { login }")
	}
	branchAlias := map[string]string{}
	if len(branches) > 0 {
		addVar("owner", owner)
		addVar("name", name)
		var refs []string
		for i, b := range sortedKeys(branches) {
			a := fmt.Sprintf("branch%d", i)
			branchAlias[a] = b
			addVar(a+"R", "refs/heads/"+b)
			refs = append(refs, fmt.Sprintf("%s: ref(qualifiedName: $%sR) { target { ... on Commit { oid %s } } }", a, a, ghRollup))
		}
		parts = append(parts, "repo: repository(owner: $owner, name: $name) { defaultBranchRef { name } "+strings.Join(refs, " ")+" }")
	}
	query := "query(" + strings.Join(vars, ", ") + ") { " + strings.Join(parts, " ") + " }"
	args = append(args, "-f", "query="+query)
	out, err := ghRun(ctx, args...)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("GitHub's answer didn't make sense: %v", err)
	}
	if len(resp.Errors) > 0 && resp.Data == nil {
		return nil, errors.New("GitHub: " + firstLine(resp.Errors[0].Message))
	}
	nodes := func(alias string) []ghItem {
		var v struct {
			Nodes []ghItem `json:"nodes"`
		}
		_ = json.Unmarshal(resp.Data[alias], &v)
		var out []ghItem
		for _, n := range v.Nodes {
			if n.Number > 0 {
				out = append(out, n)
			}
		}
		return out
	}
	r.mine, r.reviewReq, r.assigned, r.merged = nodes("mine"), nodes("reviewReq"), nodes("assigned"), nodes("merged")
	for a, l := range labelAlias {
		r.labeled[l] = nodes(a)
		r.asked["label:"+l] = resp.Data[a] != nil
	}
	if raw := resp.Data["repo"]; raw != nil {
		var repoData map[string]json.RawMessage
		_ = json.Unmarshal(raw, &repoData)
		var def struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(repoData["defaultBranchRef"], &def)
		r.defaultBranch = def.Name
		for a, b := range branchAlias {
			var ref struct {
				Target ghCommit `json:"target"`
			}
			if json.Unmarshal(repoData[a], &ref) == nil && ref.Target.Oid != "" {
				r.branches[b] = ref.Target
			}
		}
	}
	return r, nil
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// strconvQuote quotes a label for a search ("needs triage").
func strconvQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// ---------- what's new ----------

// ghFact is one thing that's so, which fires when it's new.
type ghFact struct {
	key string
	ev  *schedEvent
}

// failedChecks names a commit's failing checks ("" when it hasn't failed).
func failedChecks(c ghCommit) (failed bool, names []string) {
	if c.Rollup == nil {
		return false, nil
	}
	if c.Rollup.State != "FAILURE" && c.Rollup.State != "ERROR" {
		return false, nil
	}
	for _, n := range c.Rollup.Contexts.Nodes {
		switch {
		case n.Name != "" && slices.Contains([]string{"FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED", "CANCELLED"}, n.Conclusion):
			names = append(names, n.Name)
		case n.Context != "" && (n.State == "FAILURE" || n.State == "ERROR"):
			names = append(names, n.Context)
		}
	}
	return true, names
}

// ghFacts is what's so in r for trigger g (in a folder on branch), and
// whether r says (it was asked).
func ghFacts(r *ghResult, g *pb.GitHubTrigger, branch string) ([]ghFact, bool) {
	ev := func(summary string, pr ghItem, extra ...[2]string) *schedEvent {
		f := [][2]string{{"repo", r.repo}, {"number", fmt.Sprint(pr.Number)}, {"title", pr.Title}, {"url", pr.URL}}
		if pr.Author.Login != "" {
			f = append(f, [2]string{"author", pr.Author.Login})
		}
		if pr.HeadRefName != "" {
			f = append(f, [2]string{"branch", pr.HeadRefName})
		}
		return &schedEvent{Kind: "github", Summary: summary, Source: "GitHub", Fields: append(f, extra...)}
	}
	var out []ghFact
	switch g.Event {
	case pb.GitHubTrigger_EVENT_CI_FAILED:
		if !r.asked["mine"] {
			return nil, false
		}
		prBranches := map[string]bool{}
		for _, pr := range r.mine {
			prBranches[pr.HeadRefName] = true
			if len(pr.Commits.Nodes) == 0 {
				continue
			}
			c := pr.Commits.Nodes[0].Commit
			if failed, names := failedChecks(c); failed {
				e := ev(fmt.Sprintf("CI failed on #%d", pr.Number), pr, [2]string{"commit", c.Oid}, [2]string{"failed_checks", strings.Join(names, ", ")})
				out = append(out, ghFact{fmt.Sprintf("ci:%d:%s", pr.Number, c.Oid), e})
			}
		}
		if c, ok := r.branches[branch]; ok && branch != r.defaultBranch && !prBranches[branch] {
			if failed, names := failedChecks(c); failed {
				e := &schedEvent{Kind: "github", Summary: "CI failed on the folder's branch", Source: "GitHub",
					Fields: [][2]string{{"repo", r.repo}, {"branch", branch}, {"commit", c.Oid}, {"failed_checks", strings.Join(names, ", ")}}}
				out = append(out, ghFact{"ci:b:" + branch + ":" + c.Oid, e})
			}
		}
	case pb.GitHubTrigger_EVENT_REVIEWED:
		if !r.asked["mine"] {
			return nil, false
		}
		for _, pr := range r.mine {
			for _, n := range pr.Reviews.Nodes {
				if n.Author.Login == r.login || n.State == "PENDING" {
					continue
				}
				what := "A review"
				switch n.State {
				case "APPROVED":
					what = "An approval"
				case "CHANGES_REQUESTED":
					what = "Changes requested"
				}
				e := ev(fmt.Sprintf("%s on #%d", what, pr.Number), pr, [2]string{"reviewer", n.Author.Login}, [2]string{"review_state", n.State}, [2]string{"review_url", n.URL})
				e.Body = n.Body
				out = append(out, ghFact{"rv:" + n.ID, e})
			}
			for _, n := range pr.Comments.Nodes {
				if n.Author.Login == r.login {
					continue
				}
				e := ev(fmt.Sprintf("A comment on #%d", pr.Number), pr, [2]string{"commenter", n.Author.Login}, [2]string{"comment_url", n.URL})
				e.Body = n.Body
				out = append(out, ghFact{"cm:" + n.ID, e})
			}
		}
	case pb.GitHubTrigger_EVENT_REVIEW_REQUESTED:
		if !r.asked["reviewReq"] {
			return nil, false
		}
		for _, pr := range r.reviewReq {
			out = append(out, ghFact{fmt.Sprintf("rr:%d", pr.Number), ev(fmt.Sprintf("Your review was requested on #%d", pr.Number), pr)})
		}
	case pb.GitHubTrigger_EVENT_ASSIGNED:
		if !r.asked["assigned"] {
			return nil, false
		}
		for _, pr := range r.assigned {
			out = append(out, ghFact{fmt.Sprintf("as:%d", pr.Number), ev(fmt.Sprintf("#%d was assigned to you", pr.Number), pr)})
		}
	case pb.GitHubTrigger_EVENT_LABELED:
		if !r.asked["label:"+g.Label] {
			return nil, false
		}
		for _, pr := range r.labeled[g.Label] {
			out = append(out, ghFact{fmt.Sprintf("lb:%d", pr.Number), ev(fmt.Sprintf("#%d was labelled", pr.Number), pr, [2]string{"label", g.Label})})
		}
	case pb.GitHubTrigger_EVENT_MERGED:
		if !r.asked["merged"] {
			return nil, false
		}
		for _, pr := range r.merged {
			out = append(out, ghFact{fmt.Sprintf("mg:%d", pr.Number), ev(fmt.Sprintf("#%d was merged", pr.Number), pr)})
		}
	default:
		return nil, false
	}
	return out, true
}

// maxSeen caps what a trigger remembers, and seenFor how long: what it's
// seen, it remembers after it's gone from GitHub's answer (which shows only
// the latest few), so an item coming back into it doesn't fire again.
const (
	maxSeen = 1000
	seenFor = 30 * 24 * time.Hour
)

// ghFire fires j's triggers for what's new in r since each last looked.
// Callers hold d.mu.
func (d *Daemon) ghFire(j *ghJob, r *ghResult) {
	s := d.sched.list[j.schedule]
	if s == nil {
		return
	}
	now := schedNow()
	for _, t := range d.eventsOf(s) {
		g := t.GetGithub()
		if g == nil {
			continue
		}
		facts, ok := ghFacts(r, g, j.branch)
		if !ok {
			continue
		}
		st := d.trigStateFor(t.Id, r.repo+" "+GitHubKey(g))
		if st.SeenAt == nil {
			st.SeenAt = map[string]int64{}
		}
		// The folder now on another branch: what's so on it isn't news.
		newBranch := st.Branch != j.branch
		changed := !st.Primed || newBranch
		for _, f := range facts {
			if _, ok := st.SeenAt[f.key]; ok {
				continue
			}
			st.SeenAt[f.key], changed = now.UnixMilli(), true
			if st.Primed && !(newBranch && strings.HasPrefix(f.key, "ci:b:")) {
				f.ev.Trigger = t.Id
				_, _, _ = d.fireEvent(s, f.ev)
			}
		}
		st.Primed, st.Branch = true, j.branch
		if pruneSeen(st.SeenAt, now) {
			changed = true
		}
		if changed {
			d.saveTrigState()
		}
	}
}

// pruneSeen forgets what was seen over seenFor ago, and the oldest past
// maxSeen, reporting whether it forgot any.
func pruneSeen(seen map[string]int64, now time.Time) bool {
	cut := now.Add(-seenFor).UnixMilli()
	gone := false
	for k, at := range seen {
		if at < cut {
			delete(seen, k)
			gone = true
		}
	}
	for len(seen) > maxSeen {
		oldest, at := "", int64(1<<62)
		for k, a := range seen {
			if a < at {
				oldest, at = k, a
			}
		}
		delete(seen, oldest)
		gone = true
	}
	return gone
}

// GitHubKey is what a GitHub trigger watches, for its trigState's key.
func GitHubKey(g *pb.GitHubTrigger) string {
	return fmt.Sprintf("%d:%s", g.Event, g.Label)
}
