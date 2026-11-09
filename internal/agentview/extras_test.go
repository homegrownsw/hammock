package agentview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// A transcript line: an assistant's tool call, or the user line with its result.
func ccCall(uuid, id, name string, input any) []byte {
	in, _ := json.Marshal(input)
	return []byte(fmt.Sprintf(`{"type":"assistant","uuid":%q,"timestamp":"2026-11-09T00:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":%q,"input":%s}]}}`, uuid, id, name, in))
}

func ccResult(uuid, id, text string, structured any) []byte {
	st, _ := json.Marshal(structured)
	t, _ := json.Marshal(text)
	return []byte(fmt.Sprintf(`{"type":"user","uuid":%q,"timestamp":"2026-11-09T00:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]},"toolUseResult":%s}`, uuid, id, t, st))
}

func listSummary(l *pb.AgentTaskList) string {
	if l == nil {
		return "<none>"
	}
	var parts []string
	for _, t := range l.Tasks {
		parts = append(parts, fmt.Sprintf("%s:%s:%s", t.Id, t.Subject, strings.TrimPrefix(t.Status.String(), "STATUS_")))
	}
	return strings.Join(parts, " | ")
}

func TestTaskListTodoWrite(t *testing.T) {
	c := NewClaude("/p")
	if c.Tasks() != nil {
		t.Fatal("a task list before any")
	}
	c.Feed(ccCall("a1", "t1", "TodoWrite", map[string]any{"todos": []map[string]string{
		{"content": "Read the spec", "status": "completed", "activeForm": "Reading the spec"},
		{"content": "Write the parser", "status": "in_progress", "activeForm": "Writing the parser"},
		{"content": "Add tests", "status": "pending", "activeForm": "Adding tests"},
	}}))
	if got := listSummary(c.Tasks()); got != "1:Read the spec:COMPLETED | 2:Write the parser:IN_PROGRESS | 3:Add tests:PENDING" {
		t.Errorf("todos: %s", got)
	}
	if c.Tasks().Tasks[1].ActiveForm != "Writing the parser" {
		t.Errorf("active form: %+v", c.Tasks().Tasks[1])
	}
	// A new list replaces it; an empty one leaves an empty list (not none).
	c.Feed(ccCall("a2", "t2", "TodoWrite", map[string]any{"todos": []any{}}))
	if l := c.Tasks(); l == nil || len(l.Tasks) != 0 {
		t.Errorf("emptied: %v", l)
	}
}

func TestTaskListTaskTools(t *testing.T) {
	c := NewClaude("/p")
	c.Feed(ccCall("a1", "t1", "TaskCreate", map[string]any{"subject": "Read the spec", "description": "All of it", "activeForm": "Reading the spec"}))
	c.Feed(ccResult("u1", "t1", "Task #1 created successfully: Read the spec", nil))
	c.Feed(ccCall("a2", "t2", "TaskCreate", map[string]any{"subject": "Write the parser"}))
	// Its result names another id: that wins.
	c.Feed(ccResult("u2", "t2", "Task #5 created successfully: Write the parser", nil))
	c.Feed(ccCall("a3", "t3", "TaskCreate", map[string]any{"subject": "Add tests"}))
	c.Feed(ccCall("a3", "t3", "TaskCreate", map[string]any{"subject": "Add tests"})) // the same line again
	c.Feed(ccCall("a4", "t4", "TaskUpdate", map[string]any{"taskId": "1", "status": "completed"}))
	c.Feed(ccCall("a5", "t5", "TaskUpdate", map[string]any{"taskId": "5", "status": "in_progress", "activeForm": "Writing it"}))
	if got := listSummary(c.Tasks()); got != "1:Read the spec:COMPLETED | 5:Write the parser:IN_PROGRESS | 6:Add tests:PENDING" {
		t.Errorf("tasks: %s", got)
	}
	c.Feed(ccCall("a6", "t6", "TaskUpdate", map[string]any{"taskId": "6", "status": "deleted"}))
	if got := listSummary(c.Tasks()); got != "1:Read the spec:COMPLETED | 5:Write the parser:IN_PROGRESS" {
		t.Errorf("after delete: %s", got)
	}
	if b := c.Blocks()[len(c.Blocks())-1]; b.Title != "Updated a task" || b.Input != "#6 deleted" {
		t.Errorf("update block: %q %q", b.Title, b.Input)
	}
}

func TestSessionLinks(t *testing.T) {
	c := NewClaude("/p")
	pr := `{"type":"pr-link","sessionId":"s","prNumber":6704,"prUrl":"https://github.com/plexinc/web-tv-client/pull/6704","prRepository":"plexinc/web-tv-client","timestamp":"2026-11-06T16:08:40.722Z"}`
	c.Feed([]byte(pr))
	c.Feed([]byte(pr)) // recorded again as the session goes on: one link
	c.Feed(ccCall("a1", "t1", "Artifact", map[string]any{"file_path": "/tmp/x/signups.html", "description": "Signups"}))
	c.Feed(ccResult("u1", "t1", "Published /tmp/x/signups.html at https://claude.ai/artifact/aaaaaaaaaaaaaaaaaaaaaa (Version 1)",
		map[string]any{"url": "https://claude.ai/artifact/aaaaaaaaaaaaaaaaaaaaaa", "path": "/tmp/x/signups.html", "title": "Weekly Signups", "icon": "chart"}))
	// Reading someone's artifact isn't one this session made.
	c.Feed(ccCall("a2", "t2", "Artifact", map[string]any{"action": "read", "url": "https://claude.ai/artifact/bbbbbbbbbbbbbbbbbbbbbb?sk=abc"}))
	c.Feed(ccResult("u2", "t2", "<html>…</html>", map[string]any{"url": "https://claude.ai/artifact/bbbbbbbbbbbbbbbbbbbbbb"}))
	links := c.Links()
	if len(links) != 2 {
		t.Fatalf("links %v", links)
	}
	if l := links[0]; l.Kind != pb.AgentLink_KIND_PULL_REQUEST || l.Number != 6704 || l.Repo != "plexinc/web-tv-client" || l.Url != "https://github.com/plexinc/web-tv-client/pull/6704" {
		t.Errorf("pr %v", l)
	}
	if l := links[1]; l.Kind != pb.AgentLink_KIND_ARTIFACT || l.Title != "Weekly Signups" || l.Icon != "chart" || l.Url != "https://claude.ai/artifact/aaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("artifact %v", l)
	}
}

func TestCodexPlanTasks(t *testing.T) {
	c := NewCodex("/p")
	args, _ := json.Marshal(map[string]any{"plan": []map[string]string{{"step": "Find the bug", "status": "completed"}, {"step": "Fix it", "status": "in_progress"}}})
	a, _ := json.Marshal(string(args))
	c.Feed([]byte(fmt.Sprintf(`{"timestamp":"2026-11-09T00:00:00Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":%s,"call_id":"c1"}}`, a)))
	if got := listSummary(c.Tasks()); got != "1:Find the bug:COMPLETED | 2:Fix it:IN_PROGRESS" {
		t.Errorf("plan: %s", got)
	}
}

// Pull requests' titles and states come from Claude Code's cache of them.
func TestPRStatus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	asked := make(chan string, 4)
	old := askPR
	askPR = func(url string) (prState, bool) { asked <- url; return prState{Title: "From gh", State: "OPEN"}, true }
	ghAsked.Lock()
	ghAsked.m = nil // (nothing asked yet, whatever ran before)
	ghAsked.Unlock()
	t.Cleanup(func() { askPR = old })
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".claude", "gh-pr-status-cache.json"), []byte(`{"https://github.com/o/r/pull/7":{"number":7,"title":"Fix the thing","state":"MERGED"}}`), 0o644)
	links := []*pb.AgentLink{
		{Kind: pb.AgentLink_KIND_PULL_REQUEST, Url: "https://github.com/o/r/pull/7", Number: 7},
		{Kind: pb.AgentLink_KIND_PULL_REQUEST, Url: "https://github.com/o/r/pull/8", Number: 8},
		{Kind: pb.AgentLink_KIND_ARTIFACT, Url: "https://claude.ai/artifact/x", Title: "Mine"},
	}
	PRStatus(links)
	if links[0].Title != "Fix the thing" || links[0].State != "MERGED" || links[1].Title != "" || links[1].State != "" || links[2].Title != "Mine" {
		t.Errorf("links %v", links)
	}
	// One the cache doesn't know is asked of gh, off this goroutine: a
	// later call has its answer.
	if u := <-asked; u != "https://github.com/o/r/pull/8" {
		t.Fatalf("asked gh about %s", u)
	}
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		again := []*pb.AgentLink{{Kind: pb.AgentLink_KIND_PULL_REQUEST, Url: "https://github.com/o/r/pull/8", Number: 8}}
		PRStatus(again)
		if again[0].Title == "From gh" && again[0].State == "OPEN" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gh's answer never used: %v", again[0])
		}
	}
	select {
	case u := <-asked:
		t.Errorf("asked gh again about %s", u)
	default:
	}
}
