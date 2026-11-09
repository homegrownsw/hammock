package procinfo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSelf(t *testing.T) {
	wd, _ := os.Getwd()
	got, err := Cwd(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := filepath.EvalSymlinks(wd)
	b, _ := filepath.EvalSymlinks(got)
	if a != b {
		t.Fatalf("cwd %q want %q", got, wd)
	}
	if n, err := Name(os.Getpid()); err != nil || n == "" {
		t.Fatalf("name %q %v", n, err)
	}
}

func TestChildren(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	found := false
	for _, c := range Children(os.Getpid()) {
		if c == cmd.Process.Pid {
			found = true
		}
	}
	if !found {
		t.Fatalf("child %d not in %v", cmd.Process.Pid, Children(os.Getpid()))
	}
	if n, _ := Name(cmd.Process.Pid); n != "sleep" {
		t.Fatalf("name %q", n)
	}
}

func TestParentAndEnv(t *testing.T) {
	if p, err := Parent(os.Getpid()); err != nil || p != os.Getppid() {
		t.Fatalf("parent %d, %v; want %d", p, err, os.Getppid())
	}
	// This test binary, not /bin/sleep: macOS hides platform binaries' environments.
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperSleep")
	cmd.Env = []string{"HMK_TEST_MARK=yes", "OTHER=1"}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	time.Sleep(200 * time.Millisecond) // exec'd
	if p, _ := Parent(cmd.Process.Pid); p != os.Getpid() {
		t.Fatalf("child's parent %d", p)
	}
	found := false
	for _, kv := range Env(cmd.Process.Pid) {
		found = found || kv == "HMK_TEST_MARK=yes"
	}
	if !found {
		t.Fatalf("env %q", Env(cmd.Process.Pid))
	}
	if a := Args(cmd.Process.Pid); len(a) != 2 || a[1] != "-test.run=TestHelperSleep" {
		t.Fatalf("args %q", a)
	}
}

func TestHelperSleep(t *testing.T) {
	if os.Getenv("HMK_TEST_MARK") == "yes" {
		time.Sleep(5 * time.Second)
	}
}

func TestStartTime(t *testing.T) {
	st, err := StartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(st); d < 0 || d > 10*time.Minute {
		t.Fatalf("this process started %v ago", d)
	}
}
