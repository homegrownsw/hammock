package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/herdr"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A herdr workspace comes in as a Group: its folder, its tabs' names, and
// a tab's splits with herdr's proportions, each pane in its own folder.
func TestHerdrImport(t *testing.T) {
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir)
	sub := t.TempDir()
	plan, err := herdr.Parse([]byte(`{"version":3,"workspaces":[{"id":"w1","custom_name":"Widgets","identity_cwd":"` + dir + `","tabs":[
		{"custom_name":"Three","layout":{"Split":{"direction":"Horizontal","ratio":0.7,"first":{"Pane":1},
			"second":{"Split":{"direction":"Vertical","ratio":0.25,"first":{"Pane":2},"second":{"Pane":3}}}}},
			"panes":{"1":{"cwd":"` + dir + `"},"2":{"cwd":"` + sub + `"},"3":{"cwd":"/no/such/folder"}},"focused":1},
		{"custom_name":null,"layout":{"Pane":4},"panes":{"4":{"cwd":"` + sub + `"}},"focused":4}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	send := func(m *pb.ClientMessage) (*pb.ServerMessage, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.Call(ctx, m)
	}
	// An agent's conversation is resumed by typing its command into the
	// pane's shell (here, one that just says so).
	plan.Groups[0].Tabs[1].Panes[0].Resume = "echo herdr-$((40+2))"
	tabs, resumed, err := herdr.Import(send, plan.Groups[0])
	if err != nil || tabs != 2 || resumed != 1 {
		t.Fatalf("import: %d tabs, %d resumed, %v", tabs, resumed, err)
	}

	st := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState()
	var g *pb.Group
	for _, x := range st.Groups {
		if x.Name == "Widgets" {
			g = x
		}
	}
	if g == nil || g.Folder != dir || len(g.TabIds) != 2 {
		t.Fatalf("group: %+v", g)
	}
	tabByID := map[string]*pb.Tab{}
	for _, x := range st.Tabs {
		tabByID[x.Id] = x
	}
	cwd := map[string]string{}
	for _, p := range st.Panes {
		cwd[p.Id] = p.Cwd
	}
	three, one := tabByID[g.TabIds[0]], tabByID[g.TabIds[1]]
	if three.Name != "Three" || one.Name != "" {
		t.Errorf("tab names %q, %q", three.Name, one.Name)
	}
	s := three.Layout.GetSplit()
	if s == nil || s.Direction != pb.Split_DIRECTION_HORIZONTAL || len(s.Children) != 2 || !near(s.Weights[0], 0.7) {
		t.Fatalf("layout: %v", three.Layout)
	}
	inner := s.Children[1].GetSplit()
	if inner == nil || inner.Direction != pb.Split_DIRECTION_VERTICAL || !near(inner.Weights[0], 0.25) {
		t.Fatalf("inner split: %v", s.Children[1])
	}
	left, top, bottom := s.Children[0].GetPaneId(), inner.Children[0].GetPaneId(), inner.Children[1].GetPaneId()
	// A folder that's gone opens in the Group's.
	if cwd[left] != dir || cwd[top] != sub || cwd[bottom] != dir {
		t.Errorf("folders: %q %q %q", cwd[left], cwd[top], cwd[bottom])
	}
	waitFor(t, "the resume command to run", func() bool {
		r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: one.FocusedPaneId}}})
		return strings.Contains(r.GetPaneText().Text, "herdr-42")
	})
}

func near(a float32, b float64) bool { return a-float32(b) < 0.01 && float32(b)-a < 0.01 }
