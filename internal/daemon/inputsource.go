package daemon

import "time"

// Where a pane's input last came from, for approval requests (approval.go):
// a person-only request from a pane that something other than you, in the
// app, typed into lately says so on its card. A program of yours that may
// type into your shell's pane (hmk send) could otherwise type `hmk pair
// --print` there, and the card would read as yours.

// inputRecent is how long other input counts as lately, whatever you typed
// since; input you haven't typed after counts however old it is.
const inputRecent = 30 * time.Second

// inputSrc is who typed: kind "" is you (the app), else
// "device" (a paired phone), "peer" (a linked computer or a guest),
// "agent" (or a caller scoped as one), "program" (a plain caller: hmk in a
// shell, a script, the TUI by the kind it says), or "hammock" (Hammock itself, giving an agent its
// first prompt).
type inputSrc struct {
	kind, name, peer, pane string
}

// inputSourceOf says who c is, as the one typing. Read from c's own
// handlers (its caller is settled by then).
func inputSourceOf(c *conn) inputSrc {
	switch {
	case c == nil:
		return inputSrc{kind: "hammock"}
	case c.peer != "":
		return inputSrc{kind: "peer", name: c.name, peer: c.peer}
	case c.remote:
		return inputSrc{kind: "device", name: c.name}
	case c.personOK.Load():
		return inputSrc{}
	}
	cl := c.caller
	if c.kind == "mcp" || cl.isAgent() {
		s := inputSrc{kind: "agent"}
		if cl != nil && cl.pane != outsidePane {
			s.pane = cl.pane
		}
		return s
	}
	// Not the client's own say: a kind is whatever it sends in Hello, and a
	// plain caller saying "tui" would otherwise reset "you typed last" as it
	// types `hmk pair --print⏎` into your shell. The TUI, a program in a
	// terminal like any other, is told apart from one only by its name.
	s := inputSrc{kind: "program"}
	if c.kind == "tui" {
		s.name = "hmk tui"
	}
	if cl != nil && cl.pane != outsidePane {
		s.pane = cl.pane
	}
	return s
}

// paneInput is what a pane keeps of its input's sources (under its mu).
type paneInput struct {
	other   inputSrc
	otherAt time.Time
	youAt   time.Time
}

// noteLocked records input from s. Callers hold p.mu.
func (in *paneInput) noteLocked(s inputSrc, now time.Time) {
	if s.kind == "" {
		in.youAt = now
		return
	}
	in.other, in.otherAt = s, now
}

// recentInputFrom is who other than you typed into pane lately (inputSrc
// with kind "" if nobody did).
func (p *pane) recentInputFrom(now time.Time) inputSrc {
	p.mu.Lock()
	defer p.mu.Unlock()
	in := p.typedBy
	if in.otherAt.IsZero() || now.Sub(in.otherAt) > inputRecent && !in.otherAt.After(in.youAt) {
		return inputSrc{}
	}
	return in.other
}

// inputOrigin says, after "after typing from", who s is. Callers hold d.mu.
func (d *Daemon) inputOrigin(s inputSrc) string {
	switch s.kind {
	case "device":
		return "your device “" + cmpOr(oneLine(s.name, 60), "unnamed") + "”"
	case "peer":
		if d.isLinked(s.peer) {
			return "your linked computer “" + cmpOr(oneLine(s.name, 60), "unnamed") + "”"
		}
		return "“" + cmpOr(oneLine(s.name, 60), "someone") + "”, who you share a Group with"
	case "agent":
		if s.pane != "" && d.panes[s.pane] != nil {
			return "the agent in " + d.paneLabel(s.pane)
		}
		return "a program Hammock treats as an agent"
	case "program":
		how := "hmk send, or a script"
		if s.name != "" {
			how = s.name + ", or a program saying it's that"
		}
		if s.pane != "" && d.panes[s.pane] != nil {
			return "a program in " + d.paneLabel(s.pane) + " (" + how + ")"
		}
		return "a program outside the app (" + how + ")"
	case "hammock":
		return "Hammock, starting an agent"
	}
	return ""
}
