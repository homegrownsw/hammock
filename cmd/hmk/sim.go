package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/simshot"
)

// simShot handles `hmk sim shot PANE [FILE.png] [--pixels] [--clipboard]`.
func simShot(w *world, call caller, args []string, usage error) error {
	var pane, file string
	pixels, clip := false, false
	for _, a := range args {
		switch {
		case a == "--pixels":
			pixels = true
		case a == "--clipboard":
			clip = true
		case strings.HasPrefix(a, "-"):
			return usage
		case pane == "":
			pane = a
		case file == "":
			file = a
		default:
			return usage
		}
	}
	if pane == "" || (clip && file != "") {
		return usage
	}
	pid, err := w.resolve(pane, "pane")
	if err != nil {
		return err
	}
	// A file named for an agent or a script is in points, as tap takes them;
	// one for a person (the clipboard, the screenshots folder) keeps the pixels.
	if file == "" {
		pixels = true
	}
	var opts map[string]bool
	if pixels {
		opts = map[string]bool{"pixels": true}
	}
	r, err := simCall(call, pid, "screenshot", opts)
	if err != nil {
		return err
	}
	switch {
	case clip:
		return simshot.Copy(r.GetPng())
	case file != "":
		if len(r.GetPng()) == 0 {
			return errors.New("the simulator sent no picture")
		}
		return os.WriteFile(file, r.GetPng(), 0o644)
	}
	device := ""
	if p := w.panes[pid]; p != nil {
		device = p.Title
	}
	path, err := simshot.Save(simshot.Folder(), simshot.FileName(device, time.Now()), r.GetPng())
	if err == nil {
		fmt.Println(path)
	}
	return err
}

// simDevice is a simulator as the simulator host lists it.
type simDevice struct {
	UDID    string `json:"udid"`
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
	Booted  bool   `json:"booted"`
}

type caller func(*pb.ClientMessage) (*pb.ServerMessage, error)

func simCall(call caller, pane, op string, args any) (*pb.SimulatorResult, error) {
	var aj []byte
	if args != nil {
		aj, _ = json.Marshal(args)
	}
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: pane, Op: op, ArgsJson: string(aj)}}})
	if err != nil {
		return nil, err
	}
	return r.GetSimulatorResult(), nil
}

func listSimulators(call caller) ([]simDevice, error) {
	r, err := simCall(call, "", "devices", nil)
	if err != nil {
		return nil, err
	}
	var out []simDevice
	return out, json.Unmarshal([]byte(r.GetJson()), &out)
}

// pickSimulator finds a device by UDID (or prefix) or name; with none
// given, a booted one, else the newest iPhone.
func pickSimulator(devs []simDevice, ref string) (simDevice, error) {
	if len(devs) == 0 {
		return simDevice{}, errors.New("no simulators found (install one in Xcode ▸ Settings ▸ Components)")
	}
	if ref == "" {
		for _, d := range devs {
			if d.Booted {
				return d, nil
			}
		}
		for i := len(devs) - 1; i >= 0; i-- {
			if strings.HasPrefix(devs[i].Name, "iPhone") {
				return devs[i], nil
			}
		}
		return devs[0], nil
	}
	var match []simDevice
	for _, d := range devs {
		if strings.EqualFold(d.Name, ref) || strings.HasPrefix(d.UDID, strings.ToUpper(ref)) {
			match = append(match, d)
		}
	}
	// A name can exist for several runtimes: prefer a booted one, else the newest.
	for _, d := range match {
		if d.Booted {
			return d, nil
		}
	}
	if len(match) > 0 {
		return match[len(match)-1], nil
	}
	return simDevice{}, fmt.Errorf("no simulator matches %q (hmk sim list)", ref)
}

// runSim handles `hmk sim …`.
func runSim(w *world, call caller, args []string) error {
	usage := errors.New(`usage: hmk sim [DEVICE] [--group GROUP] [--split PANE [--down]]   open a simulator pane
       hmk sim list
       hmk sim shot PANE [FILE.png] [--pixels] [--clipboard]
                                 in points to FILE (as tap takes them); with no FILE, in the device's
                                 pixels where macOS saves screenshots; --clipboard copies it instead
       hmk sim tap PANE X Y          (points, as in a screenshot)
       hmk sim type PANE TEXT
       hmk sim button PANE home|lock|side|siri|keyboard   (keyboard: show or hide the software keyboard)
       hmk sim rotate PANE left|right|portrait|upside-down|landscape-left|landscape-right
       hmk sim pinch PANE X Y SCALE  (two fingers around X, Y in points; SCALE above 1 zooms in)
       hmk sim fold PANE open|closed|half|DEGREES   (a foldable, such as the iPhone Duo)
       hmk sim shutdown PANE                    close the pane and shut its simulator down`)
	sub := ""
	if len(args) > 1 {
		sub = args[1]
	}
	switch sub {
	case "list":
		devs, err := listSimulators(call)
		if err != nil {
			return err
		}
		for _, d := range devs {
			state := ""
			if d.Booted {
				state = "  (booted)"
			}
			fmt.Printf("%s  %-28s %s%s\n", d.UDID, d.Name, d.Runtime, state)
		}
		return nil
	case "shutdown":
		// Close the pane and shut its simulator down.
		if len(args) < 3 {
			return usage
		}
		pid, err := w.resolve(args[2], "pane")
		if err != nil {
			return err
		}
		_, err = simCall(call, pid, "shutdown", nil)
		return err
	case "shot":
		return simShot(w, call, args[2:], usage)
	case "tap", "type", "button", "rotate", "pinch", "fold":
		if len(args) < 4 {
			return usage
		}
		pid, err := w.resolve(args[2], "pane")
		if err != nil {
			return err
		}
		switch sub {
		case "tap":
			if len(args) < 5 {
				return usage
			}
			x, _ := strconv.ParseFloat(args[3], 64)
			y, _ := strconv.ParseFloat(args[4], 64)
			_, err = simCall(call, pid, "tap", map[string]float64{"x": x, "y": y})
		case "type":
			_, err = simCall(call, pid, "text", map[string]string{"text": strings.Join(args[3:], " ")})
		case "button":
			_, err = simCall(call, pid, "button", map[string]string{"name": args[3]})
		case "rotate":
			_, err = simCall(call, pid, "rotate", map[string]string{"value": args[3]})
		case "fold":
			fold := map[string]any{"value": args[3]}
			if deg, perr := strconv.ParseFloat(args[3], 64); perr == nil {
				fold = map[string]any{"angle": deg}
			}
			_, err = simCall(call, pid, "fold", fold)
		case "pinch":
			if len(args) < 6 {
				return usage
			}
			x, _ := strconv.ParseFloat(args[3], 64)
			y, _ := strconv.ParseFloat(args[4], 64)
			scale, _ := strconv.ParseFloat(args[5], 64)
			_, err = simCall(call, pid, "pinch", map[string]float64{"x": x, "y": y, "scale": scale})
		}
		return err
	}
	ref := ""
	if sub != "" && !strings.HasPrefix(sub, "--") {
		ref = sub
	}
	devs, err := listSimulators(call)
	if err != nil {
		return err
	}
	dev, err := pickSimulator(devs, ref)
	if err != nil {
		return err
	}
	spec := &pb.PaneSpec{Simulator: dev.UDID, CreatedByPaneId: paneEnv("PANE_ID")}
	var m *pb.ClientMessage
	if pref := flagValue(args, "--split"); pref != "" {
		pid, err := w.resolve(pref, "pane")
		if err != nil {
			return err
		}
		dir := pb.Split_DIRECTION_HORIZONTAL
		if hasFlag(args, "--down") {
			dir = pb.Split_DIRECTION_VERTICAL
		}
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: pid, Direction: dir, Spec: spec}}}
	} else {
		sid, err := w.browserGroup(flagValue(args, "--group"))
		if err != nil {
			return err
		}
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: spec}}}
	}
	r, err := call(m)
	if err != nil {
		return err
	}
	fmt.Println(client.CreatedID(r))
	return nil
}
