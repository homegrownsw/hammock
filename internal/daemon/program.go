package daemon

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/procinfo"
)

// The program in a terminal's foreground, for its icon: the leader
// of the terminal's foreground process group, so a job sent to the
// background doesn't count, and a program's own children (an agent's
// shells, a build's compilers) don't either. Clients map its names to
// icons, taking the first they know.

// interpreterRE matches interpreters by their process names, versions and
// all (python3.12, the framework build's "Python").
var interpreterRE = regexp.MustCompile(`^(python|pypy|ruby|node|perl|php|lua)[0-9.]*$`)

// launchers run another program named in their arguments: interpreters,
// package runners and the tools that run scripts. What they run is looked
// for in their arguments, and they're kept as the fallback.
var launchers = map[string]bool{
	"python": true, "pypy": true, "ruby": true, "node": true, "perl": true, "php": true, "lua": true,
	"npx": true, "pnpx": true, "bunx": true, "uvx": true, "npm": true, "pnpm": true, "yarn": true,
	// Shells running a script (brew is bash running brew.sh until its Ruby
	// takes over); a shell with no script stands for itself.
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"bun": true, "deno": true, "uv": true, "corepack": true, "bundle": true, "poetry": true, "pipx": true,
	"pipenv": true, "hatch": true, "pdm": true, "rye": true, "pixi": true, "conda": true, "mamba": true,
	// Commands that run a command.
	"caffeinate": true, "nice": true, "nohup": true, "time": true, "env": true,
	"sudo": true, "doas": true, "timeout": true, "gtimeout": true, "watch": true, "viddy": true, "hwatch": true,
	"entr": true, "watchexec": true, "op": true, "doppler": true, "dotenvx": true, "mise": true, "devbox": true,
}

// launcherFlagsWithValue are flags that take a value to one launcher only
// (sudo -u alex vim; python -u is unbuffered).
var launcherFlagsWithValue = map[string]map[string]bool{
	"sudo":    {"-u": true, "-g": true},
	"doas":    {"-u": true},
	"timeout": {"-s": true, "-k": true}, "gtimeout": {"-s": true, "-k": true},
	"watchexec": {"-e": true, "-w": true, "--exts": true, "--watch": true},
	"bash":      {"-o": true, "-O": true, "+o": true, "+O": true, "--rcfile": true, "--init-file": true},
	"sh":        {"-o": true, "+o": true}, "zsh": {"-o": true, "+o": true}, "dash": {"-o": true, "+o": true}, "ksh": {"-o": true, "+o": true},
}

// durationRE matches a number or duration argument (timeout 10s make).
var durationRE = regexp.MustCompile(`^[0-9.]+[smhd]?$`)

// interpreters take code inline (python -c, node -e) as well as scripts.
var interpreters = map[string]bool{"python": true, "pypy": true, "ruby": true, "node": true, "perl": true, "php": true, "lua": true}

// packageManagers run a package (exec, dlx, x) or a file, but their other
// words are their own commands or package.json's scripts.
var packageManagers = map[string]bool{"npm": true, "pnpm": true, "yarn": true, "bun": true}

// launcherVerbs are a launcher's subcommands that run what follows them
// (uv run pytest, uv tool run, bundle exec rspec, deno run main.ts).
var launcherVerbs = map[string]bool{"run": true, "exec": true, "tool": true, "--": true}

// flagsWithValue are flags whose value isn't the program (node -r
// ts-node/register app.js, uv run -p 3.12 app.py; deno run
// --allow-read=. main.ts is one word).
var flagsWithValue = map[string]bool{"-r": true, "--require": true, "--import": true, "--loader": true, "-W": true, "-X": true, "--cwd": true, "-C": true, "--filter": true, "-F": true, "--with": true, "-p": true, "--python": true, "--from": true, "-n": true}

// inlineCode flags, to an interpreter, mean the program is the code itself.
var inlineCode = map[string]bool{"-c": true, "-e": true, "--eval": true, "-p": true, "--print": true}

// packageEntries are script names that say nothing: the package around
// them does (node_modules/@anthropic-ai/claude-code/cli.js).
var packageEntries = map[string]bool{"cli": true, "index": true, "main": true, "bin": true, "run": true}

// programAliases are scripts' names for programs that go by another.
var programAliases = map[string]string{"npm-cli": "npm", "npx-cli": "npx", "claude-code": "claude", "codex-cli": "codex", "gemini-cli": "gemini", "py.test": "pytest", "__main__": ""}

// refreshProgram records the program in the terminal's foreground.
// Reports whether it changed.
func (p *pane) refreshProgram() bool {
	if !p.isTerminal() {
		return false
	}
	p.mu.Lock()
	pid, exited, asleep := p.pid, p.info.Exited, p.info.Asleep
	p.mu.Unlock()
	if asleep {
		return false // what it ran, as it was (sleep.go)
	}
	var names []string
	if !exited && pid != 0 {
		names = foregroundProgram(pid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if slices.Equal(names, p.info.Program) {
		return false
	}
	p.info.Program = names
	return true
}

// foregroundProgram names the program in front in the terminal whose
// first process (its shell, or the pane's own command) is pid.
func foregroundProgram(pid int) []string {
	pgid, tpgid, err := procinfo.Groups(pid)
	if err != nil {
		return nil
	}
	if tpgid == 0 || tpgid == pgid {
		return describeProcess(pid)
	}
	// The group's leader, unless it has gone (the first of a pipeline
	// ended): then any process of the group under pid.
	if _, err := procinfo.Name(tpgid); err == nil {
		return describeProcess(tpgid)
	}
	frontier := []int{pid}
	for depth := 0; depth < 4 && len(frontier) > 0; depth++ {
		var next []int
		for _, q := range frontier {
			for _, c := range procinfo.Children(q) {
				if g, _, err := procinfo.Groups(c); err == nil && g == tpgid {
					return describeProcess(c)
				}
				next = append(next, c)
			}
		}
		frontier = next
	}
	return describeProcess(pid)
}

// describeProcess names what pid runs, most specific first.
func describeProcess(pid int) []string {
	if h := agentName(pid); h != "" {
		return []string{h}
	}
	name, err := procinfo.Name(pid)
	if err != nil {
		return nil
	}
	return programNames(name, procinfo.Args(pid))
}

// programNames names a process from its name and arguments: what a
// launcher runs, then the launchers, most specific first.
func programNames(name string, args []string) []string {
	base := normalizeProgram(name)
	if base == "" {
		return nil
	}
	if !launchers[base] {
		return []string{base}
	}
	// A process that set its title (npm's "npm run dev") has one argument.
	if len(args) == 1 && strings.Contains(args[0], " ") {
		args = append([]string{base}, strings.Fields(args[0])...)
	}
	var found string
	var chain []string // launchers met in the arguments, in order
	cur := base        // the launcher whose arguments these are
	through := false   // a package manager was told to run what follows
loop:
	for i := 1; i < len(args) && i < 12; i++ {
		a := args[i]
		// A shell (shells, in pane.go) runs a script, or code after -c; its
		// other flags (bash -p, zsh -l) take no value but those listed in
		// launcherFlagsWithValue.
		if interpreters[cur] && inlineCode[a] || shells[cur] && a == "-c" {
			break
		}
		if a == "-m" && cur == "python" && i+1 < len(args) {
			found = normalizeProgram(args[i+1])
			break
		}
		if (strings.HasPrefix(a, "-") || shells[cur] && strings.HasPrefix(a, "+")) && a != "--" {
			if !shells[cur] && flagsWithValue[a] || launcherFlagsWithValue[cur][a] {
				i++
			}
			continue
		}
		if durationRE.MatchString(a) {
			continue
		}
		if strings.Contains(a, "=") && !strings.Contains(a, "/") {
			continue // env's VAR=value
		}
		// A package manager runs a package (exec, dlx, x) or a file; its
		// own commands and package.json's scripts (npm test, yarn dev)
		// aren't programs, so it stands for itself.
		if packageManagers[cur] && !through {
			switch {
			case a == "exec" || a == "dlx" || a == "x" || a == "--":
				through = true
				continue
			case a == "run" && i+1 < len(args) && looksLikeFile(args[i+1]):
				through = true
				continue
			case cur == "bun" && looksLikeFile(a):
			default:
				break loop
			}
		} else if launcherVerbs[a] {
			continue
		}
		n := scriptName(a)
		if n == "" {
			break
		}
		if launchers[n] {
			chain = append(chain, n)
			cur, through = n, false
			continue
		}
		found = n
		break
	}
	var out []string
	if found != "" {
		out = append(out, found)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		if !slices.Contains(out, chain[i]) {
			out = append(out, chain[i])
		}
	}
	if !slices.Contains(out, base) {
		out = append(out, base)
	}
	return out
}

// looksLikeFile: an argument that names a file (a path, or a name with an
// extension), not a package.json script.
func looksLikeFile(a string) bool {
	return strings.Contains(a, "/") || strings.Contains(strings.TrimPrefix(filepath.Base(a), "."), ".")
}

// normalizeProgram lowercases a process name, drops a login shell's dash
// and an interpreter's version.
func normalizeProgram(name string) string {
	n := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "-"))
	if m := interpreterRE.FindStringSubmatch(n); m != nil {
		return m[1]
	}
	return n
}

// scriptName is the program a launcher's argument names: a script's name
// without its extension, a package's name for its generic entry point,
// "" for nothing useful (".", a bare path).
func scriptName(arg string) string {
	clean := filepath.ToSlash(filepath.Clean(arg))
	n := strings.ToLower(filepath.Base(clean))
	for _, ext := range []string{".js", ".mjs", ".cjs", ".ts", ".mts", ".py", ".rb", ".pl", ".php", ".lua", ".exe", ".sh", ".bash", ".zsh", ".fish"} {
		n = strings.TrimSuffix(n, ext)
	}
	// A package's entry point: the package (scope dropped) says more.
	if packageEntries[n] {
		parts := strings.Split(clean, "/")
		for i := len(parts) - 2; i >= 0; i-- {
			if parts[i] == "node_modules" && i+1 < len(parts) {
				pkg := parts[i+1]
				if strings.HasPrefix(pkg, "@") && i+2 < len(parts) {
					pkg = parts[i+2]
				}
				n = strings.ToLower(pkg)
				break
			}
		}
	}
	if a, ok := programAliases[n]; ok {
		n = a
	}
	if n == "" || n == "." || n == "/" || n == ".." {
		return ""
	}
	n = normalizeProgram(n)
	if a := agents.ByProcess(n); a != nil {
		return a.ID
	}
	return n
}
