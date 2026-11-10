package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
)

// hooksPlan is what `hmk hooks install --dry-run --json` prints: for each
// harness, whether its CLI is installed, and what installing (or removing)
// Hammock's hooks would change, with a line diff per file. The Mac app's
// onboarding renders it.
type hooksPlan struct {
	Exe       string        `json:"exe"`
	Harnesses []harnessPlan `json:"harnesses"`
}

type harnessPlan struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Binary  string     `json:"binary"`  // "" when not found
	Version string     `json:"version"` // e.g. "2.1.3"; "" if unknown
	Changes []string   `json:"changes"` // plain-English, one per change
	Notes   []string   `json:"notes"`   // caveats (e.g. an existing Codex notify)
	Files   []fileDiff `json:"files"`
	// UpToDate: nothing to change (already installed for this copy of hammock).
	UpToDate bool `json:"upToDate"`
}

type fileDiff struct {
	Path    string     `json:"path"` // ~-abbreviated
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Lines   []diffLine `json:"lines"`
}

// diffLine: Op is " " (context), "+", "-", or "…" (lines skipped).
type diffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

func printHooksPlan(exe string, harnesses []string, uninstall bool) error {
	plan := hooksPlan{Exe: exe}
	home, _ := os.UserHomeDir()
	for _, h := range harnesses {
		t, a := hookTargets[h], agents.Get(h)
		if a == nil {
			continue
		}
		hp := harnessPlan{ID: h, Name: a.Name, Binary: findAgentBinary(h), Changes: []string{}, Notes: []string{}, Files: []fileDiff{}}
		hp.Version = toolVersion(hp.Binary)
		edits, notes, err := t.edits(home, exe, uninstall)
		if err != nil {
			return err
		}
		hp.Notes = append(hp.Notes, notes...)
		for _, e := range edits {
			if !e.changed {
				continue
			}
			from := e.old
			if e.diffFrom != nil {
				from = e.diffFrom
			}
			d := lineDiff(from, e.new)
			d.Path = "~" + strings.TrimPrefix(e.path, home)
			hp.Files = append(hp.Files, d)
			hp.Changes = append(hp.Changes, e.changes...)
		}
		if t.plan != nil {
			hp.Changes = append(hp.Changes, t.plan(hp.Binary, exe, uninstall)...)
		}
		hp.UpToDate = len(hp.Changes) == 0
		plan.Harnesses = append(plan.Harnesses, hp)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(plan)
}

// missingClaudeHooks counts the hook events that don't have Hammock's hook yet.
func missingClaudeHooks(doc []byte) int {
	settings := map[string]any{}
	_ = json.Unmarshal(doc, &settings)
	hooks, _ := settings["hooks"].(map[string]any)
	n := 0
	for _, ev := range claudeHookEvents {
		found := false
		for _, e := range asSlice(hooks[ev]) {
			m, _ := e.(map[string]any)
			for _, h := range asSlice(m["hooks"]) {
				if hm, ok := h.(map[string]any); ok && isHammockHook(hm["command"]) {
					found = true
				}
			}
		}
		if !found {
			n++
		}
	}
	return n
}

// claudeMCPState reports whether Hammock's MCP server is registered with Claude
// Code, and whether it points at exe.
func claudeMCPState(claude, exe string) (registered, current bool) {
	if claude == "" {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, claude, "mcp", "get", "hammock").CombinedOutput()
	if err != nil {
		return false, false
	}
	return true, bytes.Contains(out, []byte(exe+" mcp"))
}

func normalizeJSON(doc []byte) []byte {
	if len(bytes.TrimSpace(doc)) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return doc
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return doc
	}
	return append(out, '\n')
}

// findCodex finds the codex CLI, also where installers put it when the app
// (launched without a login shell's PATH) runs this.
func findCodex() string {
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{".local/bin/codex", ".npm-global/bin/codex", ".local/share/mise/shims/codex", ".bun/bin/codex", ".volta/bin/codex"} {
		if fileExistsAt(filepath.Join(home, p)) {
			return filepath.Join(home, p)
		}
	}
	for _, p := range []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex"} {
		if fileExistsAt(p) {
			return p
		}
	}
	return ""
}

var versionRE = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// toolVersion runs `bin --version` and picks out the version number.
func toolVersion(bin string) string {
	if bin == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return ""
	}
	return versionRE.FindString(string(out))
}

// lineDiff diffs two files by line (LCS; config files are small) and keeps
// three lines of context around each change, eliding the rest with "…".
func lineDiff(old, new []byte) fileDiff {
	split := func(b []byte) []string {
		s := strings.TrimSuffix(string(b), "\n")
		if s == "" {
			return nil
		}
		return strings.Split(s, "\n")
	}
	a, b := split(old), split(new)
	// lcs[i][j] = LCS length of a[i:], b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var all []diffLine
	var d fileDiff
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			all = append(all, diffLine{" ", a[i]})
			i++
			j++
		// Removals before additions, as in a unified diff.
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			all = append(all, diffLine{"-", a[i]})
			d.Removed++
			i++
		default:
			all = append(all, diffLine{"+", b[j]})
			d.Added++
			j++
		}
	}
	const ctx = 3
	keep := make([]bool, len(all))
	for k, l := range all {
		if l.Op != " " {
			for m := max(0, k-ctx); m <= min(len(all)-1, k+ctx); m++ {
				keep[m] = true
			}
		}
	}
	for k, l := range all {
		if keep[k] {
			d.Lines = append(d.Lines, l)
		} else if len(d.Lines) == 0 || d.Lines[len(d.Lines)-1].Op != "…" {
			d.Lines = append(d.Lines, diffLine{"…", ""})
		}
	}
	return d
}
