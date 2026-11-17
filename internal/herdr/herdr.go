// Package herdr reads the workspace herdr (a terminal workspace manager for
// coding agents) saved, for importing it into Hammock: its workspaces become
// Groups, their tabs tabs, with the same splits, folders and names, and its
// agents' conversations resume.
//
// herdr keeps its session in session.json in its folder (~/.config/herdr):
// workspaces, each with tabs, each with a layout tree ({"Pane": n}, or
// {"Split": {direction, ratio, first, second}}) and its panes' folders, and
// for a pane running an agent, its session ({agent, kind: "id", value}).
// What else ran in a pane isn't kept.
package herdr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Dir is herdr's folder: $HERDR_HOME, else herdr in $XDG_CONFIG_HOME, else
// ~/.config/herdr.
func Dir() string {
	if d := os.Getenv("HERDR_HOME"); filepath.IsAbs(d) {
		return d
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		return filepath.Join(x, "herdr")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "herdr")
}

// SessionPath is herdr's default session's file.
func SessionPath(dir string) string { return filepath.Join(dir, "session.json") }

// Running: herdr's server is up (its socket answers), so its panes, and the
// agents in them, are still running.
func Running(dir string) bool {
	c, err := net.DialTimeout("unix", filepath.Join(dir, "herdr.sock"), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Plan is what an import would make.
type Plan struct {
	Path    string  `json:"path"`    // the session file read
	Running bool    `json:"running"` // herdr is still running
	Groups  []Group `json:"groups"`  // its workspaces, in its order
}

// Group is one herdr workspace, as a Hammock Group.
type Group struct {
	ID     string `json:"id"`     // herdr's workspace id ("w61"), for --only
	Name   string `json:"name"`   // its name, else its folder's
	Folder string `json:"folder"` // where it works
	// Exists: Hammock has a Group by this name already (imported before,
	// perhaps).
	Exists bool  `json:"exists"`
	Tabs   []Tab `json:"tabs"`
}

// Tab is one of a workspace's tabs.
type Tab struct {
	Name    string `json:"name"` // "": Hammock names it after its work
	Layout  *Node  `json:"layout"`
	Panes   []Pane `json:"panes"`   // in Layout's order, left to right, top to bottom
	Focused int    `json:"focused"` // the one focused, in Panes
}

// Node is a tab's split tree: a pane (an index in Tab.Panes), or two
// halves side by side (right) or stacked (down), Ratio being the first's
// share.
type Node struct {
	Pane      *int    `json:"pane,omitempty"`
	Direction string  `json:"direction,omitempty"` // "right" or "down"
	Ratio     float64 `json:"ratio,omitempty"`
	First     *Node   `json:"first,omitempty"`
	Second    *Node   `json:"second,omitempty"`
}

// Pane is one terminal.
type Pane struct {
	Cwd string `json:"cwd"`
	// The agent herdr saw running in it, and its session: Resume is the
	// command that resumes it, "" when Hammock can't (an agent it doesn't
	// know, or a conversation that's gone).
	Agent     string `json:"agent,omitempty"`      // the harness, "claude"
	AgentName string `json:"agent_name,omitempty"` // "Claude Code"
	Session   string `json:"session,omitempty"`
	Resume    string `json:"resume,omitempty"`
}

// The file, as herdr writes it (version 3).
type session struct {
	Version    int `json:"version"`
	Workspaces []struct {
		ID          string  `json:"id"`
		CustomName  *string `json:"custom_name"`
		IdentityCwd string  `json:"identity_cwd"`
		Tabs        []struct {
			CustomName *string           `json:"custom_name"`
			Layout     json.RawMessage   `json:"layout"`
			Panes      map[string]paneJS `json:"panes"`
			Focused    int               `json:"focused"`
		} `json:"tabs"`
	} `json:"workspaces"`
}

type paneJS struct {
	Cwd          string `json:"cwd"`
	AgentSession *struct {
		Agent string `json:"agent"`
		Kind  string `json:"kind"` // "id" or "path"
		Value string `json:"value"`
	} `json:"agent_session"`
}

type layoutJS struct {
	Pane  *int `json:"Pane"`
	Split *struct {
		Direction string          `json:"direction"` // "Horizontal" (side by side) or "Vertical"
		Ratio     float64         `json:"ratio"`
		First     json.RawMessage `json:"first"`
		Second    json.RawMessage `json:"second"`
	} `json:"Split"`
}

// ErrNone: herdr has saved no workspace here.
var ErrNone = errors.New("herdr has no saved workspaces")

// sessionExists says whether agent a still has session id (its file is
// where it keeps them); a variable for tests.
var sessionExists = func(a *agents.Agent, id string) bool {
	if a.Locate == nil {
		return true // can't tell: let it try
	}
	_, ok := a.Locate(id)
	return ok
}

// Load reads herdr's session in dir into a plan.
func Load(dir string) (*Plan, error) {
	path := SessionPath(dir)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNone
	}
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	p.Path = path
	p.Running = Running(dir)
	return p, nil
}

// Parse reads a session file's contents.
func Parse(data []byte) (*Plan, error) {
	var s session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	plan := &Plan{}
	for i, w := range s.Workspaces {
		g := Group{ID: w.ID, Folder: w.IdentityCwd}
		switch {
		case w.CustomName != nil && *w.CustomName != "":
			g.Name = *w.CustomName
		case w.IdentityCwd != "":
			g.Name = filepath.Base(w.IdentityCwd)
		default:
			g.Name = "Workspace " + strconv.Itoa(i+1)
		}
		for _, t := range w.Tabs {
			tab := Tab{}
			if t.CustomName != nil {
				tab.Name = *t.CustomName
			}
			order := map[int]int{} // herdr's pane number → index in tab.Panes
			root, err := readLayout(t.Layout, func(n int) (int, error) {
				pj, ok := t.Panes[strconv.Itoa(n)]
				if !ok {
					return 0, fmt.Errorf("tab %q: pane %d isn't there", tab.Name, n)
				}
				if _, seen := order[n]; seen {
					return 0, fmt.Errorf("tab %q: pane %d is in it twice", tab.Name, n)
				}
				order[n] = len(tab.Panes)
				tab.Panes = append(tab.Panes, pane(pj))
				return order[n], nil
			})
			if err != nil {
				return nil, err
			}
			if len(tab.Panes) == 0 {
				continue
			}
			tab.Layout = root
			tab.Focused = order[t.Focused] // 0 if it isn't one of them
			g.Tabs = append(g.Tabs, tab)
		}
		if len(g.Tabs) > 0 {
			plan.Groups = append(plan.Groups, g)
		}
	}
	if len(plan.Groups) == 0 {
		return nil, ErrNone
	}
	return plan, nil
}

func readLayout(raw json.RawMessage, leaf func(int) (int, error)) (*Node, error) {
	var l layoutJS
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	switch {
	case l.Pane != nil:
		i, err := leaf(*l.Pane)
		if err != nil {
			return nil, err
		}
		return &Node{Pane: &i}, nil
	case l.Split != nil:
		first, err := readLayout(l.Split.First, leaf)
		if err != nil {
			return nil, err
		}
		second, err := readLayout(l.Split.Second, leaf)
		if err != nil {
			return nil, err
		}
		dir := "right"
		if l.Split.Direction == "Vertical" {
			dir = "down"
		}
		r := l.Split.Ratio
		if r <= 0.05 || r >= 0.95 {
			r = 0.5
		}
		return &Node{Direction: dir, Ratio: r, First: first, Second: second}, nil
	}
	return nil, fmt.Errorf("a layout that's neither a pane nor a split: %s", raw)
}

func pane(pj paneJS) Pane {
	p := Pane{Cwd: pj.Cwd}
	as := pj.AgentSession
	if as == nil || as.Agent == "" {
		return p
	}
	p.Agent = as.Agent
	a := agents.Get(as.Agent)
	if a == nil {
		return p
	}
	p.AgentName = a.Name
	if as.Kind != "id" || as.Value == "" || a.Resume == nil {
		return p
	}
	p.Session = as.Value
	if sessionExists(a, as.Value) {
		p.Resume = a.Resume(as.Value)
	}
	return p
}

// Only keeps the Groups whose ids are in ids.
func (p *Plan) Only(ids []string) {
	keep := p.Groups[:0]
	for _, g := range p.Groups {
		for _, id := range ids {
			if g.ID == id {
				keep = append(keep, g)
				break
			}
		}
	}
	p.Groups = keep
}

// Call sends a message to Hammock's daemon and returns its reply.
type Call func(*pb.ClientMessage) (*pb.ServerMessage, error)

// Import makes g as a Group, with its tabs; it returns how many tabs it
// made and conversations it resumed.
func Import(call Call, g Group) (tabs, resumed int, err error) {
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: g.Name}}})
	if err != nil {
		return 0, 0, err
	}
	gid := r.GetCreated().GetId()
	folder := dirOr(g.Folder, "")
	if folder != "" {
		if _, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: gid, Folder: folder}}}); err != nil {
			return 0, 0, err
		}
	}
	for _, t := range g.Tabs {
		n, err := importTab(call, gid, folder, t)
		if err != nil {
			return tabs, resumed, err
		}
		tabs++
		resumed += n
	}
	return tabs, resumed, nil
}

// dirOr is dir if it's a folder that's there, else fallback.
func dirOr(dir, fallback string) string {
	if st, err := os.Stat(dir); dir != "" && err == nil && st.IsDir() {
		return dir
	}
	return fallback
}

func importTab(call Call, gid, folder string, t Tab) (resumed int, err error) {
	cwd := func(i int) string { return dirOr(t.Panes[i].Cwd, folder) }
	first := firstPane(t.Layout)
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Name: t.Name, Pane: &pb.PaneSpec{Cwd: cwd(first)},
	}}})
	if err != nil {
		return 0, err
	}
	tid := r.GetCreated().GetId()
	st, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	if err != nil {
		return 0, err
	}
	ids := make([]string, len(t.Panes))
	for _, tab := range st.GetState().GetTabs() {
		if tab.Id == tid {
			ids[first] = tab.FocusedPaneId
		}
	}
	if ids[first] == "" {
		return 0, errors.New("the new tab has no pane")
	}
	// Each split cuts the pane holding its first half's first pane, so the
	// tree is built from the top down; then it's given herdr's proportions
	// in one go.
	var build func(n *Node) error
	build = func(n *Node) error {
		if n.Pane != nil {
			return nil
		}
		at, next := firstPane(n.First), firstPane(n.Second)
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
			SplitPaneId: ids[at], Direction: direction(n), Spec: &pb.PaneSpec{Cwd: cwd(next)},
		}}})
		if err != nil {
			return err
		}
		ids[next] = r.GetCreated().GetId()
		if err := build(n.First); err != nil {
			return err
		}
		return build(n.Second)
	}
	if err := build(t.Layout); err != nil {
		return 0, err
	}
	if len(t.Panes) > 1 {
		if _, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetLayout{SetLayout: &pb.SetLayout{TabId: tid, Layout: Layout(t.Layout, ids)}}}); err != nil {
			return 0, err
		}
	}
	// Agents resume in their pane's shell, as a resumed session does, so
	// the shell stays when they're done.
	for i, p := range t.Panes {
		if p.Resume == "" {
			continue
		}
		if _, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: ids[i], Data: []byte(p.Resume + "\r")}}}); err != nil {
			return resumed, err
		}
		resumed++
	}
	return resumed, nil
}

// firstPane is the first (top left) pane of a tree.
func firstPane(n *Node) int {
	for n.Pane == nil {
		n = n.First
	}
	return *n.Pane
}

func direction(n *Node) pb.Split_Direction {
	if n.Direction == "down" {
		return pb.Split_DIRECTION_VERTICAL
	}
	return pb.Split_DIRECTION_HORIZONTAL
}

// Layout is n as a Hammock layout of the panes ids (by index).
func Layout(n *Node, ids []string) *pb.Layout {
	if n.Pane != nil {
		return &pb.Layout{Node: &pb.Layout_PaneId{PaneId: ids[*n.Pane]}}
	}
	return &pb.Layout{Node: &pb.Layout_Split{Split: &pb.Split{
		Direction: direction(n),
		Children:  []*pb.Layout{Layout(n.First, ids), Layout(n.Second, ids)},
		Weights:   []float32{float32(n.Ratio), float32(1 - n.Ratio)},
	}}}
}
