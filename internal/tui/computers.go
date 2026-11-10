package tui

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Linked computers (ADR-0013) in the TUI: Groups on your other computers
// carry their name in their colour, the footer's row of computers picks
// which ones the sidebar shows (⌃B M cycles), and the palette has them.

// setComputers takes the daemon's list. Callers are on the UI goroutine.
func (a *App) setComputers(cs *pb.Computers) {
	if cs.Note != "" && (a.computers == nil || a.computers.Note != cs.Note) {
		a.flash(strings.ReplaceAll(cs.Note, "\n", " "))
	}
	a.computers = cs
	for _, c := range cs.List {
		if c.ThisComputer {
			a.me = c.UserId
		}
	}
	// The computer shown went away: show them all.
	if a.prefs.Computer != "" && a.computer(a.prefs.Computer) == nil {
		a.setComputerFilter("")
	}
}

// linked: other computers are linked with this one.
func (a *App) linked() bool {
	for _, c := range a.linkedList() {
		if !c.ThisComputer && !needsLinking(c) {
			return true
		}
	}
	return false
}

// needsLinking: c isn't linked until it's linked again (it doesn't link
// with this one any more, or waits for its code to be checked).
func needsLinking(c *pb.Computer) bool { return c.Refused || c.Suspect }

// computerState says what's wrong with c's link, if anything (and what to
// do: Link Again, or Remove).
func computerState(c *pb.Computer) string {
	switch {
	case c.Refused:
		return "doesn't link with this computer any more"
	case c.Suspect:
		return "introduced by a computer since unlinked: check its code"
	case c.CertChanged:
		return "its certificate changed: check its code"
	}
	return ""
}

// linkedList is the computers, this one first.
func (a *App) linkedList() []*pb.Computer {
	if a.computers == nil {
		return nil
	}
	return a.computers.List // (only linked ones come)
}

func (a *App) computer(id string) *pb.Computer {
	for _, c := range a.linkedList() {
		if c.UserId == id {
			return c
		}
	}
	return nil
}

// computerOf is the linked computer a Group is on (nil: this one, or a
// Group someone shared).
func (a *App) computerOf(g *pb.Group) *pb.Computer {
	if g.HostUserId == "" || g.MyRole != pb.Role_ROLE_OWNER {
		return nil
	}
	return a.computer(g.HostUserId)
}

// filterComputer is the computer new Groups go on: the one the sidebar
// shows, "" for this one.
func (a *App) filterComputer() string {
	if a.prefs.Computer == a.me || !a.linked() {
		return ""
	}
	return a.prefs.Computer
}

// shownGroups are the Groups the sidebar shows: all, or one computer's.
func (a *App) shownGroups() []*pb.Group {
	f := a.prefs.Computer
	if f == "" || !a.linked() {
		return a.groups
	}
	var out []*pb.Group
	for _, g := range a.groups {
		c := a.computerOf(g)
		if (f == a.me && c == nil) || (c != nil && c.UserId == f) {
			out = append(out, g)
		}
	}
	return out
}

func (a *App) setComputerFilter(id string) {
	a.prefs.Computer = id
	a.prefs.save()
	a.dirty = true
}

// cycleComputer shows the next computer's Groups (all, then each).
func (a *App) cycleComputer() {
	list := a.linkedList()
	if len(list) < 2 {
		a.flash("No other computers are linked: hmk link links one")
		return
	}
	ids := []string{""}
	for _, c := range list {
		ids = append(ids, c.UserId)
	}
	i := 0
	for j, id := range ids {
		if id == a.prefs.Computer {
			i = j
		}
	}
	next := ids[(i+1)%len(ids)]
	a.setComputerFilter(next)
	if c := a.computer(next); c != nil {
		a.flash("Showing " + a.computerName(c) + "'s Groups")
	} else {
		a.flash("Showing all your computers' Groups")
	}
}

func (a *App) computerName(c *pb.Computer) string {
	if c.ThisComputer {
		return "this computer"
	}
	return c.Name
}

// tint is a computer's badge colour in the terminal.
func (a *App) tint(c *pb.Computer) RGB {
	h := strings.TrimPrefix(c.Tint, "#")
	v, err := strconv.ParseUint(h, 16, 32)
	if c.ThisComputer || len(h) != 6 || err != nil {
		return a.th.fg
	}
	col := RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}
	// Lighter on a dark sidebar, so it reads.
	if lum(a.th.sidebar) < 0.4 {
		col = mix(col, RGB{255, 255, 255}, 0.35)
	}
	return col
}

func lum(c RGB) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

// drawComputers draws the footer's row of computers at y: All, then each
// (name, and • while reachable); the one shown highlighted. Clicking one
// shows only its Groups, again (or All) shows them all.
func (a *App) drawComputers(y, sw int) {
	s, th := a.scr, a.th
	x := 2
	item := func(id, label string, fg RGB) {
		on := a.prefs.Computer == id
		w := textWidth(label) + 2
		if x+w > sw-2 {
			return
		}
		bg := mix(th.sidebar, th.hover, a.hoverAmount("computer:"+id, a.hovered(zComputer, id)))
		if on {
			bg = th.pill
		}
		s.Paint(x, y, w, 1, bg)
		s.Text(x+1, y, w-1, label, Cell{FG: fg, HasFG: true, BG: bg, HasBG: true, Bold: on})
		a.addZone(zone{r: rect{x, y, w, 1}, kind: zComputer, id: id})
		x += w + 1
	}
	item("", "All", th.fg)
	for _, c := range a.linkedList() {
		label := c.Name
		if c.ThisComputer {
			label = "This"
		}
		if e := emojiIcon(c); e != "" {
			label = e + " " + label
		}
		switch {
		case computerState(c) != "":
			label += "!"
		case c.Online:
			label += "•"
		default:
			label += "◦"
		}
		item(c.UserId, label, a.tint(c))
	}
}

// computerItems are the palette's computer commands.
func (a *App) computerItems(act func(id, title, hint, kw string, run func())) {
	// Each request waiting on you (several computers may ask at once).
	for _, r := range a.waitingLinks() {
		act("answer-link:"+r.Id, "Check the Code from "+r.Name+"…", "", "link computer code match "+r.Name, func() { a.answerLink(r) })
		act("decline-link:"+r.Id, "Codes Don't Match: Don't Link "+r.Name, "", "link computer code decline refuse different "+r.Name, func() { a.respondLink(r, false) })
	}
	act("link-computer", "Link a Computer…", "", "pair mac another machine linux tailscale", a.openLinkComputer)
	// Those that need you, whether or not any other is linked.
	for _, c := range a.linkedList() {
		c := c
		if c.ThisComputer || computerState(c) == "" {
			continue
		}
		if len(c.Addresses) > 0 {
			act("link-again:"+c.UserId, "Link "+c.Name+" Again…", "", "link again code check certificate "+computerState(c)+" "+c.Name, func() {
				a.linkComputer(c.Addresses[0], nil)
			})
		}
		if needsLinking(c) {
			act("remove:"+c.UserId, "Remove "+c.Name+" (Here Only)", "", "forget remove computer "+computerState(c)+" "+c.Name, func() {
				a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{UserId: c.UserId, Forget: true}}}, func(*pb.ServerMessage) {
					if c.Suspect {
						a.flash("Removed " + c.Name + " here: only Link Again (its code checked) brings it back")
					} else {
						a.flash("Removed " + c.Name + " here")
					}
				})
			})
			act("unlink:"+c.UserId, "Unlink "+c.Name+" From All Your Computers…", "", "unlink lost stolen computer "+c.Name, func() { a.confirmUnlink(c) })
		}
	}
	if !a.linked() {
		return
	}
	act("show-all-computers", "Show All Computers", "⌃B M", "computers filter sidebar every", func() { a.setComputerFilter("") })
	for _, c := range a.linkedList() {
		c := c
		name := c.Name
		if c.ThisComputer {
			name = "This Computer"
		}
		act("show-computer:"+c.UserId, "Show Only "+name, "", "computer filter sidebar "+c.Name, func() { a.setComputerFilter(c.UserId) })
		for _, t := range tuiTints {
			// Each computer's colour is its own: not one another has (but
			// several may have none).
			if !strings.EqualFold(t.hex, c.Tint) && (t.hex == "none" || a.tintOwner(t.hex) == nil) {
				act("tint:"+c.UserId+":"+t.hex, "Colour "+name+" "+t.name, "", "computer color colour badge tint "+c.Name, func() {
					a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: c.UserId, Tint: t.hex}}}, nil)
				})
			}
		}
		for _, ic := range tuiIcons {
			if ic.emoji != c.Icon {
				icon := ic.emoji
				act("icon:"+c.UserId+":"+icon, "Icon "+name+" "+icon+" "+ic.name, "", "computer icon emoji badge symbol "+c.Name+" "+ic.name, func() {
					a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: c.UserId, Icon: &icon}}}, nil)
				})
			}
		}
		if c.Icon != "" {
			act("icon:"+c.UserId+":", "Icon "+name+" as Its Kind", "", "computer icon default reset badge "+c.Name, func() {
				none := ""
				a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: c.UserId, Icon: &none}}}, nil)
			})
		}
		if !needsLinking(c) {
			act("unlink:"+c.UserId, "Unlink "+name+"…", "", "remove forget computer "+c.Name, func() { a.confirmUnlink(c) })
		}
		if c.Online {
			on := c.UserId
			if c.ThisComputer {
				on = ""
			}
			act("new-group-on:"+c.UserId, "New Group on "+name, "", "create computer "+c.Name, func() { a.newGroupOn(on) })
			if !c.ThisComputer {
				act("new-agent-on:"+c.UserId, "New Agent Tab on "+name+"…", "", "start claude codex agent computer "+c.Name, func() { a.openStartAgentOn(c.UserId, c.Name) })
				act("find-session-on:"+c.UserId, "Find Session on "+name+"…", "", "resume claude codex history computer "+c.Name, func() { a.openSessionFinderOn(c.UserId) })
			}
		}
	}
}

// applyLinkRequests keeps the links showing a code, and puts a new one in
// front of you to check (the daemon's "do the codes match?").
func (a *App) applyLinkRequests(u *pb.StateUpdate) {
	if u.Full {
		a.linkReqs = nil
	}
	for _, r := range u.LinkRequests {
		if i := slices.IndexFunc(a.linkReqs, func(x *pb.LinkRequest) bool { return x.Id == r.Id }); i >= 0 {
			a.linkReqs[i] = r
		} else {
			a.linkReqs = append(a.linkReqs, r)
		}
	}
	a.linkReqs = slices.DeleteFunc(a.linkReqs, func(x *pb.LinkRequest) bool { return slices.Contains(u.ResolvedLinkIds, x.Id) })
	if a.linkShown == nil {
		a.linkShown = map[string]bool{}
	}
	for _, r := range a.linkReqs {
		if r.Code == "" || r.AcceptedHere || a.linkShown[r.Id] {
			continue
		}
		switch {
		case !r.Incoming && a.linking && a.overlay == nil:
			a.answerLink(r) // the link you just asked for: you're waiting on it
		case r.Incoming:
			// Another computer asking: said, not put over the terminal,
			// where a keystroke meant for a pane could answer it.
			a.linkShown[r.Id] = true
			a.flash(r.Name + " wants to link: ⌃B k, then Check the Code")
		}
	}
}

// waitingLinks are the link requests waiting on you here, oldest first:
// another computer asking, or a link this TUI asked for (one asked by hammock
// link prompts in its own terminal).
func (a *App) waitingLinks() []*pb.LinkRequest {
	var out []*pb.LinkRequest
	for _, r := range a.linkReqs {
		if r.Code != "" && !r.AcceptedHere && (r.Incoming || a.linking) {
			out = append(out, r)
		}
	}
	return out
}

// answerLink asks whether a link request's code matches the other computer's.
func (a *App) answerLink(r *pb.LinkRequest) {
	if a.linkShown == nil {
		a.linkShown = map[string]bool{}
	}
	a.linkShown[r.Id] = true
	who := r.Name
	if m := cmp.Or(r.Model, r.Os); m != "" {
		who += " (" + m + ")"
	}
	lines := []string{}
	if r.Incoming {
		lines = append(lines, who+" wants to link with this computer.")
	} else {
		lines = append(lines, "Linking with "+who+".")
	}
	if r.TailscaleHost != "" {
		lines = append(lines, "Tailscale: "+r.TailscaleHost+", "+cmp.Or(r.TailscaleLogin, "unknown login"))
	} else if r.Source != "" {
		lines = append(lines, "From "+r.Source)
	}
	if r.UnlinkedUnixMs > 0 {
		by := ""
		if r.UnlinkedBy != "" {
			by = ", as " + r.UnlinkedBy + " asked"
		}
		lines = append(lines, "", "⚠ You unlinked it on "+time.UnixMilli(r.UnlinkedUnixMs).Format("2 Jan 2006")+by+".",
			"Link it again only if it's back with you.")
	}
	lines = append(lines, "Key "+r.Fingerprint, "", "Check it shows the same code:", "", "        "+r.Code, "",
		"Linked computers see and drive each other's Groups.",
		"Cancel leaves it waiting; if they differ, the palette's", "Codes Don't Match says no.")
	a.openDialog(&dialog{title: "Link Computers?", okLabel: "They Match", confirm: true, lines: lines,
		submit: func([]string) { a.respondLink(r, true) }})
}

// respondLink answers a link request (the codes match, or they don't).
func (a *App) respondLink(r *pb.LinkRequest, ok bool) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: r.Id, Accept: ok}}}, func(*pb.ServerMessage) {
		if ok {
			a.flash("Linked once " + r.Name + " says they match too")
		} else {
			a.flash("Not linked with " + r.Name)
		}
	})
}

// openLinkComputer asks which computer to link (from your tailnet, or an
// address or hammock://link/ link), then links it: the code to check shows
// when it's ready.
func (a *App) openLinkComputer() {
	a.flash("Looking for your computers…")
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_DiscoverComputers{DiscoverComputers: &pb.DiscoverComputers{}}}, func(r *pb.ServerMessage) {
		found := r.GetDiscoveredComputers()
		lines := []string{"Your computers on Tailscale, and Hammock on your local network:"}
		first := ""
		for _, d := range found.GetList() {
			state := "offline"
			switch {
			case d.LinkedUserId != "":
				state = "linked"
			case d.Answering:
				state = "running Hammock"
				if first == "" {
					first = d.Name
				}
			case d.Online:
				state = "Hammock isn't answering"
			}
			via := ""
			if d.Via == "local" {
				via = " · local network"
			}
			lines = append(lines, "  "+d.Name+via+" · "+state)
		}
		if len(found.GetList()) == 0 {
			none := "  none found"
			if e := found.GetError(); e != "" {
				none += " (" + e + ")"
			}
			lines = append(lines, none)
		}
		lines = append(lines, "", "Or type an address (host or host:port: over a VPN, say), or paste a hammock://link/ link.")
		a.openDialog(&dialog{title: "Link a Computer", okLabel: "Link", lines: lines,
			fields: []*field{{label: "Computer", value: first}},
			submit: func(v []string) { a.linkComputer(strings.TrimSpace(v[0]), found.GetList()) }})
	})
}

// linkComputer links with target (a name from found, or an address or
// link). It runs until the code is checked on both, so off a.call.
func (a *App) linkComputer(target string, found []*pb.DiscoveredComputer) {
	if target == "" {
		return
	}
	for _, d := range found {
		if strings.EqualFold(d.Name, target) && len(d.Addresses) > 0 {
			target = d.Addresses[0]
		}
	}
	a.linking = true
	a.flash("Asking it to link…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()
		_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: target}}})
		a.actions <- func() {
			a.linking = false
			if err != nil {
				a.flash("⚠ " + err.Error())
				return
			}
			a.flash("Linked: its Groups show among yours")
		}
	}()
}

// tuiTints are the badge colours, as the Mac names them.
var tuiTints = []struct{ hex, name string }{
	{"none", "Default"}, {"#2563EB", "Blue"}, {"#7C3AED", "Purple"}, {"#15803D", "Green"}, {"#C2410C", "Orange"},
	{"#DB2777", "Pink"}, {"#0891B2", "Teal"}, {"#A16207", "Amber"}, {"#4F46E5", "Indigo"},
}

// tuiIcons are the emoji offered as a computer's icon in the palette (the
// Mac's picker has every emoji, and SF Symbols, which a terminal can't draw).
var tuiIcons = []struct{ emoji, name string }{
	{"💻", "Laptop"}, {"🖥️", "Desktop"}, {"🗄️", "Server"}, {"📦", "Box"}, {"🏠", "Home"}, {"🏢", "Office"},
	{"☁️", "Cloud"}, {"🎮", "Games"}, {"🧪", "Lab"}, {"🚀", "Rocket"}, {"🛰️", "Satellite"}, {"🐧", "Penguin"},
}

// emojiIcon is c's icon if it's an emoji (an SF Symbol's name is ASCII:
// the terminal can't draw it), else "".
func emojiIcon(c *pb.Computer) string {
	for _, r := range c.Icon {
		if r >= 0x80 {
			return c.Icon
		}
	}
	return ""
}

// tintOwner is the linked computer whose colour hex is, if any.
func (a *App) tintOwner(hex string) *pb.Computer {
	for _, c := range a.linkedList() {
		if strings.EqualFold(c.Tint, hex) && !needsLinking(c) {
			return c
		}
	}
	return nil
}

// confirmUnlink asks before unlinking c (this computer: from the others).
func (a *App) confirmUnlink(c *pb.Computer) {
	lines := []string{"It's unlinked from all your computers: its Groups", "stop showing on them, and theirs on it."}
	if needsLinking(c) {
		lines = []string{"For a lost or stolen computer: every computer of", "yours refuses it for good. To forget it here only,", "use Remove."}
	}
	title := "Unlink " + c.Name + "?"
	if c.ThisComputer {
		title, lines = "Unlink This Computer?", []string{"Your other computers' Groups stop showing here,", "and this one's there."}
	}
	a.openDialog(&dialog{title: title, okLabel: "Unlink", confirm: true, lines: lines, submit: func([]string) {
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{UserId: c.UserId}}}, nil)
	}})
}

// openRemoteNetworks shows the networks remote access may listen on and
// lets you choose them, and the extra addresses it gives out (ADR-0016).
func (a *App) openRemoteNetworks() {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ListDevices{ListDevices: &pb.ListDevices{}}}, func(r *pb.ServerMessage) {
		d := r.GetDevices()
		state := "off"
		if d.GetRemoteEnabled() {
			state = "on"
		}
		lines := []string{"Remote access is " + state + ". It listens only on the networks chosen,", "whenever this computer is on one:", ""}
		var chosen []string
		for _, n := range d.GetNetworks() {
			mark := "  "
			if n.Enabled {
				mark = "✓ "
				chosen = append(chosen, n.Id)
			}
			where := strings.Join(n.Addresses, ", ")
			if !n.Connected {
				where = "not on it now"
			}
			label := ""
			if n.Label != n.Id {
				label = n.Label + " · "
			}
			line := mark + n.Id + "  " + label + where
			if n.Public {
				line += "  ⚠ public"
			}
			lines = append(lines, line)
		}
		lines = append(lines, "", "Networks: tailscale, or subnets, separated by commas.",
			"Extra addresses: host[:port] to give out too (a DNS name, a VPN endpoint).")
		a.openDialog(&dialog{title: "Remote Access Networks", okLabel: "Save", lines: lines,
			fields: []*field{{label: "Networks", value: strings.Join(chosen, ", ")}, {label: "Extra addresses", value: strings.Join(d.GetExtraAddresses(), ", ")}},
			submit: func(v []string) {
				split := func(s string) []string {
					var out []string
					for _, x := range strings.Split(s, ",") {
						if x = strings.TrimSpace(x); x != "" {
							out = append(out, x)
						}
					}
					return out
				}
				a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteNetworks{SetRemoteNetworks: &pb.SetRemoteNetworks{
					Networks: split(v[0]), ExtraAddresses: split(v[1])}}}, func(*pb.ServerMessage) {
					a.flash("Remote access networks saved")
				})
			}})
	})
}
