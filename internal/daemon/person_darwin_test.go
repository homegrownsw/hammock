package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// A program that leaves its pane (a double fork: launchd's child now)
// stays the responsibility of the pane's holder, and isn't you.
func TestDetachedFromPaneIsNotPerson(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	mark := fmt.Sprintf("%d", time.Now().UnixNano()%1000000+1000000)
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{
		Command: []string{"/bin/sh", "-c", "/bin/sh -c '/bin/sleep " + mark + " &'; exec /bin/cat"},
	}}}}))
	var pid int
	waitFor(t, "the detached sleep", func() bool {
		out, _ := exec.Command("pgrep", "-f", "sleep "+mark).Output()
		for _, f := range strings.Fields(string(out)) {
			p, _ := strconv.Atoi(f)
			if pp, _ := procinfo.Parent(p); p > 0 && pp == 1 {
				pid = p
			}
		}
		return pid > 0
	})
	defer exec.Command("kill", strconv.Itoa(pid)).Run()
	if r := responsibleFor(pid); r == 0 {
		t.Skip("no responsibility on this macOS")
	} else if !d.hammockPIDs()[r] {
		t.Fatalf("responsible %d, not one of the panes' holders %v", r, d.hammockPIDs())
	}
	personHook = nil
	defer func() { personHook = testPerson }()
	if d.positivelyPerson(nil, pid, codesign.Token{}) {
		t.Fatal("a program detached from a pane passes for you")
	}
	// And it's scoped as an agent in the pane it left.
	d.mu.Lock()
	pane := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	if cl := d.scopedCaller(nil, pid); cl.pane != pane || !cl.isAgent() {
		t.Fatalf("scoped as %+v, want an agent in %s", cl, pane)
	}
}

// An ad-hoc build's app is its bundle's own program, as Info.plist names
// it: not any program put beside it.
func TestBundleExecutable(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Hammock β.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	if got := bundleExecutable(app, "Hammock"); got != filepath.Join(app, "Contents", "MacOS", "Hammock") {
		t.Errorf("no Info.plist: %s", got)
	}
	os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("<plist><dict>\n  <key>CFBundleExecutable</key><string>Hammock Beta</string>\n</dict></plist>"), 0o644)
	if got := bundleExecutable(app, "Hammock"); got != filepath.Join(app, "Contents", "MacOS", "Hammock Beta") {
		t.Errorf("from Info.plist: %s", got)
	}
}
