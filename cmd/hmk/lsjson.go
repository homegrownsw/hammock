package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// `hmk ls --json`: the same tree printWorld shows, for scripts (desktop
// widgets, status bars). A shape of its own rather than protojson so it can
// stay stable as the proto changes: titles are resolved the way the apps show
// them, enums are short lowercase words, and nothing is truncated.

type lsJSON struct {
	Groups    []lsGroup     `json:"groups"`
	Attention []lsAttention `json:"attention"` // Needs You
	Grants    []lsGrant     `json:"grants"`    // permission requests
}

type lsGroup struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Icon   string  `json:"icon,omitempty"`
	Shared bool    `json:"shared,omitempty"` // a Group someone shared with you
	Tabs   []lsTab `json:"tabs"`
}

type lsTab struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Named         bool     `json:"named"` // given a name, rather than auto-named or "Tab n"
	FocusedPaneID string   `json:"focused_pane_id,omitempty"`
	Panes         []lsPane `json:"panes"`
}

type lsPane struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"` // terminal, log, browser, simulator, editor, diff
	Title      string   `json:"title"`
	Focused    bool     `json:"focused"` // the tab's focused pane
	Cwd        string   `json:"cwd,omitempty"`
	Cols       uint32   `json:"cols,omitempty"`
	Rows       uint32   `json:"rows,omitempty"`
	URL        string   `json:"url,omitempty"`
	Path       string   `json:"path,omitempty"`  // an editor pane's file
	Dirty      bool     `json:"dirty,omitempty"` // an editor pane with unsaved edits
	Exited     bool     `json:"exited,omitempty"`
	ExitStatus int32    `json:"exit_status,omitempty"`
	StartError string   `json:"start_error,omitempty"`
	Agent      *lsAgent `json:"agent,omitempty"`
	Floating   bool     `json:"floating,omitempty"`  // over the tab's splits rather than in them
	Minimised  bool     `json:"minimised,omitempty"` // a floating pane on the tab's shelf
}

type lsAgent struct {
	Harness        string `json:"harness"`
	State          string `json:"state"` // starting, working, needs_input, idle, errored, exited
	StateSinceMs   int64  `json:"state_since_unix_ms,omitempty"`
	Summary        string `json:"summary,omitempty"`
	Seen           bool   `json:"seen"` // looked at since this state began; idle && !seen is "finished"
	SessionID      string `json:"session_id,omitempty"`
	Subagents      int32  `json:"subagents,omitempty"`
	RunningCommand bool   `json:"running_command,omitempty"`
	// What the program reports of itself (OSC 7501), when it does: then
	// harness is empty for one that isn't an agent.
	Reported *reportedView `json:"reported,omitempty"`
}

type lsAttention struct {
	ID       string `json:"id"`
	PaneID   string `json:"pane_id"`
	State    string `json:"state"`
	Summary  string `json:"summary,omitempty"`
	Question bool   `json:"question,omitempty"`
	Since    int64  `json:"created_unix_ms,omitempty"`
}

type lsGrant struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// stateJSON: AGENT_STATE_NEEDS_INPUT → "needs_input".
func stateJSON(s pb.AgentState) string {
	if s == pb.AgentState_AGENT_STATE_UNSPECIFIED {
		return "unknown"
	}
	return strings.ToLower(strings.TrimPrefix(s.String(), "AGENT_STATE_"))
}

// kindJSON: PANE_KIND_BROWSER → "browser"; unspecified is a terminal; a
// terminal following a log source (hmk log) is a "log", as people see it.
func kindJSON(p *pb.Pane) string {
	k := p.GetKind()
	if k == pb.PaneKind_PANE_KIND_UNSPECIFIED || k == pb.PaneKind_PANE_KIND_TERMINAL {
		if p.GetLog() != nil {
			return "log"
		}
		return "terminal"
	}
	return strings.ToLower(strings.TrimPrefix(k.String(), "PANE_KIND_"))
}

func printWorldJSON(w *world) error {
	out := lsJSON{Groups: []lsGroup{}, Attention: []lsAttention{}, Grants: []lsGrant{}}
	for _, s := range w.groups {
		g := lsGroup{
			ID: s.Id, Title: client.GroupTitle(s, w.groups, w.panes), Icon: s.Icon,
			Shared: s.HostUserId != "", Tabs: []lsTab{},
		}
		for _, tid := range s.TabIds {
			t := w.tabs[tid]
			if t == nil {
				continue
			}
			tab := lsTab{
				ID: t.Id, Title: client.TabTitle(t, s), Named: t.Name != "",
				FocusedPaneID: t.FocusedPaneId, Panes: []lsPane{},
			}
			for _, pid := range tabPaneIDs(t) {
				p := w.panes[pid]
				if p == nil {
					continue
				}
				lp := lsPane{
					ID: p.Id, Kind: kindJSON(p), Title: p.Title, Focused: t.FocusedPaneId == pid,
					Cwd: p.Cwd, Cols: p.Cols, Rows: p.Rows, URL: p.Url, Path: p.GetEditor().GetPath(), Dirty: p.GetEditor().GetDirty(),
					Exited: p.Exited, ExitStatus: p.ExitStatus, StartError: p.StartError,
				}
				if f := floatOf(t, pid); f != nil {
					lp.Floating, lp.Minimised = true, f.Minimised
				}
				if a := p.Agent; a != nil {
					lp.Agent = &lsAgent{
						Harness: a.Harness, State: stateJSON(a.State), StateSinceMs: a.StateSinceUnixMs,
						Summary: a.Summary, Seen: a.Seen, SessionID: a.SessionId,
						Subagents: a.Subagents, RunningCommand: a.RunningCommand, Reported: reportedOf(a),
					}
				}
				tab.Panes = append(tab.Panes, lp)
			}
			g.Tabs = append(g.Tabs, tab)
		}
		out.Groups = append(out.Groups, g)
	}
	for _, a := range w.attn {
		out.Attention = append(out.Attention, lsAttention{
			ID: a.Id, PaneID: a.PaneId, State: stateJSON(a.State), Summary: a.Summary,
			Question: a.Question, Since: a.CreatedUnixMs,
		})
	}
	for _, g := range w.grants {
		out.Grants = append(out.Grants, lsGrant{ID: g.Id, Text: grantText(g)})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
