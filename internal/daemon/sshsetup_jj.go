package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// jj (Jujutsu) signs commits with its own signing.backends.ssh.program
// (1Password's op-ssh-sign, say), not git's gpg.ssh.program, so the
// GIT_CONFIG_* panes get doesn't reach it, and it talks to 1Password
// directly: the Hammock Key never comes into it. jj has no environment
// variable that adds config, so SshSetup offers to add a file to jj's
// conf.d that, while HMK_PANE_ID is set (in Hammock's panes only: jj 0.39's
// --when.environments), points it at hmk-jj-ssh-sign. That signs as
// hmk-ssh-sign does, with jj's own program (asked of jj outside the scope)
// at your desk and the Hammock Key while you're away. Delete the file to
// undo.

// jjMinVersion is the first jj with --when.environments. Older ones ignore
// conditions they don't know, so the file would apply everywhere.
var jjMinVersion = [2]int{0, 39}

// jjInstallDirs are where jj is usually installed, besides the daemon's PATH
// (launchd's, without Homebrew's).
var jjInstallDirs = []string{
	"/opt/homebrew/bin", "/usr/local/bin", "~/.cargo/bin", "~/.local/bin",
	"~/.nix-profile/bin", "/run/current-system/sw/bin", "/nix/var/nix/profiles/default/bin",
	"~/.local/share/mise/shims", "~/.asdf/shims",
}

// findJJ is jj's path, "" if it isn't installed (replaceable in tests).
var findJJ = func() string {
	if p, err := exec.LookPath("jj"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, d := range jjInstallDirs {
		if rest, ok := strings.CutPrefix(d, "~/"); ok {
			if home == "" {
				continue
			}
			d = filepath.Join(home, rest)
		}
		p := filepath.Join(d, "jj")
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// runJJ runs jj with args (from home, outside any repo and any pane), its
// trimmed output (replaceable in tests).
var runJJ = func(jj string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, jj, args...)
	c.Dir, _ = os.UserHomeDir()
	c.Env = withoutEnv(os.Environ(), "HMK_PANE_ID")
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// withoutEnv is env without the variables named.
func withoutEnv(env []string, names ...string) []string {
	var out []string
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		if !slices.Contains(names, k) {
			out = append(out, e)
		}
	}
	return out
}

var jjVersionRE = regexp.MustCompile(`(\d+)\.(\d+)`)

// jjVersionAtLeast: "jj 0.47.1-abc…" is min or later.
func jjVersionAtLeast(out string, min [2]int) bool {
	m := jjVersionRE.FindStringSubmatch(out)
	if m == nil {
		return false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > min[0] || major == min[0] && minor >= min[1]
}

// jjConfigFile is where Hammock's file goes: the last of jj's user config
// (conf.d is read after config.toml, in name order), so it wins over yours,
// and never the first, which jj config set --user writes to.
func jjConfigFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "jj", "conf.d", "zz-hammock.toml")
}

// jjFile is what apply_jj writes, pointing jj at link.
func jjFile(link, own string) string {
	ownName := "your own signing program"
	if own != "" {
		ownName = filepath.Base(own)
	}
	quoted := "'" + link + "'" // a TOML literal string: no escapes
	if strings.Contains(link, "'") {
		quoted = strconv.Quote(link)
	}
	return "# Added by Hammock: in Hammock's panes, jj signs through hmk-jj-ssh-sign, which\n" +
		"# uses " + ownName + " while you're at this Mac, and while you're away the\n" +
		"# key of the computer you're at, or your Hammock Key. Delete this file to undo.\n" +
		"--when.environments = [\"HMK_PANE_ID\"]\n\n" +
		"[signing.backends.ssh]\n" +
		"program = " + quoted + "\n"
}

// jjSetup fills in r's jj fields; with apply, writes Hammock's file. Off d.mu.
func (d *Daemon) jjSetup(r *pb.SshSetupReply, apply bool) error {
	jj := findJJ()
	if jj == "" {
		return nil
	}
	if backend, _ := runJJ(jj, "--ignore-working-copy", "config", "get", "signing.backend"); backend != "ssh" {
		return nil // jj doesn't sign, or not with SSH: nothing to do
	}
	link := d.jjSignLink()
	r.JjPath = jj
	r.JjConfigPath = jjConfigFile()
	if out, err := runJJ(jj, "--version"); err != nil || !jjVersionAtLeast(out, jjMinVersion) {
		r.JjProblem = fmt.Sprintf("Hammock needs jj %d.%d or later to sign through the Hammock Key (this is %s).", jjMinVersion[0], jjMinVersion[1], cmpOr(out, "older"))
		return nil
	}
	own, _ := runJJ(jj, "--ignore-working-copy", "config", "get", "signing.backends.ssh.program")
	if strings.HasPrefix(filepath.Base(own), "hmk-") {
		own = "" // (Hammock's, read in a pane)
	}
	r.JjProgram = own
	r.JjChange = jjFile(link, own)
	if b, err := os.ReadFile(r.JjConfigPath); err == nil && strings.Contains(string(b), link) {
		r.JjRouted = true
	}
	if !apply || r.JjRouted {
		return nil
	}
	path := r.JjConfigPath
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".hammock-tmp"
	if err := os.WriteFile(tmp, []byte(r.JjChange), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	r.Applied, r.JjRouted = true, true
	d.mu.Lock()
	d.audit("user", "ssh-setup", "", "pointed jj's signing in Hammock's panes at hmk-jj-ssh-sign ("+path+")")
	d.mu.Unlock()
	return nil
}

// jjSignLink is the hmk-jj-ssh-sign jj uses in Hammock's panes: a link to
// the hammock CLI, which acts on its name.
func (d *Daemon) jjSignLink() string {
	return filepath.Join(d.cfg.Dir, "run", "hmk-jj-ssh-sign")
}
