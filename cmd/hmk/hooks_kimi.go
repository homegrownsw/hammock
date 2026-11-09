package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Kimi Code reads hooks only from [[hooks]] tables in ~/.kimi-code/config.toml
// (each {event, matcher, command, timeout}: any other key and the file fails
// to load), with Claude Code's event names and payloads that name their
// event (docs/en/customization/hooks.md, kimi-code 2.5.2). Hammock's go in a
// block of their own between two comment lines, appended to your file and
// otherwise left as it is; reinstalling replaces the block where it is, and
// uninstalling takes out exactly it (and any of Hammock's tables that lost
// their fence, should something have rewritten the file). A hook fails
// open: only exit status 2 blocks, so a hmk that has moved can't stop Kimi.
// Hammock's tools go in ~/.kimi-code/mcp.json.

var kimiHookEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "PermissionResult", "Stop", "StopFailure", "Interrupt", "SessionEnd"}

const (
	kimiHookMarker = " hook kimi"
	kimiFenceOpen  = "# >>> Hammock's hooks (hmk hooks install --harness kimi) >>>"
	kimiFenceClose = "# <<< Hammock's hooks <<<"
)

func init() {
	hookTargets["kimi"] = kimiHookTarget
}

var kimiHookTarget = hookTarget{
	edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
		dir := filepath.Join(home, ".kimi-code")
		path := filepath.Join(dir, "config.toml")
		doc, err := readConfig(path)
		if err != nil {
			return nil, nil, err
		}
		var notes []string
		e := configEdit{path: path, old: doc, new: doc}
		if uninstall {
			e.new, e.changed = kimiEditConfig(doc, "")
			e.changes = []string{"Removes Hammock's hooks from ~/.kimi-code/config.toml, keeping your own"}
		} else if kimiHooksElsewhere(doc) {
			notes = append(notes, "~/.kimi-code/config.toml keeps its hooks as hooks = [ … ] or [hooks]: add Hammock's yourself, as [[hooks]] tables running `"+hookCommand(exe, "kimi", "")+"`")
		} else {
			e.new, e.changed = kimiEditConfig(doc, exe)
			e.changes = []string{"Adds Hammock's hooks to ~/.kimi-code/config.toml so Hammock knows exactly what Kimi is doing"}
		}
		edits := []configEdit{e}

		mcpPath := filepath.Join(dir, "mcp.json")
		mdoc, err := readConfig(mcpPath)
		if err != nil {
			return nil, nil, err
		}
		m := configEdit{path: mcpPath, old: mdoc, new: mdoc, diffFrom: normalizeJSON(mdoc)}
		m.new, m.changed, err = kimiEditMCP(mdoc, exe, uninstall)
		if err != nil {
			return nil, nil, err
		}
		if uninstall {
			m.changes = []string{"Removes Hammock's tools from ~/.kimi-code/mcp.json"}
		} else {
			m.changes = []string{"Adds Hammock's tools to ~/.kimi-code/mcp.json"}
		}
		return append(edits, m), notes, nil
	},
	binary: func() string { return agentBinary("kimi", ".kimi-code/bin") },
}

// kimiHooksBlock is Hammock's block of [[hooks]] tables for exe.
func kimiHooksBlock(exe string) string {
	var b strings.Builder
	b.WriteString(kimiFenceOpen + "\n")
	b.WriteString("# They tell Hammock what Kimi is doing in its panes (and do nothing elsewhere).\n")
	cmd := kimiTOMLString(hookCommand(exe, "kimi", ""))
	for _, ev := range kimiHookEvents {
		fmt.Fprintf(&b, "\n[[hooks]]\nevent = %q\ncommand = %s\ntimeout = 10\n", ev, cmd)
	}
	b.WriteString(kimiFenceClose + "\n")
	return b.String()
}

// kimiTOMLString is s as a TOML basic string.
func kimiTOMLString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var (
	kimiHeaderRE      = regexp.MustCompile(`^\s*\[`)
	kimiHooksTableRE  = regexp.MustCompile(`^\s*\[\[\s*hooks\s*\]\]\s*(#.*)?$`)
	kimiHooksHeaderRE = regexp.MustCompile(`^\s*\[\s*hooks\s*\]`)
	kimiHooksKeyRE    = regexp.MustCompile(`^\s*hooks\s*=`)
	kimiCommandRE     = regexp.MustCompile(`^\s*command\s*=\s*(.*?)\s*$`)
)

// kimiHooksElsewhere: the file has hooks that aren't [[hooks]] tables (an
// inline array at its top, or a [hooks] table), beside which TOML allows
// no [[hooks]].
func kimiHooksElsewhere(doc []byte) bool {
	top := true
	for _, l := range strings.Split(string(doc), "\n") {
		switch {
		case kimiHooksHeaderRE.MatchString(l):
			return true
		case kimiHeaderRE.MatchString(l):
			top = false
		case top && kimiHooksKeyRE.MatchString(l):
			return true
		}
	}
	return false
}

// kimiEditConfig puts Hammock's block of hooks for exe into config.toml (exe
// "": takes it out), with any of Hammock's [[hooks]] found outside it, and
// changes nothing else.
func kimiEditConfig(doc []byte, exe string) ([]byte, bool) {
	lines := strings.SplitAfter(string(doc), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	at := -1 // where Hammock's block was
	var kept []string
	// removed: lines up to next were Hammock's. Taking them out, take out the
	// blank line before them too when a blank line (or the end) follows.
	removed := func(next int) {
		n := len(kept)
		if exe == "" && n > 0 && strings.TrimSpace(kept[n-1]) == "" && (next >= len(lines) || strings.TrimSpace(lines[next]) == "") {
			kept = kept[:n-1]
		}
	}
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r\n")
		switch {
		case strings.TrimSpace(l) == kimiFenceOpen:
			// To its closing line; without one, just this line (Hammock's
			// tables after it go as strays, below).
			end := i
			for end < len(lines) && strings.TrimSpace(lines[end]) != kimiFenceClose {
				end++
			}
			if end == len(lines) {
				end = i
			}
			removed(end + 1)
			if at < 0 {
				at = len(kept)
			}
			i = end
		case kimiHooksTableRE.MatchString(l) && kimiHammockTable(lines, i):
			i = kimiTableEnd(lines, i) - 1
			removed(i + 1)
		default:
			kept = append(kept, lines[i])
		}
	}
	var block []string
	if exe != "" {
		block = strings.SplitAfter(strings.TrimSuffix(kimiHooksBlock(exe), "\n"), "\n")
		block[len(block)-1] += "\n"
	}
	var out []string
	switch {
	case at >= 0:
		out = append(append(append(out, kept[:at]...), block...), kept[at:]...)
	case exe != "":
		out = kept
		if n := len(out); n > 0 {
			if !strings.HasSuffix(out[n-1], "\n") {
				out[n-1] += "\n"
			}
			if strings.TrimSpace(out[n-1]) != "" {
				out = append(out, "\n")
			}
		}
		out = append(out, block...)
	default:
		out = kept
	}
	res := []byte(strings.Join(out, ""))
	if len(bytes.TrimSpace(doc)) == 0 && exe == "" {
		return doc, false
	}
	return res, !bytes.Equal(res, doc)
}

// kimiTableEnd is the line after the [[hooks]] table starting at i: before
// the next table or Hammock's fence, less the comments and blank lines that
// lead up to it.
func kimiTableEnd(lines []string, i int) int {
	end := i + 1
	for end < len(lines) && !kimiHeaderRE.MatchString(lines[end]) && strings.TrimSpace(lines[end]) != kimiFenceOpen {
		end++
	}
	for end > i+1 {
		t := strings.TrimSpace(lines[end-1])
		if t != "" && !strings.HasPrefix(t, "#") {
			break
		}
		end--
	}
	return end
}

// kimiHammockTable: the table starting at line i is a [[hooks]] whose command
// is Hammock's.
func kimiHammockTable(lines []string, i int) bool {
	if !kimiHooksTableRE.MatchString(strings.TrimRight(lines[i], "\r\n")) {
		return false
	}
	for _, l := range lines[i+1 : kimiTableEnd(lines, i)] {
		if m := kimiCommandRE.FindStringSubmatch(l); m != nil {
			return isHookFor(kimiTOMLValue(m[1]), kimiHookMarker)
		}
	}
	return false
}

// kimiTOMLValue reads a one-line TOML string (basic or literal), else
// returns v as it is.
func kimiTOMLValue(v string) string {
	switch {
	case strings.HasPrefix(v, `'`):
		if j := strings.Index(v[1:], `'`); j >= 0 {
			return v[1 : j+1]
		}
	case strings.HasPrefix(v, `"`):
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			switch c := v[i]; {
			case c == '"':
				return b.String()
			case c == '\\' && i+1 < len(v):
				i++
				switch v[i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(v[i])
				}
			default:
				b.WriteByte(c)
			}
		}
	}
	return v
}

// kimiEditMCP adds Hammock's MCP server to mcp.json ({"mcpServers": {"hammock":
// {"command": exe, "args": ["mcp"]}}}), or with uninstall removes it if
// it's Hammock's.
func kimiEditMCP(doc []byte, exe string, uninstall bool) ([]byte, bool, error) {
	cfg := map[string]any{}
	if len(bytes.TrimSpace(doc)) > 0 {
		if err := json.Unmarshal(doc, &cfg); err != nil {
			return nil, false, fmt.Errorf("parse mcp.json: %w", err)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	cur, _ := servers["hammock"].(map[string]any)
	cmd, _ := cur["command"].(string)
	ours := cur != nil && filepath.Base(cmd) == "hmk"
	if uninstall {
		if !ours {
			return doc, false, nil
		}
		delete(servers, "hammock")
	} else {
		if cur != nil && !ours {
			return doc, false, nil // a server of yours named hammock
		}
		if ours && cmd == exe {
			return doc, false, nil
		}
		if servers == nil {
			servers = map[string]any{}
			cfg["mcpServers"] = servers
		}
		servers["hammock"] = map[string]any{"command": exe, "args": []any{"mcp"}}
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	return append(out, '\n'), true, err
}
