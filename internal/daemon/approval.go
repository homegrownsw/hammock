package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Approval requests (person.go). What only you may do, asked from anything
// but the Hammock app (hmk in a pane's shell or a terminal, the TUI, a
// program outside Hammock), isn't refused: it asks you, like an agent's
// request does. A GrantRequest with person set, worded by Hammock from
// the message ("hmk wants to get a pairing code: …") and saying where it
// came from, shown in Needs You on the Mac and pushed to your phone. The
// request waits (ApprovalPending tells the client so) and is handled as if
// sent now once you allow it, or fails with errDenied. Allow once only:
// nothing here is remembered. Only the app or a paired phone answers one
// (RespondGrant: conn.go), never the program that asked.

var errDenied = errors.New("not allowed: you said no in Hammock or on your phone, or nobody answered in time")

// askPerson asks you to allow msg, a person-only message from c (not
// positively you, not an agent). It returns errDeferred: the reply comes
// once you answer. Read loop.
func (c *conn) askPerson(msg *pb.ClientMessage) error {
	d := c.d
	if msg.Id == 0 {
		// Nothing waits for its answer (a stream of input, an ack).
		return errPersonOnly
	}
	proc := c.originProcess() // (reads processes: not under d.mu)
	d.mu.Lock()
	defer d.mu.Unlock()
	what, err := d.personAsk(c, msg)
	if err != nil {
		return err
	}
	who := "hmk"
	if c.kind != "cli" {
		who = "A program"
	}
	pane := ""
	if cl := c.caller; cl != nil && cl.pane != outsidePane && d.panes[cl.pane] != nil {
		pane = cl.pane
	}
	origin := d.origin(c, proc)
	if p := d.panes[pane]; p != nil {
		if s := p.recentInputFrom(time.Now()); s.kind != "" {
			// Typed there lately by something other than you in the app.
			origin += ", after typing from " + d.inputOrigin(s)
		}
	}
	g := &pendingGrant{
		req: &pb.GrantRequest{
			Id: newID("gr"), RequesterPaneId: pane, TargetPaneId: pane, Action: "person",
			Headline: who + " wants to " + what, Person: true, Origin: origin,
			CreatedUnixMs: time.Now().UnixMilli(),
		},
		c: c, person: true,
	}
	g.done = func(allowed bool) {
		if !allowed {
			c.replyErr(msg.Id, errDenied)
			return
		}
		go c.runApproved(msg)
	}
	d.openGrant(g)
	c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_ApprovalPending{ApprovalPending: &pb.ApprovalPending{
		RequestId: msg.Id, GrantId: g.req.Id, Headline: g.req.Headline, TimeoutMs: grantTimeout.Milliseconds(),
	}}})
	d.log.Info("asked you to allow a person-only request", "pid", c.pid, "msg", msgName(msg), "grant", g.req.Id)
	return errDeferred
}

// runApproved handles msg once you've allowed it, and replies, as the read
// loop would have.
func (c *conn) runApproved(msg *pb.ClientMessage) {
	reply, err := c.route(msg)
	if err == errDeferred {
		return
	}
	c.finish(msg, nil, reply, err)
}

// personAsk says what msg asks, after "hmk wants to": "show a pairing
// code", "unlink Studio", the whole of a schedule. A mistake in it is said
// now, not after you've allowed it. Callers hold d.mu.
func (d *Daemon) personAsk(c *conn, msg *pb.ClientMessage) (string, error) {
	group := func(id string) string {
		if g := d.groups[id]; g != nil && g.Name != "" {
			return "the Group “" + oneLine(g.Name, 60) + "”"
		}
		return "a Group"
	}
	person := func(uid string) string { return cmpOr(oneLine(d.people[uid].GetName(), 60), "someone") }
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_SaveSchedule:
		s, err := d.cleanSchedule(m.SaveSchedule.Schedule)
		if err != nil {
			return "", err
		}
		if s.Id != "" && d.sched.list[s.Id] == nil {
			return "", fmt.Errorf("no such schedule %q", s.Id)
		}
		if n := len([]rune(s.Prompt)); n > maxAskedPrompt {
			return "", fmt.Errorf("the prompt is %d characters (at most %d): put the work in a script or skill and schedule that", n, maxAskedPrompt)
		}
		if s.Id != "" {
			return "change the scheduled task to " + scheduleSummary(s), nil
		}
		return "schedule " + scheduleSummary(s), nil
	case *pb.ClientMessage_DeleteSchedule:
		s := d.sched.list[m.DeleteSchedule.ScheduleId]
		if s == nil {
			return "", fmt.Errorf("no such schedule %q", m.DeleteSchedule.ScheduleId)
		}
		return "delete the scheduled task " + scheduleSummary(s), nil
	case *pb.ClientMessage_CreatePairing:
		return "get a pairing code: anyone who has it can pair a device with " + d.macName(), nil
	case *pb.ClientMessage_ShowPairing:
		return "show a pairing code in the Hammock app", nil
	case *pb.ClientMessage_RevokeDevice:
		return "unpair " + cmpOr(oneLine(d.deviceNamed(m.RevokeDevice.DeviceId), 60), "a device"), nil
	case *pb.ClientMessage_SetRemoteAccess:
		if m.SetRemoteAccess.Enabled {
			return "turn on remote access, so your phones and computers reach this one over the networks you chose", nil
		}
		return "turn off remote access", nil
	case *pb.ClientMessage_SetRemoteNetworks:
		return "change the networks Hammock listens on to " + cmpOr(strings.Join(append(m.SetRemoteNetworks.Networks, m.SetRemoteNetworks.ExtraAddresses...), ", "), "none"), nil
	case *pb.ClientMessage_CreateInvite:
		return "get an invite link to " + group(m.CreateInvite.GroupId) + ": anyone who has it can ask to join", nil
	case *pb.ClientMessage_RevokeInvite:
		return "stop the invite link to " + group(m.RevokeInvite.GroupId) + " working", nil
	case *pb.ClientMessage_RespondJoin:
		j := d.joins[m.RespondJoin.Id]
		if j == nil {
			return "", errors.New("no such join request")
		}
		if !guestRole(m.RespondJoin.Role) {
			return "turn " + person(j.req.UserId) + " away from " + group(j.req.GroupId), nil
		}
		return "let " + person(j.req.UserId) + " " + roleVerb(m.RespondJoin.Role) + " " + group(j.req.GroupId), nil
	case *pb.ClientMessage_SetMember:
		if !guestRole(m.SetMember.Role) {
			return "remove " + person(m.SetMember.UserId) + " from " + group(m.SetMember.GroupId), nil
		}
		return "let " + person(m.SetMember.UserId) + " " + roleVerb(m.SetMember.Role) + " " + group(m.SetMember.GroupId), nil
	case *pb.ClientMessage_StopSharing:
		return "stop sharing " + group(m.StopSharing.GroupId), nil
	case *pb.ClientMessage_SetProfile:
		return "change your name or picture, as the people you share with see them", nil
	case *pb.ClientMessage_JoinShared:
		return "join a Group someone shares with you, from an invite link", nil
	case *pb.ClientMessage_LeaveShared:
		return "leave " + group(m.LeaveShared.GroupId), nil
	case *pb.ClientMessage_LinkComputer:
		return "link this computer with the one at " + cmpOr(oneLine(m.LinkComputer.Address, 100), "an address it gives"), nil
	case *pb.ClientMessage_CreateLinkInvite:
		return "get a link for linking a computer with this one: anyone who has it can ask to link", nil
	case *pb.ClientMessage_RespondLink:
		a := d.comp.attempts[m.RespondLink.Id]
		if a == nil || a.req == nil {
			return "", errors.New("no such link request")
		}
		name := cmpOr(oneLine(a.req.Name, 60), "another computer")
		if !m.RespondLink.Accept {
			return "decline linking " + name, nil
		}
		if a.req.Code != "" {
			return "confirm linking " + name + " (code " + oneLine(a.req.Code, 20) + ")", nil
		}
		return "confirm linking " + name, nil
	case *pb.ClientMessage_UnlinkComputer:
		return "unlink " + d.computerName(m.UnlinkComputer.UserId), nil
	case *pb.ClientMessage_IntroduceComputers:
		return "introduce your linked computers to each other", nil
	case *pb.ClientMessage_SshSetup:
		if m.SshSetup.Apply || m.SshSetup.ApplyJj {
			return "set up SSH to your linked computers (adding Hammock's settings to your SSH and jj config)", nil
		}
		return "check SSH to your linked computers", nil
	case *pb.ClientMessage_HammockKeyCommand:
		return "use the Hammock Key (" + strings.ToLower(strings.TrimPrefix(m.HammockKeyCommand.Op.String(), "OP_")) + ")", nil
	case *pb.ClientMessage_SetApprovalKey:
		return "change the key your phone approves Hammock Key requests with", nil
	case *pb.ClientMessage_RegisterPush:
		return "send notifications to a phone", nil
	case *pb.ClientMessage_UploadFile:
		return "put a file (" + cmpOr(oneLine(filepath.Base(m.UploadFile.Name), 60), "unnamed") + ") into " + d.paneLabel(m.UploadFile.PaneId), nil
	case *pb.ClientMessage_OpenMedia:
		if m.OpenMedia.Screen {
			return "watch this computer's screen", nil
		}
		return "watch " + d.paneLabel(m.OpenMedia.PaneId) + " live", nil
	case *pb.ClientMessage_ScreenSnapshot:
		return "take a picture of this computer's screen", nil
	case *pb.ClientMessage_ListDisplays:
		return "list this computer's displays", nil
	}
	return "do something only you may (" + msgName(msg) + ")", nil
}

// roleVerb is what a guest's role lets them do, after "let them".
func roleVerb(r pb.Role) string {
	if r == pb.Role_ROLE_DRIVER {
		return "drive (run anything as you) in"
	}
	return "view"
}

// deviceNamed is a paired device's name ("" if none).
func (d *Daemon) deviceNamed(id string) string {
	devs, _ := d.store.Devices()
	for _, dv := range devs {
		if dv.ID == id {
			return dv.Name
		}
	}
	return ""
}

// originProc is what's known of a caller's process, for where a request
// came from: none of it vouches for anything (the process can set all of
// it), so it's said as what it says.
type originProc struct {
	prog, parent, tty, ssh string
}

// originProcess reads a local caller's process for origin. Not under d.mu.
func (c *conn) originProcess() originProc {
	var o originProc
	if c.pid <= 0 {
		return o
	}
	if exe, err := procinfo.Path(c.pid); err == nil {
		o.prog = filepath.Base(exe)
	} else if n, err := procinfo.Name(c.pid); err == nil {
		o.prog = n
	}
	if pp, err := procinfo.Parent(c.pid); err == nil && pp > 1 {
		if n, err := procinfo.Name(pp); err == nil {
			o.parent = n
		}
	}
	if t := procinfo.TTY(c.pid); t >= 0 {
		o.tty = ttyName(t)
	}
	for _, kv := range procinfo.Env(c.pid) {
		if v, ok := strings.CutPrefix(kv, "SSH_CONNECTION="); ok {
			from, _, _ := strings.Cut(v, " ")
			if ip := net.ParseIP(from); ip != nil {
				o.ssh = ip.String()
			}
		}
	}
	return o
}

// origin says where c's request came from, for the request's origin line:
// the pane it's in, else its program and terminal, and the ssh session it
// says it's in. Callers hold d.mu.
func (d *Daemon) origin(c *conn, o originProc) string {
	if cl := c.caller; cl != nil && cl.pane != "" && cl.pane != outsidePane {
		if d.panes[cl.pane] != nil {
			if cl.direct && !cl.scoped {
				return "From the shell in " + d.paneLabel(cl.pane)
			}
			return "From a program in " + d.paneLabel(cl.pane) + ", not its own shell"
		}
	}
	prog := oneLine(o.prog, 40)
	if prog != "" && o.parent != "" && o.parent != o.prog {
		prog += " run by " + oneLine(o.parent, 40)
	}
	switch {
	case o.ssh != "" && prog != "":
		return "From " + prog + ", over ssh from " + o.ssh + " (as it says)"
	case prog != "" && o.tty != "":
		return "From " + prog + " on " + o.tty + ", outside Hammock"
	case prog != "":
		return "From " + prog + ", a program outside Hammock with no terminal"
	}
	return "From a program outside Hammock"
}

// ttyName is the name of the terminal device dev ("ttys004", "pts/3"), or
// "a terminal" if it can't be found.
func ttyName(dev int64) string {
	var dirs = []string{"/dev/pts", "/dev"}
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if dir == "/dev" && !strings.HasPrefix(e.Name(), "tty") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if rdev, ok := deviceOf(p); ok && rdev == dev {
				return strings.TrimPrefix(p, "/dev/")
			}
		}
	}
	return "a terminal"
}
