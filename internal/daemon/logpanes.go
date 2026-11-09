package daemon

// Log panes: a terminal pane whose command reads a log (a file, macOS's log
// stream, a simulator's, adb logcat, any command), with what it prints
// parsed into entries (internal/logview) that clients watch filtered. The
// terminal stays as it is: clients show the log view over it, and the raw
// output when asked.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alexjsp/hammock/internal/logview"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// logCommand is the command for a log source, and its title.
func logCommand(src *pb.LogSource) (cmd []string, label string, err error) {
	osLog := func(prefix []string, what string) []string {
		c := append(prefix, "stream", "--style", "compact")
		switch src.Level {
		case "", "default":
		case "info", "debug":
			c = append(c, "--level", src.Level)
		default:
			err = fmt.Errorf("level %q: it's default, info or debug", src.Level)
		}
		if src.Process != "" {
			c = append(c, "--process", src.Process)
			label = what + ": " + src.Process
		} else {
			label = what
		}
		if src.Predicate != "" {
			c = append(c, "--predicate", src.Predicate)
		}
		return c
	}
	switch src.Kind {
	case "file":
		if src.Path == "" {
			return nil, "", errors.New("which file?")
		}
		if !filepath.IsAbs(src.Path) {
			return nil, "", errors.New("the file's path must be absolute")
		}
		// -F follows it through rotation and waits for it to appear.
		cmd, label = []string{"/usr/bin/tail", "-n", "5000", "-F", src.Path}, filepath.Base(src.Path)
	case "macos":
		cmd = osLog([]string{"/usr/bin/log"}, "Console")
	case "simulator":
		dev := src.Device
		if dev == "" {
			dev = "booted"
		}
		cmd = osLog([]string{"/usr/bin/xcrun", "simctl", "spawn", dev, "log"}, "Simulator log")
	case "logcat":
		c := "adb"
		if src.Device != "" {
			c += " -s " + shellQuote(src.Device)
		}
		// Through a login shell: adb lives wherever the SDK put it.
		cmd, label = []string{loginShell(), "-l", "-c", "exec " + c + " logcat -v threadtime"}, "logcat"
		if src.Device != "" {
			label += ": " + src.Device
		}
	case "command":
		if len(src.Command) == 0 {
			return nil, "", errors.New("which command?")
		}
		line := src.Command[0]
		if len(src.Command) > 1 {
			var q []string
			for _, a := range src.Command {
				q = append(q, shellQuote(a))
			}
			line = strings.Join(q, " ")
		}
		cmd, label = []string{loginShell(), "-l", "-c", line}, strings.Join(src.Command, " ")
	default:
		return nil, "", fmt.Errorf("log source %q: it's file, macos, simulator, logcat or command", src.Kind)
	}
	if err != nil {
		return nil, "", err
	}
	if src.Label != "" {
		label = src.Label
	}
	return cmd, label, nil
}

// logPane is a log pane's entries and who's watching them.
type logPane struct {
	d     *Daemon
	id    string
	store *logview.Store

	fmu      sync.Mutex // the feeder (fed from the pane's reader, flushed by the ticker)
	feed     *logview.Feeder
	lastFeed time.Time

	mu       sync.Mutex
	watchers map[*conn]*logWatcher
	queue    []*pb.LogEntry
	facetsAt time.Time
	changed  bool // entries since facets went out
	cleared  bool
	stop     chan struct{}
	once     sync.Once
}

type logWatcher struct {
	m      *logview.Matcher
	filter *pb.LogFilter
	after  uint64 // entries up to this went out with the first page
}

func (d *Daemon) logPath(id string) string {
	return filepath.Join(d.cfg.Dir, "panelogs", id+".jsonl")
}

// newLogPane starts keeping a log pane's entries (loading those kept).
func (d *Daemon) newLogPane(id string) *logPane {
	_ = os.MkdirAll(filepath.Join(d.cfg.Dir, "panelogs"), 0o700)
	lp := &logPane{d: d, id: id, store: logview.NewStore(d.logPath(id)), watchers: map[*conn]*logWatcher{}, stop: make(chan struct{})}
	lp.feed = &logview.Feeder{Store: lp.store, OnEntry: lp.added}
	go lp.run()
	return lp
}

// write takes the pane's output, from output offset off.
func (lp *logPane) write(p []byte, off uint64) {
	lp.fmu.Lock()
	lp.feed.Write(p, off)
	lp.lastFeed = time.Now()
	lp.fmu.Unlock()
}

// restarted: a new process, its output counted from 0 again.
func (lp *logPane) restarted() {
	lp.fmu.Lock()
	lp.feed.Restarted()
	lp.fmu.Unlock()
}

func (lp *logPane) added(e *pb.LogEntry) {
	lp.mu.Lock()
	lp.changed = true
	if len(lp.watchers) > 0 {
		lp.queue = append(lp.queue, e)
	}
	lp.mu.Unlock()
}

func (lp *logPane) run() {
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	lastSync := time.Now()
	for {
		select {
		case <-lp.stop:
			return
		case <-tick.C:
		}
		// An entry is held until the next begins (it may go on for lines);
		// after a pause it's whole.
		lp.fmu.Lock()
		if !lp.lastFeed.IsZero() && time.Since(lp.lastFeed) > 250*time.Millisecond {
			lp.feed.Flush(true)
			lp.lastFeed = time.Time{}
		}
		lp.fmu.Unlock()
		lp.deliver()
		if time.Since(lastSync) > time.Second {
			lp.store.Sync()
			lastSync = time.Now()
		}
	}
}

// deliver sends watchers the entries that came since, each its own filter's.
func (lp *logPane) deliver() {
	lp.mu.Lock()
	q := lp.queue
	lp.queue = nil
	var facets []*pb.LogFacet
	if lp.changed && time.Since(lp.facetsAt) > 2*time.Second && len(lp.watchers) > 0 {
		facets = lp.store.Facets(100)
		lp.facetsAt, lp.changed = time.Now(), false
	}
	if len(q) == 0 && facets == nil {
		lp.mu.Unlock()
		return
	}
	total := int64(lp.store.Len())
	for c, w := range lp.watchers {
		v := &pb.LogView{PaneId: lp.id, Total: total, Facets: facets}
		for _, e := range q {
			if e.Seq > w.after && w.m.Match(e) {
				v.Entries = append(v.Entries, e)
			}
		}
		if len(v.Entries) > 0 || facets != nil {
			c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_LogView{LogView: v}})
		}
	}
	lp.mu.Unlock()
}

// watch starts (or changes the filter of) c's view, sending its first page.
func (lp *logPane) watch(c *conn, w *pb.WatchLog) {
	limit := int(w.Limit)
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	m := logview.NewMatcher(w.Filter)
	if w.BeforeSeq > 0 {
		es, more := lp.store.Query(m, w.BeforeSeq, 0, limit)
		c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_LogView{LogView: &pb.LogView{
			PaneId: lp.id, Entries: es, Older: true, HasMore: more, Filter: w.Filter,
		}}})
		return
	}
	// Flush what's queued first, so this page and what follows meet exactly.
	lp.deliver()
	lp.mu.Lock()
	defer lp.mu.Unlock()
	es, more := lp.store.Query(m, 0, 0, limit)
	after := uint64(0)
	if n := len(es); n > 0 {
		after = es[n-1].Seq
	}
	// Anything newer than the page but still queued goes out with the queue.
	lp.watchers[c] = &logWatcher{m: m, filter: w.Filter, after: after}
	c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_LogView{LogView: &pb.LogView{
		PaneId: lp.id, Replace: true, Entries: es, HasMore: more, Total: int64(lp.store.Len()),
		Facets: lp.store.Facets(100), Filter: w.Filter,
	}}})
}

func (lp *logPane) unwatch(c *conn) {
	lp.mu.Lock()
	delete(lp.watchers, c)
	lp.mu.Unlock()
}

// read answers ReadLog.
func (lp *logPane) read(r *pb.ReadLog) *pb.LogView {
	limit := int(r.Limit)
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	lp.fmu.Lock()
	lp.feed.Flush(false)
	lp.fmu.Unlock()
	es, more := lp.store.Query(logview.NewMatcher(r.Filter), 0, r.AfterSeq, limit)
	return &pb.LogView{PaneId: lp.id, Replace: true, Entries: es, HasMore: more, Total: int64(lp.store.Len()), Filter: r.Filter}
}

func (lp *logPane) clear() {
	lp.store.Clear()
	lp.mu.Lock()
	lp.queue = nil
	for c, w := range lp.watchers {
		w.after = 0
		c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_LogView{LogView: &pb.LogView{
			PaneId: lp.id, Replace: true, Facets: []*pb.LogFacet{}, Filter: w.filter,
		}}})
	}
	lp.mu.Unlock()
}

// close stops it, keeping its file (for the next daemon) unless remove.
func (lp *logPane) close(remove bool) {
	lp.once.Do(func() {
		close(lp.stop)
		lp.fmu.Lock()
		lp.feed.Flush(true)
		lp.fmu.Unlock()
		lp.store.Close()
		if remove {
			_ = os.Remove(lp.d.logPath(lp.id))
		}
	})
}

// logOf is pane id's log, or an error saying it isn't a log pane.
func (d *Daemon) logOf(id string) (*logPane, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.panes[id]
	if p == nil {
		return nil, fmt.Errorf("no such pane %q", id)
	}
	if p.log == nil {
		return nil, fmt.Errorf("pane %s isn't a log pane", id)
	}
	return p.log, nil
}

func (d *Daemon) watchLog(c *conn, w *pb.WatchLog) error {
	lp, err := d.logOf(w.PaneId)
	if err != nil {
		return err
	}
	if !w.Watch && w.BeforeSeq == 0 {
		lp.unwatch(c)
		return nil
	}
	lp.watch(c, proto.CloneOf(w))
	return nil
}

// dropLogWatches forgets a closed connection's views.
func (d *Daemon) dropLogWatches(c *conn) {
	d.mu.Lock()
	var lps []*logPane
	for _, p := range d.panes {
		if p.log != nil {
			lps = append(lps, p.log)
		}
	}
	d.mu.Unlock()
	for _, lp := range lps {
		lp.unwatch(c)
	}
}
