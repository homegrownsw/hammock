package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Amp has no shell hooks: its CLI loads plugins (TypeScript, run under Bun
// in a process of its own with the CLI's environment) from
// ~/.config/amp/plugins, which see its events and each thread's state
// (ampcode.com/docs/customize/plugins and /docs/plugin-api; @ampcode/cli
// 0.0.1794038434). Hammock writes its own plugin there, which forwards them as
// `hmk hook amp <event>` with the event's JSON on stdin, fire and forget,
// and only in a Hammock pane (HMK_PANE_ID set). internal/agents/amp.go reads
// them.
//
// Amp also reads $XDG_CONFIG_HOME/amp/plugins instead when that's set;
// Hammock writes to ~/.config, where Amp looks otherwise (and on a Mac, where
// XDG_CONFIG_HOME is rarely set).

func init() {
	hookTargets["amp"] = hookTarget{
		edits: pluginFile{
			file:    ".config/amp/plugins/hammock.ts",
			content: ampPlugin,
			why:     "so Hammock knows exactly what Amp is doing",
		}.target().edits,
		binary: func() string { return agentBinary("amp", ".amp/bin") },
		// The Amp text editor is "amp" too.
		used: func(home string) bool {
			return dirAt(filepath.Join(configHome(home), "amp")) || dirAt(filepath.Join(home, ".amp"))
		},
	}
}

// ampPlugin is Hammock's Amp plugin, for this copy of hmk.
//
// It only reports the thread you're looking at (amp.activeThread): one Amp
// can run several threads at once (subagents', or ones you've switched
// away from), and the pane shows one. When you switch, it reports the new
// one's state.
//
// Its tool.call handler answers { action: "allow" }, which is what Amp
// takes when a plugin has no handler for it (its plugin host's default):
// it doesn't approve anything Amp would ask you about, and Amp's own
// permissions still apply.
func ampPlugin(exe string) []byte {
	q, _ := json.Marshal(exe)
	return []byte(strings.NewReplacer("__HMK__", string(q), "__MARKER__", pluginMarker).Replace(ampPluginSource))
}

const ampPluginSource = `// __MARKER__: tells Hammock what Amp is doing in a
// Hammock pane. Reinstalling Hammock's hooks rewrites this file;
// "hmk hooks install --uninstall --harness amp" removes it.
import { spawn } from "node:child_process"

export const description = "Tells Hammock what Amp is doing, when Amp runs in a Hammock pane. Written by hmk hooks install."

const HMK = __HMK__

// One at a time, in order (a turn's last tool and its end come moments
// apart), never holding Amp up: each waits for the one before, three
// seconds at most.
let queue = Promise.resolve()

function send(event, payload) {
  if (!process.env.HMK_PANE_ID) return
  let body
  try {
    body = JSON.stringify(payload ?? {})
  } catch {
    return
  }
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
          const child = spawn(HMK, ["hook", "amp", event], { stdio: ["pipe", "ignore", "ignore"] })
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

// The text of the last reply among a turn's messages.
function reply(messages) {
  if (!Array.isArray(messages)) return ""
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (m?.role !== "assistant" || !Array.isArray(m.content)) continue
    const text = m.content
      .filter((b) => b?.type === "text" && typeof b.text === "string")
      .map((b) => b.text)
      .join("\n")
      .trim()
    if (text) return text.slice(0, 4000)
  }
  return ""
}

export default function (amp) {
  if (!process.env.HMK_PANE_ID) return

  const active = () => amp.activeThread?.current?.id
  // An event of a thread you aren't looking at, when Amp says which you are.
  const elsewhere = (event) => {
    const id = active()
    return !!id && event?.thread?.id !== id
  }
  const forward = (name, payload) => send(name, payload)

  // The shown thread's state: approvals have no event of their own.
  let watching = null
  let stateSub = null
  const watch = (id) => {
    if (!id || id === watching) return
    watching = id
    try {
      stateSub?.unsubscribe?.()
    } catch {}
    stateSub = null
    let previous = ""
    try {
      stateSub = amp.threads.get(id).state.subscribe((state) => {
        if (watching !== id || state === previous) return
        forward("state", { thread: { id }, state, previous })
        previous = state
      })
    } catch {}
  }
  try {
    amp.activeThread?.subscribe?.((t) => watch(t?.id))
    watch(active())
  } catch {}

  amp.on("session.start", (event) => {
    if (elsewhere(event)) return
    forward("session.start", { thread: event.thread })
  })
  amp.on("agent.start", (event) => {
    if (!elsewhere(event)) forward("agent.start", { thread: event.thread, message: event.message })
    return {}
  })
  amp.on("tool.call", (event) => {
    if (!elsewhere(event)) forward("tool.call", { thread: event.thread, tool: event.tool, input: event.input })
    return { action: "allow" }
  })
  amp.on("tool.result", (event) => {
    if (!elsewhere(event)) forward("tool.result", { thread: event.thread, tool: event.tool, input: event.input, status: event.status })
  })
  amp.on("agent.end", (event) => {
    if (!elsewhere(event))
      forward("agent.end", { thread: event.thread, message: event.message, status: event.status, reply: reply(event.messages) })
  })
}
`
