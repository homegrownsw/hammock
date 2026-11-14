package vt

import (
	"slices"
	"strings"
	"unicode"
)

// The Program Status Protocol (OSC 7501,
// https://www.superlogical.com/rex/docs/build/program-status): a program
// tells its terminal what it's doing (idle, working, done, blocked on the
// user, failed) and why. libghostty-vt parses and checks each report, and
// answers a program's support query (OSC 7501 ; ?); the records it reports
// are kept here, one set per pane, by the spec's rules.

// StatusState is what a program reports it's doing.
type StatusState int

const (
	StatusIdle StatusState = iota
	StatusWorking
	StatusDone
	StatusBlocked
	StatusError
	StatusClear // not a state: removes the record and those beneath it
)

func (s StatusState) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusWorking:
		return "working"
	case StatusDone:
		return "done"
	case StatusBlocked:
		return "blocked"
	case StatusError:
		return "error"
	case StatusClear:
		return "clear"
	}
	return ""
}

// ParseStatusState reads a state's name in the protocol ("blocked").
func ParseStatusState(s string) (StatusState, bool) {
	for st := StatusIdle; st <= StatusClear; st++ {
		if st.String() == s {
			return st, true
		}
	}
	return 0, false
}

// StatusReport is one report (OSC 7501) as libghostty-vt passed it on:
// checked, its base64 decoded, free of control characters.
type StatusReport struct {
	State    StatusState
	Kind     string // blocked only: "permission", "question", "auth" or ""
	Progress int    // 0–100, or -1 for none (working and blocked only)
	ID       string // "" for the root record; "/" nests ("build/test")
	App      string // a stable name ("cargo", "claude-code"), not a label
	Title    string
	Message  string
}

// StatusRecord is a report kept: the last a program sent for its id.
type StatusRecord struct {
	StatusReport
	// Seen: done or failed, and you've looked since. It then rests (ranks
	// as idle), and goes with the program or the next prompt.
	Seen bool
	seq  uint64 // when last updated, for evicting the oldest
}

// rank is how much the record needs you.
func (x *StatusRecord) rank() int {
	if x.Seen {
		return statusRank[StatusIdle]
	}
	return statusRank[x.State]
}

// MaxStatusRecords is how many records a pane keeps: the spec's minimum
// (it allows 64 to 256). A pane shows one state, rolled up from them all.
const MaxStatusRecords = 64

// StatusRecords is a terminal's records, by the spec's rules. Not safe for
// concurrent use.
type StatusRecords struct {
	recs map[string]*StatusRecord
	seq  uint64
}

// Len is how many records there are.
func (r *StatusRecords) Len() int { return len(r.recs) }

// Apply applies a report: replaces its record whole, or clears it and those
// beneath it (every record, for the root). Reports whether anything changed.
func (r *StatusRecords) Apply(rep StatusReport) bool {
	if rep.State == StatusClear {
		n := len(r.recs)
		if rep.ID == "" {
			r.recs = nil
			return n > 0
		}
		for id := range r.recs {
			if id == rep.ID || strings.HasPrefix(id, rep.ID+"/") {
				delete(r.recs, id)
			}
		}
		return len(r.recs) != n
	}
	if rep.State > StatusError {
		return false
	}
	rep.Title, rep.Message = CleanStatusText(rep.Title), CleanStatusText(rep.Message)
	if rep.State != StatusBlocked {
		rep.Kind = ""
	}
	if rep.State != StatusWorking && rep.State != StatusBlocked || rep.Progress < 0 || rep.Progress > 100 {
		rep.Progress = -1
	}
	if r.recs == nil {
		r.recs = map[string]*StatusRecord{}
	}
	old := r.recs[rep.ID]
	r.seq++
	if old != nil && old.StatusReport == rep {
		old.seq = r.seq // the same again: no news, not even if seen
		return false
	}
	r.recs[rep.ID] = &StatusRecord{StatusReport: rep, seq: r.seq}
	for len(r.recs) > MaxStatusRecords {
		var oldest *StatusRecord
		for _, x := range r.recs {
			if oldest == nil || x.seq < oldest.seq {
				oldest = x
			}
		}
		delete(r.recs, oldest.ID)
	}
	return true
}

// Drop removes the records keep doesn't want. Reports whether any went.
func (r *StatusRecords) Drop(keep func(StatusRecord) bool) bool {
	n := len(r.recs)
	for id, x := range r.recs {
		if !keep(*x) {
			delete(r.recs, id)
		}
	}
	return len(r.recs) != n
}

// Ended drops what a program's end, or a new prompt, ends: records of work
// under way or waiting on the user, and resting ones. What it finished, or
// failed at, stays for you to see.
func (r *StatusRecords) Ended() bool {
	return r.Drop(func(x StatusRecord) bool { return !x.Seen && (x.State == StatusDone || x.State == StatusError) })
}

// Records lists the records, root first, then by id (each with the app it
// inherits from its nearest ancestor that has one).
func (r *StatusRecords) Records() []StatusRecord {
	out := make([]StatusRecord, 0, len(r.recs))
	for _, x := range r.recs {
		rec := *x
		rec.App = r.app(rec.ID)
		out = append(out, rec)
	}
	slices.SortFunc(out, func(a, b StatusRecord) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Restore puts back records saved with Records (a restarted daemon's).
func (r *StatusRecords) Restore(recs []StatusRecord) {
	r.recs = nil
	for _, x := range recs {
		if r.recs == nil {
			r.recs = map[string]*StatusRecord{}
		}
		r.seq++
		x.seq = r.seq
		r.recs[x.ID] = &x
	}
}

// Seen marks what was done or failed as looked at. Reports whether any
// record changed.
func (r *StatusRecords) Seen() bool {
	changed := false
	for _, x := range r.recs {
		if !x.Seen && (x.State == StatusDone || x.State == StatusError) {
			x.Seen, changed = true, true
		}
	}
	return changed
}

// app is id's app: its own, else its nearest ancestor's.
func (r *StatusRecords) app(id string) string {
	for {
		if x := r.recs[id]; x != nil && x.App != "" {
			return x.App
		}
		if id == "" {
			return ""
		}
		i := strings.LastIndexByte(id, '/')
		if i < 0 {
			id = ""
		} else {
			id = id[:i]
		}
	}
}

// statusRank is how much a state needs you, most first.
var statusRank = map[StatusState]int{StatusBlocked: 5, StatusError: 4, StatusDone: 3, StatusWorking: 2, StatusIdle: 1}

// Top is the record that most needs you: blocked, then failed, finished,
// working, idle (what's been seen ranks as idle); among equals the root,
// then the most recently updated. False with no records.
func (r *StatusRecords) Top() (StatusRecord, bool) {
	var top *StatusRecord
	for _, x := range r.recs {
		switch {
		case top == nil, x.rank() > top.rank():
			top = x
		case x.rank() < top.rank():
		case top.ID != "" && (x.ID == "" || x.seq > top.seq):
			top = x
		}
	}
	if top == nil {
		return StatusRecord{}, false
	}
	rec := *top
	rec.App = r.app(rec.ID)
	return rec, true
}

// Root is the root record, if there is one.
func (r *StatusRecords) Root() (StatusRecord, bool) {
	x := r.recs[""]
	if x == nil {
		return StatusRecord{}, false
	}
	return *x, true
}

// CleanStatusText makes a program's text safe to show outside the
// terminal: invisible formatting (text direction overrides, zero-width
// characters: Unicode's Cf) and any control character removed, spaces
// collapsed. libghostty-vt has already refused reports with controls.
func CleanStatusText(s string) string {
	s = strings.Map(func(c rune) rune {
		switch {
		case unicode.Is(unicode.Cf, c), unicode.IsControl(c):
			return -1
		case unicode.IsSpace(c):
			return ' '
		}
		return c
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
