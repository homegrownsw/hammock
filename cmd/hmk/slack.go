package main

// hmk slack: Slack for automations' Slack triggers (the daemon's slack.go).
// The tokens are read from stdin, never the command line (where they'd be
// in your shell's history and the process list), and only from you: the
// daemon refuses SlackCommand to agents.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const slackUsage = `usage:
  hmk slack [status] [--on COMPUTER]       whether Slack is connected, which workspace, and whether
                                           it's listening; not connected: the link that makes your
                                           Slack app
  hmk slack connect [--on COMPUTER]        connect your Slack app: give its User OAuth Token (xoxp-…)
                                           and an app-level token (xapp-…, with connections:write) on
                                           stdin, one a line (asked for at a terminal); again: change them
  hmk slack disconnect [--on COMPUTER]     forget the tokens (Slack triggers stop)

Make the app from Hammock's manifest (the link hmk slack prints), install it to
your workspace, then copy OAuth & Permissions ▸ User OAuth Token, and make an
app-level token under Basic Information ▸ App-Level Tokens with the
connections:write scope. One computer of yours holds the connection.`

func runSlack(args []string) error {
	verb := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb, args = args[0], args[1:]
	}
	if verb == "help" {
		fmt.Println(slackUsage)
		return nil
	}
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			fmt.Println(slackUsage)
			return nil
		case a == "--on" && i+1 < len(args):
			i++
		default:
			return fmt.Errorf("unknown option %s\n%s", a, slackUsage)
		}
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
	on, err := w.onComputer(args)
	if err != nil {
		return err
	}
	cmd := &pb.SlackCommand{ComputerId: on}
	switch verb {
	case "status":
		cmd.Action = pb.SlackCommand_ACTION_STATUS
	case "connect":
		cmd.Action = pb.SlackCommand_ACTION_CONNECT
		if cmd.UserToken, cmd.AppToken, err = readSlackTokens(os.Stdin, os.Stderr); err != nil {
			return err
		}
	case "disconnect":
		cmd.Action = pb.SlackCommand_ACTION_DISCONNECT
	default:
		return fmt.Errorf("unknown command %q\n%s", verb, slackUsage)
	}
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: cmd}})
	if err != nil {
		return err
	}
	printSlack(os.Stdout, verb, r.GetSlackReply(), w.slackElsewhere(on))
	return nil
}

// slackElsewhere names another computer of yours that holds Slack ("" none).
func (w *world) slackElsewhere(on string) string {
	if w.computers == nil {
		return ""
	}
	for _, c := range w.computers.List {
		if c.GetSlack().GetTeamId() != "" && !(c.ThisComputer && on == "") && c.UserId != on {
			return c.Name
		}
	}
	return ""
}

func printSlack(out io.Writer, verb string, r *pb.SlackReply, elsewhere string) {
	l := r.GetLink()
	if l == nil {
		if verb == "disconnect" {
			fmt.Fprintln(out, "Disconnected: the tokens are forgotten.")
			return
		}
		if elsewhere != "" {
			fmt.Fprintf(out, "Not connected here: Slack is connected on %s.\n", elsewhere)
			return
		}
		fmt.Fprintln(out, "Not connected. Make your Slack app from Hammock's manifest:")
		fmt.Fprintln(out, "  "+r.GetManifestUrl())
		fmt.Fprintln(out, "then: hmk slack connect")
		return
	}
	who := l.User
	if who != "" {
		who = " as " + who
	}
	state := "listening"
	switch {
	case l.Problem != "":
		state = l.Problem
	case l.Idle:
		state = "not listening: no automation that's on has a Slack trigger"
	case !l.Listening:
		state = "connecting"
	}
	fmt.Fprintf(out, "Connected to %s%s (%s): %s\n", l.Team, who, strings.TrimSuffix(l.Url, "/"), state)
}

// readSlackTokens reads the two tokens, one a line: asked for (and not
// shown) at a terminal. Either order: they're told apart by their start.
func readSlackTokens(in *os.File, prompt io.Writer) (user, app string, err error) {
	var lines []string
	if term.IsTerminal(int(in.Fd())) {
		for _, q := range []string{"User OAuth Token (xoxp-…): ", "App-level token (xapp-…): "} {
			fmt.Fprint(prompt, q)
			b, err := term.ReadPassword(int(in.Fd()))
			fmt.Fprintln(prompt)
			if err != nil {
				return "", "", err
			}
			lines = append(lines, string(b))
		}
	} else {
		sc := bufio.NewScanner(io.LimitReader(in, 64<<10))
		for sc.Scan() && len(lines) < 2 {
			if s := strings.TrimSpace(sc.Text()); s != "" {
				lines = append(lines, s)
			}
		}
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "xapp-"):
			app = l
		case user == "":
			user = l
		}
	}
	if user == "" || app == "" {
		return "", "", errors.New("give both tokens on stdin, one a line: the User OAuth Token (xoxp-…) and an app-level token (xapp-…)")
	}
	return user, app, nil
}
