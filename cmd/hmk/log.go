package main

// hmk log: open log panes (a file, macOS's log, a simulator's, adb logcat,
// any command) and read their entries filtered.

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	"github.com/alexjsp/hammock/internal/logview"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

const logUsage = `usage: hmk log file PATH | macos | sim [DEVICE] | logcat | -- CMD...   open a log pane
           [--process P] [--predicate P] [--level default|info|debug]   (macos, sim)
           [--serial S] (logcat)  [--group G | --split PANE [--down]] [--cwd DIR] [--name N]
       hmk log read PANE [TEXT] [--level L,...] [--process P] [--tag T] [--limit N] [--follow] [--json]
       hmk log clear PANE`

func runLog(w *world, call caller, args []string) error {
	usage := errors.New(logUsage)
	if len(args) < 2 {
		return usage
	}
	switch args[1] {
	case "read":
		return runLogRead(w, call, args[2:])
	case "clear":
		if len(args) < 3 {
			return usage
		}
		pid, err := w.logPane(args[2])
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_ClearLog{ClearLog: &pb.ClearLog{PaneId: pid}}})
		return err
	}

	// Opening one: the source, then flags (and for a command, what's after --).
	opts := args[2:]
	var cmd []string
	if i := indexOf(args, "--"); i >= 0 {
		opts, cmd = args[2:max(i, 2)], args[i+1:]
	}
	src := &pb.LogSource{Kind: args[1], Process: flagValue(opts, "--process"), Predicate: flagValue(opts, "--predicate"),
		Level: flagValue(opts, "--level"), Label: flagValue(opts, "--name")}
	positional := func() string {
		if len(opts) > 0 && !strings.HasPrefix(opts[0], "--") {
			return opts[0]
		}
		return ""
	}
	switch src.Kind {
	case "file":
		p := positional()
		if p == "" {
			return usage
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		src.Path = abs
	case "macos":
	case "sim", "simulator":
		src.Kind = "simulator"
		if ref := positional(); ref != "" {
			devs, err := listSimulators(call)
			if err != nil {
				return err
			}
			d, err := pickSimulator(devs, ref)
			if err != nil {
				return err
			}
			src.Device = d.UDID
		}
	case "logcat":
		src.Device = flagValue(opts, "--serial")
	case "--":
		src.Kind = "command"
		cmd = args[2:]
		opts = nil
	default:
		return usage
	}
	if src.Kind == "command" && len(cmd) == 0 {
		return usage
	}
	src.Command = cmd
	spec := &pb.PaneSpec{Log: src, Cwd: absDir(flagValue(opts, "--cwd")), CreatedByPaneId: paneEnv("PANE_ID")}
	var m *pb.ClientMessage
	if ref := flagValue(opts, "--split"); ref != "" {
		pid, err := w.resolve(ref, "pane")
		if err != nil {
			return err
		}
		dir := pb.Split_DIRECTION_HORIZONTAL
		if hasFlag(opts, "--down") {
			dir = pb.Split_DIRECTION_VERTICAL
		}
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: pid, Direction: dir, Spec: spec}}}
	} else {
		gid, err := w.browserGroup(flagValue(opts, "--group"))
		if err != nil {
			return err
		}
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: spec}}}
	}
	r, err := call(m)
	if err != nil {
		return err
	}
	id := client.CreatedID(r)
	// A new tab: its pane is what log read wants.
	if strings.HasPrefix(id, "tb_") {
		if s, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}); err == nil {
			for _, t := range s.GetState().GetTabs() {
				if t.Id == id && t.FocusedPaneId != "" {
					id = t.FocusedPaneId
				}
			}
		}
	}
	fmt.Println(id)
	return nil
}

// logPane finds a log pane by its id, or its tab's.
func (w *world) logPane(ref string) (string, error) {
	if id, err := w.resolve(ref, "pane"); err == nil {
		return id, nil
	}
	tid, err := w.resolve(ref, "tab")
	if err != nil {
		return "", err
	}
	return w.tabs[tid].FocusedPaneId, nil
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

// logFilterFlags reads read's filter flags; the words left are the text.
func logFilterFlags(args []string) (*pb.LogFilter, map[string]string, error) {
	f := &pb.LogFilter{}
	flags := map[string]string{}
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "--follow":
			flags[a] = "1"
		case "--level", "--process", "--tag", "--limit", "--hide":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s wants a value", a)
			}
			v := args[i+1]
			i++
			switch a {
			case "--level":
				for _, l := range strings.Split(v, ",") {
					lv := logview.LevelNamed(l)
					if lv == pb.LogLevel_LOG_LEVEL_UNSPECIFIED {
						return nil, nil, fmt.Errorf("level %q: debug, info, notice, warning, error or fault", l)
					}
					f.Levels = append(f.Levels, lv)
				}
			case "--process":
				f.Only = append(f.Only, "process:"+v)
			case "--tag":
				f.Only = append(f.Only, "tag:"+v)
			case "--hide":
				f.Hide = append(f.Hide, v)
			default:
				flags[a] = v
			}
		default:
			if strings.HasPrefix(a, "--") {
				return nil, nil, errors.New(logUsage)
			}
			words = append(words, a)
		}
	}
	f.Text = strings.Join(words, " ")
	return f, flags, nil
}

func runLogRead(w *world, call caller, args []string) error {
	if len(args) == 0 {
		return errors.New(logUsage)
	}
	pid, err := w.logPane(args[0])
	if err != nil {
		return err
	}
	f, flags, err := logFilterFlags(args[1:])
	if err != nil {
		return err
	}
	limit := 100
	if v := flags["--limit"]; v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 {
			return errors.New("--limit wants a number")
		}
	}
	read := func(after uint64) ([]*pb.LogEntry, error) {
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadLog{ReadLog: &pb.ReadLog{PaneId: pid, Filter: f, Limit: int32(limit), AfterSeq: after}}})
		if err != nil {
			return nil, err
		}
		return r.GetLogView().GetEntries(), nil
	}
	color := false
	if st, err := os.Stdout.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 && flags["--json"] == "" {
		color = true
	}
	print := func(es []*pb.LogEntry) error {
		if flags["--json"] != "" {
			return logview.Marshal(os.Stdout, es)
		}
		for _, e := range es {
			fmt.Println(formatEntry(e, color))
		}
		return nil
	}
	es, err := read(0)
	if err != nil {
		return err
	}
	if err := print(es); err != nil {
		return err
	}
	if flags["--follow"] == "" {
		return nil
	}
	var last uint64
	if n := len(es); n > 0 {
		last = es[n-1].Seq
	} else {
		// From now: the newest entry's seq, whatever the filter.
		if all, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadLog{ReadLog: &pb.ReadLog{PaneId: pid, Limit: 1}}}); err == nil {
			if e := all.GetLogView().GetEntries(); len(e) > 0 {
				last = e[0].Seq
			}
		}
	}
	for {
		time.Sleep(500 * time.Millisecond)
		es, err := read(last)
		if err != nil {
			return err
		}
		if len(es) > 0 {
			last = es[len(es)-1].Seq
			if err := print(es); err != nil {
				return err
			}
		}
	}
}

// formatEntry is one entry as a line (or several, indented): time, level,
// process[pid], tag, message.
func formatEntry(e *pb.LogEntry, color bool) string {
	var b strings.Builder
	b.WriteString(time.UnixMilli(e.TimeUnixMs).Format("15:04:05.000"))
	lv := strings.ToUpper(logview.LevelName(e.Level))
	if color {
		code := map[pb.LogLevel]string{pb.LogLevel_LOG_LEVEL_DEBUG: "2", pb.LogLevel_LOG_LEVEL_NOTICE: "36",
			pb.LogLevel_LOG_LEVEL_WARNING: "33", pb.LogLevel_LOG_LEVEL_ERROR: "31", pb.LogLevel_LOG_LEVEL_FAULT: "1;31"}[e.Level]
		if code != "" {
			lv = "\x1b[" + code + "m" + lv + "\x1b[0m"
		}
	}
	fmt.Fprintf(&b, " %-7s", lv)
	if e.Process != "" {
		b.WriteString(" " + e.Process)
		if e.Pid != 0 {
			fmt.Fprintf(&b, "[%d]", e.Pid)
		}
	}
	if e.Tag != "" {
		b.WriteString(" " + e.Tag)
		if e.Category != "" {
			b.WriteString(":" + e.Category)
		}
	}
	if e.Process != "" || e.Tag != "" {
		b.WriteString(":")
	}
	b.WriteString(" " + strings.ReplaceAll(e.Message, "\n", "\n    "))
	for _, k := range slices.Sorted(maps.Keys(e.Fields)) {
		fmt.Fprintf(&b, " %s=%s", k, e.Fields[k])
	}
	return b.String()
}
