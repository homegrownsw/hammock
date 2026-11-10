package daemon

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

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

// The bridge's socket takes only its own path, the page's own token, and
// the DevTools frontend (by its Origin): a token for one page opens no
// other, and a program connecting itself (no Origin) or a web page (its
// own) is refused even with the right one.
func TestDevToolsBridgeRefusesStrangers(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "one"}}}))
	t1 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Url: "about:blank"}}}}))
	t2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Url: "about:blank"}}}}))
	d.mu.Lock()
	a, b := d.tabs[t1].FocusedPaneId, d.tabs[t2].FocusedPaneId
	d.mu.Unlock()
	wsOf := func(page string) string {
		u := d.browsers.hostURL(DevToolsURL + page)
		i := strings.Index(u, "ws=")
		if i < 0 {
			t.Fatalf("frontend URL %q", u)
		}
		return "ws://" + u[i+3:]
	}
	wsA, wsB := wsOf(a), wsOf(b)
	tokA := wsA[strings.LastIndex(wsA, "/")+1:]
	if tokB := wsB[strings.LastIndex(wsB, "/")+1:]; tokA == tokB {
		t.Fatal("one token for two pages")
	}
	try := func(u, origin string) int {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		ws, r, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: h})
		if err == nil {
			ws.CloseNow()
			return http.StatusSwitchingProtocols
		}
		if r == nil {
			t.Fatalf("%s: %v", u, err)
		}
		return r.StatusCode
	}
	for name, tc := range map[string]struct {
		url, origin string
		want        int
	}{
		"wrong secret":        {strings.Replace(wsA, tokA, strings.Repeat("0", len(tokA)), 1), devtoolsOrigin, http.StatusNotFound},
		"another page's":      {strings.Replace(wsB, "/"+b+"/", "/"+a+"/", 1), devtoolsOrigin, http.StatusNotFound},
		"no such pane":        {strings.Replace(wsA, "/"+a+"/", "/pn_x/", 1), devtoolsOrigin, http.StatusNotFound},
		"no Origin":           {wsA, "", http.StatusForbidden},
		"a web page's Origin": {wsA, "http://127.0.0.1:3000", http.StatusForbidden},
		"lookalike Origin":    {wsA, "devtools://devtools.example", http.StatusForbidden},
		"the frontend":        {wsA, devtoolsOrigin, http.StatusSwitchingProtocols},
	} {
		if got := try(tc.url, tc.origin); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}

// A DevTools pane's address (which carries its page's token) is never its
// title, and a page can't make itself DevTools: not by going to a DevTools
// address, nor by opening one as a popup.
func TestDevToolsAddressStaysPut(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "one"}}}))
	t1 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Url: "about:blank"}}}}))
	t2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Url: "about:blank"}}}}))
	d.mu.Lock()
	page, other := d.tabs[t1].FocusedPaneId, d.tabs[t2].FocusedPaneId
	d.mu.Unlock()
	dt := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: page, Spec: &pb.PaneSpec{Url: DevToolsURL + page}}}}))
	frontend := d.browsers.hostURL(DevToolsURL + page)
	info := func(id string) *pb.Pane {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.panes[id].snapshotInfo()
	}
	// Untitled, Chromium titles it by its address.
	d.browserState(hostMsg{ID: dt, URL: frontend, Title: frontend})
	if i := info(dt); i.Url != DevToolsURL+page || i.Title != "DevTools" {
		t.Errorf("untitled DevTools: url %q, title %q", i.Url, i.Title)
	}
	d.browserState(hostMsg{ID: dt, URL: frontend, Title: "DevTools - example.com/"})
	if i := info(dt); i.Title != "DevTools - example.com/" {
		t.Errorf("DevTools' own title: %q", i.Title)
	}
	// DevTools for a DevTools pane: no (it reaches its page all the same).
	if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: dt, Spec: &pb.PaneSpec{Url: DevToolsURL + dt}}}}); err == nil {
		t.Error("DevTools for DevTools")
	}

	// A page that went to a DevTools address (an error page for it) keeps
	// its last address, and so doesn't come back as DevTools.
	d.browserState(hostMsg{ID: other, URL: "https://example.com/", Title: "Example"})
	d.browserState(hostMsg{ID: other, URL: "hmk-devtools:" + page, Title: "hmk-devtools:" + page})
	if i := info(other); i.Url != "https://example.com/" || strings.Contains(i.Title, "devtools") {
		t.Errorf("a page at a DevTools address: url %q, title %q", i.Url, i.Title)
	}
	if d.browsers.hostURL(info(other).Url) != "https://example.com/" {
		t.Error("it would reload as DevTools")
	}

	// A page's popup to a DevTools address opens nothing.
	count := func() int {
		d.mu.Lock()
		defer d.mu.Unlock()
		return len(d.groups[g].TabIds)
	}
	before := count()
	for _, u := range []string{DevToolsURL + page, "HMK-DEVTOOLS:" + page, " hmk-devtools:" + page} {
		d.browserPopup(other, u, false)
	}
	if count() != before {
		t.Error("a page opened DevTools as a popup")
	}
}
