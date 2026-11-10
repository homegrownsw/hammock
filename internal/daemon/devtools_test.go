package daemon

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A DevTools pane opens for a browser pane in the same Group only (it
// reaches all of that page), and isn't something to navigate to.
func TestDevToolsPane(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	ctx := context.Background()
	g1 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "one"}}}))
	g2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "two"}}}))
	t1 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g1, Pane: &pb.PaneSpec{Url: "about:blank"}}}}))
	t2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g2, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	page, other := d.tabs[t1].FocusedPaneId, d.tabs[t2].FocusedPaneId
	d.mu.Unlock()

	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: page, Spec: &pb.PaneSpec{Url: DevToolsURL + page}}}})
	d.mu.Lock()
	p := d.panes[client.CreatedID(r)]
	d.mu.Unlock()
	if p == nil || !p.isBrowser() || p.snapshotInfo().Url != DevToolsURL+page {
		t.Fatalf("devtools pane: %v", p)
	}
	// From another Group, of a pane that isn't a page, or as an address: no.
	for name, m := range map[string]*pb.ClientMessage{
		"other group": {Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: other, Spec: &pb.PaneSpec{Url: DevToolsURL + page}}}},
		"not a page":  {Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: other, Spec: &pb.PaneSpec{Url: DevToolsURL + other}}}},
		"navigate":    {Msg: &pb.ClientMessage_BrowserNavigate{BrowserNavigate: &pb.BrowserNavigate{PaneId: page, Action: "load", Url: DevToolsURL + page}}},
	} {
		if _, err := c.Call(ctx, m); err == nil {
			t.Errorf("%s: allowed", name)
		}
	}

	// Its page closes: so does it.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: page}}})
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		d.mu.Lock()
		_, still := d.panes[p.id]
		d.mu.Unlock()
		if !still {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("DevTools pane outlived its page")
		}
	}
}

// The bridge's socket takes only its own path and secret.
func TestDevToolsBridgeRefusesStrangers(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	u := d.browsers.hostURL(DevToolsURL + "pn_x")
	i := strings.Index(u, "ws=")
	if i < 0 {
		t.Fatalf("frontend URL %q", u)
	}
	base := "http://" + u[i+3:]
	for _, bad := range []string{
		strings.Replace(base, d.browsers.devtools.token, "0000", 1), // wrong secret
		base, // no such pane
	} {
		r, err := http.Get(bad)
		if err == nil {
			r.Body.Close()
			if r.StatusCode != http.StatusNotFound {
				t.Errorf("%s: %d", bad, r.StatusCode)
			}
		}
	}
}
