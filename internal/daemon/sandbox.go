package daemon

import (
	"errors"

	"github.com/homegrownsw/hammock/internal/codesign"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Sandboxed callers. An agent harness that sandboxes the commands it runs
// (Claude Code's and Codex's Bash on macOS, under Seatbelt) runs them
// without asking: the sandbox is what keeps them in. A pane's command runs
// outside it (under the pane's holder), so hmk run from that sandbox could
// run anything, or type it into a pane, beyond the sandbox, unasked. So a
// caller in a sandbox needs your grant for anything that runs or types
// something, its own panes and Groups included (SandboxGrant); reading and
// organising stay free. It's scoped as an agent whatever it is: something
// sandboxed outside Hammock's panes, or under a pane's own shell, too.
//
// Told on macOS, by the connecting process's audit token (sandboxes are
// inherited and can't be left, so whatever a sandboxed program starts is
// sandboxed). Not on Linux yet (codesign.PIDSandboxed): a sandboxed agent's
// commands there are taken as the agent's.

// The grant actions a sandboxed caller asks for: typing into a pane (or
// answering, prompting or switching an agent there), and running a command
// (a new terminal, an agent, a log pane's command, a simulator's app).
const (
	grantSandboxSend = "sandbox-send"
	grantSandboxRun  = "sandbox-run"
)

// SandboxRefusal starts the error a sandboxed caller gets for what needs
// your grant: hmk asks for it (SandboxGrant) and tries again.
const SandboxRefusal = "out of scope (sandboxed)"

// sandboxedWho follows the agent's name in a request worded around
// something else (a schedule): "Claude in Web ▸ api (in a sandbox: …)".
const sandboxedWho = " (in a sandbox: this runs outside it)"

var errSandboxed = errors.New(SandboxRefusal + ": this runs or types something outside your sandbox, so it needs the user's permission")

// sandboxedHook, when set (tests), stands in for asking macOS whether a
// local connection's process is sandboxed.
var sandboxedHook func(lp localPeer) bool

// peerSandboxed reports whether a local connection's process is sandboxed:
// by its audit token, as the listener took it (the one process at both
// ends of its first message: localPeerOf), else by its pid.
func peerSandboxed(lp localPeer) bool {
	if sandboxedHook != nil {
		return sandboxedHook(lp)
	}
	if lp.tok.Valid() {
		return lp.tok.Sandboxed()
	}
	return codesign.PIDSandboxed(lp.pid)
}

// identifyPeer is identify, and whether the process is sandboxed: one that
// is, in a pane, is scoped as an agent (one outside the panes is, by scope).
func (d *Daemon) identifyPeer(lp localPeer) (*caller, bool) {
	cl := d.identify(lp.pid)
	sandboxed := lp.pid > 0 && peerSandboxed(lp)
	if sandboxed && cl != nil {
		cp := *cl
		cp.agent, cp.sandboxed = true, true
		cl = &cp
	}
	return cl, sandboxed
}

// SandboxGrant is the grant a sandboxed caller's msg needs, if it runs or
// types anything: the action, and the pane it's asked about ("" for the
// caller's own: a new tab, an agent in a Group). Browser panes, editors,
// diffs and the rest run nothing of the caller's.
func SandboxGrant(msg *pb.ClientMessage) (action, target string, ok bool) {
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Input:
		return grantSandboxSend, m.Input.PaneId, true
	case *pb.ClientMessage_AgentSend:
		return grantSandboxSend, m.AgentSend.PaneId, true
	case *pb.ClientMessage_AgentAnswer:
		return grantSandboxSend, m.AgentAnswer.PaneId, true
	case *pb.ClientMessage_SwitchAgent:
		return grantSandboxSend, m.SwitchAgent.PaneId, true
	case *pb.ClientMessage_CreatePane:
		if specRuns(m.CreatePane.Spec) {
			return grantSandboxRun, m.CreatePane.SplitPaneId, true
		}
	case *pb.ClientMessage_CreateTab:
		if specRuns(m.CreateTab.Pane) {
			return grantSandboxRun, "", true
		}
	case *pb.ClientMessage_ReplacePane:
		if specRuns(m.ReplacePane.Spec) {
			return grantSandboxRun, m.ReplacePane.PaneId, true
		}
	case *pb.ClientMessage_StartAgent:
		return grantSandboxRun, m.StartAgent.SplitPaneId, true
	case *pb.ClientMessage_SimulatorCall:
		// An app installed or launched on a simulator runs on this Mac.
		switch m.SimulatorCall.Op {
		case "install", "launch":
			return grantSandboxRun, m.SimulatorCall.PaneId, true
		}
	}
	return "", "", false
}

// specRuns reports whether a new pane made from spec runs a command: a
// terminal (a plain shell too: its folder and environment are the
// caller's to choose, and a shell's startup runs what's there), or a log
// pane following a command's output.
func specRuns(spec *pb.PaneSpec) bool {
	switch {
	case spec == nil:
		return true
	case spec.Url != "", spec.Simulator != "", spec.Editor != nil, spec.Diff != nil:
		return false
	case spec.Log != nil:
		return spec.Log.Kind == "command"
	}
	return true
}

// sandboxCheck holds a sandboxed caller's message, once agentCheck's scoping
// has let it through or found it out of scope, to your grant (SandboxGrant). A
// grant for a pane (typing into it, running something beside it) is also
// your leave for what scope asks about that pane, so it's asked once.
func (c *conn) sandboxCheck(msg *pb.ClientMessage, scoped error) error {
	cl := c.caller
	if cl == nil || !cl.sandboxed || scoped != nil && !errors.Is(scoped, errAgentScope) {
		return scoped
	}
	action, target, ok := SandboxGrant(msg)
	if !ok {
		return scoped
	}
	self := cl.pane
	if target == "" {
		target = self
	}
	if scoped != nil && target == self {
		return scoped // out of scope whatever you allow (another's Group): nothing to ask
	}
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.granted(self, action, target) {
		return errSandboxed
	}
	return nil
}
