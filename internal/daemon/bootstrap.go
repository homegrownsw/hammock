package daemon

import (
	"encoding/json"
	"time"

	"github.com/alexjsp/hammock/internal/procinfo"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// Bootstrap (person.go). What only you may do asks you in the app or on
// your phone; but on a computer with nothing to ask yet (Linux, or a Mac
// before the app has ever been open, with no phone paired and no computer
// linked) pairing that first phone would need it already paired. So while
// nothing could answer, a program with a terminal that isn't an agent's
// (yours at the console, over ssh, in a pane's shell) does them directly.
//
// The window shuts for good, kept in state.db, the first time anything is
// paired or linked and the first time the app connects: after that, with
// the app quit (or killed) and nothing paired, what only you may do waits
// for you to open the app and allow it, or times out. Unpairing or
// unlinking everything doesn't reopen it. The first phone or computer
// paired or linked after a terminal did any of these is said in Needs You
// (and pushed), kept in state.db until the app (or a device paired before
// it was said) clears it: never the one paired then.

// bootstrapAttentionPrefix starts the id of the Needs You item saying a
// terminal did what only you may while nothing could ask you: only you
// clear it (personStrict, mayClearBootstrap).
const bootstrapAttentionPrefix = "at_person_"

// The bootstrap's state in state.db: whether its window has shut, where a
// terminal did what only you may while it was open (until something is
// paired or linked), and the Needs You item saying so.
const (
	bootstrapClosedSetting  = "bootstrap.closed"
	bootstrapBySetting      = "bootstrap.by"
	bootstrapWarningSetting = "bootstrap.warning"
)

// bootstrapWarning is the kept Needs You item, and the device or computer
// paired or linked as it was raised (which may not clear it).
type bootstrapWarning struct {
	Summary   string `json:"summary"`
	CreatedMs int64  `json:"created_ms"`
	Device    string `json:"device,omitempty"`
	Computer  string `json:"computer,omitempty"`
}

// loadBootstrap restores the bootstrap's state at startup, and shuts the
// window if something is already paired or linked (state from before it
// was kept). Callers hold d.mu.
func (d *Daemon) loadBootstrap() {
	d.remote.bootstrapBy = d.store.Setting(bootstrapBySetting)
	if w, ok := d.bootstrapWarning(); ok {
		item := &pb.AttentionItem{Id: bootstrapAttentionPrefix + "bootstrap", Summary: w.Summary, CreatedUnixMs: w.CreatedMs}
		d.attention[item.Id] = item
	}
	if devs, err := d.store.Devices(); len(d.comp.links) > 0 || err == nil && len(devs) > 0 {
		d.closeBootstrap("something is paired or linked")
	}
}

// bootstrapWarning is the kept warning, if there is one. Callers hold d.mu.
func (d *Daemon) bootstrapWarning() (bootstrapWarning, bool) {
	var w bootstrapWarning
	s := d.store.Setting(bootstrapWarningSetting)
	if s == "" || json.Unmarshal([]byte(s), &w) != nil || w.Summary == "" {
		return w, false
	}
	return w, true
}

// bootstrapClosed reports whether the window has shut. Callers hold d.mu.
func (d *Daemon) bootstrapClosed() bool { return d.store.Setting(bootstrapClosedSetting) != "" }

// closeBootstrap shuts the window for good (why: "the app connected").
// Callers hold d.mu.
func (d *Daemon) closeBootstrap(why string) {
	if d.bootstrapClosed() {
		return
	}
	d.persist(d.store.SetSetting(bootstrapClosedSetting, time.Now().UTC().Format(time.RFC3339)))
	d.audit("local", "bootstrap-shut", "", "what only you may do asks you from now on: "+why)
}

// nobodyToAsk reports whether nothing could answer a request now: no phone
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

// bootstrapOpen reports whether a terminal may still do what only you may
// directly: the window hasn't shut and nothing could answer. Callers hold
// d.mu.
func (d *Daemon) bootstrapOpen() bool { return !d.bootstrapClosed() && d.nobodyToAsk() }

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
	if !d.bootstrapOpen() {
		return false
	}
	what, err := d.personAsk(c, msg)
	if err != nil {
		what = "do something only you may (" + msgName(msg) + ")"
	}
	from := d.origin(c, proc)
	d.audit("local", "bootstrap", "", what+" ("+from+"): nothing could ask you yet")
	d.remote.bootstrapBy = from
	d.persist(d.store.SetSetting(bootstrapBySetting, from))
	return true
}

// afterBootstrap shuts the window once something is first paired or
// linked (what: "Paired Alex's iPhone"; device or computer: which), and
// says, if a terminal did what only you may while it was open, that it
// did. Callers hold d.mu.
func (d *Daemon) afterBootstrap(what, device, computer string) {
	d.closeBootstrap(what)
	if d.remote.bootstrapBy == "" {
		return
	}
	text := what + " from a terminal, before anything could ask you (" + d.remote.bootstrapBy + "). If that wasn't you, remove it in Settings ▸ Devices."
	d.remote.bootstrapBy = ""
	d.persist(d.store.SetSetting(bootstrapBySetting, ""))
	d.audit("local", "bootstrap-closed", "", text)
	now := time.Now().UnixMilli()
	if b, err := json.Marshal(bootstrapWarning{Summary: text, CreatedMs: now, Device: device, Computer: computer}); err == nil {
		d.persist(d.store.SetSetting(bootstrapWarningSetting, string(b)))
	}
	item := &pb.AttentionItem{Id: bootstrapAttentionPrefix + "bootstrap", Summary: text, CreatedUnixMs: now}
	d.attention[item.Id] = item
	d.publish(&pb.StateUpdate{Attention: []*pb.AttentionItem{proto.CloneOf(item)}})
	d.push("errored", item.Id, pushNote{Title: "Hammock", Body: text}, func() bool { return d.attention[item.Id] == item })
}

// mayClearBootstrap reports whether c may clear the bootstrap warning: the
// app, or a phone paired before it was raised (never the one paired as it
// was, nor one paired since: either may be the program's own, answering
// for you), never a linked computer. Callers hold d.mu.
func (d *Daemon) mayClearBootstrap(c *conn) bool {
	if !c.remote && c.peer == "" && c.personOK.Load() {
		return true
	}
	if !c.remote || c.deviceID == "" || c.peer != "" {
		return false
	}
	w, ok := d.bootstrapWarning()
	if !ok {
		return true // nothing kept (an item from before it was): as before
	}
	if c.deviceID == w.Device {
		return false
	}
	devs, _ := d.store.Devices()
	for _, dv := range devs {
		if dv.ID == c.deviceID {
			return dv.Paired.UnixMilli() < w.CreatedMs
		}
	}
	return false
}

// clearBootstrapWarning forgets the kept warning. Callers hold d.mu.
func (d *Daemon) clearBootstrapWarning() {
	d.persist(d.store.SetSetting(bootstrapWarningSetting, ""))
}
