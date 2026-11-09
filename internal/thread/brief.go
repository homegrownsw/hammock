package thread

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Brief builds a handoff prompt for starting a fresh session in another
// harness when converting the transcript itself isn't possible. It is built
// only from the records, so it needs no model call.
func Brief(t Thread, recs []Record) string {
	var users []string
	var lastAssistant string
	var files []string
	var commands []string
	for _, r := range recs {
		switch r.Kind {
		case KindUser:
			if s := strings.TrimSpace(r.Text); s != "" {
				users = append(users, s)
			}
		case KindAssistant:
			if s := strings.TrimSpace(r.Text); s != "" {
				lastAssistant = s
			}
		case KindToolCall:
			if r.Edit != nil && r.Edit.Path != "" && !slices.Contains(files, r.Edit.Path) {
				files = append(files, r.Edit.Path)
			}
			if r.Tool == ToolShell {
				var in struct {
					Command string `json:"command"`
				}
				if json.Unmarshal(r.Input, &in) == nil && in.Command != "" {
					commands = append(commands, firstLine(in.Command))
				}
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "You are continuing a task that was started in another coding agent (%s) in %s. Pick up where it left off.\n", t.Harness, t.Cwd)
	if len(users) > 0 {
		fmt.Fprintf(&b, "\n## Original request\n%s\n", clipText(users[0], 4000))
	}
	if len(users) > 1 {
		b.WriteString("\n## Later instructions\n")
		for _, u := range users[max(1, len(users)-5):] {
			fmt.Fprintf(&b, "- %s\n", clipText(firstLine(u), 300))
		}
	}
	if len(files) > 0 {
		b.WriteString("\n## Files changed so far\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	if len(commands) > 0 {
		b.WriteString("\n## Recent commands\n")
		for _, c := range commands[max(0, len(commands)-8):] {
			fmt.Fprintf(&b, "- `%s`\n", clipText(c, 200))
		}
	}
	if lastAssistant != "" {
		fmt.Fprintf(&b, "\n## Where it left off\n%s\n", clipText(lastAssistant, 3000))
	}
	b.WriteString("\nCheck the working tree (e.g. git status / git diff) before continuing, then carry on with the task.\n")
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
