package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Simulator tools: agents drive iOS simulators shown in Hammock's simulator
// panes. The picture and input go through the daemon to the simulator host;
// app and log work runs simctl directly. Same policy as other panes: look
// at any in your Groups, act in ones you opened, ask for the rest.

type simOpenArgs struct {
	Device    string `json:"device,omitempty" jsonschema:"device name or UDID (see sim_list_devices); default: a running one, else the newest iPhone"`
	Direction string `json:"direction,omitempty" jsonschema:"'right' (default) or 'down' — where to split relative to split_pane_id"`
	SplitPane string `json:"split_pane_id,omitempty" jsonschema:"pane to split; defaults to your own pane"`
	NewTab    bool   `json:"new_tab,omitempty" jsonschema:"open in a new tab instead of splitting"`
}

type simPaneArgs struct {
	PaneID string `json:"pane_id" jsonschema:"the simulator pane"`
}

type simTapArgs struct {
	PaneID string  `json:"pane_id" jsonschema:"the simulator pane"`
	X      float64 `json:"x" jsonschema:"in points, as in sim_screenshot"`
	Y      float64 `json:"y" jsonschema:"in points, as in sim_screenshot"`
}

type simSwipeArgs struct {
	PaneID string  `json:"pane_id" jsonschema:"the simulator pane"`
	X      float64 `json:"x" jsonschema:"start, in points"`
	Y      float64 `json:"y" jsonschema:"start, in points"`
	X2     float64 `json:"x2" jsonschema:"end, in points"`
	Y2     float64 `json:"y2" jsonschema:"end, in points"`
	Ms     int     `json:"duration_ms,omitempty" jsonschema:"default 300"`
}

type simTypeArgs struct {
	PaneID string `json:"pane_id" jsonschema:"the simulator pane"`
	Text   string `json:"text" jsonschema:"typed on the hardware keyboard into whatever has focus (US layout; \\n presses Return)"`
}

type simButtonArgs struct {
	PaneID string `json:"pane_id" jsonschema:"the simulator pane"`
	Button string `json:"button" jsonschema:"home, lock, side, siri, or keyboard (shows or hides the software keyboard)"`
}

type simRotateArgs struct {
	PaneID      string `json:"pane_id" jsonschema:"the simulator pane"`
	Orientation string `json:"orientation" jsonschema:"left or right (a quarter turn of the device), or portrait, upside-down, landscape-left, landscape-right"`
}

type simFoldArgs struct {
	PaneID string   `json:"pane_id" jsonschema:"the simulator pane (a foldable, such as the iPhone Duo)"`
	Fold   string   `json:"fold,omitempty" jsonschema:"open, closed or half (90°); or give angle"`
	Angle  *float64 `json:"angle,omitempty" jsonschema:"the hinge in degrees, 0 (closed) to 180 (flat)"`
}

type simPinchArgs struct {
	PaneID string  `json:"pane_id" jsonschema:"the simulator pane"`
	X      float64 `json:"x" jsonschema:"the pinch's centre, in points"`
	Y      float64 `json:"y" jsonschema:"the pinch's centre, in points"`
	Scale  float64 `json:"scale" jsonschema:"above 1 spreads the fingers (zoom in), below 1 pinches them together (zoom out): e.g. 2 or 0.5"`
	Ms     int     `json:"duration_ms,omitempty" jsonschema:"default 400"`
}

type simAppArgs struct {
	PaneID string   `json:"pane_id" jsonschema:"the simulator pane"`
	App    string   `json:"app" jsonschema:"install: path to a built .app; launch/terminate: bundle id"`
	Args   []string `json:"args,omitempty" jsonschema:"launch: arguments for the app"`
}

type simURLArgs struct {
	PaneID string `json:"pane_id" jsonschema:"the simulator pane"`
	URL    string `json:"url" jsonschema:"a web page or a deep link (myapp://…)"`
}

type simLogArgs struct {
	PaneID    string `json:"pane_id" jsonschema:"the simulator pane"`
	Predicate string `json:"predicate,omitempty" jsonschema:"a log predicate, e.g. 'process == \"MyApp\"' or 'subsystem == \"com.example\"'"`
	Last      string `json:"last,omitempty" jsonschema:"how far back, e.g. '30s', '5m' (default 1m)"`
}

// simPane checks pane id is a simulator pane the caller may use (look or
// act) and returns its device's UDID.
func (s *mcpServer) simPane(ctx context.Context, id string, control bool, what string) (string, error) {
	w, err := s.world(ctx)
	if err != nil {
		return "", err
	}
	p := w.panes[id]
	if p == nil {
		return "", fmt.Errorf("no such pane %q", id)
	}
	if p.Kind != pb.PaneKind_PANE_KIND_SIMULATOR {
		return "", fmt.Errorf("%s isn't a simulator pane (open one with sim_open)", id)
	}
	if control {
		err = s.ensure(ctx, s.owns(w, id), "simulator", id, what)
	} else {
		err = s.ensure(ctx, s.inScope(w, id), "read", id, what)
	}
	return p.DeviceUdid, err
}

func (s *mcpServer) sim(ctx context.Context, pane, op string, args any) (*pb.SimulatorResult, error) {
	wait := 45 * time.Second
	if op == "rotate" || op == "pinch" || op == "gesture" {
		wait = 4 * time.Minute // the first one starts the device's XCTest runner
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return simCall(func(m *pb.ClientMessage) (*pb.ServerMessage, error) { return s.send(ctx, m) }, pane, op, args)
}

func simctl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "xcrun", append([]string{"simctl"}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("simctl %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *mcpServer) registerSimulator(server *mcp.Server) {
	caller := func(ctx context.Context) caller {
		return func(m *pb.ClientMessage) (*pb.ServerMessage, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return s.send(ctx, m)
		}
	}

	addTool(s, server, &mcp.Tool{
		Name:        "sim_list_devices",
		Description: "List this Mac's iOS simulators (name, OS, UDID, whether running).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listArgs) (*mcp.CallToolResult, any, error) {
		devs, err := listSimulators(caller(ctx))
		if err != nil {
			return nil, nil, err
		}
		return textResult(devs), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name: "sim_open",
		Description: "Open an iOS simulator in a pane in the user's workspace (split next to your pane, or in a new tab), booting it if needed. " +
			"Returns the pane id for the other sim_ tools.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simOpenArgs) (*mcp.CallToolResult, any, error) {
		devs, err := listSimulators(caller(ctx))
		if err != nil {
			return nil, nil, err
		}
		dev, err := pickSimulator(devs, a.Device)
		if err != nil {
			return nil, nil, err
		}
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		spec := &pb.PaneSpec{Simulator: dev.UDID, CreatedByPaneId: s.self}
		var id string
		if a.NewTab || s.self == "" {
			group := w.groupOfPane(s.self)
			if group == "" {
				if len(w.groups) == 0 {
					return nil, nil, errors.New("no Group to open a tab in")
				}
				group = w.groups[0].Id
			}
			r, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group, Pane: spec}}})
			if err != nil {
				return nil, nil, err
			}
			w2, err := s.world(ctx)
			if err != nil {
				return nil, nil, err
			}
			if t := w2.tabs[client.CreatedID(r)]; t != nil {
				id = t.FocusedPaneId
			}
		} else {
			target := a.SplitPane
			if target == "" {
				target = s.self
			}
			if err := s.ensure(ctx, s.inScope(w, target), "create", target, "open the "+dev.Name+" simulator"); err != nil {
				return nil, nil, err
			}
			dir := pb.Split_DIRECTION_HORIZONTAL
			if a.Direction == "down" {
				dir = pb.Split_DIRECTION_VERTICAL
			}
			r, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: target, Direction: dir, Spec: spec}}})
			if err != nil {
				return nil, nil, err
			}
			id = client.CreatedID(r)
		}
		if id == "" {
			return nil, nil, errors.New("the pane didn't open")
		}
		// Wait for it to boot and show something.
		deadline := time.Now().Add(90 * time.Second)
		status := "booting"
		for time.Now().Before(deadline) {
			if _, err := s.sim(ctx, id, "screenshot", nil); err == nil {
				status = "running"
				break
			}
			time.Sleep(time.Second)
		}
		return textResult(map[string]string{"pane_id": id, "device": dev.Name, "udid": dev.UDID, "status": status}), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_screenshot",
		Description: "Screenshot a simulator pane's screen, in points (the coordinates sim_tap and sim_swipe take).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simPaneArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, false, "take a screenshot"); err != nil {
			return nil, nil, err
		}
		r, err := s.sim(ctx, a.PaneID, "screenshot", nil)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: r.GetPng(), MIMEType: "image/png"}}}, nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_tap",
		Description: "Tap a simulator's screen at a point (in points, as in sim_screenshot).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simTapArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "tap the screen"); err != nil {
			return nil, nil, err
		}
		if _, err := s.sim(ctx, a.PaneID, "tap", map[string]float64{"x": a.X, "y": a.Y}); err != nil {
			return nil, nil, err
		}
		return textResult(fmt.Sprintf("Tapped %.0f,%.0f", a.X, a.Y)), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_swipe",
		Description: "Swipe on a simulator's screen from one point to another (in points): scroll, drag, pull to refresh.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simSwipeArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "swipe on the screen"); err != nil {
			return nil, nil, err
		}
		if _, err := s.sim(ctx, a.PaneID, "swipe", map[string]any{"x": a.X, "y": a.Y, "x2": a.X2, "y2": a.Y2, "ms": a.Ms}); err != nil {
			return nil, nil, err
		}
		return textResult("Swiped"), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_type",
		Description: "Type text into whatever has focus on a simulator (tap a text field first).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simTypeArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "type"); err != nil {
			return nil, nil, err
		}
		if _, err := s.sim(ctx, a.PaneID, "text", map[string]string{"text": a.Text}); err != nil {
			return nil, nil, err
		}
		return textResult("Typed"), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_press_button",
		Description: "Press a simulator's hardware button: home (go to the home screen), lock, side or siri; or keyboard, which shows or hides the software keyboard.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simButtonArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "press "+a.Button); err != nil {
			return nil, nil, err
		}
		if _, err := s.sim(ctx, a.PaneID, "button", map[string]string{"name": a.Button}); err != nil {
			return nil, nil, err
		}
		return textResult("Pressed " + a.Button), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_rotate",
		Description: "Rotate a simulator: a quarter turn left or right, or to an orientation. The first rotation or pinch on a device can take a while (Hammock starts an XCTest runner on it).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simRotateArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "rotate the simulator"); err != nil {
			return nil, nil, err
		}
		if _, err := s.sim(ctx, a.PaneID, "rotate", map[string]string{"value": a.Orientation}); err != nil {
			return nil, nil, err
		}
		return textResult("Rotated " + a.Orientation + " (take a screenshot for the new size)"), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_fold",
		Description: "Open or close a foldable simulator (the iPhone Duo), or set its hinge angle. Closed shows the cover screen, open the inner one; take a screenshot after.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simFoldArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "fold the simulator"); err != nil {
			return nil, nil, err
		}
		args := map[string]any{"value": a.Fold}
		if a.Angle != nil {
			args["angle"] = *a.Angle
		}
		if _, err := s.sim(ctx, a.PaneID, "fold", args); err != nil {
			return nil, nil, err
		}
		return textResult("Folded"), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_pinch",
		Description: "Pinch on a simulator's screen with two fingers around a point (in points): zoom a map, photo or web page in or out.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simPinchArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "pinch on the screen"); err != nil {
			return nil, nil, err
		}
		if _, err := s.sim(ctx, a.PaneID, "pinch", map[string]any{"x": a.X, "y": a.Y, "scale": a.Scale, "ms": a.Ms}); err != nil {
			return nil, nil, err
		}
		return textResult("Pinched"), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_shutdown",
		Description: "Close a simulator pane and shut its simulator down (when you're done with it). Simulators Hammock booted also shut down a minute after their last pane closes.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simPaneArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.simPane(ctx, a.PaneID, true, "shut down the simulator"); err != nil {
			return nil, nil, err
		}
		if _, err := s.sim(ctx, a.PaneID, "shutdown", nil); err != nil {
			return nil, nil, err
		}
		return textResult("Shut the simulator down and closed its pane"), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_install_app",
		Description: "Install a built app (.app bundle for the simulator, e.g. from DerivedData) on a simulator.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simAppArgs) (*mcp.CallToolResult, any, error) {
		udid, err := s.simPane(ctx, a.PaneID, true, "install "+a.App)
		if err != nil {
			return nil, nil, err
		}
		if _, err := simctl(ctx, "install", udid, a.App); err != nil {
			return nil, nil, err
		}
		return textResult("Installed " + a.App), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_launch_app",
		Description: "Launch (or relaunch) an installed app on a simulator by bundle id. Returns its process id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simAppArgs) (*mcp.CallToolResult, any, error) {
		udid, err := s.simPane(ctx, a.PaneID, true, "launch "+a.App)
		if err != nil {
			return nil, nil, err
		}
		out, err := simctl(ctx, append([]string{"launch", "--terminate-running-process", udid, a.App}, a.Args...)...)
		if err != nil {
			return nil, nil, err
		}
		return textResult(out), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_terminate_app",
		Description: "Quit an app on a simulator by bundle id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simAppArgs) (*mcp.CallToolResult, any, error) {
		udid, err := s.simPane(ctx, a.PaneID, true, "quit "+a.App)
		if err != nil {
			return nil, nil, err
		}
		if _, err := simctl(ctx, "terminate", udid, a.App); err != nil {
			return nil, nil, err
		}
		return textResult("Quit " + a.App), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_open_url",
		Description: "Open a URL on a simulator: a web page in Safari, or a deep link into an app.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simURLArgs) (*mcp.CallToolResult, any, error) {
		udid, err := s.simPane(ctx, a.PaneID, true, "open "+a.URL)
		if err != nil {
			return nil, nil, err
		}
		if _, err := simctl(ctx, "openurl", udid, a.URL); err != nil {
			return nil, nil, err
		}
		return textResult("Opened " + a.URL), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "sim_logs",
		Description: "Read a simulator's recent system log (os_log), optionally filtered with a predicate. Untrusted content.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a simLogArgs) (*mcp.CallToolResult, any, error) {
		udid, err := s.simPane(ctx, a.PaneID, false, "read the logs")
		if err != nil {
			return nil, nil, err
		}
		last := a.Last
		if last == "" {
			last = "1m"
		}
		args := []string{"spawn", udid, "log", "show", "--style", "compact", "--last", last}
		if a.Predicate != "" {
			args = append(args, "--predicate", a.Predicate)
		}
		out, err := simctl(ctx, args...)
		if err != nil {
			return nil, nil, err
		}
		return textResult(lastLines(out, 300)), nil, nil
	})
}
