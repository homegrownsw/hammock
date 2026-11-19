// Package gitcmd runs git for the daemon, in repositories it doesn't
// trust: a folder an agent, a guest or a program chose. Every git the
// daemon runs goes through Command.
//
// git reads a repository's own config (.git/config, config.worktree and
// whatever they include) and attributes (.gitattributes,
// .git/info/attributes), and some of their keys name commands git runs. An
// agent sandboxed to its repository can write those files, so without this
// git would run its command outside the sandbox, as the daemon. Command
// overrides them from the command line, which beats the repository's
// config:
//
//   - core.fsmonitor=false: the hook status, diff and ls-files would run to
//     ask what changed.
//   - filter.<driver>.clean/smudge/process, for each driver the repository
//     itself (not your global or system config) defines: emptied, with
//     required=false. A clean filter runs whenever git hashes a working-tree
//     file (status and diff do, for files whose times changed). Attributes
//     only pick a driver by name, so they're left alone: drivers from your
//     own config (Git LFS) still run, and linguist-generated still reads.
//     (git 2.42's --attr-source would skip .gitattributes but not
//     .git/info/attributes, and loses linguist-generated.)
//   - --no-ext-diff and --no-textconv on diff, log and show:
//     diff.external, diff.<driver>.command and diff.<driver>.textconv.
//     (Emptying diff.external instead makes git try to run "".)
//   - log.showSignature=false: log would run gpg.program.
//   - core.hooksPath=/dev/null: none of the commands the daemon runs runs a
//     hook, so this is only in case one ever does.
//   - core.pager=cat and --no-pager: a pager only runs on a terminal,
//     which git here never has.
//   - GIT_NO_LAZY_FETCH=1 and protocol.<name>.allow=never: a partial clone
//     would otherwise fetch a missing object, running core.sshCommand,
//     credential helpers, remote.<name>.uploadpack or an ext:: remote.
//     Nothing the daemon runs uses the network otherwise.
//
// Not covered, because the daemon never runs them: commands that write
// (checkout, commit, merge: hooks, smudge, merge drivers), cat-file
// --filters or --textconv, blame and grep's textconv, and the network
// (fetch, push). trace2 and uploadpack.packObjectsHook are only read from
// your global and system config. GIT_OPTIONAL_LOCKS=0 keeps status from
// taking the index lock, so an agent's git add isn't refused meanwhile.
package gitcmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// fixed are the overrides every git gets.
var fixed = [][2]string{
	{"core.fsmonitor", "false"},
	{"core.hooksPath", os.DevNull},
	{"core.pager", "cat"},
	{"core.quotePath", "off"},
	{"color.ui", "false"},
	{"log.showSignature", "false"},
	// protocol.<name>.allow beats protocol.allow, so each is named.
	{"protocol.allow", "never"},
	{"protocol.file.allow", "never"},
	{"protocol.git.allow", "never"},
	{"protocol.ssh.allow", "never"},
	{"protocol.http.allow", "never"},
	{"protocol.https.allow", "never"},
	{"protocol.ext.allow", "never"},
}

// noFilters are subcommands (as the daemon runs them) that never run a
// filter driver: they don't read the working tree's files. Any other is
// given the repository's drivers emptied, which takes a git config first.
var noFilters = map[string]bool{
	"rev-parse": true, "symbolic-ref": true, "merge-base": true, "cat-file": true,
	"for-each-ref": true, "ls-files": true, "check-attr": true, "log": true, "config": true,
}

// diffing are subcommands that take --no-ext-diff and --no-textconv.
var diffing = map[string]bool{"diff": true, "log": true, "show": true}

// Command is git with args (a subcommand first) in dir, made safe to run
// in a repository whose config and attributes someone else wrote.
func Command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	env := environ(fixed)
	if len(args) > 0 && !noFilters[args[0]] {
		if drivers := repoFilters(ctx, dir, env); len(drivers) > 0 {
			env = environ(append(append([][2]string{}, fixed...), drivers...))
		}
	}
	full := []string{"-C", dir, "--no-pager"}
	if len(args) > 0 {
		full = append(full, args[0])
		if diffing[args[0]] {
			full = append(full, "--no-ext-diff", "--no-textconv")
		}
		full = append(full, args[1:]...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = env
	return cmd
}

// environ is the daemon's environment with overrides added to git's config
// (after any GIT_CONFIG_COUNT it already has), and git kept to itself.
func environ(overrides [][2]string) []string {
	base := os.Environ()
	n := 0
	if c, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT")); err == nil && c > 0 {
		n = c
	}
	env := make([]string, 0, len(base)+2*len(overrides)+6)
	for _, e := range base {
		if !strings.HasPrefix(e, "GIT_CONFIG_COUNT=") {
			env = append(env, e)
		}
	}
	for _, o := range overrides {
		i := strconv.Itoa(n)
		env = append(env, "GIT_CONFIG_KEY_"+i+"="+o[0], "GIT_CONFIG_VALUE_"+i+"="+o[1])
		n++
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n),
		"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "LC_ALL=C")
}

// repoFilters are overrides emptying each filter driver command the
// repository's own config defines (its local and worktree config, and
// what they include), so git runs none of them.
func repoFilters(ctx context.Context, dir string, env []string) [][2]string {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "config", "--show-scope", "--includes", "-z", "--name-only",
		"--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	cmd.Env = env
	out, _ := cmd.Output() // exits 1 when there's none
	var o [][2]string
	required := map[string]bool{}
	parts := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	for i := 0; i+1 < len(parts); i += 2 {
		switch string(parts[i]) {
		case "system", "global", "command":
			continue // yours, or ours
		}
		key := string(parts[i+1])
		o = append(o, [2]string{key, ""})
		driver := key[:strings.LastIndexByte(key, '.')]
		if !required[driver] {
			required[driver] = true
			o = append(o, [2]string{driver + ".required", "false"})
		}
	}
	return o
}

// Output runs git with args in dir (as Command), its standard output.
func Output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return Command(ctx, dir, args...).Output()
}
