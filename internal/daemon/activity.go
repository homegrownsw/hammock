package daemon

import (
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
)

// Activity (Settings ▸ Devices, hmk audit): the audit log in words. Each
// event is described when it happens, while its panes, tabs and Groups
// still have their names: who did it ("Claude in Web ▸ api", "iPhone",
// "Duncan") and what ("typed in Web ▸ api ▸ pane 2").

// audit records an event. Callers hold d.mu.
func (d *Daemon) audit(actor, action, target, detail string) {
	d.persist(d.store.Audit(*d.auditEvent(actor, action, target, detail)))
}

// auditEvent is an event described now, to record. Callers hold d.mu.
func (d *Daemon) auditEvent(actor, action, target, detail string) *state.AuditEvent {
	who, summary, kind := d.describeAudit(actor, action, target, detail)
	return &state.AuditEvent{Actor: actor, Action: action, Target: target, Detail: detail, Who: who, Summary: summary, Kind: kind}
}

// closes reports whether msg closes a pane or tab (described before it goes).
func closes(msg *pb.ClientMessage) bool {
	switch msg.Msg.(type) {
	case *pb.ClientMessage_ClosePane, *pb.ClientMessage_CloseTab:
		return true
	}
	return false
}

// auditActor is who c is, for the audit log: a guest, an agent (by its
// pane), else its client kind and name ("ios:iPhone", "cli:hmk").
// Callers hold d.mu.
func (c *conn) auditActor() string {
	switch {
	case c.peer != "":
		return "guest:" + c.peer
	case c.isAgent() || c.caller.isAgent():
		if p := c.fromPane; p != "" {
			return "agent:" + p
		}
		if c.caller != nil && c.caller.pane != "" {
			return "agent:" + c.caller.pane
		}
		return "agent:"
	}
	return c.kind + ":" + c.name
}

// describeAudit puts an event in words. Callers hold d.mu.
func (d *Daemon) describeAudit(actor, action, target, detail string) (who, summary, kind string) {
	who, kind = d.auditWho(actor)
	pane := func() string { return d.auditPane(target) }
	switch action {
	case "input":
		summary = "typed in " + pane()
	case "create-pane":
		summary = "opened a pane beside " + pane()
	case "create-tab":
		summary = "opened a tab in " + d.groupLabel(target)
	case "close-pane":
		summary = "closed " + pane()
	case "close-tab":
		summary = "closed the tab " + d.tabLabel(target)
	case "signal":
		summary = "interrupted " + pane()
	case "browse":
		summary = "opened a page in " + pane()
	case "answer":
		summary = "answered a question in " + pane()
	case "message":
		summary = "sent a message to " + pane()
	case "upload":
		summary = "uploaded a file to " + pane()
	case "clear-log":
		summary = "cleared the log in " + pane()
	case "start-agent":
		h, dir, _ := strings.Cut(target, " in ")
		summary = "started " + capitalize(h) + " in " + client.DirTitle(dir)
	case "group-picture":
		summary = "changed the picture of " + d.groupLabel(target)
	case "switch-agent":
		summary = "switched the agent in " + pane() + " (" + detail + ")"
	case "ssh-sign":
		// Here: a program in a pane signed with a key on the computer you were at.
		summary = "used your SSH key " + detail + " in " + pane()
	case "screen-share":
		summary = "started sharing this Mac's screen"
	case "screen-ended":
		summary = "ended screen sharing"
	case "ssh-lent":
		// There: this computer's key, used by a pane on a linked computer.
		summary = "used your SSH key " + detail

	case "grant-auto":
		summary, kind = "was allowed (already permitted): "+grantWhat(detail), "agent"
	case "grant-requested":
		summary, kind = "asked for permission: "+grantWhat(detail), "agent"
	case "grant-allowed":
		summary = "allowed: " + grantWhat(detail)
	case "grant-denied":
		summary = "denied: " + grantWhat(detail)

	case "pairing-shown":
		summary = "showed the pairing code"
	case "pairing-refused":
		summary, kind = "couldn't show the pairing code: "+detail, "security"
	case "pair-rejected":
		summary, kind = "tried to pair with a wrong or expired code", "security"
	case "paired":
		summary = "was paired with this Mac"
	case "auth-failed":
		summary, kind = "failed to sign in", "security"
		if detail != "" {
			summary += " (" + detail + ")"
		}
	case "device-revoked":
		name := detail
		if name == "" {
			name = "a device"
		}
		summary = "removed " + name

	case "connected":
		summary = "connected to share Groups"
	case "join-rejected":
		summary, kind = "tried to join with an invalid invite", "security"
	case "join-requested":
		summary = "asked to join " + d.groupLabel(target)
	case "join-declined":
		summary = "declined " + detail + " joining " + d.groupLabel(target)
	case "join-approved":
		summary = "let " + detail + " join " + d.groupLabel(target)
	case "member-changed":
		summary = "changed what " + detail + " can do in " + d.groupLabel(target)
	case "member-removed":
		summary = "removed " + detail + " from " + d.groupLabel(target)
	case "config-loosened":
		// A security setting loosened by an edit to the config file (configwatch.go).
		who, summary, kind = "Config file", detail, "security"
	case "sharing-stopped":
		summary = "stopped sharing " + d.groupLabel(target)
	case "left":
		summary = "left " + d.groupLabel(target)

	default:
		if op, ok := strings.CutPrefix(action, "browser "); ok {
			summary = browserWhat(op) + " " + pane()
		} else if op, ok := strings.CutPrefix(action, "simulator "); ok {
			summary = simulatorWhat(op) + " " + pane()
		} else {
			summary = action
			if target != "" {
				summary += " " + target
			}
		}
	}
	return who, summary, kind
}

// auditWho names an actor, and says what kind it is.
func (d *Daemon) auditWho(actor string) (who, kind string) {
	k, name, _ := strings.Cut(actor, ":")
	switch k {
	case "user":
		return "You", "you"
	case "agent", "mcp":
		// mcp:<pane> from grants; agent:<pane> from the rest.
		if p := d.panes[name]; p != nil {
			w := "An agent"
			if a := p.snapshotInfo().Agent; a != nil && a.Harness != "" {
				w = capitalize(a.Harness)
			}
			if path := d.tabPath(name); path != "" {
				w += " in " + path
			}
			return w, "agent"
		}
		return "An agent", "agent"
	case "ios", "device", "remote":
		switch name {
		case "", "ios":
			if k == "remote" {
				return "A device", "device"
			}
			return "Your iPhone", "device"
		}
		return name, "device"
	case "guest":
		if p := d.people[name]; p != nil && p.Name != "" {
			return p.Name, "guest"
		}
		return "A guest", "guest"
	case "computer":
		// A linked computer (by its user id): its name as last known.
		return d.computerName(name), "you"
	case "peer":
		if name == "" {
			return "Another Mac", "guest"
		}
		return name, "guest"
	case "cli":
		return "hmk (command line)", "you"
	case "tui":
		return "hmk tui", "you"
	case "mac":
		return "You", "you"
	}
	return actor, ""
}

// auditPane names a pane where it is, or what it was.
func (d *Daemon) auditPane(id string) string {
	if d.panes[id] == nil {
		return "a pane that's closed"
	}
	return d.paneLabel(id)
}

// tabLabel names a tab: "Group ▸ tab".
func (d *Daemon) tabLabel(id string) string {
	t := d.tabs[id]
	if t == nil {
		return "(closed)"
	}
	for _, pid := range tabPanes(t) {
		if s := d.tabPath(pid); s != "" {
			return s
		}
	}
	return client.TabTitle(t, d.groups[t.GroupId])
}

// groupLabel names a Group as the sidebar does.
func (d *Daemon) groupLabel(id string) string {
	g := d.groups[id]
	if g == nil {
		return "a Group"
	}
	if g.Name != "" {
		return g.Name
	}
	if lp := d.panes[g.LastFocusedPaneId]; lp != nil {
		if cwd := lp.snapshotInfo().Cwd; cwd != "" {
			return client.DirTitle(cwd)
		}
	}
	if g.Folder != "" {
		return client.DirTitle(g.Folder)
	}
	return "a Group"
}

// grantWhat is a grant's request without its requester ("Claude in X
// wants to read Y" → "read Y"), the agent's own words kept.
func grantWhat(detail string) string {
	if _, rest, ok := strings.Cut(detail, " wants to "); ok {
		return rest
	}
	return detail
}

// browserWhat says what a DevTools method or a guest's input does.
func browserWhat(op string) string {
	switch op {
	case "Runtime.evaluate", "Runtime.callFunctionOn":
		return "ran JavaScript in"
	case "Input.dispatchMouseEvent", "mouse":
		return "clicked in"
	case "Input.dispatchKeyEvent", "Input.insertText", "text", "key":
		return "typed in"
	case "cut":
		return "cut text in"
	case "Page.navigate":
		return "went to a page in"
	case "Page.reload":
		return "reloaded"
	case "Emulation.setDeviceMetricsOverride":
		return "resized the page in"
	}
	return "used the browser (" + op + ") in"
}

// simulatorWhat says what a simulator op does.
func simulatorWhat(op string) string {
	switch op {
	case "touch", "tap":
		return "tapped in"
	case "swipe":
		return "swiped in"
	case "type", "text", "key":
		return "typed in"
	case "button":
		return "pressed a button in"
	case "rotate":
		return "rotated"
	case "pinch":
		return "pinched in"
	case "fold":
		return "folded"
	case "install":
		return "installed an app in"
	case "launch":
		return "launched an app in"
	case "terminate":
		return "quit an app in"
	case "open-url", "openurl":
		return "opened a link in"
	case "shutdown":
		return "shut down"
	}
	return "used the simulator (" + op + ") in"
}

// activity is the log in words, newest first, with runs of the same thing
// by the same one (typing in a pane, say) as one event with a count.
// Callers hold d.mu (to describe events from before descriptions).
func (d *Daemon) activity(events []state.AuditEvent) []*pb.AuditEvent {
	const run = 10 * time.Minute
	var out []*pb.AuditEvent
	for _, e := range events {
		who, summary, kind := e.Who, e.Summary, e.Kind
		if summary == "" {
			who, summary, kind = d.describeAudit(e.Actor, e.Action, e.Target, e.Detail)
		}
		ms := e.Time.UnixMilli()
		if n := len(out); n > 0 {
			last := out[n-1]
			if last.Who == who && last.Summary == summary && last.FirstUnixMs-ms < run.Milliseconds() {
				last.Count++
				last.FirstUnixMs = ms
				continue
			}
		}
		out = append(out, &pb.AuditEvent{TimeUnixMs: ms, FirstUnixMs: ms, Count: 1, Actor: e.Actor, Action: e.Action, Target: e.Target, Detail: e.Detail,
			Who: who, Summary: summary, Kind: kind})
	}
	return out
}
