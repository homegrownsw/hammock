package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Linked computers: hmk link, hmk computers, and --on COMPUTER.

// computer finds a linked computer (or this one) by name, id prefix or
// "this".
func (w *world) computer(ref string) (*pb.Computer, error) {
	if w.computers == nil {
		return nil, errors.New("this daemon doesn't know about linked computers")
	}
	ref = strings.TrimSpace(ref)
	var found []*pb.Computer
	for _, c := range w.computers.List {
		if (ref == "this" && c.ThisComputer) || strings.EqualFold(c.Name, ref) || strings.HasPrefix(c.UserId, ref) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no computer %q (see hmk computers)", ref)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%q is ambiguous: use its id (see hmk computers)", ref)
}

// onComputer is the id to send for --on COMPUTER ("" for this one).
func (w *world) onComputer(args []string) (string, error) {
	ref := flagValue(args, "--on")
	if ref == "" {
		return "", nil
	}
	c, err := w.computer(ref)
	if err != nil {
		return "", err
	}
	if c.ThisComputer {
		return "", nil
	}
	return c.UserId, nil
}

// computerName is the name of the computer a Group is on ("" for this one
// or a Group someone shared).
func (w *world) computerName(g *pb.Group) string {
	if g.HostUserId == "" || w.computers == nil {
		return ""
	}
	for _, c := range w.computers.List {
		if c.UserId == g.HostUserId {
			return c.Name
		}
	}
	return ""
}

func printComputers(w *world) {
	if w.computers == nil || len(w.computers.List) <= 1 {
		fmt.Println("No other computers are linked. Link one with: hmk link")
		return
	}
	for _, c := range w.computers.List {
		state := "offline"
		switch {
		case c.Refused:
			state = "doesn't link with this computer any more (hmk link --again it, or hmk computers remove it)"
		case c.Suspect:
			state = "introduced by a computer since unlinked: check its code (hmk link --again it, or hmk computers remove it)"
		case c.CertChanged:
			state = "its certificate changed: check its code (hmk link --again it)"
		case c.ThisComputer:
			state = "this computer"
		case c.Online:
			state = "online"
		case c.LastSeenUnixMs > 0:
			state = "offline, last seen " + time.UnixMilli(c.LastSeenUnixMs).Format("2006-01-02 15:04")
		}
		active := " "
		if c.UserId == w.computers.ActiveId {
			active = "*"
		}
		icon := ""
		if c.Icon != "" {
			icon = "  icon " + c.Icon
		}
		printfSafe("%s%s  %-20s %-12s %s  %s  %s%s\n", active, c.UserId, truncate(c.Name, 20), truncate(cmp.Or(c.Model, c.Os), 12), c.Tint, c.Fingerprint, state, icon)
	}
	fmt.Println("(* the one used last: it notifies)")
	if w.computers.Note != "" {
		printlnSafe(w.computers.Note)
	}
}

// runComputers is hmk computers [unlink NAME | remove NAME | tint NAME #RRGGBB | icon NAME ICON].
func runComputers(w *world, call func(*pb.ClientMessage) (*pb.ServerMessage, error), args []string) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "--json" {
		if len(args) == 1 {
			if w.computers == nil {
				return printProtoJSON(&pb.Computers{})
			}
			return printProtoJSON(w.computers)
		}
		printComputers(w)
		return nil
	}
	switch args[0] {
	case "unlink":
		if len(args) < 2 {
			return errors.New("usage: hmk computers unlink COMPUTER|this")
		}
		c, err := w.computer(args[1])
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{UserId: c.UserId}}})
		if err == nil && c.ThisComputer {
			fmt.Println("Unlinked this computer from your others.")
		} else if err == nil {
			printfSafe("Unlinked %s from all your computers.\n", c.Name)
		}
		return err
	case "remove", "forget":
		// Here only: for one that doesn't link with this one any more.
		if len(args) < 2 {
			return errors.New("usage: hmk computers remove COMPUTER")
		}
		c, err := w.computer(args[1])
		if err != nil {
			return err
		}
		if c.ThisComputer {
			return errors.New("that's this computer (hmk computers unlink this leaves the others)")
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{UserId: c.UserId, Forget: true}}})
		if err == nil {
			printfSafe("Removed %s here (your other computers aren't asked to unlink it).\n", c.Name)
			if c.Suspect {
				fmt.Println("It isn't linked again by an introduction: only hmk link --again (its code checked) brings it back.")
			}
		}
		return err
	case "tint", "colour", "color":
		if len(args) < 3 {
			return errors.New("usage: hmk computers tint COMPUTER #RRGGBB|none")
		}
		c, err := w.computer(args[1])
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: c.UserId, Tint: args[2]}}})
		return err
	case "icon":
		// An SF Symbol's name or an emoji; "" (or "default"): its kind's symbol.
		if len(args) < 3 {
			return errors.New("usage: hmk computers icon COMPUTER SYMBOL|EMOJI|default")
		}
		c, err := w.computer(args[1])
		if err != nil {
			return err
		}
		icon := args[2]
		if icon == "default" {
			icon = ""
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: c.UserId, Icon: &icon}}})
		return err
	}
	return errors.New("usage: hmk computers [unlink COMPUTER | remove COMPUTER | tint COMPUTER #RRGGBB|none | icon COMPUTER SYMBOL|EMOJI|default]")
}

// runLink is hmk link [COMPUTER|ADDRESS|LINK] [--accept] [--invite], or
// hmk link --again COMPUTER (one listed in hmk computers that doesn't
// link with this one, waits for its code, or whose certificate changed).
func runLink(c *client.Client, w *world, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if len(args) > 0 {
		switch args[0] {
		case "pending":
			return linkPending(w.linkReqs, hasFlag(args, "--json"))
		case "confirm", "refuse":
			if len(args) < 2 {
				return fmt.Errorf("usage: hmk link %s ID|CODE (see hmk link pending)", args[0])
			}
			r, err := findLinkRequest(w.linkReqs, strings.Join(args[1:], " "))
			if err != nil {
				return err
			}
			if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: r.Id, Accept: args[0] == "confirm"}}}); err != nil {
				return err
			}
			switch {
			case args[0] == "refuse":
				printfSafe("Refused %s.\n", r.Name)
			case r.AcceptedThere:
				printfSafe("Linked with %s.\n", r.Name)
			default:
				printfSafe("Linked with %s once it says the codes match too.\n", r.Name)
			}
			return nil
		}
	}
	switch {
	case hasFlag(args, "--invite"):
		r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateLinkInvite{CreateLinkInvite: &pb.CreateLinkInvite{}}})
		if err != nil {
			return err
		}
		fmt.Println(r.GetInvite().Link)
		fmt.Fprintln(os.Stderr, "On your other computer: hmk link LINK (or paste it in Link a Computer…)")
		return nil
	case hasFlag(args, "--accept"):
		// Reachable first: remote access on (what the link makes does it).
		r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateLinkInvite{CreateLinkInvite: &pb.CreateLinkInvite{}}})
		if err != nil {
			return err
		}
		fmt.Println("Waiting for another computer to ask to link (hmk link, or Link a Computer… there)…")
		fmt.Println("If it can't find this one, use this link there: " + r.GetInvite().Link)
		return answerLink(ctx, c, w.linkReqs, true, nil)
	}
	var target string
	for _, a := range args {
		if !strings.HasPrefix(a, "--") {
			target = a
		}
	}
	if hasFlag(args, "--again") {
		lc, err := w.computer(target)
		if err != nil {
			return err
		}
		if lc.ThisComputer || len(lc.Addresses) == 0 {
			return fmt.Errorf("%s has no address to link it at: hmk link ADDRESS", lc.Name)
		}
		target = lc.Addresses[0]
	}
	if !isLinkAddress(target) {
		r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_DiscoverComputers{DiscoverComputers: &pb.DiscoverComputers{}}})
		if err != nil {
			return err
		}
		found := r.GetDiscoveredComputers()
		if found.Error != "" {
			return errors.New(found.Error)
		}
		if target == "" {
			if len(found.List) == 0 {
				fmt.Println("No other computers of yours are on your tailnet, or running Hammock on your chosen local networks.")
				fmt.Println("On a VPN (no Bonjour there)? Link one by its address: hmk link HOST[:PORT]")
				return nil
			}
			for _, d := range found.List {
				state := "offline"
				switch {
				case d.LinkedUserId != "":
					state = "linked"
				case d.Answering:
					state = "running Hammock: hmk link " + d.Name
				case d.Online:
					state = "Hammock isn't answering (open it there, or turn on remote access)"
				}
				via := "Tailscale"
				if d.Via == "local" {
					via = "local network"
				}
				printfSafe("%-24s %-8s %-14s %s\n", d.Name, d.Os, via, state)
			}
			return nil
		}
		var match *pb.DiscoveredComputer
		for _, d := range found.List {
			if strings.EqualFold(d.Name, target) {
				match = d
			}
		}
		switch {
		case match == nil:
			return fmt.Errorf("no computer %q on your tailnet or local network (see hmk link)", target)
		case match.LinkedUserId != "":
			return fmt.Errorf("%s is linked already", match.Name)
		case len(match.Addresses) == 0:
			return fmt.Errorf("%s has no address", match.Name)
		}
		target = match.Addresses[0]
	}
	done := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: target}}})
		done <- err
	}()
	if err := answerLink(ctx, c, nil, false, done); err != nil {
		return err
	}
	fmt.Println("Linked. Its Groups show among yours, and yours there.")
	return nil
}

// isLinkAddress: hmk link's target is used as it is (host:port, an IP, a
// dotted or MagicDNS name, or a link), not looked up on your tailnet by name.
func isLinkAddress(t string) bool {
	return strings.Contains(t, ":") || strings.HasPrefix(t, "hammock://") ||
		net.ParseIP(strings.Trim(t, "[]")) != nil || strings.Contains(t, ".")
}

// answerLink waits for a link request with its code (incoming, or the one
// this asked for), asks whether it matches, and answers. shown are those
// already showing when this connected; done, when set, is the asking
// call's result.
func answerLink(ctx context.Context, c *client.Client, shown []*pb.LinkRequest, incoming bool, done chan error) error {
	asked := false
	answers := make(chan bool, 1)
	var req *pb.LinkRequest
	consider := func(list []*pb.LinkRequest) {
		for _, r := range list {
			if asked || r.Code == "" || r.Incoming != incoming || r.AcceptedHere {
				continue
			}
			asked, req = true, r
			ts := ""
			if r.Source != "" {
				ts = ", from " + r.Source
			}
			if r.TailscaleHost != "" {
				ts = ", on Tailscale " + r.TailscaleHost
				if r.TailscaleLogin != "" {
					ts += " (" + r.TailscaleLogin + ")"
				}
			}
			if r.UnlinkedUnixMs > 0 {
				by := ""
				if r.UnlinkedBy != "" {
					by = ", as " + r.UnlinkedBy + " asked"
				}
				printfSafe("\n⚠ You unlinked %s on %s%s (lost or stolen?). Link it again only if it's back with you.", r.Name, time.UnixMilli(r.UnlinkedUnixMs).Format("2 Jan 2006"), by)
			}
			printfSafe("\n%s (%s%s, key %s) shows a code. Here it's:\n\n    %s\n\nDo they match? [y/N] ", r.Name, cmp.Or(r.Model, r.Os), ts, r.Fingerprint, r.Code)
			go func() {
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				answers <- strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
			}()
		}
	}
	consider(shown)
	for {
		select {
		case err := <-done:
			return err
		case ok := <-answers:
			if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: req.Id, Accept: ok}}}); err != nil {
				return err
			}
			if done == nil {
				if ok {
					fmt.Println("Linked once the other computer says they match too.")
				}
				return nil
			}
			if ok {
				fmt.Println("Waiting for the other computer…")
			}
		case ev, open := <-c.Events:
			if !open {
				return errors.New("the daemon went away")
			}
			consider(ev.GetState().GetLinkRequests())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// onComputerCalls is call, with the session messages (list, resume,
// excerpts) sent to linked computer cid ("" for this one).
func onComputerCalls(call caller, cid string) caller {
	return func(m *pb.ClientMessage) (*pb.ServerMessage, error) {
		switch x := m.Msg.(type) {
		case *pb.ClientMessage_ListSessions:
			x.ListSessions.ComputerId = cid
		case *pb.ClientMessage_ResumeSession:
			x.ResumeSession.ComputerId = cid
		case *pb.ClientMessage_SessionExcerpts:
			x.SessionExcerpts.ComputerId = cid
		}
		return call(m)
	}
}

// linkPending lists link requests showing a code here: what hmk link
// confirm or refuse answers (for a computer without the app, and scripts).
func linkPending(reqs []*pb.LinkRequest, asJSON bool) error {
	var open []*pb.LinkRequest
	for _, r := range reqs {
		if r.Code != "" && !r.AcceptedHere {
			open = append(open, r)
		}
	}
	if asJSON {
		type req struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Model          string `json:"model,omitempty"`
			OS             string `json:"os,omitempty"`
			Code           string `json:"code"`
			Fingerprint    string `json:"fingerprint"`
			Incoming       bool   `json:"incoming"`
			AcceptedThere  bool   `json:"accepted_there"`
			From           string `json:"from,omitempty"`
			TailscaleHost  string `json:"tailscale_host,omitempty"`
			TailscaleLogin string `json:"tailscale_login,omitempty"`
			UnlinkedUnixMs int64  `json:"unlinked_unix_ms,omitempty"`
			CreatedUnixMs  int64  `json:"created_unix_ms"`
		}
		out := []req{}
		for _, r := range open {
			out = append(out, req{r.Id, r.Name, r.Model, r.Os, r.Code, r.Fingerprint, r.Incoming, r.AcceptedThere,
				r.Source, r.TailscaleHost, r.TailscaleLogin, r.UnlinkedUnixMs, r.CreatedUnixMs})
		}
		return printJSON(out)
	}
	if len(open) == 0 {
		fmt.Println("No computer is asking to link. Let one ask: hmk link --accept (or --invite), then hmk link THIS-COMPUTER there.")
		return nil
	}
	for _, r := range open {
		from := r.Source
		if r.TailscaleHost != "" {
			from = "Tailscale " + r.TailscaleHost
			if r.TailscaleLogin != "" {
				from += " (" + r.TailscaleLogin + ")"
			}
		}
		printfSafe("%s  %-20s code %s  %s, key %s", r.Id, r.Name, r.Code, cmp.Or(from, cmp.Or(r.Model, r.Os)), r.Fingerprint)
		if r.UnlinkedUnixMs > 0 {
			fmt.Printf("  ⚠ unlinked here %s", time.UnixMilli(r.UnlinkedUnixMs).Format("2 Jan 2006"))
		}
		fmt.Println()
	}
	fmt.Println("If the code matches the one on the other computer: hmk link confirm ID (else hmk link refuse ID)")
	return nil
}

// findLinkRequest finds a link request showing a code by its id, or by the
// code itself ("482913" or "482 913").
func findLinkRequest(reqs []*pb.LinkRequest, s string) (*pb.LinkRequest, error) {
	digits := strings.ReplaceAll(s, " ", "")
	for _, r := range reqs {
		if r.Code == "" || r.AcceptedHere {
			continue
		}
		if r.Id == s || strings.ReplaceAll(r.Code, " ", "") == digits {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no computer is asking to link with %q (see hmk link pending)", s)
}
