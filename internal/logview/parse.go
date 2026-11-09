// Package logview turns what a log pane's command prints into entries
// (time, level, process, tag, message) and keeps them for filtering. It
// knows macOS's log stream (compact style, also a simulator's), adb logcat
// (threadtime), JSON lines, logfmt (Go's slog, among others) and plain text
// with a timestamp or level near the start; a line it can't place is a
// message as it stands.
package logview

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Splitter turns a terminal's output into lines: escape sequences and
// control characters go, \r\n ends a line and a lone \r starts it again
// (progress bars).
type Splitter struct {
	buf   []byte
	state int // 0 text, 1 after ESC, 2 CSI, 3 OSC/DCS (until BEL or ESC \), 4 ESC in OSC
	cr    bool
}

// Write takes output and returns the lines it completed.
func (s *Splitter) Write(p []byte) []string {
	var out []string
	for _, b := range p {
		switch s.state {
		case 1:
			switch b {
			case '[':
				s.state = 2
			case ']', 'P', '_', '^', 'X':
				s.state = 3
			case '(', ')', '*', '+', '#', '%':
				// A charset designation: one more byte, dropped as text below
				// would keep it, so treat it as part of the sequence.
				s.state = 5
			default:
				s.state = 0
			}
			continue
		case 2:
			if b >= 0x40 && b <= 0x7e {
				s.state = 0
			}
			continue
		case 3:
			if b == 0x07 {
				s.state = 0
			} else if b == 0x1b {
				s.state = 4
			}
			continue
		case 4:
			s.state = 0
			if b != '\\' {
				s.state = 3
			}
			continue
		case 5:
			s.state = 0
			continue
		}
		switch {
		case b == 0x1b:
			s.state = 1
		case b == '\n':
			out = append(out, string(s.buf))
			s.buf, s.cr = s.buf[:0], false
		case b == '\r':
			s.cr = true
		case b == '\t' || b >= 0x20 && b != 0x7f:
			if s.cr {
				s.buf, s.cr = s.buf[:0], false
			}
			s.buf = append(s.buf, b)
		}
		if len(s.buf) > 1<<20 {
			// A "line" this long isn't one: cut it.
			out = append(out, string(s.buf))
			s.buf = s.buf[:0]
		}
	}
	return out
}

// Pending is the line still being written (shown after a pause).
func (s *Splitter) Pending() string { return string(s.buf) }

// Take returns the pending line and forgets it.
func (s *Splitter) Take() string {
	l := string(s.buf)
	s.buf, s.cr = s.buf[:0], false
	return l
}

// Parser makes entries from lines, folding continuation lines (a stack
// trace, a multi-line message) into the entry before.
type Parser struct {
	// The format of the last entry, which decides what a continuation is.
	last    format
	lastKey string // logcat: the header a continued message repeats
	lines   int    // lines in the last entry
	lastErr bool   // the last entry was an error (a trace may follow)
	inTrace bool   // reading a Go trace into it
	Now     func() time.Time
}

type format int

const (
	fmtNone format = iota
	fmtOSLog
	fmtLogcat
	fmtJSON
	fmtLogfmt
	fmtPlain
)

// maxLines caps how many lines fold into one entry.
const maxLines = 500

// Line parses one line. cont reports that it continues the last entry
// (its text is then in e.Message, to append on a new line); skip that it's
// noise (a header log stream prints).
func (p *Parser) Line(line string) (e *pb.LogEntry, cont bool, skip bool) {
	e, cont, skip = p.parse(line)
	if !cont && !skip {
		p.inTrace = false
		p.lastErr = e.Level >= pb.LogLevel_LOG_LEVEL_ERROR
	}
	return e, cont, skip
}

func (p *Parser) parse(line string) (e *pb.LogEntry, cont bool, skip bool) {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	trimmed := strings.TrimRight(line, " \t")
	if trimmed == "" {
		if p.last != fmtNone && p.last != fmtJSON && p.lines < maxLines {
			// A blank line inside a multi-line message.
			p.lines++
			return &pb.LogEntry{}, true, false
		}
		return nil, false, true
	}
	if m := osLogRe.FindStringSubmatch(trimmed); m != nil {
		p.last, p.lines = fmtOSLog, 1
		return parseOSLog(m), false, false
	}
	if strings.HasPrefix(trimmed, "Timestamp ") && strings.Contains(trimmed, "Process[PID:TID]") ||
		strings.HasPrefix(trimmed, "Filtering the log data") {
		return nil, false, true
	}
	if m := logcatRe.FindStringSubmatch(trimmed); m != nil {
		e := parseLogcat(m, now())
		key := m[3] + " " + m[4] + " " + m[5] + " " + m[6]
		if p.last == fmtLogcat && key == p.lastKey && p.lines < maxLines {
			// logcat repeats the header on every line of a message.
			p.lines++
			return &pb.LogEntry{Message: e.Message}, true, false
		}
		p.last, p.lastKey, p.lines = fmtLogcat, key, 1
		return e, false, false
	}
	if strings.HasPrefix(trimmed, "--------- beginning of ") {
		p.last, p.lines = fmtLogcat, 1
		p.lastKey = ""
		return &pb.LogEntry{TimeUnixMs: now().UnixMilli(), Level: pb.LogLevel_LOG_LEVEL_INFO, Message: trimmed}, false, false
	}
	if p.continues(line) {
		p.lines++
		return &pb.LogEntry{Message: line}, true, false
	}
	if strings.HasPrefix(trimmed, "{") {
		if e := parseJSON(trimmed); e != nil {
			p.last, p.lines = fmtJSON, 1
			if e.TimeUnixMs == 0 {
				e.TimeUnixMs = now().UnixMilli()
			}
			return e, false, false
		}
	}
	if e := parseLogfmt(trimmed); e != nil {
		p.last, p.lines = fmtLogfmt, 1
		if e.TimeUnixMs == 0 {
			e.TimeUnixMs = now().UnixMilli()
		}
		return e, false, false
	}
	p.last, p.lines = fmtPlain, 1
	return parsePlain(trimmed, now()), false, false
}

// continues: whether line belongs to the entry before.
func (p *Parser) continues(line string) bool {
	if p.lines >= maxLines {
		return false
	}
	switch p.last {
	case fmtOSLog:
		// Anything that isn't a header (it matched above) is more message.
		return true
	case fmtNone, fmtJSON:
		return false
	}
	// A Go panic's goroutines, after its message.
	if (p.lastErr || p.inTrace) && strings.HasPrefix(line, "goroutine ") {
		p.inTrace = true
		return true
	}
	if p.inTrace && (goFrameRe.MatchString(line) || strings.HasPrefix(line, "created by ") || strings.HasPrefix(line, "exit status ")) {
		return true
	}
	// Indented lines, and the usual stack trace shapes.
	if line[0] == ' ' || line[0] == '\t' {
		return true
	}
	return strings.HasPrefix(line, "Caused by:") || closingRe.MatchString(line)
}

var goFrameRe = regexp.MustCompile(`^[\w./*()\[\]-]+\(.*\)$`)

var closingRe = regexp.MustCompile(`^[\]\)\}]+[,;]?$`)

// macOS log stream --style compact:
// 2026-11-01 13:40:09.375 Db Hammock[40392:b683f0] [com.apple.defaults:User Defaults] message
var osLogRe = regexp.MustCompile(`^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d+) (Db|Df|I|E|F|A|Fa|Er|De|In) +(.+?)\[(\d+):[0-9a-f]+\] (?:\[([^\]:]+)(?::([^\]]*))?\] )?(.*)$`)

func parseOSLog(m []string) *pb.LogEntry {
	e := &pb.LogEntry{Process: m[3], Tag: m[5], Category: m[6], Message: m[7]}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.000000", m[1], time.Local); err == nil {
		e.TimeUnixMs = t.UnixMilli()
	} else if t, err := time.ParseInLocation("2006-01-02 15:04:05.000", m[1], time.Local); err == nil {
		e.TimeUnixMs = t.UnixMilli()
	}
	if pid, err := strconv.Atoi(m[4]); err == nil {
		e.Pid = int32(pid)
	}
	switch m[2] {
	case "Db", "De":
		e.Level = pb.LogLevel_LOG_LEVEL_DEBUG
	case "I", "In":
		e.Level = pb.LogLevel_LOG_LEVEL_INFO
	case "Df", "A":
		e.Level = pb.LogLevel_LOG_LEVEL_NOTICE
	case "E", "Er":
		e.Level = pb.LogLevel_LOG_LEVEL_ERROR
	case "F", "Fa":
		e.Level = pb.LogLevel_LOG_LEVEL_FAULT
	}
	return e
}

// adb logcat -v threadtime:
// 11-01 13:31:23.203  1234  1250 I Tag     : message
var logcatRe = regexp.MustCompile(`^(\d\d-\d\d) (\d\d:\d\d:\d\d\.\d+) +(\d+) +(\d+) ([VDIWEFA]) (.*?) *: ?(.*)$`)

func parseLogcat(m []string, now time.Time) *pb.LogEntry {
	e := &pb.LogEntry{Tag: strings.TrimSpace(m[6]), Message: m[7]}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.000", fmt.Sprintf("%d-%s %s", now.Year(), m[1], m[2]), time.Local); err == nil {
		if t.After(now.Add(24 * time.Hour)) {
			t = t.AddDate(-1, 0, 0) // last December's, read in January
		}
		e.TimeUnixMs = t.UnixMilli()
	} else {
		e.TimeUnixMs = now.UnixMilli()
	}
	if pid, err := strconv.Atoi(m[3]); err == nil {
		e.Pid = int32(pid)
	}
	e.Level = map[string]pb.LogLevel{
		"V": pb.LogLevel_LOG_LEVEL_DEBUG, "D": pb.LogLevel_LOG_LEVEL_DEBUG, "I": pb.LogLevel_LOG_LEVEL_INFO,
		"W": pb.LogLevel_LOG_LEVEL_WARNING, "E": pb.LogLevel_LOG_LEVEL_ERROR, "F": pb.LogLevel_LOG_LEVEL_FAULT, "A": pb.LogLevel_LOG_LEVEL_FAULT,
	}[m[5]]
	return e
}

// LevelNamed reads a level as programs write it (INFO, warn, 30, E…).
func LevelNamed(s string) pb.LogLevel {
	switch strings.ToLower(strings.Trim(s, "[]<> ")) {
	case "trace", "debug", "dbg", "verbose", "v", "d", "db", "10", "20", "fine", "finer", "finest":
		return pb.LogLevel_LOG_LEVEL_DEBUG
	case "info", "information", "informational", "i", "30", "log":
		return pb.LogLevel_LOG_LEVEL_INFO
	case "notice", "default", "n", "df":
		return pb.LogLevel_LOG_LEVEL_NOTICE
	case "warn", "warning", "w", "40":
		return pb.LogLevel_LOG_LEVEL_WARNING
	case "error", "err", "e", "50", "severe":
		return pb.LogLevel_LOG_LEVEL_ERROR
	case "fault", "fatal", "critical", "crit", "panic", "emerg", "emergency", "alert", "f", "60", "dpanic":
		return pb.LogLevel_LOG_LEVEL_FAULT
	}
	return pb.LogLevel_LOG_LEVEL_UNSPECIFIED
}

// LevelName is a level's name in filters and the CLI.
func LevelName(l pb.LogLevel) string {
	switch l {
	case pb.LogLevel_LOG_LEVEL_DEBUG:
		return "debug"
	case pb.LogLevel_LOG_LEVEL_NOTICE:
		return "notice"
	case pb.LogLevel_LOG_LEVEL_WARNING:
		return "warning"
	case pb.LogLevel_LOG_LEVEL_ERROR:
		return "error"
	case pb.LogLevel_LOG_LEVEL_FAULT:
		return "fault"
	}
	return "info"
}

var (
	timeKeys  = []string{"time", "timestamp", "ts", "@timestamp", "t", "date", "datetime"}
	levelKeys = []string{"level", "lvl", "severity", "messageType", "loglevel", "log.level", "level_name", "levelname"}
	msgKeys   = []string{"msg", "message", "eventMessage", "event", "text", "log"}
	procKeys  = []string{"process", "processImagePath", "proc", "app", "service", "service.name"}
	tagKeys   = []string{"subsystem", "logger", "tag", "name", "module", "component", "source", "caller", "logger_name"}
	catKeys   = []string{"category"}
	pidKeys   = []string{"pid", "processID"}
	// What macOS's ndjson adds that nobody reads.
	noiseKeys = map[string]bool{"machTimestamp": true, "bootUUID": true, "processImageUUID": true, "senderImageUUID": true,
		"senderProgramCounter": true, "activityIdentifier": true, "parentActivityIdentifier": true, "traceID": true,
		"timezoneName": true, "launchPersonaName": true, "launchPersonaID": true, "launchPersonaType": true,
		"personaID": true, "personaName": true, "personaType": true, "backtrace": true, "formatString": true,
		"eventType": true, "senderImagePath": true, "userID": true, "threadID": true, "v": true, "hostname": true}
)

func parseJSON(line string) *pb.LogEntry {
	var obj map[string]any
	d := json.NewDecoder(strings.NewReader(line))
	d.UseNumber()
	if d.Decode(&obj) != nil {
		return nil
	}
	e := &pb.LogEntry{}
	take := func(keys []string) (string, bool) {
		for _, k := range keys {
			if v, ok := obj[k]; ok && v != nil {
				delete(obj, k)
				return stringify(v), true
			}
		}
		return "", false
	}
	if v, ok := take(timeKeys); ok {
		e.TimeUnixMs = parseTime(v)
	}
	if v, ok := take(levelKeys); ok {
		e.Level = LevelNamed(v)
	}
	if v, ok := take(msgKeys); ok {
		e.Message = v
	}
	if v, ok := take(procKeys); ok {
		e.Process = path.Base(v)
	}
	if v, ok := take(tagKeys); ok {
		e.Tag = v
	}
	if v, ok := take(catKeys); ok {
		e.Category = v
	}
	if v, ok := take(pidKeys); ok {
		if n, err := strconv.Atoi(v); err == nil {
			e.Pid = int32(n)
		}
	}
	for k, v := range obj {
		if noiseKeys[k] || v == nil {
			continue
		}
		if e.Fields == nil {
			e.Fields = map[string]string{}
		}
		e.Fields[k] = stringify(v)
	}
	if e.Level == pb.LogLevel_LOG_LEVEL_UNSPECIFIED {
		e.Level = pb.LogLevel_LOG_LEVEL_INFO
	}
	if e.Message == "" && len(e.Fields) == 0 {
		return nil
	}
	return e
}

func stringify(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// logfmt: key=value pairs, values quoted when they have spaces. A line is
// logfmt when it has a level= or msg= and is mostly pairs.
var logfmtPair = regexp.MustCompile(`([A-Za-z_][\w.\-/]*)=("(?:[^"\\]|\\.)*"|\S*)`)

func parseLogfmt(line string) *pb.LogEntry {
	if !strings.Contains(line, "level=") && !strings.Contains(line, "msg=") && !strings.Contains(line, "lvl=") {
		return nil
	}
	pairs := logfmtPair.FindAllStringSubmatchIndex(line, -1)
	if len(pairs) < 2 {
		return nil
	}
	covered := 0
	obj := map[string]string{}
	var order []string
	for _, ix := range pairs {
		covered += ix[1] - ix[0]
		k, v := line[ix[2]:ix[3]], line[ix[4]:ix[5]]
		if strings.HasPrefix(v, `"`) {
			if u, err := strconv.Unquote(v); err == nil {
				v = u
			} else {
				v = strings.Trim(v, `"`)
			}
		}
		if _, dup := obj[k]; !dup {
			order = append(order, k)
		}
		obj[k] = v
	}
	if covered*10 < len(strings.ReplaceAll(line, " ", ""))*7 {
		return nil // prose that happens to have an =
	}
	e := &pb.LogEntry{}
	take := func(keys []string) (string, bool) {
		for _, k := range keys {
			if v, ok := obj[k]; ok {
				delete(obj, k)
				return v, true
			}
		}
		return "", false
	}
	if v, ok := take(timeKeys); ok {
		e.TimeUnixMs = parseTime(v)
	}
	if v, ok := take(levelKeys); ok {
		e.Level = LevelNamed(v)
	}
	if v, ok := take(msgKeys); ok {
		e.Message = v
	}
	if v, ok := take(procKeys); ok {
		e.Process = v
	}
	if v, ok := take(tagKeys); ok {
		e.Tag = v
	}
	if v, ok := take(pidKeys); ok {
		if n, err := strconv.Atoi(v); err == nil {
			e.Pid = int32(n)
		}
	}
	for _, k := range order {
		if v, ok := obj[k]; ok {
			if e.Fields == nil {
				e.Fields = map[string]string{}
			}
			e.Fields[k] = v
		}
	}
	if e.Level == pb.LogLevel_LOG_LEVEL_UNSPECIFIED {
		e.Level = pb.LogLevel_LOG_LEVEL_INFO
	}
	return e
}

// Plain text: a timestamp and level near the start, if it has them.
var (
	plainTimeRe  = regexp.MustCompile(`^\[?(\d{4}-\d\d-\d\d[T ]\d\d:\d\d:\d\d(?:[.,]\d+)?(?:Z|[+-]\d\d:?\d\d)?|\d{4}/\d\d/\d\d \d\d:\d\d:\d\d(?:\.\d+)?|[A-Z][a-z]{2} [ \d]\d \d\d:\d\d:\d\d|\d\d:\d\d:\d\d(?:\.\d+)?)\]?[ :]*`)
	plainLevelRe = regexp.MustCompile(`^\[?(TRACE|DEBUG|DBG|INFO|INF|NOTICE|WARN|WARNING|WRN|ERROR|ERR|FATAL|FTL|CRITICAL|CRIT|PANIC|SEVERE|[Tt]race|[Dd]ebug|[Ii]nfo|[Ww]arn(?:ing)?|[Ee]rror|[Ff]atal|[DIWEF])\]?(?::| |$)\s*`)
	// Errors said in prose ("panic: …", "Error: …", "error[E0308]: …").
	plainErrRe  = regexp.MustCompile(`^(?:panic|fatal error|Error|ERROR|error(?:\[\w+\])?|Uncaught \w*Error|Traceback \(most recent call last\)|Exception in thread .*|\w+(?:Error|Exception)):`)
	plainWarnRe = regexp.MustCompile(`^(?:warning|Warning|WARNING)(?:\[\w+\])?:`)
)

func parsePlain(line string, now time.Time) *pb.LogEntry {
	e := &pb.LogEntry{Level: pb.LogLevel_LOG_LEVEL_INFO}
	rest := line
	if m := plainTimeRe.FindStringSubmatchIndex(rest); m != nil {
		if t := parseTime(rest[m[2]:m[3]]); t != 0 {
			e.TimeUnixMs = t
			rest = rest[m[1]:]
		}
	}
	if m := plainLevelRe.FindStringSubmatchIndex(rest); m != nil {
		word := rest[m[2]:m[3]]
		// A lone capital is a level only bracketed or followed by a space and more.
		if len(word) > 1 || m[1] < len(rest) {
			if l := LevelNamed(word); l != pb.LogLevel_LOG_LEVEL_UNSPECIFIED {
				e.Level = l
				rest = rest[m[1]:]
			}
		}
	} else if plainErrRe.MatchString(rest) {
		e.Level = pb.LogLevel_LOG_LEVEL_ERROR
	} else if plainWarnRe.MatchString(rest) {
		e.Level = pb.LogLevel_LOG_LEVEL_WARNING
	}
	if e.TimeUnixMs == 0 {
		e.TimeUnixMs = now.UnixMilli()
	}
	e.Message = strings.TrimLeftFunc(rest, unicode.IsSpace)
	if e.Message == "" {
		e.Message = line
	}
	return e
}

var timeLayouts = []string{
	time.RFC3339Nano, "2006-01-02T15:04:05.999999999Z0700", "2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999-0700", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05,999", "2006/01/02 15:04:05.999999", "2006/01/02 15:04:05", "Jan _2 15:04:05", "15:04:05.999999",
}

// parseTime reads a timestamp in the usual shapes (or Unix seconds or
// milliseconds), in local time when it doesn't say; 0 if it can't.
func parseTime(s string) int64 {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseFloat(s, 64); err == nil && n > 1e9 {
		if n > 1e12 {
			return int64(n) // milliseconds
		}
		return int64(n * 1000)
	}
	now := time.Now()
	for _, l := range timeLayouts {
		t, err := time.ParseInLocation(l, s, time.Local)
		if err != nil {
			continue
		}
		switch l {
		case "Jan _2 15:04:05":
			t = t.AddDate(now.Year(), 0, 0)
		case "15:04:05.999999":
			y, m, d := now.Date()
			t = time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
		}
		return t.UnixMilli()
	}
	return 0
}
