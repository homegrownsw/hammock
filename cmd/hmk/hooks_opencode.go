package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// OpenCode and Kilo CLI (a fork of OpenCode 1.x) have no shell hooks: they
// load JavaScript plugins from their config folders, which see their
// events. Hammock writes its own plugin there, which turns those events into
// Hammock's (`hmk hook opencode Stop`, the JSON on stdin), fire and forget,
// and only in a Hammock pane (HMK_PANE_ID set).
//
// Where each runs, from their source (anomalyco/opencode 2.2.5,
// Kilo-Org/kilocode 7.8.27):
//   - OpenCode 2.x runs its server in a background service that every
//     terminal shares, so a server plugin can't tell which pane it's for.
//     Its TUI, in the pane, loads TUI plugins: each folder in
//     ~/.config/opencode/plugins with a tui.js. Its server loads every file
//     and folder there too, so the folder has an index.js that does nothing,
//     or the server would call it a broken plugin.
//   - OpenCode 1.x and Kilo run their server in the TUI's own process (a
//     worker with its environment), loading ~/.config/opencode/plugins/*.js
//     and ~/.config/kilo/plugin/*.js. (Kilo attaches to `kilo daemon`
//     instead when one is running: its plugin then runs there, outside the
//     pane, and does nothing; Hammock reads Kilo's screen as for any agent
//     without hooks.) OpenCode 2's server loads that file as well, where its
//     setup does nothing.

func init() {
	hookTargets["opencode"] = openCodeHookTarget()
	hookTargets["kilo"] = pluginFile{
		file:    ".config/kilo/plugin/hammock.js",
		content: func(exe string) []byte { return openCodeServerPlugin(exe, "kilo", "Kilo CLI") },
		why:     "so Hammock knows exactly what Kilo is doing",
	}.target()
}

const openCodePluginDir = ".config/opencode/plugins/hammock"

func openCodeHookTarget() hookTarget {
	parts := []pluginFile{
		{file: openCodePluginDir + "/tui.js", content: openCodeTUIPlugin},
		{file: openCodePluginDir + "/index.js", content: openCodeServerStub},
		{file: ".config/opencode/plugins/hammock.js", content: func(exe string) []byte { return openCodeServerPlugin(exe, "opencode", "OpenCode 1.x") }},
	}
	var targets []hookTarget
	for _, p := range parts {
		targets = append(targets, p.target())
	}
	return hookTarget{
		edits: func(home, exe string, uninstall bool) ([]configEdit, []string, error) {
			var edits []configEdit
			var notes []string
			for _, t := range targets {
				e, n, err := t.edits(home, exe, uninstall)
				if err != nil {
					return nil, nil, err
				}
				edits, notes = append(edits, e...), append(notes, n...)
			}
			// One line for the three files.
			said := false
			for i := range edits {
				edits[i].changes = nil
				if edits[i].changed && !said {
					said = true
					edits[i].changes = []string{"Adds Hammock's plugin to ~/.config/opencode/plugins so Hammock knows exactly what OpenCode is doing"}
					if uninstall {
						edits[i].changes = []string{"Removes Hammock's plugin from ~/.config/opencode/plugins"}
					}
				}
			}
			return edits, notes, nil
		},
		// The plugin's folder goes with its files: OpenCode 2's server
		// would take an empty one for a broken plugin.
		apply: func(exe string, uninstall, dry bool) error {
			if uninstall && !dry {
				home, _ := os.UserHomeDir()
				_ = os.Remove(filepath.Join(home, openCodePluginDir)) // only when empty
			}
			return nil
		},
	}
}

// openCodePluginSource fills in a plugin's source: the hmk it runs, the harness
// it reports as and the agent's name.
func openCodePluginSource(src, exe, harness, name string) []byte {
	q, _ := json.Marshal(exe)
	return []byte(strings.NewReplacer("__HMK__", string(q), "__HARNESS__", harness, "__NAME__", name, "__MARKER__", pluginMarker).Replace(src))
}

// openCodePluginSend is how each plugin tells Hammock: `hmk hook HARNESS EVENT` with
// the payload on stdin, in the background and in order, never failing or
// holding the agent up.
const openCodePluginSend = `import { spawn, spawnSync } from "node:child_process"

const HMK = __HMK__
const HARNESS = "__HARNESS__"

// One at a time, in order (a turn's last tool and its end come moments
// apart), never holding the agent up: each waits for the one before.
let queue = Promise.resolve()

function send(event, payload) {
  if (!process.env.HMK_PANE_ID) return
  const body = JSON.stringify(payload ?? {})
  queue = queue.then(
    () =>
      new Promise((done) => {
        const timer = setTimeout(done, 3000)
        timer.unref?.()
        const finish = () => {
          clearTimeout(timer)
          done()
        }
        try {
          const child = spawn(HMK, ["hook", HARNESS, event], { stdio: ["pipe", "ignore", "ignore"] })
          child.on("error", finish)
          child.on("exit", finish)
          child.stdin.on("error", () => {})
          child.stdin.end(body)
          child.unref()
        } catch {
          finish()
        }
      }),
  )
}

// A tool's input as Hammock reads it: its path as file_path too.
function toolInput(args) {
  const input = args && typeof args === "object" ? { ...args } : {}
  const path = input.filePath ?? input.path
  if (typeof path === "string") input.file_path = path
  return input
}
`

// openCodeServerPlugin is the plugin for OpenCode 1.x's and Kilo's server, run in
// the pane's own process: their plugin hooks for what you send and the
// tools it runs, their events for the rest. A subagent's session (one
// with a parent) counts as the session that started it.
func openCodeServerPlugin(exe, harness, name string) []byte {
	return openCodePluginSource(`// __MARKER__: tells Hammock what __NAME__ is doing
// in a Hammock pane. Reinstalling Hammock's hooks rewrites this file;
// "hmk hooks install --uninstall --harness __HARNESS__" removes it.
`+openCodePluginSend+`
const parents = new Map() // a subagent's session → the one that started it
const busy = new Set() // sessions working on something
const roles = new Map() // message → user or assistant
const replies = new Map() // session → its latest reply's text

function root(id) {
  for (let i = 0; id && parents.has(id) && i < 64; i++) id = parents.get(id)
  return id
}

function stopped(id, event, payload) {
  if (!busy.delete(id)) return
  send(event, { session_id: id, ...payload })
}

export const HammockPlugin = async () => {
  if (!process.env.HMK_PANE_ID) return {}
  send("SessionStart", {})
  return {
    "chat.message": async (input, output) => {
      const id = input?.sessionID
      if (!id || parents.has(id)) return
      const prompt = (output?.parts ?? [])
        .filter((p) => p?.type === "text" && !p.synthetic && !p.ignored)
        .map((p) => p.text)
        .join("\n")
      busy.add(id)
      send("UserPromptSubmit", { session_id: id, prompt })
    },
    "tool.execute.before": async (input, output) => {
      const id = input?.sessionID
      if (!id || parents.has(id)) return
      send("PreToolUse", { session_id: id, tool_name: input.tool, tool_input: toolInput(output?.args) })
    },
    "tool.execute.after": async (input) => {
      const id = input?.sessionID
      if (!id || parents.has(id)) return
      send("PostToolUse", { session_id: id, tool_name: input.tool, tool_input: toolInput(input.args) })
    },
    event: async ({ event }) => {
      const props = event?.properties ?? {}
      const info = props.info
      if (info?.id && info.parentID) parents.set(info.id, info.parentID)
      if (event?.type === "message.updated" && info?.id) roles.set(info.id, info.role)
      const part = props.part
      if (event?.type === "message.part.updated" && part?.type === "text" && !part.synthetic && roles.get(part.messageID) === "assistant")
        replies.set(part.sessionID, part.text)
      const id = props.sessionID
      if (!id) return
      switch (event.type) {
        case "session.status":
          if (props.status?.type === "busy" && !parents.has(id)) busy.add(id)
          if (props.status?.type === "idle" && !parents.has(id)) stopped(id, "Stop", { last_assistant_message: replies.get(id) ?? "" })
          break
        case "session.idle":
          if (!parents.has(id)) stopped(id, "Stop", { last_assistant_message: replies.get(id) ?? "" })
          break
        case "session.error": {
          if (parents.has(id)) break
          const error = props.error
          if (error?.name === "MessageAbortedError") stopped(id, "Stop", {})
          else stopped(id, "StopFailure", { error: error?.name ?? "", error_details: error?.data?.message ?? "" })
          break
        }
        case "permission.asked": {
          const target = (props.patterns ?? [])[0] ?? ""
          const input = props.permission === "bash" ? { command: props.metadata?.command ?? target } : { file_path: props.metadata?.filepath ?? target }
          send("PermissionRequest", { session_id: root(id), tool_name: props.permission, tool_input: input })
          break
        }
        case "question.asked":
          send("PermissionRequest", { session_id: root(id), message: props.questions?.[0]?.question ?? "" })
          break
        case "permission.replied":
        case "question.replied":
        case "question.rejected":
          send("PostToolUse", { session_id: root(id) })
          break
      }
    },
  }
}

// OpenCode 1.x and Kilo take the server function from here (older ones by
// its name, above); OpenCode 2's server, which loads this file too, calls
// setup, which does nothing: its TUI plugin tells Hammock instead.
export default { id: "hammock-__HARNESS__", server: HammockPlugin, setup() {} }
`, exe, harness, name)
}

// openCodeTUIPlugin is OpenCode 2's TUI plugin. The TUI can show several
// sessions (tabs); the pane's is the one on screen (its root, for a
// subagent's). Its sessions' events say when a turn starts and ends, the
// tools it runs and what it asks you.
func openCodeTUIPlugin(exe string) []byte {
	return openCodePluginSource(`// __MARKER__: tells Hammock what OpenCode is doing
// in a Hammock pane. Reinstalling Hammock's hooks rewrites this folder;
// "hmk hooks install --uninstall --harness opencode" removes it.
`+openCodePluginSend+`
export default {
  id: "hammock",
  setup(api) {
    if (!process.env.HMK_PANE_ID) return
    let selected

    const rootOf = (id) => {
      try {
        return api.data.session.root(id) ?? id
      } catch {
        return id
      }
    }
    const current = () => {
      try {
        const route = api.ui.router.current()
        return route?.type === "session" && route.sessionID ? rootOf(route.sessionID) : undefined
      } catch {
        return undefined
      }
    }
    // The text of a session's latest message of a kind (user, assistant).
    const latest = (id, type) => {
      try {
        const messages = api.data.session.message.list(id) ?? []
        for (let i = messages.length - 1; i >= 0; i--) {
          const m = messages[i]
          if (m?.type !== type) continue
          if (type === "user") return m.text ?? ""
          const text = (m.content ?? []).filter((c) => c?.type === "text").map((c) => c.text).join("\n")
          if (text) return text
        }
      } catch {}
      return ""
    }
    const sync = () => {
      const id = current()
      if (id === selected) return
      selected = id
      if (!id) return
      send("SessionStart", { session_id: id })
      let running = false
      try {
        running = api.data.session.status(id) === "running"
      } catch {}
      if (running) send("UserPromptSubmit", { session_id: id })
    }

    const handle = (event) => {
      sync()
      const data = event?.data ?? {}
      const id = event?.type === "form.created" ? data.form?.sessionID : data.sessionID
      if (!selected || !id) return
      const own = id === selected
      if (!own && rootOf(id) !== selected) return
      switch (event.type) {
        case "session.execution.started":
          if (own) send("UserPromptSubmit", { session_id: id, prompt: latest(id, "user") })
          break
        case "session.execution.succeeded":
        case "session.execution.interrupted":
          if (own) send("Stop", { session_id: id, last_assistant_message: latest(id, "assistant") })
          break
        case "session.execution.failed":
          if (own) send("StopFailure", { session_id: id, error: data.error?.type ?? "", error_details: data.error?.message ?? "" })
          break
        case "session.tool.called":
          if (own) send("PreToolUse", { session_id: id, tool_name: data.name, tool_input: toolInput(data.input) })
          break
        case "session.tool.success":
        case "session.tool.failed":
          if (own) send("PostToolUse", { session_id: id })
          break
        case "permission.asked": {
          const target = (data.resources ?? [])[0] ?? ""
          const input = data.action === "shell" || data.action === "bash" ? { command: target } : { file_path: target }
          send("PermissionRequest", { session_id: selected, tool_name: data.action, tool_input: input, message: data.message ?? "" })
          break
        }
        case "form.created":
          send("PermissionRequest", { session_id: selected, message: data.form?.title ?? "" })
          break
        case "permission.replied":
        case "form.replied":
        case "form.cancelled":
          send("PostToolUse", { session_id: selected })
          break
      }
    }

    send("SessionStart", {})
    sync()
    const poll = setInterval(sync, 250)
    poll.unref?.()
    const unlisten = api.data.listen(({ details }) => {
      try {
        handle(details)
      } catch {}
    })
    // Quitting: say so before the process is gone.
    const exit = () => {
      try {
        spawnSync(HMK, ["hook", HARNESS, "SessionEnd"], {
          input: JSON.stringify({ session_id: selected }),
          stdio: ["pipe", "ignore", "ignore"],
          timeout: 1000,
        })
      } catch {}
    }
    process.once("exit", exit)
    return () => {
      clearInterval(poll)
      unlisten?.()
      process.off("exit", exit)
    }
  },
}
`, exe, "opencode", "OpenCode")
}

// openCodeServerStub is the plugin folder's server part, for OpenCode 2's
// server, which loads every folder in plugins/: it does nothing.
func openCodeServerStub(string) []byte {
	return []byte(`// ` + pluginMarker + `: the server's part of Hammock's
// OpenCode plugin, which does nothing (tui.js, beside it, tells Hammock what
// OpenCode is doing in a Hammock pane).
export default { id: "hammock", setup() {} }
`)
}
