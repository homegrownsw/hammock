package daemon

import (
	"time"

	"github.com/alexjsp/hammock/internal/procinfo"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// Bootstrap (person.go). What only you may do asks you in the app or on
// your phone; but on a computer with nothing to ask yet (Linux, or a Mac
// before the app is open, with no phone paired and no computer linked)
// pairing that first phone would need it already paired. So while nothing
// could answer, a program with a terminal that isn't an agent's (yours at
// the console, over ssh, in a pane's shell) does them directly. The
// window shuts once anything is paired or linked (the records, whatever
// state they're in: a suspect link still counts), and unpairing or
// unlinking is itself only yours, asked about, so an agent can't reopen
// it. The first phone or computer paired or linked after a terminal did
// any of these is said in Needs You (and pushed), there and on the new one.

// bootstrapAttentionPrefix starts the id of the Needs You item saying a
// terminal did what only you may while nothing could ask you: only you
// clear it (personStrict).
const bootstrapAttentionPrefix = "at_person_"

// nobodyToAsk reports whether nothing could answer a request: no phone
// paired, no computer linked, the Mac app not open. Callers hold d.mu.
func (d *Daemon) nobodyToAsk() bool {
	if len(d.comp.links) > 0 {
		return false
	}
	if devs, err := d.store.Devices(); err != nil || len(devs) > 0 {
		return false
	}
	for o := range d.conns {
		if !o.remote && o.peer == "" && o.kind == "mac" && o.personOK.Load() {
			return false
		}
	}
	return true
}

// hasTTY reports whether pid has a controlling terminal (tests fake it).
var hasTTY = func(pid int) bool { return procinfo.TTY(pid) >= 0 }

// bootstrap reports whether c, not an agent and in a terminal, may do msg
// (person-only) directly because nothing could ask you, noting that it
// did. Read loop.
func (c *conn) bootstrap(msg *pb.ClientMessage) bool {
	if c.caller.isAgent() || c.pid <= 0 || !hasTTY(c.pid) {
		return false
	}
	proc := c.originProcess() // (reads processes: not under d.mu)
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.nobodyToAsk() {
		return false
	}
	what, err := d.personAsk(c, msg)
	if err != nil {
		what = "do something only you may (" + msgName(msg) + ")"
	}
	from := d.origin(c, proc)
	d.audit("local", "bootstrap", "", what+" ("+from+"): nothing could ask you yet")
	d.remote.bootstrapBy = from
	return true
}

// afterBootstrap says, once something is first paired or linked (what:
// "Paired Alex's iPhone"), that a terminal did what only you may while
// nothing could ask you, if one did. Callers hold d.mu.
func (d *Daemon) afterBootstrap(what string) {
	if d.remote.bootstrapBy == "" {
		return
	}
	text := what + " from a terminal, before anything could ask you (" + d.remote.bootstrapBy + "). If that wasn't you, remove it in Settings ▸ Devices."
	d.remote.bootstrapBy = ""
	d.audit("local", "bootstrap-closed", "", text)
	item := &pb.AttentionItem{Id: bootstrapAttentionPrefix + "bootstrap", Summary: text, CreatedUnixMs: time.Now().UnixMilli()}
	d.attention[item.Id] = item
	d.publish(&pb.StateUpdate{Attention: []*pb.AttentionItem{proto.CloneOf(item)}})
	d.push("errored", item.Id, pushNote{Title: "Hammock", Body: text}, func() bool { return d.attention[item.Id] == item })
}
