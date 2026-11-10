package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/holder"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestSecureDirTightensAndRefusesLinks(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "state")
	if err := secureDir(dir, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := secureDir(dir, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v not tightened", st.Mode().Perm())
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := secureDir(link, true); err != nil {
		t.Fatalf("state dir symlink refused: %v", err)
	}
	if err := secureDir(link, false); err == nil {
		t.Fatal("run dir symlink accepted")
	}
	// Someone else's dir (root's /usr) is refused, when not running as root.
	if os.Getuid() != 0 {
		if err := secureDir("/usr", true); err == nil {
			t.Fatal("another user's dir accepted")
		}
	}
}

func TestLockFileRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "run"), 0o700)
	target := filepath.Join(dir, "victim")
	_ = os.WriteFile(target, []byte("keep"), 0o600)
	_ = os.Symlink(target, filepath.Join(dir, "run", "daemon.lock"))
	if f, err := lockDir(dir); err == nil {
		f.Close()
		t.Fatal("lock followed a symlink")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatal("symlink target truncated")
	}
}

// A pane's holder takes only the daemon that started it, before and after
// a daemon restart.
func TestHolderNeedsToken(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "echo ready-marker; exec cat"}},
	}}}))
	d.mu.Lock()
	pid := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pid}}})
	waitOutput(t, c, pid, "ready-marker")
	sock := filepath.Join(dir, "run", "panes", pid+".sock")
	if hc, err := holder.Dial(sock, 0, nil); err == nil {
		hc.Close()
		t.Fatal("attached to a holder without its token")
	}
	c.Close()
	d.Shutdown()
	d2 := startDaemon(t, dir)
	defer d2.Shutdown()
	c2 := dial(t, dir)
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pid}}})
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pid, Data: []byte("after-restart\r")}}})
	waitOutput(t, c2, pid, "after-restart")
	d2.mu.Lock()
	reconnected := d2.panes[pid].pid
	d2.mu.Unlock()
	if reconnected == 0 {
		t.Fatal("not reconnected")
	}
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
}
