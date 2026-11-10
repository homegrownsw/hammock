package daemon

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// How your own SSH setup reaches Hammock's agent.
//
// Hammock's agent passes what it doesn't hold itself on to an upstream agent:
// ssh-upstream-agent in ~/.config/hammock/config if you set it, else the one
// ~/.ssh/config names for every host (IdentityAgent under Host *: how
// 1Password, Secretive and yubikey-agent are usually set up), else the
// daemon's own SSH_AUTH_SOCK (macOS's ssh-agent).
//
// But IdentityAgent beats the SSH_AUTH_SOCK panes are given, so ssh in a
// pane would skip Hammock's agent: SshSetup offers to add a block to
// ~/.ssh/config that, inside Hammock's panes only (HMK_PANE_ID is set),
// points ssh at SSH_AUTH_SOCK, which is Hammock's. Everywhere else ssh goes
// on using your agent as before. Git's commit signing goes through
// hmk-ssh-sign (cmd/hmk), given to panes with GIT_CONFIG_*; jj's through
// hmk-jj-ssh-sign, once you let Hammock add a file to jj's config
// (sshsetup_jj.go).

// knownSSHAgents name agents by their socket, and say whether each asks you
// before it signs (then, while you're away from this Mac, its keys aren't
// offered: nobody's here to approve, and ssh would wait on them).
var knownSSHAgents = []struct {
	match, name string
	asks        bool
}{
	{"com.1password", "1Password", true},
	{".1password", "1Password", true},
	{"Secretive", "Secretive", true},
	{"yubikey-agent", "yubikey-agent", true},
	{"com.apple.launchd", "ssh-agent", false},
}

// agentName names the agent at socket, and whether it asks you.
func sshAgentName(socket string) (name string, asks bool) {
	if socket == "" {
		return "", false
	}
	for _, a := range knownSSHAgents {
		if strings.Contains(socket, a.match) {
			return a.name, a.asks
		}
	}
	return "an SSH agent", false
}

// sshConfigPath is ~/.ssh/config (replaceable in tests).
var sshConfigPath = func() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", "config")
}

const sshBlockMark = "# Added by Hammock"

// sshConfigScan is what ~/.ssh/config says that matters here.
type sshConfigScan struct {
	agent  string // IdentityAgent for every host (expanded), "" if none
	routed bool   // Hammock's block is there
}

var sshScanCache struct {
	sync.Mutex
	mod  time.Time
	size int64
	scan sshConfigScan
}

// scanSSHConfig reads ~/.ssh/config (again only when it changed).
func scanSSHConfig() sshConfigScan {
	path := sshConfigPath()
	fi, err := os.Stat(path)
	if err != nil {
		return sshConfigScan{}
	}
	c := &sshScanCache
	c.Lock()
	defer c.Unlock()
	if fi.ModTime().Equal(c.mod) && fi.Size() == c.size {
		return c.scan
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return sshConfigScan{}
	}
	c.mod, c.size, c.scan = fi.ModTime(), fi.Size(), parseSSHConfig(string(b))
	return c.scan
}

// parseSSHConfig finds the IdentityAgent that applies to every host
// (before any Host or Match block, or under Host * or Match all), and
// whether Hammock's block comes before it (ssh takes the first value it finds:
// a block after it routes nothing).
func parseSSHConfig(text string) sshConfigScan {
	var s sshConfigScan
	everyHost := true
	agentFirst := false
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, sshBlockMark) && !agentFirst {
			s.routed = true
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitSSHConfigLine(line)
		switch strings.ToLower(key) {
		case "host":
			everyHost = val == "*"
		case "match":
			everyHost = strings.EqualFold(val, "all")
		case "identityagent":
			if everyHost && s.agent == "" && !strings.EqualFold(val, "SSH_AUTH_SOCK") && !strings.EqualFold(val, "none") {
				s.agent = expandHome(val)
				agentFirst = !s.routed
			}
		}
	}
	return s
}

// splitSSHConfigLine splits "Key value", "Key=value" or `Key "value"`.
func splitSSHConfigLine(line string) (string, string) {
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, ""
	}
	key := line[:i]
	val := strings.TrimLeft(line[i:], " \t=")
	val = strings.Trim(strings.TrimSpace(val), `"`)
	return key, val
}

// upstream is the agent Hammock's passes requests on to (see the top).
func (h *sshHub) upstream() string {
	if v, ok := hammockconfig.Value("ssh-upstream-agent"); ok && strings.TrimSpace(v) != "" {
		return expandHome(strings.Trim(strings.TrimSpace(v), `"`))
	}
	if a := scanSSHConfig().agent; a != "" {
		if self := SSHAgentSocketPath(h.d.cfg.Dir); filepath.Clean(a) != filepath.Clean(self) {
			return a
		}
	}
	return h.own
}

// sshBlock is what SshSetup adds to ~/.ssh/config.
func sshBlock(agent string) string {
	via := "your SSH agent"
	if name, _ := sshAgentName(agent); name != "" && name != "an SSH agent" {
		via = name
	}
	return sshBlockMark + ": in Hammock's panes, ssh uses Hammock's agent, which passes\n" +
		"# requests on to " + via + " and adds your Hammock Key. Delete these lines to undo.\n" +
		"Match exec \"test -n \\\"$HMK_PANE_ID\\\"\"\n" +
		"  IdentityAgent SSH_AUTH_SOCK\n" +
		"Match all\n\n"
}

// insertSSHBlock puts block at the very top, before anything it should
// win over (ssh takes the first value it finds, and an IdentityAgent may
// come before any Host line, or in an Include). The block ends with Match
// all, so what followed it applies to every host as it did.
func insertSSHBlock(text, block string) string { return block + text }

// sshSetup reports how your SSH setup reaches Hammock's agent, and with
// apply adds Hammock's block to ~/.ssh/config (keeping the old file beside
// it). Off d.mu.
func (d *Daemon) sshSetup(m *pb.SshSetup) (*pb.SshSetupReply, error) {
	h := d.sshHub
	path := sshConfigPath()
	scan := scanSSHConfig()
	up := h.upstream()
	name, _ := sshAgentName(up)
	r := &pb.SshSetupReply{
		AgentName: name, AgentSocket: up, Routed: scan.routed || scan.agent == "",
		ConfigPath: path, Change: sshBlock(up), GitProgram: gitSSHProgram(),
	}
	if err := d.jjSetup(r, m.ApplyJj); err != nil {
		return nil, err
	}
	if !m.Apply || r.Routed {
		return r, nil
	}
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// Through a link (dotfiles kept with stow or chezmoi): write the file
	// it points at, keeping the link.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
		r.BackupPath = path + ".hammock-backup"
		if err := os.WriteFile(r.BackupPath, old, mode); err != nil {
			return nil, err
		}
	}
	tmp := path + ".hammock-tmp"
	if err := os.WriteFile(tmp, []byte(insertSSHBlock(string(old), r.Change)), mode); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	r.Applied, r.Routed = true, true
	d.mu.Lock()
	d.audit("user", "ssh-setup", "", "pointed ssh in Hammock's panes at Hammock's agent ("+path+")")
	d.mu.Unlock()
	return r, nil
}

// gitSSHProgram is your own gpg.ssh.program (git's global config).
func gitSSHProgram() string {
	out, err := exec.Command("git", "config", "--global", "--get", "gpg.ssh.program").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sshSignLink is the hmk-ssh-sign that panes' git uses for
// gpg.ssh.program: a link to the hammock CLI, which acts on its name.
func (d *Daemon) sshSignLink() string {
	return filepath.Join(d.cfg.Dir, "run", "hmk-ssh-sign")
}

// makeSSHSignLink points it, and jj's (jjSignLink), at the CLI bundled
// beside hmkd (the app's Contents/Helpers/hmk, or bin/hmk). "" if there's
// no CLI to point at.
func (d *Daemon) makeSSHSignLink() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	var cli string
	for _, p := range []string{filepath.Join(filepath.Dir(exe), "..", "Helpers", "hmk"), filepath.Join(filepath.Dir(exe), "hmk")} {
		if fileExists(p) {
			cli = filepath.Clean(p)
			break
		}
	}
	if cli == "" {
		return ""
	}
	if !linkTo(d.sshSignLink(), cli) {
		return ""
	}
	linkTo(d.jjSignLink(), cli)
	return d.sshSignLink()
}

// linkTo makes link a symlink to target, if it isn't already.
func linkTo(link, target string) bool {
	if cur, err := os.Readlink(link); err == nil && cur == target {
		return true
	}
	_ = os.Remove(link)
	return os.Symlink(target, link) == nil
}
