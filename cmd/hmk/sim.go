package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/emulator"
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
	// Platform is iOS or tvOS (an Apple TV, driven by its remote).
	Platform string `json:"platform"`
}

// remoteArgs reads `hmk sim remote PANE` arguments after the pane: a button
// with --repeat N and --hold MS, or swipe DIRECTION [short|medium|long].
func remoteArgs(args []string) (map[string]any, error) {
	if len(args) == 0 {
		return nil, errors.New("which button?")
	}
	if args[0] == "swipe" {
		if len(args) < 2 || len(args) > 3 {
			return nil, errors.New("swipe up|down|left|right [short|medium|long]")
		}
		m := map[string]any{"swipe": args[1]}
		if len(args) == 3 {
			m["distance"] = args[2]
		}
		return m, nil
	}
	m := map[string]any{"name": args[0]}
	for i := 1; i < len(args); i++ {
		if (args[i] != "--repeat" && args[i] != "--hold") || i+1 >= len(args) {
			return nil, fmt.Errorf("unexpected %q", args[i])
		}
		n, err := strconv.Atoi(args[i+1])
		if err != nil {
			return nil, fmt.Errorf("%s: %q isn't a number", args[i], args[i+1])
		}
		m[map[string]string{"--repeat": "repeat", "--hold": "holdms"}[args[i]]] = n
		i++
	}
	return m, nil
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

// simClipboard handles `hmk sim clipboard PANE [TEXT|-]`: prints the device's
// clipboard, or puts TEXT (stdin for -) on it.
func simClipboard(call caller, pane string, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		r, err := simCall(call, pane, "get_clipboard", nil)
		if err != nil {
			return err
		}
		var j struct{ Text string }
		if err := json.Unmarshal([]byte(r.GetJson()), &j); err != nil {
			return err
		}
		fmt.Fprint(stdout, j.Text)
		if j.Text != "" && !strings.HasSuffix(j.Text, "\n") && stdout == io.Writer(os.Stdout) && stdoutIsTerminal() {
			fmt.Fprintln(stdout)
		}
		return nil
	}
	text := strings.Join(args, " ")
	if text == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	_, err := simCall(call, pane, "set_clipboard", map[string]string{"text": text})
	return err
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
		return simDevice{}, errors.New("no simulators found (install one in Xcode ▸ Settings ▸ Components, or an Android Virtual Device in Android Studio)")
	}
	if ref == "" {
		// A running simulator, else the newest iPhone: an emulator only by name.
		var sims []simDevice
		for _, d := range devs {
			if !emulator.IsAVD(d.UDID) {
				sims = append(sims, d)
			}
		}
		if len(sims) == 0 {
			return simDevice{}, errors.New("no simulators found: name an Android Virtual Device (hmk sim list)")
		}
		for _, d := range sims {
			if d.Booted {
				return d, nil
			}
		}
		for i := len(sims) - 1; i >= 0; i-- {
			if strings.HasPrefix(sims[i].Name, "iPhone") {
				return sims[i], nil
			}
		}
		return sims[0], nil
	}
	var match []simDevice
	for _, d := range devs {
		// By name, UDID (or its start), or an AVD's id (avd:Pixel_9, or just Pixel_9).
		if strings.EqualFold(d.Name, ref) || strings.HasPrefix(d.UDID, strings.ToUpper(ref)) ||
			strings.EqualFold(d.UDID, ref) || strings.EqualFold(d.UDID, emulator.Prefix+ref) {
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
       hmk sim clipboard PANE [TEXT|-]   with no TEXT, prints the device's clipboard; with TEXT (or - for
                                 stdin), puts it on the device's clipboard (not pasted)
       hmk sim tap PANE X Y          (points, as in a screenshot)
       hmk sim type PANE TEXT
       hmk sim button PANE home|lock|side|siri|keyboard   (keyboard: show or hide the software keyboard)
                                 Android: back|home|recents|power|volume_up|volume_down
       hmk sim rotate PANE left|right|portrait|upside-down|landscape-left|landscape-right
       hmk sim pinch PANE X Y SCALE  (two fingers around X, Y in points; SCALE above 1 zooms in)
       hmk sim fold PANE open|closed|half|DEGREES   (a foldable, such as the iPhone Duo)
       hmk sim remote PANE BUTTON [--repeat N] [--hold MS]   an Apple TV's or Android TV's remote: up, down, left,
                                 right, select, menu, home, play_pause, volume_up, volume_down,
                                 mute or siri (--hold MS: a long press)
       hmk sim remote PANE swipe up|down|left|right [short|medium|long]   moves focus (as presses)
       hmk sim shutdown PANE                    close the pane and shut its simulator down
       hmk sim takeover PANE                    an Android emulator its pane can't show (in Android Studio, or stopped):
                                                start it in the pane (from a terminal outside Hammock)`)
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
			fmt.Printf("%s  %-40s %s%s\n", d.UDID, d.Name, d.Runtime, state)
		}
		return nil
	case "shutdown", "takeover":
		// Close the pane and shut its simulator down; or take an emulator over from Android Studio.
		if len(args) < 3 {
			return usage
		}
		pid, err := w.resolve(args[2], "pane")
		if err != nil {
			return err
		}
		_, err = simCall(call, pid, sub, nil)
		return err
	case "shot":
		return simShot(w, call, args[2:], usage)
	case "clipboard":
		if len(args) < 3 {
			return usage
		}
		pid, err := w.resolve(args[2], "pane")
		if err != nil {
			return err
		}
		return simClipboard(call, pid, args[3:], os.Stdin, os.Stdout)
	case "remote":
		if len(args) < 4 {
			return usage
		}
		pid, err := w.resolve(args[2], "pane")
		if err != nil {
			return err
		}
		ra, err := remoteArgs(args[3:])
		if err != nil {
			return fmt.Errorf("%w\n%v", err, usage)
		}
		_, err = simCall(call, pid, "remote", ra)
		return err
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
