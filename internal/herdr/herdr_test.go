package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/homegrownsw/hammock/internal/agents"
)

// A session as herdr 0.9 saves it (made-up folders and ids).
const sample = `{
  "version": 3,
  "workspaces": [
    {
      "id": "w1", "custom_name": null, "identity_cwd": "/Users/me/Developer/app",
      "tabs": [
        {
          "custom_name": "Checkout [#42]",
          "layout": {"Split": {"direction": "Horizontal", "ratio": 0.6,
            "first": {"Pane": 7},
            "second": {"Split": {"direction": "Vertical", "ratio": 0.5, "first": {"Pane": 3}, "second": {"Pane": 9}}}}},
          "panes": {
            "3": {"cwd": "/Users/me/Developer/app/web"},
            "7": {"cwd": "/Users/me/Developer/app", "agent_session": {"source": "herdr:claude", "agent": "claude", "kind": "id", "value": "11111111-2222-3333-4444-555555555555"}},
            "9": {"cwd": "/Users/me/Developer/app", "agent_session": {"source": "herdr:codex", "agent": "codex", "kind": "id", "value": "gone"}}
          },
          "zoomed": false, "focused": 3, "root_pane": 7
        },
        {
          "custom_name": null,
          "layout": {"Pane": 1},
          "panes": {"1": {"cwd": "/Users/me/Developer/app", "agent_session": {"source": "herdr:mystery", "agent": "mystery", "kind": "id", "value": "x"}}},
          "zoomed": false, "focused": 1, "root_pane": 1
        }
      ],
      "active_tab": 0
    },
    {"id": "w2", "custom_name": "Notes", "identity_cwd": "/Users/me/Documents", "tabs": [
      {"custom_name": null, "layout": {"Pane": 2}, "panes": {"2": {"cwd": "/Users/me/Documents", "agent_session": {"agent": "claude", "kind": "path", "value": "/x.jsonl"}}}, "focused": 2}
    ]},
    {"id": "w3", "custom_name": null, "identity_cwd": "", "tabs": []}
  ],
  "active": 0, "selected": 0
}`

func TestParse(t *testing.T) {
	old := sessionExists
	sessionExists = func(_ *agents.Agent, id string) bool { return id != "gone" }
	t.Cleanup(func() { sessionExists = old })

	p, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	// The workspace with no tabs is left out.
	if len(p.Groups) != 2 {
		t.Fatalf("groups: %+v", p.Groups)
	}
	g := p.Groups[0]
	if g.ID != "w1" || g.Name != "app" || g.Folder != "/Users/me/Developer/app" || len(g.Tabs) != 2 {
		t.Fatalf("first group: %+v", g)
	}
	if p.Groups[1].Name != "Notes" {
		t.Errorf("named workspace: %q", p.Groups[1].Name)
	}

	tab := g.Tabs[0]
	if tab.Name != "Checkout [#42]" || len(tab.Panes) != 3 {
		t.Fatalf("tab: %+v", tab)
	}
	// Panes in the layout's order: 7, then 3 above 9.
	if tab.Panes[0].Agent != "claude" || tab.Panes[1].Cwd != "/Users/me/Developer/app/web" || tab.Panes[2].Agent != "codex" {
		t.Errorf("pane order: %+v", tab.Panes)
	}
	if tab.Focused != 1 {
		t.Errorf("focused %d, want 1 (herdr's pane 3)", tab.Focused)
	}
	l := tab.Layout
	if l.Direction != "right" || l.Ratio != 0.6 || *l.First.Pane != 0 || l.Second.Direction != "down" || *l.Second.First.Pane != 1 || *l.Second.Second.Pane != 2 {
		t.Errorf("layout: %+v", l)
	}

	// Claude's conversation resumes; Codex's is gone, so a shell.
	if c := tab.Panes[0]; c.Resume != "claude --resume '11111111-2222-3333-4444-555555555555'" || c.AgentName != "Claude Code" {
		t.Errorf("claude pane: %+v", c)
	}
	if c := tab.Panes[2]; c.Resume != "" || c.Session != "gone" {
		t.Errorf("codex pane: %+v", c)
	}
	// An agent Hammock doesn't know, or a session given as a path: a shell.
	if u := g.Tabs[1]; u.Name != "" || u.Panes[0].Agent != "mystery" || u.Panes[0].Resume != "" {
		t.Errorf("unknown agent's tab: %+v", u)
	}
	if n := p.Groups[1].Tabs[0].Panes[0]; n.Resume != "" || n.Session != "" {
		t.Errorf("path session: %+v", n)
	}

	p.Only([]string{"w2"})
	if len(p.Groups) != 1 || p.Groups[0].ID != "w2" {
		t.Errorf("only: %+v", p.Groups)
	}
}

func TestParseRejectsBrokenLayouts(t *testing.T) {
	for _, s := range []string{
		`{"workspaces":[{"id":"w1","tabs":[{"layout":{"Pane":4},"panes":{"1":{"cwd":"/"}}}]}]}`,
		`{"workspaces":[{"id":"w1","tabs":[{"layout":{"Split":{"direction":"Vertical","ratio":0.5,"first":{"Pane":1},"second":{"Pane":1}}},"panes":{"1":{"cwd":"/"}}}]}]}`,
		`{"workspaces":[{"id":"w1","tabs":[{"layout":{"Floating":1},"panes":{"1":{"cwd":"/"}}}]}]}`,
	} {
		if _, err := Parse([]byte(s)); err == nil || errors.Is(err, ErrNone) {
			t.Errorf("%s: %v", s, err)
		}
	}
	// A ratio at the edge (or none) is halved.
	p, err := Parse([]byte(`{"workspaces":[{"id":"w1","tabs":[{"layout":{"Split":{"direction":"Horizontal","ratio":0,"first":{"Pane":1},"second":{"Pane":2}}},"panes":{"1":{},"2":{}}}]}]}`))
	if err != nil || p.Groups[0].Tabs[0].Layout.Ratio != 0.5 || p.Groups[0].Name != "Workspace 1" {
		t.Errorf("%+v %v", p, err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); !errors.Is(err, ErrNone) {
		t.Errorf("no file: %v", err)
	}
	os.WriteFile(SessionPath(dir), []byte(`{"version":3,"workspaces":[]}`), 0o600)
	if _, err := Load(dir); !errors.Is(err, ErrNone) {
		t.Errorf("no workspaces: %v", err)
	}
	os.WriteFile(SessionPath(dir), []byte(sample), 0o600)
	p, err := Load(dir)
	if err != nil || p.Path != filepath.Join(dir, "session.json") || p.Running {
		t.Errorf("%+v %v", p, err)
	}
}

func TestDir(t *testing.T) {
	t.Setenv("HERDR_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "/x/config")
	if d := Dir(); d != "/x/config/herdr" {
		t.Errorf("XDG: %s", d)
	}
	t.Setenv("HERDR_HOME", "/x/herdr")
	if d := Dir(); d != "/x/herdr" {
		t.Errorf("HERDR_HOME: %s", d)
	}
}
