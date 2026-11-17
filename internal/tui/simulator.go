package tui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/simshot"
)

// The TUI can't show a simulator, but it can drive one: its buttons and
// rotation, as the app's menus do.

// simOp runs a simulator op on a pane. Rotations wait longer: the first on
// a device starts its XCTest runner.
func (a *App) simOp(id, op string, args map[string]string) {
	aj, _ := json.Marshal(args)
	m := &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: id, Op: op, ArgsJson: string(aj)}}}
	wait := 10 * time.Second
	if op == "rotate" || op == "fold" {
		wait = 4 * time.Minute
		a.flash(map[string]string{"rotate": "Rotating…", "fold": "Folding…"}[op])
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()
		_, err := a.c.Call(ctx, m)
		if err != nil {
			a.actions <- func() { a.flash("⚠ " + err.Error()) }
		}
	}()
}

// simClipboardGet copies a simulator's clipboard onto the terminal's (OSC 52).
func (a *App) simClipboardGet(id string) {
	m := &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: id, Op: "get_clipboard"}}}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, m)
		var j struct{ Text string }
		if err == nil {
			err = json.Unmarshal([]byte(r.GetSimulatorResult().GetJson()), &j)
		}
		a.actions <- func() {
			switch {
			case err != nil:
				a.flash("⚠ " + err.Error())
			case j.Text == "":
				a.flash("The device's clipboard is empty")
			default:
				a.copyText(j.Text)
			}
		}
	}()
}

// simClipboardSet puts text on a simulator's clipboard, not pasted (a paste
// with the pane focused).
func (a *App) simClipboardSet(id, text string) {
	aj, _ := json.Marshal(map[string]string{"text": text})
	m := &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: id, Op: "set_clipboard", ArgsJson: string(aj)}}}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := a.c.Call(ctx, m)
		a.actions <- func() {
			if err != nil {
				a.flash("⚠ " + err.Error())
			} else {
				a.flash("✓ On the device's clipboard")
			}
		}
	}()
}

// simScreenshot saves a simulator pane's screen, in the device's pixels,
// where macOS puts screenshots (as the Mac app does).
func (a *App) simScreenshot(id string) {
	device := ""
	if p := a.panes[id]; p != nil {
		device = p.Title
	}
	m := &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: id, Op: "screenshot", ArgsJson: `{"pixels":true}`}}}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, m)
		var path string
		if err == nil {
			path, err = simshot.Save(simshot.Folder(), simshot.FileName(device, time.Now()), r.GetSimulatorResult().GetPng())
		}
		a.actions <- func() {
			if err != nil {
				a.flash("⚠ " + err.Error())
			} else {
				a.flash("Screenshot saved to " + filepath.Base(filepath.Dir(path)))
			}
		}
	}()
}

// isTVPane reports whether a pane shows a TV (an Apple TV, or an Android TV
// emulator), which its remote drives (it has no touch screen).
func isTVPane(p *pb.Pane) bool {
	return p != nil && p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR && (p.DevicePlatform == "tvOS" || p.DevicePlatform == "Android TV")
}

// tvKeys are the keys that are an Apple TV remote's buttons (edKey names;
// a space is Play/Pause).
var tvKeys = map[string]string{"up": "up", "down": "down", "left": "left", "right": "right", "enter": "select", "esc": "menu", " ": "play_pause"}

// tvEditKeys are keys without text a text field takes, as HID keyboard
// usages (as the Mac sends them).
var tvEditKeys = map[string]int{"backspace": 42, "tab": 43, "delete": 76}

// tvInput sends typed bytes to a focused Apple TV pane: its remote's keys
// as its remote's presses, anything else typed (for its text fields).
// Send, not Call, keeps them in order.
func (a *App) tvInput(id string, b []byte) {
	if !canDrive(a.paneGroup(id)) {
		return
	}
	send := func(op string, args map[string]any) {
		aj, _ := json.Marshal(args)
		_ = a.c.Send(bgctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: id, Op: op, ArgsJson: string(aj)}}})
	}
	for _, k := range editorKeysOf(b) {
		name := k.name
		if name == "" && k.r == ' ' {
			name = " "
		}
		switch {
		case tvKeys[name] != "" && !k.ctrl && !k.alt:
			// A terminal doesn't say when a key's let go: a whole press each time.
			send("remote", map[string]any{"name": tvKeys[name], "phase": "down"})
			send("remote", map[string]any{"name": tvKeys[name], "phase": "up"})
		case tvEditKeys[name] != 0:
			send("key", map[string]any{"usage": tvEditKeys[name], "shift": k.shift})
		case k.name == "" && k.r != 0:
			send("text", map[string]any{"text": string(k.r)})
		}
	}
	a.liveSoon(id)
}

// simItems: what you can do to a simulator pane, for its menu and the palette.
func (a *App) simItems(id string) []menuItem {
	if isTVPane(a.panes[id]) {
		press := func(label, name string) menuItem {
			return menuItem{label: label, run: func() { a.simOp(id, "remote", map[string]string{"name": name}) }}
		}
		items := []menuItem{
			press("Home (TV)", "home"), press("Back", "menu"), press("Select", "select"), press("Play/Pause", "play_pause"),
			press("Volume Up", "volume_up"), press("Volume Down", "volume_down"), press("Mute", "mute"),
		}
		if a.panes[id].DevicePlatform == "tvOS" {
			items = append(items, press("Siri", "siri"))
		}
		if a.panes[id].DeviceElsewhere {
			items = append(items, startItem(a, id))
		}
		if a.panes[id].DevicePlatform == "Android TV" {
			items = append(items, menuItem{label: "Copy Device Clipboard", run: func() { a.simClipboardGet(id) }}) // an Apple TV has none
		}
		return append(items, menuItem{label: "Screenshot to File", run: func() { a.simScreenshot(id) }})
	}
	if p := a.panes[id]; p != nil && p.DevicePlatform == "Android" {
		button := func(label, name string) menuItem {
			return menuItem{label: label, run: func() { a.simOp(id, "button", map[string]string{"name": name}) }}
		}
		items := []menuItem{
			button("Back", "back"), button("Home", "home"), button("Recents", "recents"), button("Power", "power"),
			button("Volume Up", "volume_up"), button("Volume Down", "volume_down"),
			{label: "Rotate Left", run: func() { a.simOp(id, "rotate", map[string]string{"value": "left"}) }},
			{label: "Rotate Right", run: func() { a.simOp(id, "rotate", map[string]string{"value": "right"}) }},
			{label: "Copy Device Clipboard", run: func() { a.simClipboardGet(id) }},
			{label: "Screenshot to File", run: func() { a.simScreenshot(id) }},
		}
		if p.DeviceFoldable {
			items = append(items,
				menuItem{label: "Open Phone", run: func() { a.simOp(id, "fold", map[string]string{"value": "open"}) }},
				menuItem{label: "Close Phone", run: func() { a.simOp(id, "fold", map[string]string{"value": "closed"}) }})
		}
		if p.DeviceElsewhere {
			items = append(items, startItem(a, id))
		}
		return items
	}
	items := []menuItem{
		{label: "Home", run: func() { a.simOp(id, "button", map[string]string{"name": "home"}) }},
		{label: "Lock", run: func() { a.simOp(id, "button", map[string]string{"name": "lock"}) }},
		{label: "Rotate Left", run: func() { a.simOp(id, "rotate", map[string]string{"value": "left"}) }},
		{label: "Rotate Right", run: func() { a.simOp(id, "rotate", map[string]string{"value": "right"}) }},
		{label: "Toggle Software Keyboard", run: func() { a.simOp(id, "button", map[string]string{"name": "keyboard"}) }},
		{label: "Copy Device Clipboard", run: func() { a.simClipboardGet(id) }},
		// The screenshot not to the clipboard: OSC 52 only carries text.
		{label: "Screenshot to File", run: func() { a.simScreenshot(id) }},
	}
	if p := a.panes[id]; p != nil && p.DeviceFoldable {
		items = append(items,
			menuItem{label: "Open Phone", run: func() { a.simOp(id, "fold", map[string]string{"value": "open"}) }},
			menuItem{label: "Half Open Phone", run: func() { a.simOp(id, "fold", map[string]string{"value": "half"}) }},
			menuItem{label: "Close Phone", run: func() { a.simOp(id, "fold", map[string]string{"value": "closed"}) }})
	}
	return items
}

// startItem starts an Android emulator in Hammock, as its pane offers:
// restarting it there (stopping it in Android Studio), or booting a stopped one.
func startItem(a *App, id string) menuItem {
	label := "Start in Hammock"
	if p := a.panes[id]; p != nil && p.DeviceNote == "Running in Android Studio" {
		label = "Restart in Hammock (stops it in Android Studio)"
	}
	return menuItem{label: label, run: func() { a.simOp(id, "takeover", nil) }}
}
