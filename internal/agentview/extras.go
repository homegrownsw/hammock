package agentview

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Extras is what a session keeps besides its conversation: its task list
// and the pull requests and artifacts it made. Parsers that know them
// implement it.
type Extras interface {
	// Tasks is the task list as it stands (nil: the session never had one).
	Tasks() *pb.AgentTaskList
	// Links is the session's pull requests and artifacts, oldest first.
	Links() []*pb.AgentLink
}

// taskList follows an agent's task list through its tools: a whole list at
// once (TodoWrite, Codex's update_plan), or one task at a time (TaskCreate,
// then TaskUpdate by id).
type taskList struct {
	tasks   []*pb.AgentTask
	set     bool
	next    int               // the id the next created task is given, until its result says
	created map[string]string // TaskCreate's tool_use id -> the id given it
}

func (l *taskList) list() *pb.AgentTaskList {
	if !l.set {
		return nil
	}
	out := &pb.AgentTaskList{}
	for _, t := range l.tasks {
		out.Tasks = append(out.Tasks, proto.CloneOf(t))
	}
	return out
}

func taskState(s string) pb.AgentTask_Status {
	switch s {
	case "completed", "done":
		return pb.AgentTask_STATUS_COMPLETED
	case "in_progress", "in-progress", "active":
		return pb.AgentTask_STATUS_IN_PROGRESS
	}
	return pb.AgentTask_STATUS_PENDING
}

// replace: a whole list (TodoWrite's todos: content, status, activeForm;
// update_plan's plan: step, status).
func (l *taskList) replace(items []any) {
	l.set = true
	l.tasks = nil
	for i, it := range items {
		m, _ := it.(map[string]any)
		str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
		subject := str("content")
		if subject == "" {
			subject = str("step")
		}
		if subject == "" {
			continue
		}
		l.tasks = append(l.tasks, &pb.AgentTask{Id: strconv.Itoa(i + 1), Subject: subject, ActiveForm: str("activeForm"), Status: taskState(str("status"))})
	}
}

// create: TaskCreate's input (subject, activeForm). Ids count up from 1 in
// a session's list; its result names the one given ("Task #3 created"),
// which wins if they differ.
func (l *taskList) create(toolID string, in map[string]any) {
	str := func(k string) string { s, _ := in[k].(string); return strings.TrimSpace(s) }
	subject := str("subject")
	if subject == "" {
		subject = str("description")
	}
	if subject == "" {
		return
	}
	l.set = true
	l.next++
	id := strconv.Itoa(l.next)
	if l.created == nil {
		l.created = map[string]string{}
	}
	l.created[toolID] = id
	l.tasks = append(l.tasks, &pb.AgentTask{Id: id, Subject: subject, ActiveForm: str("activeForm"), Status: pb.AgentTask_STATUS_PENDING})
}

var taskNumber = regexp.MustCompile(`#(\d+)`)

// created: TaskCreate's result, with the id the task was given.
func (l *taskList) createdResult(toolID, text string, failed bool) {
	id, ok := l.created[toolID]
	if !ok {
		return
	}
	delete(l.created, toolID)
	for i, t := range l.tasks {
		if t.Id != id {
			continue
		}
		if failed {
			l.tasks = append(l.tasks[:i], l.tasks[i+1:]...)
			l.next--
			return
		}
		if m := taskNumber.FindStringSubmatch(text); m != nil && m[1] != id {
			t.Id = m[1]
			if n, _ := strconv.Atoi(m[1]); n > l.next {
				l.next = n
			}
		}
		return
	}
}

// update: TaskUpdate's input (taskId, and any of status, subject,
// activeForm; status "deleted" removes it).
func (l *taskList) update(in map[string]any) {
	str := func(k string) string { s, _ := in[k].(string); return strings.TrimSpace(s) }
	id := str("taskId")
	if id == "" {
		if n, ok := in["taskId"].(float64); ok {
			id = strconv.Itoa(int(n))
		}
	}
	id = strings.TrimPrefix(id, "#")
	for i, t := range l.tasks {
		if t.Id != id {
			continue
		}
		if s := str("status"); s == "deleted" {
			l.tasks = append(l.tasks[:i], l.tasks[i+1:]...)
			return
		} else if s != "" {
			t.Status = taskState(s)
		}
		if s := str("subject"); s != "" {
			t.Subject = s
		}
		if s := str("activeForm"); s != "" {
			t.ActiveForm = s
		}
		return
	}
}

// linkList is a session's links, one per URL (the latest title wins).
type linkList []*pb.AgentLink

func (ls *linkList) add(l *pb.AgentLink) {
	for i, x := range *ls {
		if x.Url == l.Url {
			if l.Title == "" {
				l.Title = x.Title
			}
			(*ls)[i] = l
			return
		}
	}
	*ls = append(*ls, l)
}

func (ls linkList) clone() []*pb.AgentLink {
	out := make([]*pb.AgentLink, len(ls))
	for i, l := range ls {
		out[i] = proto.CloneOf(l)
	}
	return out
}

// artifactURL is a published artifact's link, without a share key.
var artifactURL = regexp.MustCompile(`^https://claude\.ai/(?:code/)?artifact/[A-Za-z0-9-]+`)

// artifactLink reads the Artifact tool's structured result: a publish says
// url and title (reads and listings don't say both).
func artifactLink(structured json.RawMessage) *pb.AgentLink {
	var r struct {
		URL   string `json:"url"`
		Title string `json:"title"`
		Icon  string `json:"icon"`
		Path  string `json:"path"`
	}
	if json.Unmarshal(structured, &r) != nil || r.Title == "" || r.Path == "" {
		return nil
	}
	u := artifactURL.FindString(r.URL)
	if u == "" {
		return nil
	}
	return &pb.AgentLink{Kind: pb.AgentLink_KIND_ARTIFACT, Url: u, Title: r.Title, Icon: r.Icon}
}

// PRStatus fills in pull requests' titles and states from Claude Code's
// cache of them (~/.claude/gh-pr-status-cache.json, which it keeps for the
// PRs its sessions link), else from asking gh (in the background: a later
// call has the answer), leaving those neither knows as they are.
func PRStatus(links []*pb.AgentLink) {
	cache := prCache()
	for _, l := range links {
		if l.Kind != pb.AgentLink_KIND_PULL_REQUEST {
			continue
		}
		s, ok := cache[l.Url]
		if !ok {
			s, ok = ghPR(l.Url)
		}
		if ok {
			if s.Title != "" {
				l.Title = s.Title
			}
			l.State = s.State
		}
	}
}

// ghAsked is what gh said of each pull request, and when it was asked.
var ghAsked struct {
	sync.Mutex
	m map[string]*ghAnswer
}

type ghAnswer struct {
	st      prState
	ok      bool
	at      time.Time
	running bool
}

// ghPRAgain is how long gh's answer about a pull request stands.
const ghPRAgain = 10 * time.Minute

// ghPR is what gh last said of a pull request, asking it again (off this
// goroutine) when that's old or it hasn't been asked.
func ghPR(url string) (prState, bool) {
	ghAsked.Lock()
	defer ghAsked.Unlock()
	if ghAsked.m == nil {
		ghAsked.m = map[string]*ghAnswer{}
	}
	a := ghAsked.m[url]
	if a == nil {
		a = &ghAnswer{}
		ghAsked.m[url] = a
	}
	if !a.running && time.Since(a.at) > ghPRAgain {
		a.running = true
		go func() {
			st, ok := askPR(url)
			ghAsked.Lock()
			if ok || !a.ok { // (a failure keeps the last answer)
				a.st, a.ok = st, ok
			}
			a.at, a.running = time.Now(), false
			ghAsked.Unlock()
		}()
	}
	return a.st, a.ok
}

// askPR asks about a pull request (askGH; tests replace it).
var askPR = askGH

// askGH runs gh pr view for a pull request's title and state.
func askGH(url string) (prState, bool) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh"} {
			if _, err := os.Stat(p); err == nil {
				gh = p
				break
			}
		}
	}
	if gh == "" {
		return prState{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, gh, "pr", "view", url, "--json", "title,state,isDraft").Output()
	if err != nil {
		return prState{}, false
	}
	var r struct {
		Title   string `json:"title"`
		State   string `json:"state"`
		IsDraft bool   `json:"isDraft"`
	}
	if json.Unmarshal(out, &r) != nil {
		return prState{}, false
	}
	if r.IsDraft && r.State == "OPEN" {
		r.State = "DRAFT"
	}
	return prState{Title: r.Title, State: r.State}, true
}

type prState struct {
	Title string `json:"title"`
	State string `json:"state"`
}

var prCacheState struct {
	sync.Mutex
	mod  time.Time
	data map[string]prState
}

// prCache is the cache file, read again when it changes.
func prCache() map[string]prState {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	path := filepath.Join(home, ".claude", "gh-pr-status-cache.json")
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	prCacheState.Lock()
	defer prCacheState.Unlock()
	if !st.ModTime().Equal(prCacheState.mod) {
		var m map[string]prState
		if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &m) == nil {
			prCacheState.data, prCacheState.mod = m, st.ModTime()
		}
	}
	return prCacheState.data
}
