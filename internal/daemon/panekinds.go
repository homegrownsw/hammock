package daemon

import (
	"fmt"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Hammock's tools shown in panes can be turned off (Settings ▸ Tools, the
// <kind>-enabled keys): someone with their own way of reviewing diffs, say, shouldn't have
// agents opening Hammock's. hmk mcp leaves a kind's tools out; this refuses
// agents what they'd do with one anyway (a session from before it was
// turned off, or hmk run by an agent). People can still open one by hand.

// specKind is the kind of pane spec makes ("" for a terminal).
func specKind(spec *pb.PaneSpec) string {
	switch {
	case spec == nil:
		return ""
	case spec.Url != "":
		return "browser" // DevTools panes too
	case spec.Simulator != "":
		return "simulator"
	case spec.Log != nil:
		return "log"
	case spec.Editor != nil:
		return "editor"
	case spec.Diff != nil:
		return "diff"
	}
	return ""
}

// msgPaneKind is the kind of pane msg opens or works with, "" if none.
func msgPaneKind(msg *pb.ClientMessage) string {
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_CreatePane:
		return specKind(m.CreatePane.Spec)
	case *pb.ClientMessage_CreateTab:
		return specKind(m.CreateTab.Pane)
	case *pb.ClientMessage_ReplacePane:
		return specKind(m.ReplacePane.Spec)
	case *pb.ClientMessage_BrowserNavigate, *pb.ClientMessage_BrowserCall, *pb.ClientMessage_BrowserLogs,
		*pb.ClientMessage_BrowserInput, *pb.ClientMessage_ShowOutput:
		return "browser"
	case *pb.ClientMessage_SimulatorCall:
		if m.SimulatorCall.Op == "devices" {
			return "" // listing them: hmk log sim finds a simulator's log by it
		}
		return "simulator"
	case *pb.ClientMessage_WatchLog, *pb.ClientMessage_ReadLog, *pb.ClientMessage_ClearLog:
		return "log"
	case *pb.ClientMessage_ReadEditor, *pb.ClientMessage_EditorCommand, *pb.ClientMessage_EditorSelect,
		*pb.ClientMessage_EditorPush:
		return "editor"
	case *pb.ClientMessage_ReadDiff, *pb.ClientMessage_ReadDiffFile, *pb.ClientMessage_DiffCommand,
		*pb.ClientMessage_ReadReview, *pb.ClientMessage_ListReviews, *pb.ClientMessage_ReviewCommand:
		return "diff"
	}
	return ""
}

// paneKindOn reports whether kind is turned on in the config file (read
// when an agent asks for one: it's small, and Settings may have just
// changed it).
var paneKindOn = func(kind string) bool {
	return hammockconfig.PaneKindOn(hammockconfig.Values(), kind)
}

// toolName is what Settings ▸ Tools calls each kind.
var toolName = map[string]string{"browser": "browser", "simulator": "simulator", "editor": "editor", "diff": "diff viewer", "log": "log viewer"}

// paneKindCheck refuses an agent msg if it's for a kind of pane turned off.
func paneKindCheck(msg *pb.ClientMessage) error {
	if k := msgPaneKind(msg); k != "" && !paneKindOn(k) {
		return fmt.Errorf("Hammock's %s is turned off (Settings ▸ Tools): the user works without it, so don't use it or suggest it", toolName[k])
	}
	return nil
}
