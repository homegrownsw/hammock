package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Pi has no shell hooks; it loads TypeScript extensions from
// ~/.pi/agent/extensions/*.ts, which subscribe to its events (pi-mono
// packages/coding-agent docs/extensions.md, pi 1.5.0). Hammock writes one
// there that forwards the events it needs to `hmk hook pi <event>`, with a
// small JSON payload (internal/agents/pi.go parsePiHook reads them), and
// adds Hammock's tools to ~/.pi/agent/mcp.json.

const piExtensionFile = ".pi/agent/extensions/hammock.ts"

func init() {
	plugin := pluginFile{file: piExtensionFile, content: piExtension, why: "so Hammock knows exactly what Pi is doing"}.target()
	hookTargets["pi"] = hookTarget{
		edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
			edits, notes, err := plugin.edits(home, exe, uninstall)
			if err != nil {
				return nil, nil, err
			}
			m, err := piMCPEdit(home, exe, uninstall)
			if err != nil {
				return nil, nil, err
			}
			return append(edits, m...), notes, nil
		},
		// pi.dev/install.sh links it into ~/.pi/agent/bin or ~/bin when
		// they're on PATH (else ~/.local/bin or Homebrew's, searched anyway).
		binary: func() string { return agentBinary("pi", ".pi/agent/bin", "bin") },
		// "pi" is a short name other programs use too.
		used: func(home string) bool { return dirAt(filepath.Join(home, ".pi")) },
	}
}

// piMCPEdit adds (or takes out) Hammock's MCP server in ~/.pi/agent/mcp.json,
// keeping everything else there. Hammock's entry is "hammock", run with "mcp".
func piMCPEdit(home, exe string, uninstall bool) ([]configEdit, error) {
	path := filepath.Join(home, ".pi", "agent", "mcp.json")
	doc, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	out, changed, err := editPiMCP(doc, exe, uninstall)
	if err != nil {
		return nil, err
	}
	if uninstall && !changed {
		return nil, nil
	}
	e := configEdit{path: path, old: doc, new: out, changed: changed, diffFrom: normalizeJSON(doc)}
	where := "~" + strings.TrimPrefix(path, home)
	if uninstall {
		e.changes = []string{"Removes Hammock's tools from " + where}
	} else {
		e.changes = []string{"Adds Hammock's tools to " + where}
	}
	return []configEdit{e}, nil
}

func editPiMCP(doc []byte, exe string, uninstall bool) ([]byte, bool, error) {
	cfg := map[string]any{}
	if len(bytes.TrimSpace(doc)) > 0 {
		if err := json.Unmarshal(doc, &cfg); err != nil {
			return nil, false, fmt.Errorf("parse mcp.json: %w", err)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	cur, _ := servers["hammock"].(map[string]any)
	ours := cur != nil && isHammockMCP(cur)
	changed := false
	switch {
	case uninstall:
		if !ours {
			return doc, false, nil
		}
		delete(servers, "hammock")
		changed = true
	case cur != nil && !ours:
		return doc, false, nil // someone else's "hammock": left alone
	default:
		if servers == nil {
			servers = map[string]any{}
		}
		if cur == nil || cur["command"] != exe {
			if cur == nil {
				cur = map[string]any{}
			}
			cur["command"], cur["args"] = exe, []any{"mcp"}
			servers["hammock"] = cur
			changed = true
		}
	}
	if !changed {
		return doc, false, nil
	}
	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	return append(out, '\n'), true, err
}

// isHammockMCP: an mcp.json entry runs `<…/hmk> mcp`.
func isHammockMCP(e map[string]any) bool {
	cmd, _ := e["command"].(string)
	args, _ := e["args"].([]any)
	return filepath.Base(cmd) == "hmk" && len(args) == 1 && args[0] == "mcp"
}

// piExtension is Hammock's Pi extension, calling exe.
func piExtension(exe string) []byte {
	q, _ := json.Marshal(exe)
	return []byte(strings.Replace(piExtensionSource, "__HMK__", string(q), 1))
}

const piExtensionSource = `// ` + pluginMarker + `: tells Hammock what Pi is doing.
// Reinstalling Hammock's hooks rewrites this file; ` + "`hmk hooks install --uninstall --harness pi`" + ` removes it.
// Outside a Hammock pane (no HMK_PANE_ID), and in Pi's print, JSON and RPC
// modes, it does nothing. Each event runs ` + "`hmk hook pi <event>`" + ` with a little
// JSON on stdin, in the background, one after another: Pi never waits for it.
// @ts-nocheck

import { spawn } from "node:child_process";

const HMK = __HMK__;
const inHammock = !!process.env.HMK_PANE_ID;

export default function (pi) {
	if (!inHammock) return;

	let live = false; // this is the pane's interactive Pi
	let queue = Promise.resolve();

	function run(event, data) {
		return new Promise((resolve) => {
			let done = false;
			const finish = () => {
				if (done) return;
				done = true;
				clearTimeout(timer);
				resolve();
			};
			const timer = setTimeout(finish, 3000);
			timer.unref?.();
			try {
				const child = spawn(HMK, ["hook", "pi", event], { stdio: ["pipe", "ignore", "ignore"] });
				child.on("error", finish);
				child.on("exit", finish);
				child.stdin.on("error", () => {});
				child.stdin.end(JSON.stringify(data));
				child.unref();
			} catch {
				finish();
			}
		});
	}

	function session(ctx) {
		const out = {};
		try {
			const id = ctx?.sessionManager?.getSessionId?.();
			if (typeof id === "string" && id) out.session_id = id;
			const file = ctx?.sessionManager?.getSessionFile?.();
			if (typeof file === "string" && file) out.transcript_path = file;
		} catch {}
		return out;
	}

	function send(event, ctx, data) {
		if (!live) return queue;
		const payload = { ...session(ctx), ...(data || {}) };
		queue = queue.then(() => run(event, payload), () => run(event, payload));
		return queue;
	}

	function toolInput(args) {
		const out = {};
		if (args && typeof args === "object") {
			for (const k of ["command", "path", "pattern"]) {
				if (typeof args[k] === "string") out[k] = args[k].slice(0, 2000);
			}
		}
		return out;
	}

	function lastReply(ctx) {
		try {
			const branch = ctx?.sessionManager?.getBranch?.() || [];
			for (let i = branch.length - 1; i >= 0; i--) {
				const m = branch[i]?.type === "message" ? branch[i].message : undefined;
				if (m?.role !== "assistant") continue;
				const parts = Array.isArray(m.content) ? m.content : [];
				const text = parts.filter((p) => p?.type === "text").map((p) => p.text).join("\n");
				return { text: text.slice(0, 2000), error: m.stopReason === "error" ? String(m.errorMessage || "error") : "" };
			}
		} catch {}
		return { text: "", error: "" };
	}

	pi.on("session_start", (event, ctx) => {
		live = ctx?.mode ? ctx.mode === "tui" : ctx?.hasUI === true;
		send("session_start", ctx, { reason: event?.reason });
	});

	pi.on("before_agent_start", (event, ctx) => {
		send("before_agent_start", ctx, { prompt: typeof event?.prompt === "string" ? event.prompt.slice(0, 2000) : "" });
	});

	pi.on("tool_execution_start", (event, ctx) => {
		if (event?.parentToolCallId) return;
		send("tool_execution_start", ctx, { tool_name: event?.toolName, tool_input: toolInput(event?.args) });
	});

	pi.on("tool_execution_end", (event, ctx) => {
		if (event?.parentToolCallId) return;
		send("tool_execution_end", ctx, { tool_name: event?.toolName });
	});

	pi.on("ui_prompt_start", (event, ctx) => {
		send("ui_prompt_start", ctx, { title: typeof event?.title === "string" ? event.title : "" });
	});

	pi.on("ui_prompt_end", (_event, ctx) => {
		let idle = false;
		try {
			idle = ctx?.isIdle?.() === true;
		} catch {}
		send("ui_prompt_end", ctx, { idle });
	});

	pi.on("agent_settled", (event, ctx) => {
		const reply = lastReply(ctx);
		send("agent_settled", ctx, { aborted: event?.aborted === true, last_assistant_message: reply.text, error: reply.error });
	});

	pi.on("session_shutdown", async (event, ctx) => {
		const sent = send("session_shutdown", ctx, { reason: event?.reason });
		if (event?.reason !== "quit" || !live) return;
		// Pi is exiting: give the last events a moment to leave, no more.
		await Promise.race([sent, new Promise((r) => setTimeout(r, 300))]);
	});
}
`
