package agents

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// codexBuiltins are Codex's own commands (0.166's), which only run at the
// start of a message.
var codexBuiltins = slashed("agents", "app", "approve", "archive", "cd", "compact", "config", "copy", "daemon", "delete", "diff",
	"experimental", "export", "fork", "goal", "hooks", "ide", "import", "init", "keymap", "mcp", "memories", "mention", "model", "new",
	"permissions", "plan", "plugins", "presets", "pwd", "raw", "recap", "rename", "resume", "review", "side", "skills", "status",
	"statusline", "theme", "title", "usage", "vim", "voice", "warnings", "worktree")

// claudeBuiltins are Claude Code's own commands, which only run at the start
// of a message: elsewhere in one they're sent as text. (Its skills and
// commands, bundled or yours, run anywhere in one.)
var claudeBuiltins = slashed("add-dir", "agents", "bashes", "clear", "compact", "config", "context", "cost", "doctor", "effort",
	"exit", "export", "fast", "feedback", "help", "hooks", "ide", "init", "keybindings", "login", "logout", "mcp", "memory",
	"model", "output-style", "permissions", "plan", "plugin", "pr-comments", "release-notes", "resume", "review", "rewind",
	"security-review", "skills", "status", "statusline", "tasks", "terminal-setup", "theme", "todos", "upgrade", "usage", "vim")

// claudeBundledSkills are the skills that come with Claude Code.
var claudeBundledSkills = slashed("claude-api", "code-review", "fewer-permission-prompts", "loop", "schedule", "simplify",
	"update-config")

// slashed is names with a "/" before each.
func slashed(names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "/" + n
	}
	return out
}

// codexSlashCommands lists what can follow "/" in Codex (0.166's built-ins)
// and your saved prompts (~/.codex/prompts, run as /prompts:NAME).
func codexSlashCommands() []string {
	out := slices.Clone(codexBuiltins)
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	if ents, err := os.ReadDir(filepath.Join(home, "prompts")); err == nil {
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				out = append(out, "/prompts:"+strings.TrimSuffix(e.Name(), ".md"))
			}
		}
	}
	sort.Strings(out)
	return out
}

// slashCommands lists what can follow "/" in Claude Code: its built-ins
// and bundled skills, your commands and skills, the project's, and those of
// the plugins you have installed (as /plugin:name).
func slashCommands(cwd string) []string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	return claudeSlashCommands(dir, cwd)
}

// claudeSlashCommands is slashCommands with Claude's config directory
// (~/.claude) given.
func claudeSlashCommands(config, cwd string) []string {
	set := map[string]bool{}
	for _, c := range slices.Concat(claudeBuiltins, claudeBundledSkills) {
		set[strings.TrimPrefix(c, "/")] = true
	}
	add := func(root, prefix string) {
		for _, c := range commandsIn(filepath.Join(root, "commands")) {
			set[prefix+c] = true
		}
		for _, s := range skillsIn(filepath.Join(root, "skills")) {
			set[prefix+s] = true
		}
	}
	add(config, "")
	add(filepath.Join(cwd, ".claude"), "")
	for name, root := range claudePlugins(config, cwd) {
		add(root, name+":")
		for _, c := range pluginExtras(root) {
			set[name+":"+c] = true
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, "/"+c)
	}
	sort.Strings(out)
	return out
}

// commandsIn lists the commands in a commands folder: each .md file, those
// in subfolders as folder:name.
func commandsIn(dir string) []string {
	// The folder itself may be a link (into a dotfiles repo, say).
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	var out []string
	_ = filepath.WalkDir(dir, func(path string, de os.DirEntry, err error) error {
		if err == nil && !de.IsDir() && strings.HasSuffix(path, ".md") {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, strings.ReplaceAll(strings.TrimSuffix(rel, ".md"), string(filepath.Separator), ":"))
		}
		return nil
	})
	return out
}

// skillsIn lists the skills in a skills folder you can run with "/": each
// folder (or link to one, as skill installers make) with a SKILL.md, unless
// its front matter says user-invocable: false.
func skillsIn(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if s, ok := skillAt(filepath.Join(dir, e.Name())); ok {
			out = append(out, s)
		}
	}
	return out
}

// skillAt is the name you run the skill in a folder by, if it is one you
// can: its front matter's name, else the folder's.
func skillAt(dir string) (string, bool) {
	// os.ReadFile follows links, so a linked skill counts as a folder one.
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return "", false
	}
	name, invocable := skillFront(b)
	if name == "" {
		name = filepath.Base(dir)
	}
	return name, invocable
}

// skillFront reads a SKILL.md's front matter: its name, and whether it's
// not hidden from "/".
func skillFront(skill []byte) (name string, invocable bool) {
	s := strings.TrimPrefix(strings.ReplaceAll(string(skill), "\r\n", "\n"), "\uFEFF")
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return "", true
	}
	front, _, _ := strings.Cut(rest, "\n---")
	invocable = true
	for line := range strings.SplitSeq(front, "\n") {
		k, v, ok := strings.Cut(line, ":")
		// Top-level keys only (a description can run over indented lines).
		if !ok || strings.HasPrefix(k, " ") || strings.HasPrefix(k, "\t") {
			continue
		}
		v, _, _ = strings.Cut(v, " #")
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			// A name you could type: no spaces.
			if v != "" && !strings.ContainsAny(v, " \t") {
				name = v
			}
		case "user-invocable":
			if strings.EqualFold(v, "false") {
				invocable = false
			}
		}
	}
	return name, invocable
}

// claudePlugins maps each installed plugin that's on for cwd to where it's
// installed: those installed for you (or by your organisation), or for this
// project, and not turned off in your settings or the project's.
func claudePlugins(config, cwd string) map[string]string {
	b, err := os.ReadFile(filepath.Join(config, "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(b, &installed) != nil {
		return nil
	}
	enabled := map[string]bool{}
	for _, f := range []string{filepath.Join(config, "settings.json"), filepath.Join(cwd, ".claude", "settings.json"),
		filepath.Join(cwd, ".claude", "settings.local.json")} {
		var s struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &s) == nil {
			maps.Copy(enabled, s.EnabledPlugins)
		}
	}
	out := map[string]string{}
	// In order, so that of two plugins of one name (from two marketplaces)
	// it's always the same one.
	for _, id := range slices.Sorted(maps.Keys(installed.Plugins)) {
		if on, set := enabled[id]; set && !on {
			continue
		}
		name, _, _ := strings.Cut(id, "@")
		if _, dup := out[name]; dup {
			continue
		}
		for _, in := range installed.Plugins[id] {
			if in.InstallPath == "" || (in.Scope == "project" || in.Scope == "local") && !within(cwd, in.ProjectPath) {
				continue
			}
			out[name] = in.InstallPath
			break
		}
	}
	return out
}

// pluginExtras lists a plugin's commands and skills in the places its
// manifest (.claude-plugin/plugin.json) names besides commands/ and skills/:
// each a folder of them, or one command file or skill folder.
func pluginExtras(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, ".claude-plugin", "plugin.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Commands, Skills json.RawMessage
	}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	// A path or a list of them, relative to the plugin.
	paths := func(raw json.RawMessage) []string {
		var one string
		var many []string
		if json.Unmarshal(raw, &one) == nil {
			many = []string{one}
		} else if json.Unmarshal(raw, &many) != nil {
			return nil
		}
		var out []string
		for _, p := range many {
			if p = filepath.Join(root, p); within(p, root) {
				out = append(out, p)
			}
		}
		return out
	}
	var out []string
	for _, p := range paths(m.Commands) {
		if strings.HasSuffix(p, ".md") {
			out = append(out, strings.TrimSuffix(filepath.Base(p), ".md"))
		} else {
			out = append(out, commandsIn(p)...)
		}
	}
	for _, p := range paths(m.Skills) {
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err == nil {
			if s, ok := skillAt(p); ok {
				out = append(out, s)
			}
		} else {
			out = append(out, skillsIn(p)...)
		}
	}
	return out
}

// within: path is dir or inside it.
func within(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
