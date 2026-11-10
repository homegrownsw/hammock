package logview

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// Store holds a log pane's newest entries (and, given a file, keeps them
// across daemon restarts), counts them by process, tag and level, and finds
// the ones a filter wants.
type Store struct {
	mu      sync.Mutex
	entries []*pb.LogEntry
	nextSeq uint64
	counts  map[string]int64
	max     int
	offset  uint64 // the output offset the entries go up to (to skip on replay)

	path    string
	f       *os.File
	w       *bufio.Writer
	written int64
	maxFile int64
	dirty   bool
}

// Max entries held; the file is rewritten from them past maxFile bytes.
const (
	DefaultMax     = 100_000
	defaultMaxFile = 48 << 20
)

// NewStore makes an empty store, kept in path if it's set (loading what's
// there).
func NewStore(path string) *Store {
	s := &Store{counts: map[string]int64{}, max: DefaultMax, maxFile: defaultMaxFile, nextSeq: 1, path: path}
	if path == "" {
		return s
	}
	if f, err := os.Open(path); err == nil {
		r := bufio.NewReaderSize(f, 1<<16)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				var rec record
				if json.Unmarshal(line, &rec) == nil {
					e := rec.entry()
					if e.Seq >= s.nextSeq {
						s.nextSeq = e.Seq + 1
					}
					s.addLocked(e)
					s.offset = rec.O
				}
			}
			if err != nil {
				break
			}
		}
		f.Close()
	}
	s.trimLocked()
	return s
}

// record is an entry on disk: short keys, empty fields left out.
type record struct {
	S uint64            `json:"s"`
	O uint64            `json:"o,omitempty"`
	T int64             `json:"t,omitempty"`
	L int32             `json:"l,omitempty"`
	P string            `json:"p,omitempty"`
	I int32             `json:"i,omitempty"`
	G string            `json:"g,omitempty"`
	C string            `json:"c,omitempty"`
	M string            `json:"m,omitempty"`
	F map[string]string `json:"f,omitempty"`
}

func (r record) entry() *pb.LogEntry {
	return &pb.LogEntry{Seq: r.S, TimeUnixMs: r.T, Level: pb.LogLevel(r.L), Process: r.P, Pid: r.I, Tag: r.G, Category: r.C, Message: r.M, Fields: r.F}
}

func toRecord(e *pb.LogEntry, off uint64) record {
	return record{S: e.Seq, O: off, T: e.TimeUnixMs, L: int32(e.Level), P: e.Process, I: e.Pid, G: e.Tag, C: e.Category, M: e.Message, F: e.Fields}
}

// Offset is the output offset the held entries were read up to.
func (s *Store) Offset() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offset
}

// SetOffset resets it (a new process starts its output at 0).
func (s *Store) SetOffset(o uint64) {
	s.mu.Lock()
	s.offset = o
	s.mu.Unlock()
}

// Add numbers e and keeps it, read up to output offset off.
func (s *Store) Add(e *pb.LogEntry, off uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Seq = s.nextSeq
	s.nextSeq++
	s.addLocked(e)
	if off > s.offset {
		s.offset = off
	}
	s.trimLocked()
	s.appendLocked(e)
}

func (s *Store) addLocked(e *pb.LogEntry) {
	s.entries = append(s.entries, e)
	for _, k := range keysOf(e) {
		s.counts[k]++
	}
}

func (s *Store) trimLocked() {
	if len(s.entries) <= s.max+s.max/4 {
		return
	}
	drop := len(s.entries) - s.max
	for _, e := range s.entries[:drop] {
		for _, k := range keysOf(e) {
			if s.counts[k]--; s.counts[k] <= 0 {
				delete(s.counts, k)
			}
		}
	}
	s.entries = append([]*pb.LogEntry(nil), s.entries[drop:]...)
}

func keysOf(e *pb.LogEntry) []string {
	k := []string{"level:" + LevelName(e.Level)}
	if e.Process != "" {
		k = append(k, "process:"+e.Process)
	}
	if e.Tag != "" {
		k = append(k, "tag:"+e.Tag)
	}
	return k
}

func (s *Store) appendLocked(e *pb.LogEntry) {
	if s.path == "" {
		return
	}
	if s.w == nil || s.written > s.maxFile {
		s.rewriteLocked()
		return // the rewrite has e
	}
	b, _ := json.Marshal(toRecord(e, s.offset))
	n, _ := s.w.Write(append(b, '\n'))
	s.written += int64(n)
	s.dirty = true
}

// rewriteLocked writes the file afresh from the entries held.
func (s *Store) rewriteLocked() {
	if s.f != nil {
		s.w.Flush()
		s.f.Close()
		s.f, s.w = nil, nil
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	w := bufio.NewWriterSize(f, 1<<16)
	var n int64
	for _, e := range s.entries {
		b, _ := json.Marshal(toRecord(e, s.offset))
		m, _ := w.Write(append(b, '\n'))
		n += int64(m)
	}
	if w.Flush() != nil || os.Rename(tmp, s.path) != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	s.f, s.w, s.written = f, w, n
}

// Sync writes what's buffered to the file.
func (s *Store) Sync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil && s.dirty {
		s.w.Flush()
		s.dirty = false
	}
}

// Close syncs and closes the file.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w != nil {
		s.w.Flush()
		s.f.Close()
		s.f, s.w = nil, nil
	}
}

// Clear forgets every entry (the file too); numbering carries on.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	s.counts = map[string]int64{}
	if s.path != "" {
		s.rewriteLocked()
	}
}

// Len is how many entries are held.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// Facets are the held entries' processes, tags and levels by count, most
// first (up to limit of each kind but levels).
func (s *Store) Facets(limit int) []*pb.LogFacet {
	s.mu.Lock()
	defer s.mu.Unlock()
	byKind := map[string][]*pb.LogFacet{}
	for k, n := range s.counts {
		kind, _, _ := strings.Cut(k, ":")
		byKind[kind] = append(byKind[kind], &pb.LogFacet{Key: k, Count: n})
	}
	var out []*pb.LogFacet
	for _, kind := range []string{"level", "process", "tag"} {
		fs := byKind[kind]
		sort.Slice(fs, func(i, j int) bool {
			if fs[i].Count != fs[j].Count {
				return fs[i].Count > fs[j].Count
			}
			return fs[i].Key < fs[j].Key
		})
		if kind != "level" && len(fs) > limit {
			fs = fs[:limit]
		}
		out = append(out, fs...)
	}
	return out
}

// Query returns up to limit entries matching m, oldest first: the newest
// ones before seq before (0: the newest of all), or with after set, the
// oldest after it. more reports that there are older (or, after, newer)
// matches still.
func (s *Store) Query(m *Matcher, before, after uint64, limit int) (out []*pb.LogEntry, more bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 500
	}
	if after > 0 {
		i := sort.Search(len(s.entries), func(i int) bool { return s.entries[i].Seq > after })
		for ; i < len(s.entries); i++ {
			if m.Match(s.entries[i]) {
				if len(out) == limit {
					return out, true
				}
				out = append(out, s.entries[i])
			}
		}
		return out, false
	}
	end := len(s.entries)
	if before > 0 {
		end = sort.Search(len(s.entries), func(i int) bool { return s.entries[i].Seq >= before })
	}
	for i := end - 1; i >= 0; i-- {
		if m.Match(s.entries[i]) {
			if len(out) == limit {
				more = true
				break
			}
			out = append(out, s.entries[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, more
}

// Matcher is a compiled LogFilter.
type Matcher struct {
	text   string
	re     *regexp.Regexp
	levels map[pb.LogLevel]bool
	only   map[string][]string // kind → values (any of them)
	hide   map[string]bool
	since  int64
}

// NewMatcher compiles f (nil matches everything). A /regex/ that doesn't
// compile is searched for as text.
func NewMatcher(f *pb.LogFilter) *Matcher {
	m := &Matcher{}
	if f == nil {
		return m
	}
	t := strings.TrimSpace(f.Text)
	if len(t) > 2 && strings.HasPrefix(t, "/") && strings.HasSuffix(t, "/") {
		if re, err := regexp.Compile("(?i)" + t[1:len(t)-1]); err == nil {
			m.re = re
		}
	}
	if m.re == nil {
		m.text = strings.ToLower(t)
	}
	if len(f.Levels) > 0 {
		m.levels = map[pb.LogLevel]bool{}
		for _, l := range f.Levels {
			m.levels[l] = true
		}
	}
	for _, k := range f.Only {
		kind, v, ok := strings.Cut(k, ":")
		if !ok {
			continue
		}
		if kind == "level" {
			if m.levels == nil {
				m.levels = map[pb.LogLevel]bool{}
			}
			m.levels[LevelNamed(v)] = true
			continue
		}
		if m.only == nil {
			m.only = map[string][]string{}
		}
		m.only[kind] = append(m.only[kind], v)
	}
	for _, k := range f.Hide {
		if m.hide == nil {
			m.hide = map[string]bool{}
		}
		m.hide[k] = true
	}
	m.since = f.SinceUnixMs
	return m
}

// Match: whether e passes.
func (m *Matcher) Match(e *pb.LogEntry) bool {
	if m == nil {
		return true
	}
	if m.levels != nil && !m.levels[e.Level] {
		return false
	}
	if m.since > 0 && e.TimeUnixMs < m.since {
		return false
	}
	if m.hide != nil && (m.hide["process:"+e.Process] || m.hide["tag:"+e.Tag] || m.hide["level:"+LevelName(e.Level)]) {
		return false
	}
	for kind, vals := range m.only {
		v := e.Process
		if kind == "tag" {
			v = e.Tag
		}
		found := false
		for _, want := range vals {
			if v == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if m.re != nil {
		return m.re.MatchString(e.Message) || m.re.MatchString(e.Process) || m.re.MatchString(e.Tag) || fieldsMatch(e, m.re.MatchString)
	}
	if m.text != "" {
		has := func(s string) bool { return strings.Contains(strings.ToLower(s), m.text) }
		return has(e.Message) || has(e.Process) || has(e.Tag) || fieldsMatch(e, has)
	}
	return true
}

func fieldsMatch(e *pb.LogEntry, f func(string) bool) bool {
	for k, v := range e.Fields {
		if f(v) || f(k) {
			return true
		}
	}
	return false
}

// Feeder runs a pane's output through a Splitter and Parser into a Store,
// holding each entry back until the next begins (or Flush, after a pause)
// so a stack trace arrives as one entry.
type Feeder struct {
	Store   *Store
	OnEntry func(*pb.LogEntry) // after it's stored

	split   Splitter
	parse   Parser
	pending *pb.LogEntry
	off     uint64 // output offset of the end of the last whole line
}

// Write takes output that starts at offset off, skipping what the store
// already read (output replayed after a restart).
func (f *Feeder) Write(p []byte, off uint64) {
	if skip := f.Store.Offset(); off < skip {
		if off+uint64(len(p)) <= skip {
			return
		}
		p, off = p[skip-off:], skip
	}
	// Each line's end offset: count bytes up to each newline.
	start := off
	for len(p) > 0 {
		i := indexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i+1]
		}
		lines := f.split.Write(chunk)
		start += uint64(len(chunk))
		for _, l := range lines {
			f.line(l, start)
		}
		p = p[len(chunk):]
	}
}

func indexByte(p []byte, b byte) int {
	for i, c := range p {
		if c == b {
			return i
		}
	}
	return -1
}

func (f *Feeder) line(l string, end uint64) {
	e, cont, skip := f.parse.Line(l)
	if !skip && !cont {
		// The entry before ends with the line before this one.
		f.commit()
	}
	f.off = end
	if skip {
		return
	}
	if cont {
		if f.pending != nil {
			f.pending.Message += "\n" + e.Message
			return
		}
		// A continuation after the entry went out (a pause mid-trace): its
		// own entry, as the last one was.
		last := f.lastStored()
		e.TimeUnixMs = time.Now().UnixMilli()
		if last != nil {
			e.Level, e.Process, e.Pid, e.Tag, e.Category = last.Level, last.Process, last.Pid, last.Tag, last.Category
		}
		if strings.TrimSpace(e.Message) == "" {
			return
		}
		f.pending = e
		return
	}
	f.pending = e
}

func (f *Feeder) lastStored() *pb.LogEntry {
	f.Store.mu.Lock()
	defer f.Store.mu.Unlock()
	if n := len(f.Store.entries); n > 0 {
		return f.Store.entries[n-1]
	}
	return nil
}

func (f *Feeder) commit() {
	if f.pending == nil {
		return
	}
	e := f.pending
	f.pending = nil
	e.Message = strings.TrimRight(e.Message, "\n ")
	f.Store.Add(e, f.off)
	if f.OnEntry != nil {
		f.OnEntry(proto.CloneOf(e))
	}
}

// Flush sends the entry being held (and a line still being written, when
// it has waited long enough to be whole).
func (f *Feeder) Flush(partial bool) {
	if partial && f.split.Pending() != "" {
		f.line(f.split.Take(), f.off)
	}
	f.commit()
}

// Restarted: the pane's process started again, its output from offset 0.
func (f *Feeder) Restarted() {
	f.Flush(true)
	f.Store.SetOffset(0)
	f.off = 0
	f.parse = Parser{}
}

// Marshal writes entries as JSON lines (the CLI's --json).
func Marshal(w io.Writer, es []*pb.LogEntry) error {
	enc := json.NewEncoder(w)
	for _, e := range es {
		o := map[string]any{"seq": e.Seq, "time": time.UnixMilli(e.TimeUnixMs).Format(time.RFC3339Nano), "level": LevelName(e.Level), "message": e.Message}
		if e.Process != "" {
			o["process"] = e.Process
		}
		if e.Pid != 0 {
			o["pid"] = e.Pid
		}
		if e.Tag != "" {
			o["tag"] = e.Tag
		}
		if e.Category != "" {
			o["category"] = e.Category
		}
		if len(e.Fields) > 0 {
			o["fields"] = e.Fields
		}
		if err := enc.Encode(o); err != nil {
			return err
		}
	}
	return nil
}
