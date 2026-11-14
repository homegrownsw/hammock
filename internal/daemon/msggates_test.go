package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// A new ClientMessage must be placed in each gate that decides who may send it and where it goes
// (AGENTS.md, "A new daemon message"). Each gate is a type switch whose default is a guess (refuse, or "not
// routed"), so one forgotten there fails only at run time, open or closed. This test fails instead: every
// message is either named in the gate's source or listed below as deliberately not there. Adding a message
// means deciding, for each gate, which of the two it is.
var gateExempt = map[string][]string{
	// share.go: what a guest's daemon (View or Drive) may send. Not named: refused to guests.
	"peerCheck": {
		"Authenticate", "BrowserCall", "BrowserLogs", "CloseGroup", "ConfigWritten", "CreateGroup", "CreateInvite",
		"CreateLinkInvite", "CreatePairing", "DeleteSchedule", "DiscoverComputers", "EndScreenSession",
		"HammockKeyCommand", "HoldPane", "JoinShared", "LeaveShared", "LinkComputer", "ListAudit", "ListDevices",
		"ListDisplays", "ListReviews", "ListSessions", "MoveGroup", "OpenWindow", "Pair", "PeerHello", "ProveApp", "ReadSession",
		"RegisterPush", "RenameGroup", "ReopenClosed", "RequestGrant", "RespondGrant", "RespondHammockKey",
		"RespondJoin", "RespondLink", "ResumeSession", "RevokeDevice", "RevokeInvite", "RunSchedule",
		"SaveSchedule", "ScreenInput", "ScreenSnapshot", "ServerStatus", "SessionExcerpts", "SetApprovalKey",
		"SetColors", "SetComputerTint", "SetGroupFolder", "SetGroupIcon", "SetGroupIconImage", "SetProfile",
		"SetRemoteAccess", "SetRemoteNetworks", "SetSchedulePaused", "ShowOutput", "ShowPairing", "ShutdownDaemon",
		"SkipScheduleRun", "SshAgentCall", "SshSetup", "StopSharing", "UnlinkComputer",
	},
	// peer.go: the group, tab or pane a message is about, so a guest's daemon sends it to the host. Not named:
	// about nothing hosted elsewhere (handled here).
	"msgTarget": {
		"Authenticate", "BrowserCall", "BrowserLogs", "CloseMedia", "ConfigWritten", "CreateGroup",
		"CreateLinkInvite", "CreatePairing", "DiscoverComputers", "EndScreenSession", "HammockKeyCommand", "Hello",
		"HoldPane", "IntroduceComputers", "JoinShared", "LeaveShared", "LinkComputer", "ListAudit", "ListDevices",
		"ListDisplays", "ListReviews", "ListSessions", "MediaAck", "MoveGroup", "OpenWindow", "Pair", "PeerHello",
		"PeerIntroLink", "PeerJoin", "PeerLinkConfirm", "PeerLinkHello", "PeerLinkReveal", "Presence", "ProveApp",
		"ReadSession", "RegisterPush", "ReopenClosed", "RequestGrant", "RespondHammockKey", "RespondJoin",
		"RespondLink", "ResumeSession", "RevokeDevice", "RevokeInvite", "ScreenInput", "ScreenSnapshot",
		"ServerStatus", "SessionExcerpts", "SetApprovalKey", "SetColors", "SetComputerTint", "SetMediaView",
		"SetMember", "SetProfile", "SetRemoteAccess", "SetRemoteNetworks", "ShowPairing", "ShutdownDaemon",
		"SshAgentCall", "SshSetup", "Subscribe", "UnlinkComputer", "UnlinkRequest",
	},
	// caller.go: what an agent in a pane may do. Not named: refused to agents.
	"agentCheck": {
		"Authenticate", "BrowserInput", "BrowserMenuChoice", "CloseMedia", "ConfigWritten", "CreateInvite",
		"CreateLinkInvite", "CreatePairing", "DiscoverComputers", "HammockKeyCommand", "HoldPane",
		"IntroduceComputers", "JoinShared", "LeaveShared", "LinkComputer", "ListDevices", "ListDisplays",
		"MediaAck", "OpenMedia", "Pair", "PeerHello", "PeerIntroLink", "PeerJoin", "PeerLinkConfirm",
		"PeerLinkHello", "PeerLinkReveal", "ProveApp", "ReadRepo", "ReadSession", "RegisterPush", "ReopenClosed",
		"RespondHammockKey", "RespondJoin", "RespondLink", "ResumeSession", "RevokeDevice", "RevokeInvite",
		"ScreenInput", "ScreenSnapshot", "SetApprovalKey", "SetComputerTint", "SetMediaView", "SetMember",
		"SetProfile", "SetRemoteAccess", "SetRemoteNetworks", "ShowPairing", "SshAgentCall", "SshSetup",
		"StopSharing", "UnlinkComputer", "UnlinkRequest", "UploadFile",
	},
	// computers.go: what your other computers may ask of this one. Not named: refused to them.
	"ownerCheck": {
		"Authenticate", "BrowserCall", "BrowserLogs", "ConfigWritten", "CreateInvite", "CreateLinkInvite",
		"CreatePairing", "DiscoverComputers", "EndScreenSession", "FloatPane", "HammockKeyCommand", "HoldPane",
		"IntroduceComputers", "JoinShared", "LeaveShared", "LinkComputer", "ListAudit", "ListDevices", "MoveGroup",
		"OpenWindow", "Pair", "PeerHello", "PeerIntroLink", "PeerJoin", "PeerLinkConfirm", "PeerLinkHello", "PeerLinkReveal",
		"ProveApp", "RegisterPush", "RequestGrant", "RespondHammockKey", "RespondJoin", "RespondLink",
		"RevokeDevice", "RevokeInvite", "ServerStatus", "SetApprovalKey", "SetColors", "SetMember", "SetProfile",
		"SetRemoteAccess", "SetRemoteNetworks", "ShowOutput", "ShowPairing", "ShutdownDaemon", "SshSetup",
		"StopSharing", "UnlinkComputer", "UnlinkRequest",
	},
}

func TestEveryClientMessageIsPlacedInEachGate(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]map[string]bool{} // gate → message types its body names
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || gateExempt[fn.Name.Name] == nil {
				continue
			}
			set := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && strings.HasPrefix(sel.Sel.Name, "ClientMessage_") {
					set[strings.TrimPrefix(sel.Sel.Name, "ClientMessage_")] = true
				}
				return true
			})
			named[fn.Name.Name] = set
		}
	}

	all := clientMessageKinds(t)
	for gate, exempt := range gateExempt {
		set, ok := named[gate]
		if !ok {
			t.Errorf("no func %s in this package: renamed? Update gateExempt and AGENTS.md", gate)
			continue
		}
		var missing, stale []string
		for _, m := range all {
			if !set[m] && !slices.Contains(exempt, m) {
				missing = append(missing, m)
			}
		}
		for _, m := range exempt {
			if set[m] || !slices.Contains(all, m) {
				stale = append(stale, m)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s doesn't place %d message(s): name each in %s, or add it to gateExempt[%q] if it's deliberately not there:\n\t%s",
				gate, len(missing), gate, gate, strings.Join(missing, "\n\t"))
		}
		if len(stale) > 0 {
			t.Errorf("gateExempt[%q] lists messages %s now names, or that no longer exist; drop them:\n\t%s",
				gate, gate, strings.Join(stale, "\n\t"))
		}
	}
}

// clientMessageKinds lists ClientMessage's oneof cases, by their generated wrapper types' names.
func clientMessageKinds(t *testing.T) []string {
	t.Helper()
	const gen = "../proto/hammockv1/hammock.pb.go"
	src, err := os.ReadFile(gen)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), gen, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "isClientMessage_Msg" || fn.Recv == nil {
			continue
		}
		if star, ok := fn.Recv.List[0].Type.(*ast.StarExpr); ok {
			if id, ok := star.X.(*ast.Ident); ok {
				kinds = append(kinds, strings.TrimPrefix(id.Name, "ClientMessage_"))
			}
		}
	}
	if len(kinds) < 50 {
		t.Fatalf("found only %d ClientMessage kinds in %s: has the generated code changed shape?", len(kinds), gen)
	}
	sort.Strings(kinds)
	return kinds
}
