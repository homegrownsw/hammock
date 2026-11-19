package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestBrowserProfileKey(t *testing.T) {
	for in, want := range map[string]string{
		"gp_abcd2345": "gp_abcd2345",
		"":            "",
		"../x":        "",
		"a/b":         "",
		".":           "",
		"..":          "",
		"gp x":        "",
	} {
		if got := browserProfileKey(in); got != want {
			t.Errorf("browserProfileKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// The first start deletes the profile every pane used to share; each start
// deletes gone Groups' profiles, and nothing else.
func TestPrepareBrowserProfiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, rel))
		return err == nil
	}
	// The old layout: one Default profile, Chromium's own files beside it.
	write("Default/Cookies")
	write("Default/Local Storage/leveldb/000003.log")
	write("Local State")
	if err := prepareBrowserProfiles(root, map[string]bool{"gp_aaaaaaaa": true}); err != nil {
		t.Fatal(err)
	}
	if exists("Default") {
		t.Error("the shared profile survived")
	}
	if !exists("Local State") {
		t.Error("deleted Local State")
	}

	// From then on, Default is left alone (Chromium's global profile, kept
	// in memory, makes one), and only gone Groups' profiles go.
	write("Default/Preferences")
	write("group-gp_aaaaaaaa/Cookies")
	write("group-gp_bbbbbbbb/Cookies")
	write("Safe Browsing/x")
	if err := prepareBrowserProfiles(root, map[string]bool{"gp_aaaaaaaa": true}); err != nil {
		t.Fatal(err)
	}
	if !exists("Default/Preferences") {
		t.Error("deleted Default again")
	}
	if !exists("group-gp_aaaaaaaa/Cookies") {
		t.Error("deleted a live Group's profile")
	}
	if exists("group-gp_bbbbbbbb") {
		t.Error("kept a gone Group's profile")
	}
	if !exists("Safe Browsing/x") || !exists("Local State") {
		t.Error("deleted Chromium's own files")
	}
	if browserProfileDir(root, "gp_aaaaaaaa") != filepath.Join(root, "group-gp_aaaaaaaa") {
		t.Error("browserProfileDir doesn't name them as the host does")
	}
}

// fakeBrowserHost stands in for Hammock Browser on dir's socket: each
// connection gets hello (ids, groups), and what the daemon sends comes out
// of msgs.
func fakeBrowserHost(t *testing.T, dir string, hello func() map[string]any) (msgs chan map[string]any, conns chan net.Conn) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(BrowserSocketPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", BrowserSocketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	msgs, conns = make(chan map[string]any, 100), make(chan net.Conn, 10)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			conns <- c
			b, _ := json.Marshal(hello())
			_, _ = c.Write(append(b, '\n'))
			go func() {
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var m map[string]any
					if json.Unmarshal(sc.Bytes(), &m) == nil {
						msgs <- m
					}
				}
			}()
		}
	}()
	return msgs, conns
}

// expectHostMsg waits for a message of type kind for pane id ("" for any).
func expectHostMsg(t *testing.T, msgs chan map[string]any, kind, id string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-msgs:
			if m["t"] == kind && (id == "" || m["id"] == id) {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s for %q from the daemon", kind, id)
			return nil
		}
	}
}

// Browser panes open in their Group's profile, reopen in another's when
// their tab moves there, and a closed Group's profile is dropped.
func TestBrowserGroupProfiles(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	var mu sync.Mutex
	has := map[string]any{} // the fake's browsers, by Group profile
	msgs, conns := fakeBrowserHost(t, dir, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		ids := []string{}
		for id := range has {
			ids = append(ids, id)
		}
		return map[string]any{"t": "hello", "ids": ids, "groups": has}
	})
	d.browsers.path = "/nonexistent" // never spawn one: the fake answers the dial

	group := func(name string) string {
		return client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: name}}}))
	}
	a, b := group("a"), group("b")
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: a, Pane: &pb.PaneSpec{Url: "about:blank"},
	}}}))
	d.mu.Lock()
	page := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	if m := expectHostMsg(t, msgs, "create", page); m["group"] != a {
		t.Fatalf("created in %v, want Group a's profile (%s)", m["group"], a)
	}

	// Moved to b: reopened in b's profile.
	d.mu.Lock()
	err := d.moveTab(tab, b, 0)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if m := expectHostMsg(t, msgs, "create", page); m["group"] != b {
		t.Fatalf("after moving: created in %v, want Group b's profile (%s)", m["group"], b)
	}

	// A host that has it in the wrong profile (it missed the move) reopens
	// it in the right one when it reconnects.
	mu.Lock()
	has[page] = a
	mu.Unlock()
	(<-conns).Close()
	select {
	case <-conns:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't reconnect")
	}
	if m := expectHostMsg(t, msgs, "create", page); m["group"] != b {
		t.Fatalf("on reconnecting: created in %v, want Group b's profile (%s)", m["group"], b)
	}

	// A tab of a's merged into b's tab: its page reopens in b's profile.
	tab2 := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: a, Pane: &pb.PaneSpec{Url: "about:blank"},
	}}}))
	d.mu.Lock()
	page2 := d.tabs[tab2].FocusedPaneId
	d.mu.Unlock()
	expectHostMsg(t, msgs, "create", page2)
	d.mu.Lock()
	err = d.mergeTab(tab2, page, pb.Split_DIRECTION_HORIZONTAL)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if m := expectHostMsg(t, msgs, "create", page2); m["group"] != b {
		t.Fatalf("after merging: created in %v, want Group b's profile (%s)", m["group"], b)
	}

	// Closing b, which Reopen Closed can undo, closes its pages but keeps
	// its profile (its logins), also when the host next starts.
	closeB := func() {
		t.Helper()
		d.mu.Lock()
		err := d.closeGroup(b, true)
		d.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	closeB()
	expectHostMsg(t, msgs, "close", page)
	if !d.browserProfiles()[b] {
		t.Fatal("a reopenable Group's profile isn't kept at the host's start")
	}

	// Reopened: back in the same profile, never dropped meanwhile.
	d.mu.Lock()
	_, err = d.reopenEntry(d.nextClosed())
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.After(5 * time.Second); ; {
		var m map[string]any
		select {
		case m = <-msgs:
		case <-deadline:
			t.Fatal("no create for the reopened Group's page")
		}
		if m["t"] == "dropProfile" {
			t.Fatalf("dropped %v, a Group that could be reopened", m["group"])
		}
		if m["t"] == "create" {
			if m["group"] != b {
				t.Fatalf("reopened in %v, want Group b's profile (%s)", m["group"], b)
			}
			break
		}
	}

	// Closed again, and pushed off Reopen Closed: gone for good, so its
	// profile is dropped.
	closeB()
	expectHostMsg(t, msgs, "close", "")
	d.mu.Lock()
	for range maxClosedTabs {
		d.pushClosed(&closedTab{panes: map[string]*pb.Pane{"x": {}}})
	}
	d.mu.Unlock()
	if m := expectHostMsg(t, msgs, "dropProfile", ""); m["group"] != b {
		t.Fatalf("dropped %v, want Group b's profile (%s)", m["group"], b)
	}
	if d.browserProfiles()[b] {
		t.Fatal("a gone Group's profile is kept at the host's start")
	}

	// A Group nothing can bring back (no tabs to remember) drops at once.
	empty := group("c")
	d.mu.Lock()
	err = d.closeGroup(empty, true)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if m := expectHostMsg(t, msgs, "dropProfile", ""); m["group"] != empty {
		t.Fatalf("dropped %v, want Group c's profile (%s)", m["group"], empty)
	}
}
