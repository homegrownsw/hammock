package daemon

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alexjsp/hammock/internal/hammockconfig"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Security settings in the config file. It's yours to edit by hand, so
// Hammock takes what it says; but anything of yours can write it, an agent
// included, and some keys open Hammock up: screen sharing, the networks
// remote access listens on, the SSH agent Hammock's passes requests to and
// forwarding keys to linked computers, links a browser pane opens in apps
// without asking, paste protection. When one of those is loosened by a
// change nobody here made (the Mac app says what it writes, ConfigWritten;
// the daemon notes its own), the change stands, and you're told: an audit
// entry, and an item in Needs You (a push, if you're away) saying what.

// configCheck is a security setting: its value in effect from its lines
// (set: the key is in the file at all), and how a change from old to now
// loosened it, in words ("" if it didn't).
type configCheck struct {
	key      string
	parse    func(lines []string, set bool) []string
	loosened func(old, now []string) string
}

func parseBool(def bool) func([]string, bool) []string {
	return func(lines []string, _ bool) []string {
		v := def
		if len(lines) > 0 {
			switch lines[0] {
			case "true":
				v = true
			case "false":
				v = false
			}
		}
		if v {
			return []string{"true"}
		}
		return []string{"false"}
	}
}

func parseList(lines []string, _ bool) []string {
	var out []string
	if len(lines) > 0 {
		for _, s := range strings.Split(lines[0], ",") {
			if s = strings.ToLower(strings.TrimSpace(s)); s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}
	return out
}

func parseFirst(lines []string, _ bool) []string {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil
	}
	return []string{strings.TrimSpace(lines[0])}
}

// parseNetworks is chosenNetworks' reading of remote-network.
func parseNetworks(lines []string, set bool) []string {
	if !set {
		return []string{netTailscale}
	}
	var out []string
	for _, v := range lines {
		if id := normalizeNetID(v); id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// added is what now has that old hasn't.
func added(old, now []string) []string {
	var out []string
	for _, v := range now {
		if !slices.Contains(old, v) {
			out = append(out, v)
		}
	}
	return out
}

func turnedOn(old, now []string) bool { return now[0] == "true" && old[0] != "true" }

var configChecks = []configCheck{
	{"screen-sharing", parseBool(false), func(old, now []string) string {
		if turnedOn(old, now) {
			return "Screen sharing was turned on: your iPhone and other Macs may see and control this Mac's screen"
		}
		return ""
	}},
	{"remote-network", parseNetworks, func(old, now []string) string {
		if a := added(old, now); len(a) > 0 {
			return "Remote access now also listens on " + strings.Join(a, ", ")
		}
		return ""
	}},
	{"ssh-upstream-agent", parseFirst, func(old, now []string) string {
		if len(now) > 0 && !slices.Equal(old, now) {
			return "Hammock's SSH agent now passes requests on to " + now[0]
		}
		return ""
	}},
	{"ssh-agent-forwarding", parseBool(true), func(old, now []string) string {
		if turnedOn(old, now) {
			return "SSH agent forwarding was turned on: panes on your linked computers use the keys of the one you're at"
		}
		return ""
	}},
	{"browser-open-schemes", parseList, func(old, now []string) string {
		if a := added(old, now); len(a) > 0 {
			return "Browser panes now open " + strings.Join(a, ":, ") + ": links in their apps without asking"
		}
		return ""
	}},
	{"clipboard-paste-protection", parseBool(true), func(old, now []string) string {
		if now[0] == "false" && old[0] != "false" {
			return "Paste protection was turned off: pastes that could run commands no longer ask first"
		}
		return ""
	}},
}

// configWatch is what the daemon last saw of the security settings, and
// what the app (or the daemon itself) said it wrote.
type configWatch struct {
	mu      sync.Mutex
	stamp   fileStamp
	seen    map[string][]string // key → its value in effect when last read (nil: not read yet)
	written map[string]written  // key → what was last written here, and when
	pending map[string]loosening
}

type written struct {
	value []string // in effect
	at    time.Time
}

// loosening is a change not yet said: the app may be about to say it
// made it.
type loosening struct {
	old   []string
	since time.Time
}

// configWrittenFor is how long the word that a change was made here stands.
const configWrittenFor = time.Minute

// noteWritten records that key's lines were set to lines here (in the
// app's Settings, or by the daemon): not a change to warn about.
func (w *configWatch) noteWritten(key string, lines []string, set bool) {
	for _, c := range configChecks {
		if c.key != key {
			continue
		}
		w.mu.Lock()
		if w.written == nil {
			w.written = map[string]written{}
		}
		w.written[key] = written{value: c.parse(lines, set), at: time.Now()}
		w.mu.Unlock()
	}
}

// ours reports whether now is what was written here for c's key, lately.
// Callers hold w.mu.
func (w *configWatch) ours(c configCheck, now []string) bool {
	wr, ok := w.written[c.key]
	return ok && time.Since(wr.at) < configWrittenFor && slices.Equal(wr.value, now)
}

// configWatchEvery is how often the config file is looked at.
var configWatchEvery = 2 * time.Second

// watchConfig checks the security settings whenever the config file
// changes, until the daemon stops.
func (d *Daemon) watchConfig() {
	t := time.NewTicker(configWatchEvery)
	defer t.Stop()
	for {
		d.checkConfig()
		select {
		case <-d.done:
			return
		case <-t.C:
		}
	}
}

// checkConfig reads the security settings if the file changed, and tells
// you about any loosened by nobody here: a tick later, so the app's
// ConfigWritten, sent just after it writes, isn't beaten to it.
func (d *Daemon) checkConfig() {
	w := &d.cfgw
	st := stamp(hammockconfig.Path())
	w.mu.Lock()
	first := w.seen == nil
	if first {
		w.seen, w.pending = map[string][]string{}, map[string]loosening{}
	}
	if st != w.stamp || first {
		w.stamp = st
		for _, c := range configChecks {
			now := c.parse(hammockconfig.Repeated(c.key))
			old := w.seen[c.key]
			w.seen[c.key] = now
			if first {
				continue // as it was when the daemon started: yours
			}
			if p, ok := w.pending[c.key]; ok {
				old = p.old // loosened from what it was before that
			}
			if c.loosened(old, now) != "" && !w.ours(c, now) {
				w.pending[c.key] = loosening{old: old, since: time.Now()}
			} else {
				delete(w.pending, c.key) // put back, or done here
			}
		}
	}
	var keys, tell []string
	for _, c := range configChecks {
		p, ok := w.pending[c.key]
		if !ok || time.Since(p.since) < configWatchEvery/2 {
			continue // the app's word may be on its way
		}
		delete(w.pending, c.key)
		if now := w.seen[c.key]; !w.ours(c, now) {
			if what := c.loosened(p.old, now); what != "" {
				keys, tell = append(keys, c.key), append(tell, what)
			}
		}
	}
	w.mu.Unlock()
	if len(tell) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, what := range tell {
		d.configLoosened(keys[i], what+" (in the config file, not in Hammock's Settings).")
	}
}

// configLoosened records and tells you about a security setting loosened
// in the config file. Callers hold d.mu.
func (d *Daemon) configLoosened(key, what string) {
	d.audit("config", "config-loosened", key, what)
	item := &pb.AttentionItem{Id: "at_config_" + key, Summary: what, CreatedUnixMs: time.Now().UnixMilli()}
	d.attention[item.Id] = item
	d.publish(&pb.StateUpdate{Attention: []*pb.AttentionItem{item}})
	d.push("errored", item.Id, pushNote{Title: "Hammock", Body: what}, func() bool { return d.attention[item.Id] == item })
	d.log.Warn("security setting loosened in the config file", "key", key, "what", what)
}

// configWritten is the Mac app's word that it wrote these settings.
func (d *Daemon) configWritten(m *pb.ConfigWritten) {
	for _, e := range m.Entries {
		d.cfgw.noteWritten(e.Key, e.Values, len(e.Values) > 0)
	}
}
