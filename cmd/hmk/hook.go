package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/daemon"
)

// runHook forwards an agent hook event to the daemon. It must never block or
// fail the agent: errors are swallowed and it exits 0 quickly.
//
//	hmk hook claude            (Claude Code: event JSON on stdin)
//	hmk hook codex             (Codex hooks.json: event JSON on stdin)
//	hmk hook codex '{"type":…}' (Codex notify, from older installs: event JSON as the last argument)
//	hmk hook cursor            (Cursor's hooks.json: event JSON on stdin; prints {})
//	hmk hook antigravity EVENT (Antigravity CLI's hooks.json: event JSON on stdin; prints {})
func runHook(args []string) error {
	return forwardHook(args, os.Stdin, os.Stdout)
}

func forwardHook(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) > 0 && (args[0] == "cursor" || args[0] == "antigravity") {
		// Cursor reads every hook's stdout as its decision, and takes an
		// unreadable one as "block" for its permission events; {} is a
		// valid answer for each event that leaves its decision alone (an
		// empty one counts as the hook failing).
		// Antigravity CLI wants a JSON object from every hook; {} injects
		// nothing and decides nothing.
		defer fmt.Fprintln(stdout, "{}")
	}
	paneID := paneEnv("PANE_ID")
	if paneID == "" || len(args) == 0 {
		return nil // not running inside Hammock
	}
	harness := args[0]
	var payload []byte
	event := ""
	if len(args) > 1 && strings.HasPrefix(strings.TrimSpace(args[len(args)-1]), "{") {
		payload = []byte(args[len(args)-1])
		if len(args) > 2 {
			event = args[1]
		}
	} else {
		if len(args) > 1 {
			event = args[1]
		}
		payload, _ = io.ReadAll(io.LimitReader(stdin, 1<<20))
	}
	if !json.Valid(payload) {
		payload = []byte("{}")
	}
	if harness != "cursor" && fromCursor(payload) {
		// Cursor also runs Claude Code's hooks (~/.claude/settings.json),
		// so Hammock's Claude hook fires in Cursor too: its own hooks say it,
		// and this one answers as they do.
		fmt.Fprintln(stdout, "{}")
		return nil
	}
	if harness != "grok" && fromGrok() {
		return nil // Grok runs Claude Code's hooks too: its own say it
	}
	sock := paneEnv("HOOK_SOCKET")
	if sock == "" {
		sock = daemon.HookSocketPath(daemon.DefaultDir())
	}
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = json.NewEncoder(conn).Encode(daemon.HookEvent{PaneID: paneID, Harness: harness, Event: event, Payload: payload})
	_, _ = io.Copy(io.Discard, conn)
	return nil
}

// fromCursor: payload is a Cursor hook's (each has cursor_version).
func fromCursor(payload []byte) bool {
	var p struct {
		CursorVersion *string `json:"cursor_version"`
	}
	return json.Unmarshal(payload, &p) == nil && p.CursorVersion != nil
}

// runHooks prints the configuration snippets that route agent hooks to hammock.
func runHooks(args []string) error {
	if len(args) > 0 && args[0] == "install" {
		return runHooksInstall(args[1:])
	}
	if len(args) == 0 || args[0] != "print" {
		return fmt.Errorf("usage: hmk hooks print | install [--dry-run [--json]] [--uninstall] [--harness claude,codex]")
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "hmk"
	}
	claude, _, _ := installClaudeHooks(nil, exe)
	codex, _, _ := installCodexHooks(nil, exe)
	fmt.Printf("# Claude Code — merge into ~/.claude/settings.json\n%s\n", claude)
	fmt.Printf("# Codex — merge into ~/.codex/hooks.json, with [features] hooks = true in ~/.codex/config.toml\n%s", codex)
	return nil
}

// paneEnv reads a variable Hammock sets in panes (HMK_<name>).
func paneEnv(name string) string {
	return os.Getenv("HMK_" + name)
}
