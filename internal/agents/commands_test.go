package agents

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestClaudeSlashCommands(t *testing.T) {
	tmp := t.TempDir()
	config := filepath.Join(tmp, "claude")
	project := filepath.Join(tmp, "project")
	elsewhere := filepath.Join(tmp, "elsewhere")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	skill := "---\ndescription: y\n  name: an indented line isn't the skill's name\n---\nBody\n"

	// Your skills: a folder, a link to one (as skill installers make), one
	// named in its front matter, ones hidden from "/" (written in various
	// ways), and a folder that isn't a skill.
	write(filepath.Join(config, "skills", "plain", "SKILL.md"), skill)
	write(filepath.Join(tmp, "personal-skills", "linked", "SKILL.md"), skill)
	link(filepath.Join(tmp, "personal-skills", "linked"), filepath.Join(config, "skills", "linked"))
	write(filepath.Join(config, "skills", "folder-name", "SKILL.md"), "\uFEFF---\r\nname: \"front-name\"\r\n---\r\n")
	write(filepath.Join(config, "skills", "hidden", "SKILL.md"), "---\nname: hidden\nuser-invocable: false\n---\n")
	write(filepath.Join(config, "skills", "hidden2", "SKILL.md"), "\uFEFF---\r\nuser-invocable: False # not in the menu\r\n---\r\n")
	write(filepath.Join(config, "skills", "notes", "README.md"), "not a skill")
	// Your commands, one in a subfolder.
	write(filepath.Join(config, "commands", "mine.md"), "x")
	write(filepath.Join(config, "commands", "git", "sync.md"), "x")
	// The project's.
	write(filepath.Join(project, ".claude", "skills", "proj-skill", "SKILL.md"), skill)
	write(filepath.Join(project, ".claude", "commands", "proj-cmd.md"), "x")

	// Plugins: one installed for you, one by your organisation, one for
	// another project, one for this one, and one turned off.
	plugin := func(name string) string {
		root := filepath.Join(config, "plugins", "cache", "market", name, "1.0.0")
		write(filepath.Join(root, "skills", "do-thing", "SKILL.md"), skill)
		write(filepath.Join(root, "skills", "references", "notes.md"), "not a skill")
		write(filepath.Join(root, "commands", "cmd.md"), "x")
		return root
	}
	write(filepath.Join(config, "plugins", "installed_plugins.json"), `{"version": 2, "plugins": {
		"user-tools@market": [{"scope": "user", "installPath": "`+plugin("user-tools")+`"}],
		"org-tools@market": [{"scope": "managed", "installPath": "`+plugin("org-tools")+`"}],
		"other-proj@market": [{"scope": "project", "projectPath": "`+elsewhere+`", "installPath": "`+plugin("other-proj")+`"}],
		"this-proj@market": [{"scope": "project", "projectPath": "`+project+`", "installPath": "`+plugin("this-proj")+`"}],
		"off@market": [{"scope": "user", "installPath": "`+plugin("off")+`"}],
		"proj-off@market": [{"scope": "user", "installPath": "`+plugin("proj-off")+`"}],
		"mine-here@market": [{"scope": "local", "projectPath": "`+project+`", "installPath": "`+plugin("mine-here")+`"}],
		"mine-there@market": [{"scope": "local", "projectPath": "`+elsewhere+`", "installPath": "`+plugin("mine-there")+`"}]
	}}`)
	// You turned proj-off on; the project turns it off, and its local
	// settings turn this-proj back on after the project turned it off.
	write(filepath.Join(project, ".claude", "settings.json"), `{"enabledPlugins": {"proj-off@market": false, "this-proj@market": false}}`)
	write(filepath.Join(project, ".claude", "settings.local.json"), `{"enabledPlugins": {"this-proj@market": true}}`)
	// The user plugin's manifest names more: a folder of skills, one skill
	// folder, one command file, and a path outside the plugin.
	userRoot := filepath.Join(config, "plugins", "cache", "market", "user-tools", "1.0.0")
	write(filepath.Join(userRoot, ".claude-plugin", "plugin.json"),
		`{"name": "user-tools", "skills": ["./.claude-plugin/skills/", "./one-skill", "../../../../../../personal-skills"], "commands": "./extra/run.md"}`)
	write(filepath.Join(userRoot, ".claude-plugin", "skills", "manifest-skill", "SKILL.md"), skill)
	write(filepath.Join(userRoot, "one-skill", "SKILL.md"), skill)
	write(filepath.Join(userRoot, "extra", "run.md"), "x")
	write(filepath.Join(config, "settings.json"), `{"enabledPlugins": {"off@market": false, "user-tools@market": true, "proj-off@market": true}}`)

	got := claudeSlashCommands(config, project)
	for _, want := range []string{"/clear", "/simplify", "/plain", "/linked", "/mine", "/git:sync", "/proj-skill", "/proj-cmd",
		"/user-tools:do-thing", "/user-tools:cmd", "/org-tools:do-thing", "/this-proj:do-thing",
		"/user-tools:manifest-skill", "/user-tools:one-skill", "/user-tools:run", "/front-name", "/mine-here:do-thing"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	for _, unwanted := range []string{"/hidden", "/notes", "/other-proj:do-thing", "/off:do-thing", "/user-tools:references", "/user-tools:linked",
		"/folder-name", "/hidden2", "/proj-off:do-thing", "/mine-there:do-thing"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("has %s", unwanted)
		}
	}
	if !slices.IsSorted(got) {
		t.Errorf("not sorted: %v", got)
	}
}
