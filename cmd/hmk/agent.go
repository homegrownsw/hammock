package main

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/agents"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// hmk agent: the agent view in text, for checking what the daemon parses
// and for answering from a shell.
//
//	hmk agent watch PANE              stream the view
//	hmk agent answer PANE N [TEXT]    pick option N of the prompt on screen (with TEXT: typed into it)
//	hmk agent pick PANE N[,N]         multi-select: tick these, then next
//	hmk agent send PANE TEXT          send a message
func runAgent(args []string) error {
	if len(args) > 0 && agents.Get(args[0]) != nil {
		return runStartAgent(args)
	}
	if len(args) < 2 {
		return fmt.Errorf("usage: hmk agent AGENT [--dir DIR] [--group GROUP] [PROMPT]   (AGENT: %s)\n       hmk agent watch|answer|pick|send PANE …", agentIDs())
	}
	ctx := context.Background()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	w, err := fetchWorld(ctx, c)
	if err != nil {
		return err
	}
	pane, err := w.resolve(args[1], "pane")
	if err != nil {
		return err
	}
	watch := func() (*pb.AgentView, error) {
		if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: pane, Watch: true}}}); err != nil {
			return nil, err
		}
		for ev := range c.Events {
			if v := ev.GetAgentView(); v != nil {
				return v, nil
			}
		}
		return nil, fmt.Errorf("connection closed")
	}
	switch args[0] {
	case "watch":
		v, err := watch()
		if err != nil {
			return err
		}
		printView(v)
		for ev := range c.Events {
			if v := ev.GetAgentView(); v != nil {
				printView(v)
			}
		}
		return nil
	case "answer", "pick":
		v, err := watch()
		if err != nil {
			return err
		}
		if v.Prompt == nil || v.Prompt.Kind == pb.AgentPrompt_KIND_NONE {
			return fmt.Errorf("no prompt showing")
		}
		a := &pb.AgentAnswer{PaneId: pane, OptionsSeen: v.Prompt.Options}
		if len(args) < 3 {
			return fmt.Errorf("usage: hmk agent %s PANE …", args[0])
		}
		if args[0] == "answer" {
			n, _ := strconv.Atoi(args[2])
			a.Option = int32(n)
			if len(args) > 3 {
				a.Option, a.Text = v.Prompt.Other, strings.Join(args[3:], " ")
				if n != 0 {
					a.Option = int32(n)
				}
			}
		} else {
			for _, n := range strings.Split(args[2], ",") {
				i, _ := strconv.Atoi(n)
				a.Toggles = append(a.Toggles, int32(i))
			}
		}
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err = c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AgentAnswer{AgentAnswer: a}})
		return err
	case "send":
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AgentSend{AgentSend: &pb.AgentSend{PaneId: pane, Text: strings.Join(args[2:], " ")}}})
		return err
	}
	return fmt.Errorf("unknown agent command %q", args[0])
}

func printView(v *pb.AgentView) {
	if v.Error != "" {
		fmt.Fprintln(os.Stderr, "error:", v.Error)
	}
	if v.Replace {
		fmt.Printf("── session %s (%d blocks, more: %v)\n", v.SessionId, len(v.Blocks), v.HasMore)
	}
	for _, b := range v.Blocks {
		kind := strings.ToLower(strings.TrimPrefix(b.Kind.String(), "KIND_"))
		line := b.Title
		if line == "" {
			line = b.Text
		}
		line = strings.ReplaceAll(line, "\n", " ⏎ ")
		if len(line) > 110 {
			line = line[:110] + "…"
		}
		state := ""
		if b.Pending {
			state = " [pending]"
		}
		if b.Error {
			state += " [error]"
		}
		for _, q := range b.Questions {
			line += fmt.Sprintf(" | %s → %q", q.Header, q.Answer)
		}
		fmt.Printf("%-9s %s%s\n", kind, line, state)
	}
	if p := v.Prompt; p != nil && p.Kind != pb.AgentPrompt_KIND_NONE {
		fmt.Printf("» prompt %s %q: %q (selected %d, multi %v %v, other %d) %s\n", strings.TrimPrefix(p.Kind.String(), "KIND_"), p.Title, p.Options, p.Selected, p.MultiSelect, p.Checked, p.Other, p.Tabs)
	}
	if v.Working {
		fmt.Printf("» working: %s\n", v.Status)
	}
	if v.Draft != "" {
		fmt.Printf("» draft (typed in the terminal): %q\n", v.Draft)
	}
}

// runStartAgent: hmk agent claude|codex [--dir DIR] [--group GROUP] [--on
// COMPUTER] [PROMPT…] starts the agent in a new tab (in DIR, by default
// here; on a linked computer, its home folder), in GROUP or else the
// folder's Group, or a new one.
func runStartAgent(args []string) error {
	usage := fmt.Errorf("usage: hmk agent AGENT [--dir DIR] [--group GROUP] [--on COMPUTER] [PROMPT]   (AGENT: %s)\n(on another computer, DIR is absolute or ~/…, its home)", agentIDs())
	req := &pb.StartAgent{Harness: args[0]}
	var words []string
	groupRef, on := "", ""
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case (a == "--dir" || a == "--group" || a == "--on") && i+1 < len(args):
			switch a {
			case "--dir":
				req.Cwd = args[i+1]
			case "--group":
				groupRef = args[i+1]
			default:
				on = args[i+1]
			}
			i++
		case strings.HasPrefix(a, "--"):
			return usage
		default:
			words = append(words, a)
		}
	}
	req.Prompt = strings.Join(words, " ")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	w, err := fetchWorld(ctx, c)
	if err != nil {
		return err
	}
	paneGroup := ""
	if p := w.panes[paneEnv("PANE_ID")]; p != nil {
		if t := w.tabs[p.TabId]; t != nil {
			paneGroup = t.GroupId
		}
	}
	if err := w.placeAgent(req, groupRef, on, paneGroup); err != nil {
		return err
	}
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: req}})
	if err != nil {
		return err
	}
	fmt.Println(r.GetCreated().GetId())
	return nil
}

// placeAgent works out where hmk agent starts: on --on's computer, or in
// --group (on whichever computer that Group is: an --on that disagrees is
// refused), else the pane's Group it was run in. Its folder defaults to
// here (the working directory), or "~" on another computer.
func (w *world) placeAgent(req *pb.StartAgent, groupRef, on, paneGroup string) error {
	var err error
	if on != "" {
		// "this" (or this computer's name) is as no --on at all.
		if req.ComputerId, err = w.onComputer([]string{"--on", on}); err != nil {
			return err
		}
	}
	there := req.ComputerId
	switch {
	case groupRef != "":
		if req.GroupId, err = w.resolve(groupRef, "group"); err != nil {
			return err
		}
		host := ""
		for _, g := range w.groups {
			if g.Id == req.GroupId {
				host = g.HostUserId
			}
		}
		if on != "" && host != req.ComputerId {
			where := "this computer"
			if host != "" {
				where = cmp.Or(w.computerName(&pb.Group{HostUserId: host}), "another computer")
			}
			return fmt.Errorf("%s is on %s, not %s: leave out --on, or pick a Group there", groupRef, where, on)
		}
		there, req.ComputerId = host, "" // the Group says where
	case there == "":
		req.GroupId = paneGroup // run in a pane: its Group
	}
	switch {
	case there == "":
		if req.Cwd == "" {
			req.Cwd, _ = os.Getwd()
		}
		req.Cwd = absDir(req.Cwd)
	case req.Cwd == "":
		req.Cwd = "~" // a folder there, not here
	default:
		// A folder there: absolute, or under its home. One under this
		// computer's home (the shell expanded a ~) goes as ~/… there.
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if rest, ok := strings.CutPrefix(filepath.Clean(req.Cwd), home); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
				req.Cwd = "~" + rest
			}
		}
		if !filepath.IsAbs(req.Cwd) && req.Cwd != "~" && !strings.HasPrefix(req.Cwd, "~/") {
			return fmt.Errorf("--dir %s: a folder on another computer is absolute, or ~/… (its home)", req.Cwd)
		}
	}
	return nil
}

// agentIDs lists the agents Hammock knows: "claude, codex, …".
func agentIDs() string {
	ids := make([]string, len(agents.All))
	for i, a := range agents.All {
		ids[i] = a.ID
	}
	return strings.Join(ids, ", ")
}
