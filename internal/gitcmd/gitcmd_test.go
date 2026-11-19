package gitcmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Sam", "GIT_AUTHOR_EMAIL=sam@example.com",
		"GIT_COMMITTER_NAME=Sam", "GIT_COMMITTER_EMAIL=sam@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// isolate keeps the test from your own git config, giving it global
// config of its own instead.
func isolate(t *testing.T, global string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(p, []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", p)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "")
}

// Only the repository's own filter drivers are emptied: yours (global)
// still run.
func TestRepoFiltersOnlyTheRepository(t *testing.T) {
	isolate(t, "[filter \"mine\"]\n\tclean = mine-clean\n")
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	run(t, dir, "config", "filter.x.clean", "evil clean")
	run(t, dir, "config", "filter.x.smudge", "evil smudge")
	inc := filepath.Join(t.TempDir(), "included")
	if err := os.WriteFile(inc, []byte("[filter \"odd=name.with dots\"]\n\tprocess = evil process\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "config", "include.path", inc)

	got := repoFilters(context.Background(), dir, environ(fixed))
	want := [][2]string{
		{"filter.x.clean", ""}, {"filter.x.required", "false"}, {"filter.x.smudge", ""},
		{"filter.odd=name.with dots.process", ""}, {"filter.odd=name.with dots.required", "false"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("overrides = %q, want %q", got, want)
	}

	// git sees them emptied, the odd name too, and yours as it was.
	for key, value := range map[string]string{"filter.x.clean": "", "filter.odd=name.with dots.process": "", "filter.mine.clean": "mine-clean"} {
		cmd := Command(context.Background(), dir, "diff") // its environment, with the drivers looked up
		cmd.Args = []string{"git", "-C", dir, "config", "--get", key}
		b, _ := cmd.Output()
		if s := strings.TrimSpace(string(b)); s != value {
			t.Errorf("%s = %q, want %q", key, s, value)
		}
	}
}

// Diffing commands get --no-ext-diff and --no-textconv, after the
// subcommand; others don't, and don't look up drivers.
func TestCommandArgs(t *testing.T) {
	isolate(t, "")
	dir := t.TempDir()
	run(t, dir, "init", "-q")
	run(t, dir, "config", "filter.x.clean", "evil")
	diff := Command(context.Background(), dir, "diff", "-z", "HEAD")
	if want := []string{"git", "-C", dir, "--no-pager", "diff", "--no-ext-diff", "--no-textconv", "-z", "HEAD"}; !slices.Equal(diff.Args, want) {
		t.Errorf("diff args = %q", diff.Args)
	}
	if !slices.Contains(diff.Env, "GIT_CONFIG_VALUE_"+strconv.Itoa(len(fixed))+"=") {
		t.Error("diff didn't get the repository's filter emptied")
	}
	rp := Command(context.Background(), dir, "rev-parse", "HEAD")
	if want := []string{"git", "-C", dir, "--no-pager", "rev-parse", "HEAD"}; !slices.Equal(rp.Args, want) {
		t.Errorf("rev-parse args = %q", rp.Args)
	}
	if !slices.Contains(rp.Env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(fixed))) {
		t.Error("rev-parse looked up filter drivers, or lacks the fixed overrides")
	}
}

// Overrides go after any GIT_CONFIG_COUNT the daemon already has.
func TestEnvironKeepsExistingConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "user.name")
	t.Setenv("GIT_CONFIG_VALUE_0", "Sam")
	env := environ([][2]string{{"core.fsmonitor", "false"}})
	for _, want := range []string{"GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_COUNT=2"} {
		if !slices.Contains(env, want) {
			t.Errorf("env lacks %s", want)
		}
	}
	n := 0
	for _, e := range env {
		if strings.HasPrefix(e, "GIT_CONFIG_COUNT=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("GIT_CONFIG_COUNT %d times", n)
	}
}
