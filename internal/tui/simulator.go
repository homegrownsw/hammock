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

// simItems: what you can do to a simulator pane, for its menu and the palette.
func (a *App) simItems(id string) []menuItem {
	items := []menuItem{
		{label: "Home", run: func() { a.simOp(id, "button", map[string]string{"name": "home"}) }},
		{label: "Lock", run: func() { a.simOp(id, "button", map[string]string{"name": "lock"}) }},
		{label: "Rotate Left", run: func() { a.simOp(id, "rotate", map[string]string{"value": "left"}) }},
		{label: "Rotate Right", run: func() { a.simOp(id, "rotate", map[string]string{"value": "right"}) }},
		{label: "Toggle Software Keyboard", run: func() { a.simOp(id, "button", map[string]string{"name": "keyboard"}) }},
		// Not to the clipboard: OSC 52 only carries text.
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
