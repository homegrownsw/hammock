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
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/homegrownsw/hammock/internal/gitcmd"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
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
		// The folder's repository's triggers, and a job for each repository
		// the others name.
		var own []*pb.EventTrigger
		watched := map[string]*ghJob{}
		for _, t := range d.eventsOf(s) {
			g := t.GetGithub()
			switch {
			case g == nil:
			case g.Watch == "":
				own = append(own, cloneTrigger(t))
			default:
				j := watched[g.Watch]
				if j == nil {
					j = &ghJob{schedule: s.Id, repo: g.Watch}
					watched[g.Watch] = j
					jobs = append(jobs, j)
				}
				j.triggers = append(j.triggers, cloneTrigger(t))
			}
		}
		if len(own) > 0 {
			jobs = append(jobs, &ghJob{schedule: s.Id, dir: s.Cwd, triggers: own})
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
			d.ghTriggerErrors(j, err.Error(), nil)
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
		if j.repo == "" {
			repo, err := githubRepoOf(j.dir)
			if err != nil {
				fail([]*ghJob{j}, err)
				continue
			}
			j.repo, j.branch = repo, gitBranchOf(j.dir)
		}
		byRepo[j.repo] = append(byRepo[j.repo], j)
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
			d.ghTriggerErrors(j, "", res)
			d.ghFire(j, res)
		}
		d.mu.Unlock()
	}
}

func cloneTrigger(t *pb.EventTrigger) *pb.EventTrigger {
	g := t.GetGithub()
	return &pb.EventTrigger{Id: t.Id, Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{
		Event: g.GetEvent(), Label: g.GetLabel(), Watch: g.GetWatch(), Branch: g.GetBranch(), Tags: g.GetTags(), Workflow: g.GetWorkflow()}}}
}

// ghTriggerErrors notes (or clears) why j's triggers can't fire, and the
// repository they watch: msg for all of them, else what r says of each (a
// branch it hasn't got). Callers hold d.mu.
func (d *Daemon) ghTriggerErrors(j *ghJob, msg string, r *ghResult) {
	s := d.sched.list[j.schedule]
	if s == nil {
		return
	}
	mine := map[string]bool{}
	for _, t := range j.triggers {
		mine[t.Id] = true
	}
	changed := false
	for _, t := range s.Events {
		g := t.GetGithub()
		if g == nil || !mine[t.Id] {
			continue
		}
		m := msg
		if m == "" && r != nil {
			m = ghMissing(r, g)
		}
		if t.Error != m {
			t.Error, changed = m, true
		}
		if j.repo != "" && g.Repo != j.repo {
			g.Repo, changed = j.repo, true
		}
		if r != nil && d.noteChecked(t, schedNow()) {
			changed = true
		}
	}
	if changed {
		d.saveSchedule(s)
	}
}

// checkedSaveEvery is how often a trigger that looks often (GitHub, a
// command check) is saved, and the apps told, for its last look alone.
const checkedSaveEvery = 5 * time.Minute

// noteChecked notes t looked now: kept on it at once (so anything that
// saves or sends it has it, and hmk asks for it fresh), but worth saving
// and telling the apps for that alone only now and then. due: save it.
// Callers hold d.mu.
func (d *Daemon) noteChecked(t *pb.EventTrigger, now time.Time) (due bool) {
	t.CheckedUnixMs = now.UnixMilli()
	if d.sched.checkSaved == nil {
		d.sched.checkSaved = map[string]int64{}
	}
	if now.UnixMilli()-d.sched.checkSaved[t.Id] < checkedSaveEvery.Milliseconds() {
		return false
	}
	d.sched.checkSaved[t.Id] = now.UnixMilli()
	return true
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

// ghHead is a branch's newest commit, for commit triggers.
type ghHead struct {
	Oid             string `json:"oid"`
	MessageHeadline string `json:"messageHeadline"`
	URL             string `json:"url"`
	Author          struct {
		Name string `json:"name"`
		User *struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"author"`
}

type ghRelease struct {
	TagName      string `json:"tagName"`
	Name         string `json:"name"`
	URL          string `json:"url"`
	IsPrerelease bool   `json:"isPrerelease"`
	IsDraft      bool   `json:"isDraft"`
	Description  string `json:"description"`
}

// ghResult is what one look found in a repository.
type ghResult struct {
	repo, login   string
	defaultBranch string
	mine          []ghItem            // your open pull requests
	branches      map[string]ghCommit // a task folder's branch → its head
	heads         map[string]ghHead   // a commit trigger's branch ("": the default) → its head
	releases      []ghRelease         // the newest
	tags          []string            // the newest
	reviewReq     []ghItem
	assigned      []ghItem
	labeled       map[string][]ghItem // label →
	merged        []ghItem
	issues        []ghItem          // the newest issues
	runs          []ghWorkflowRun   // the newest finished workflow runs
	notes         []ghNotification  // your unread notifications about it
	asked         map[string]bool   // which of these were asked for
	failed        map[string]string // what of it couldn't be asked ("workflows", "notifications"), and why
}

// ghWorkflowRun is a finished run of an Actions workflow (REST).
type ghWorkflowRun struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	DisplayTitle string `json:"display_title"`
	Conclusion   string `json:"conclusion"`
	HeadBranch   string `json:"head_branch"`
	HTMLURL      string `json:"html_url"`
	Event        string `json:"event"`
	RunNumber    int    `json:"run_number"`
}

// ghNotification is one of your GitHub notifications (REST).
type ghNotification struct {
	ID        string `json:"id"`
	Reason    string `json:"reason"`
	UpdatedAt string `json:"updated_at"`
	Subject   struct {
		Title string `json:"title"`
		Type  string `json:"type"`
		URL   string `json:"url"`
	} `json:"subject"`
}

const (
	ghRollup = `statusCheckRollup { state contexts(first: 50) { nodes { ... on CheckRun { name conclusion } ... on StatusContext { context state } } } }`
	ghHeadOf = `target { ... on Commit { oid messageHeadline url author { name user { login } } } }`
)

// ghAsk asks GitHub what's so in repo for js's triggers, in one GraphQL query.
func ghAsk(ctx context.Context, repo, login string, js []*ghJob) (*ghResult, error) {
	owner, name, _ := strings.Cut(repo, "/")
	need := map[pb.GitHubTrigger_Event]bool{}
	labels := map[string]bool{}
	branches := map[string]bool{}
	heads := map[string]bool{} // "": the default branch
	for _, j := range js {
		for _, t := range j.triggers {
			g := t.GetGithub()
			need[g.Event] = true
			switch g.Event {
			case pb.GitHubTrigger_EVENT_LABELED:
				labels[g.Label] = true
			case pb.GitHubTrigger_EVENT_CI_FAILED:
				if j.branch != "" {
					branches[j.branch] = true
				}
			case pb.GitHubTrigger_EVENT_COMMIT:
				heads[g.Branch] = true
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
	r := &ghResult{repo: repo, login: login, labeled: map[string][]ghItem{}, branches: map[string]ghCommit{}, heads: map[string]ghHead{}, asked: map[string]bool{}, failed: map[string]string{}}
	base := "repo:" + repo + " "
	if need[pb.GitHubTrigger_EVENT_WORKFLOW] || need[pb.GitHubTrigger_EVENT_NOTIFICATION] {
		ghAskREST(ctx, r, need)
	}
	if need[pb.GitHubTrigger_EVENT_ISSUE_OPENED] {
		r.asked["issues"] = true
		search("issues", base+"is:issue sort:created-desc", 20, "number title url author { login }")
	}
	if need[pb.GitHubTrigger_EVENT_CI_FAILED] || need[pb.GitHubTrigger_EVENT_REVIEWED] || need[pb.GitHubTrigger_EVENT_APPROVED] {
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
	headAlias := map[string]string{}
	releases := need[pb.GitHubTrigger_EVENT_RELEASE]
	if len(branches) > 0 || len(heads) > 0 || releases {
		r.asked["repo"] = true
		addVar("owner", owner)
		addVar("name", name)
		var refs []string
		for i, b := range sortedKeys(branches) {
			a := fmt.Sprintf("branch%d", i)
			branchAlias[a] = b
			addVar(a+"R", "refs/heads/"+b)
			refs = append(refs, fmt.Sprintf("%s: ref(qualifiedName: $%sR) { target { ... on Commit { oid %s } } }", a, a, ghRollup))
		}
		def := "defaultBranchRef { name }"
		for i, b := range sortedKeys(heads) {
			if b == "" {
				def = "defaultBranchRef { name " + ghHeadOf + " }"
				continue
			}
			a := fmt.Sprintf("head%d", i)
			headAlias[a] = b
			addVar(a+"R", "refs/heads/"+b)
			refs = append(refs, fmt.Sprintf("%s: ref(qualifiedName: $%sR) { %s }", a, a, ghHeadOf))
		}
		if releases {
			refs = append(refs, "releases(first: 10, orderBy: {field: CREATED_AT, direction: DESC}) { nodes { tagName name url isPrerelease isDraft description } }",
				`tags: refs(refPrefix: "refs/tags/", first: 30, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) { nodes { name } }`)
		}
		parts = append(parts, "repo: repository(owner: $owner, name: $name) { "+def+" "+strings.Join(refs, " ")+" }")
	}
	if len(parts) == 0 {
		return r, nil // (only REST's: workflows, notifications)
	}
	query := "query(" + strings.Join(vars, ", ") + ") { " + strings.Join(parts, " ") + " }"
	if len(vars) == 0 {
		query = "query { " + strings.Join(parts, " ") + " }"
	}
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
	if len(resp.Errors) > 0 && (resp.Data == nil || r.asked["repo"] && ghNull(resp.Data["repo"])) {
		// (No such repository, or not one you can see: the searches in it
		// find nothing, so say so.)
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
	r.issues = nodes("issues")
	for a, l := range labelAlias {
		r.labeled[l] = nodes(a)
		r.asked["label:"+l] = resp.Data[a] != nil
	}
	if raw := resp.Data["repo"]; raw != nil {
		var repoData map[string]json.RawMessage
		_ = json.Unmarshal(raw, &repoData)
		var def struct {
			Name   string `json:"name"`
			Target ghHead `json:"target"`
		}
		_ = json.Unmarshal(repoData["defaultBranchRef"], &def)
		r.defaultBranch = def.Name
		if def.Target.Oid != "" {
			r.heads[""] = def.Target
		}
		for a, b := range branchAlias {
			var ref struct {
				Target ghCommit `json:"target"`
			}
			if json.Unmarshal(repoData[a], &ref) == nil && ref.Target.Oid != "" {
				r.branches[b] = ref.Target
			}
		}
		for a, b := range headAlias {
			var ref struct {
				Target ghHead `json:"target"`
			}
			if json.Unmarshal(repoData[a], &ref) == nil && ref.Target.Oid != "" {
				r.heads[b] = ref.Target
			}
		}
		var rel struct {
			Nodes []ghRelease `json:"nodes"`
		}
		_ = json.Unmarshal(repoData["releases"], &rel)
		r.releases = rel.Nodes
		var tags struct {
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		}
		_ = json.Unmarshal(repoData["tags"], &tags)
		for _, t := range tags.Nodes {
			r.tags = append(r.tags, t.Name)
		}
	}
	return r, nil
}

// ghAskREST asks what GraphQL can't tell: the newest finished runs of the
// repository's workflows, and your unread notifications about it. What
// fails is noted (r.failed) for the triggers that wanted it.
func ghAskREST(ctx context.Context, r *ghResult, need map[pb.GitHubTrigger_Event]bool) {
	if need[pb.GitHubTrigger_EVENT_WORKFLOW] {
		out, err := ghRun(ctx, "api", "repos/"+r.repo+"/actions/runs?per_page=50&status=completed")
		var v struct {
			Runs []ghWorkflowRun `json:"workflow_runs"`
		}
		switch {
		case err != nil:
			r.failed["workflows"] = err.Error()
		case json.Unmarshal(out, &v) != nil:
			r.failed["workflows"] = "GitHub's answer about workflow runs didn't make sense"
		default:
			r.runs, r.asked["workflows"] = v.Runs, true
		}
	}
	if need[pb.GitHubTrigger_EVENT_NOTIFICATION] {
		out, err := ghRun(ctx, "api", "repos/"+r.repo+"/notifications?per_page=50")
		var v []ghNotification
		switch {
		case err != nil:
			r.failed["notifications"] = err.Error()
		case json.Unmarshal(out, &v) != nil:
			r.failed["notifications"] = "GitHub's answer about notifications didn't make sense"
		default:
			r.notes, r.asked["notifications"] = v, true
		}
	}
}

// workflowMatches: run is a run of the workflow named (its name, or its
// file's: ci.yml, or .github/workflows/ci.yml).
func workflowMatches(run ghWorkflowRun, name string) bool {
	name = strings.TrimSpace(name)
	return strings.EqualFold(run.Name, name) || strings.EqualFold(run.Path, name) || strings.EqualFold(path.Base(run.Path), name)
}

// ghWord is s if it's one of GitHub's own words (a conclusion, a reason:
// lower case and _), else "".
func ghWord(s string) string {
	if s == "" || len(s) > 40 || strings.ContainsFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r == '_') }) {
		return ""
	}
	return strings.ReplaceAll(s, "_", " ")
}

// ghHTMLURL is the web page for one of GitHub's API URLs
// (api.github.com/repos/o/r/pulls/1 → github.com/o/r/pull/1).
func ghHTMLURL(api string) string {
	u, ok := strings.CutPrefix(api, "https://api.github.com/repos/")
	if !ok {
		return api
	}
	u = strings.Replace(u, "/pulls/", "/pull/", 1)
	return "https://github.com/" + u
}

// ghNull: a GraphQL field that's missing or null.
func ghNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// ghMissing says why r has nothing for g when that's a mistake in g (a
// branch the repository hasn't got), else "".
func ghMissing(r *ghResult, g *pb.GitHubTrigger) string {
	switch g.Event {
	case pb.GitHubTrigger_EVENT_WORKFLOW:
		return r.failed["workflows"]
	case pb.GitHubTrigger_EVENT_NOTIFICATION:
		return r.failed["notifications"]
	}
	if g.Event != pb.GitHubTrigger_EVENT_COMMIT || !r.asked["repo"] {
		return ""
	}
	if _, ok := r.heads[g.Branch]; ok {
		return ""
	}
	if g.Branch == "" {
		return r.repo + " has no commits yet"
	}
	return r.repo + " has no branch “" + g.Branch + "”"
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
	case pb.GitHubTrigger_EVENT_APPROVED:
		if !r.asked["mine"] {
			return nil, false
		}
		for _, pr := range r.mine {
			for _, n := range pr.Reviews.Nodes {
				if n.Author.Login == r.login || n.State != "APPROVED" {
					continue
				}
				e := ev(fmt.Sprintf("#%d was approved", pr.Number), pr, [2]string{"reviewer", n.Author.Login}, [2]string{"review_url", n.URL})
				e.Body = n.Body
				out = append(out, ghFact{"ap:" + n.ID, e})
			}
		}
	case pb.GitHubTrigger_EVENT_ISSUE_OPENED:
		if !r.asked["issues"] {
			return nil, false
		}
		for _, is := range r.issues {
			if is.Author.Login == r.login {
				continue // (yours)
			}
			out = append(out, ghFact{fmt.Sprintf("io:%d", is.Number), ev(fmt.Sprintf("Issue #%d was opened", is.Number), is)})
		}
	case pb.GitHubTrigger_EVENT_WORKFLOW:
		if !r.asked["workflows"] {
			return nil, false
		}
		for _, run := range r.runs {
			if !workflowMatches(run, g.Workflow) {
				continue
			}
			summary := "A run of the “" + g.Workflow + "” workflow finished"
			if w := ghWord(run.Conclusion); w != "" {
				summary += ": " + w
			}
			f := [][2]string{{"repo", r.repo}, {"workflow", run.Name}, {"conclusion", run.Conclusion}, {"branch", run.HeadBranch},
				{"title", run.DisplayTitle}, {"url", run.HTMLURL}, {"run_number", fmt.Sprint(run.RunNumber)}, {"event", run.Event}}
			out = append(out, ghFact{fmt.Sprintf("wf:%d", run.ID), &schedEvent{Kind: "github", Summary: summary, Source: "GitHub", Fields: f}})
		}
	case pb.GitHubTrigger_EVENT_NOTIFICATION:
		if !r.asked["notifications"] {
			return nil, false
		}
		for _, n := range r.notes {
			summary := "A GitHub notification about " + r.repo
			if w := ghWord(n.Reason); w != "" {
				summary += " (" + w + ")"
			}
			f := [][2]string{{"repo", r.repo}, {"reason", n.Reason}, {"type", n.Subject.Type}, {"title", n.Subject.Title}, {"url", ghHTMLURL(n.Subject.URL)}}
			out = append(out, ghFact{"nt:" + n.ID + "@" + n.UpdatedAt, &schedEvent{Kind: "github", Summary: summary, Source: "GitHub", Fields: f}})
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
	case pb.GitHubTrigger_EVENT_COMMIT:
		if !r.asked["repo"] {
			return nil, false
		}
		h, ok := r.heads[g.Branch]
		if !ok {
			return nil, true
		}
		on := cmpOr(g.Branch, r.defaultBranch)
		f := [][2]string{{"repo", r.repo}, {"branch", on}, {"commit", h.Oid}, {"title", h.MessageHeadline}, {"url", h.URL}}
		if h.Author.User != nil && h.Author.User.Login != "" {
			f = append(f, [2]string{"author", h.Author.User.Login})
		} else if h.Author.Name != "" {
			f = append(f, [2]string{"author", h.Author.Name})
		}
		e := &schedEvent{Kind: "github", Summary: "A new commit on " + on + " in " + r.repo, Source: "GitHub", Fields: f}
		out = append(out, ghFact{"co:" + on + ":" + h.Oid, e})
	case pb.GitHubTrigger_EVENT_RELEASE:
		if !r.asked["repo"] {
			return nil, false
		}
		// A release and its tag are one fact: whichever is seen first fires.
		released := map[string]bool{}
		for _, rl := range r.releases {
			if rl.IsDraft || !schedtrig.TagMatches(g.Tags, rl.TagName) {
				continue
			}
			released[rl.TagName] = true
			f := [][2]string{{"repo", r.repo}, {"tag", rl.TagName}, {"title", cmpOr(rl.Name, rl.TagName)}, {"url", rl.URL}}
			if rl.IsPrerelease {
				f = append(f, [2]string{"prerelease", "true"})
			}
			e := &schedEvent{Kind: "github", Summary: "A new release of " + r.repo + ": " + rl.TagName, Source: "GitHub", Fields: f, Body: rl.Description}
			out = append(out, ghFact{"tag:" + rl.TagName, e})
		}
		for _, tag := range r.tags {
			if released[tag] || !schedtrig.TagMatches(g.Tags, tag) {
				continue
			}
			f := [][2]string{{"repo", r.repo}, {"tag", tag}, {"title", tag},
				{"url", "https://github.com/" + r.repo + "/releases/tag/" + url.PathEscape(tag)}}
			out = append(out, ghFact{"tag:" + tag, &schedEvent{Kind: "github", Summary: "A new tag in " + r.repo + ": " + tag, Source: "GitHub", Fields: f}})
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
	maxSeen     = 1000
	seenFor     = 30 * 24 * time.Hour
	seenRefresh = 24 * time.Hour
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
			if at, ok := st.SeenAt[f.key]; ok {
				// Still so: remembered for seenFor after it's last seen, not
				// first (else a tag, or a label left on, fires again after
				// it). Noted once a day, not saved at every look.
				if now.UnixMilli()-at > seenRefresh.Milliseconds() {
					st.SeenAt[f.key], changed = now.UnixMilli(), true
				}
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

// GitHubKey is what a GitHub trigger watches, for its trigState's key (the
// repository is added to it): changing what it watches starts it afresh.
func GitHubKey(g *pb.GitHubTrigger) string {
	k := fmt.Sprintf("%d:%s", g.Event, g.Label)
	if g.Branch != "" || g.Tags != "" {
		k += ":" + g.Branch + ":" + g.Tags
	}
	if g.Workflow != "" {
		k += ":wf:" + g.Workflow
	}
	return k
}
