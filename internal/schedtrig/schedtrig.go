// Package schedtrig reads and words a scheduled task's event triggers
// (pb.EventTrigger): what starts a run when it happens. hmk, its MCP tools,
// the TUI and the daemon share it, so a trigger reads the same everywhere.
//
// A trigger written out (hmk schedule --trigger, MCP schedule_save):
//
//	github:ci-failed | github:review-requested | github:reviewed |
//	github:label=NAME | github:assigned | github:merged
//	agent:finished | agent:needs-you | agent:finished,needs-you  [@GROUP|PANE]
//	exit  [@GROUP|PANE]                 a pane's command exits non-zero
//	log:REGEX@PANE                      a log pane's line matches
//	files[:GLOB][@DIR]                  files change (DIR: the task's folder)
//	calendar:MINUTES[,title=TEXT][,calendar=NAME][,attendees]
//	webhook
package schedtrig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Usage lists the forms Parse reads, for usage texts.
const Usage = `github:ci-failed|review-requested|reviewed|label=NAME|assigned|merged
agent:finished|needs-you[,…][@GROUP|PANE]   exit[@GROUP|PANE]   log:REGEX@PANE
files[:GLOB][@DIR]   calendar:MINUTES[,title=TEXT][,calendar=NAME][,attendees]   webhook`

// Resolver finds the pane or Group ref names (an id, a name, an index), for
// a trigger that may be in either (groups) or a pane only.
type Resolver func(ref string, groups bool) (paneID, groupID string, err error)

var githubEvents = map[string]pb.GitHubTrigger_Event{
	"ci-failed": pb.GitHubTrigger_EVENT_CI_FAILED, "review-requested": pb.GitHubTrigger_EVENT_REVIEW_REQUESTED,
	"reviewed": pb.GitHubTrigger_EVENT_REVIEWED, "label": pb.GitHubTrigger_EVENT_LABELED,
	"assigned": pb.GitHubTrigger_EVENT_ASSIGNED, "merged": pb.GitHubTrigger_EVENT_MERGED,
}

// GitHubEventName is the word Parse reads for e ("ci-failed").
func GitHubEventName(e pb.GitHubTrigger_Event) string {
	for k, v := range githubEvents {
		if v == e {
			return k
		}
	}
	return ""
}

// Parse reads one trigger as written out (above). resolve may be nil when
// it names no pane or Group.
func Parse(spec string, resolve Resolver) (*pb.EventTrigger, error) {
	spec = strings.TrimSpace(spec)
	// The kind, then ":" and what follows it, or "@" and where (exit@PANE).
	kind, rest := spec, ""
	if i := strings.IndexAny(spec, ":@"); i >= 0 {
		kind, rest = spec[:i], spec[i+1:]
		if spec[i] == '@' {
			rest = "@" + rest
		}
	}
	target := ""
	at := func() {
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			rest, target = rest[:i], strings.TrimSpace(rest[i+1:])
		}
	}
	where := func(groups bool) (pane, group string, err error) {
		if target == "" {
			return "", "", nil
		}
		if resolve == nil {
			return "", "", fmt.Errorf("%s: can't look up %q here", spec, target)
		}
		return resolve(target, groups)
	}
	switch strings.ToLower(kind) {
	case "github", "gh":
		ev, label, _ := strings.Cut(strings.TrimSpace(rest), "=")
		e, ok := githubEvents[strings.ToLower(ev)]
		if !ok {
			return nil, fmt.Errorf("%q: github:ci-failed, review-requested, reviewed, label=NAME, assigned or merged", spec)
		}
		if e == pb.GitHubTrigger_EVENT_LABELED && strings.TrimSpace(label) == "" {
			return nil, errors.New("github:label=NAME: which label?")
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: e, Label: strings.TrimSpace(label)}}}, nil
	case "agent":
		at()
		a := &pb.AgentTrigger{}
		for _, w := range strings.Split(rest, ",") {
			switch strings.ToLower(strings.TrimSpace(w)) {
			case "finished", "finishes", "done":
				a.Finished = true
			case "needs-you", "needs_you", "waiting", "asks":
				a.NeedsYou = true
			case "":
			default:
				return nil, fmt.Errorf("%q: agent:finished, agent:needs-you, or both (agent:finished,needs-you)", spec)
			}
		}
		if !a.Finished && !a.NeedsYou {
			a.Finished = true
		}
		var err error
		if a.PaneId, a.GroupId, err = where(true); err != nil {
			return nil, err
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Agent{Agent: a}}, nil
	case "exit", "exits", "failed":
		at()
		x := &pb.ExitTrigger{}
		var err error
		if x.PaneId, x.GroupId, err = where(true); err != nil {
			return nil, err
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Exit{Exit: x}}, nil
	case "log":
		at()
		if target == "" || strings.TrimSpace(rest) == "" {
			return nil, fmt.Errorf("%q: log:REGEX@PANE (a log pane)", spec)
		}
		l := &pb.LogTrigger{Pattern: strings.TrimSpace(rest)}
		if _, err := CompileLog(l.Pattern); err != nil {
			return nil, err
		}
		var err error
		if l.PaneId, _, err = where(false); err != nil {
			return nil, err
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Log{Log: l}}, nil
	case "files", "file":
		at()
		f := &pb.FilesTrigger{Glob: strings.TrimSpace(rest), Folder: target}
		if err := CheckGlob(f.Glob); err != nil {
			return nil, err
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Files{Files: f}}, nil
	case "calendar", "cal":
		c := &pb.CalendarTrigger{}
		for i, w := range strings.Split(rest, ",") {
			w = strings.TrimSpace(w)
			k, v, _ := strings.Cut(w, "=")
			switch {
			case i == 0:
				n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(w, "m"), "min"))
				if err != nil || n < 0 || n > 24*60 {
					return nil, fmt.Errorf("%q: calendar:MINUTES (before the event, 0 to 1440)", spec)
				}
				c.MinutesBefore = int32(n)
			case k == "title":
				c.Title = v
			case k == "calendar":
				c.Calendar = v
			case w == "attendees" || w == "with-attendees":
				c.WithAttendees = true
			default:
				return nil, fmt.Errorf("%q: calendar:MINUTES[,title=TEXT][,calendar=NAME][,attendees]", spec)
			}
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Calendar{Calendar: c}}, nil
	case "webhook", "hook":
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Webhook{Webhook: &pb.WebhookTrigger{}}}, nil
	}
	return nil, fmt.Errorf("trigger %q: one of\n%s", spec, Usage)
}

// Names finds a pane's or Group's name by id ("" when it's gone).
type Names func(id string) string

// Describe says what fires t, in words: "CI fails on my pull request",
// "An agent finishes in Assistant". names may be nil.
func Describe(t *pb.EventTrigger, names Names) string {
	name := func(pane, group string) string {
		id := cmpOr(pane, group)
		if id == "" {
			return ""
		}
		if names == nil {
			return " in " + id
		}
		n := names(id)
		if n == "" {
			if pane != "" {
				return " in a pane that's closed"
			}
			return " in a Group that's closed"
		}
		return " in " + n
	}
	switch k := t.GetKind().(type) {
	case *pb.EventTrigger_Github:
		g := k.Github
		in := ""
		if g.Repo != "" {
			in = " (" + g.Repo + ")"
		}
		switch g.Event {
		case pb.GitHubTrigger_EVENT_CI_FAILED:
			return "CI fails on my pull request or branch" + in
		case pb.GitHubTrigger_EVENT_REVIEW_REQUESTED:
			return "My review is requested" + in
		case pb.GitHubTrigger_EVENT_REVIEWED:
			return "My pull request gets a review or comment" + in
		case pb.GitHubTrigger_EVENT_LABELED:
			return "An issue or pull request is labelled “" + g.Label + "”" + in
		case pb.GitHubTrigger_EVENT_ASSIGNED:
			return "An issue or pull request is assigned to me" + in
		case pb.GitHubTrigger_EVENT_MERGED:
			return "My pull request is merged" + in
		}
		return "GitHub" + in
	case *pb.EventTrigger_Agent:
		a := k.Agent
		what := "finishes"
		switch {
		case a.Finished && a.NeedsYou:
			what = "finishes or needs me"
		case a.NeedsYou:
			what = "needs me"
		}
		return "An agent " + what + name(a.PaneId, a.GroupId)
	case *pb.EventTrigger_Exit:
		return "A command fails" + name(k.Exit.PaneId, k.Exit.GroupId)
	case *pb.EventTrigger_Log:
		return "A log line matches /" + k.Log.Pattern + "/" + name(k.Log.PaneId, "")
	case *pb.EventTrigger_Files:
		f := k.Files
		what := "Files change"
		if f.Glob != "" {
			what = f.Glob + " changes"
		}
		if f.Folder != "" {
			what += " in " + tildeHome(f.Folder)
		}
		return what
	case *pb.EventTrigger_Calendar:
		c := k.Calendar
		out := fmt.Sprintf("%d minutes before ", c.MinutesBefore)
		if c.MinutesBefore == 0 {
			out = "At the start of "
		} else if c.MinutesBefore == 1 {
			out = "1 minute before "
		}
		out += "events"
		if c.Title != "" {
			out += " titled “" + c.Title + "”"
		}
		if c.Calendar != "" {
			out += " in " + c.Calendar
		}
		if c.WithAttendees {
			out += " with other people"
		}
		return out
	case *pb.EventTrigger_Webhook:
		return "Its webhook is called"
	}
	return "An unknown event"
}

// Spec writes t out as Parse reads it, with pane and Group ids.
func Spec(t *pb.EventTrigger) string {
	target := func(pane, group string) string {
		if id := cmpOr(pane, group); id != "" {
			return "@" + id
		}
		return ""
	}
	switch k := t.GetKind().(type) {
	case *pb.EventTrigger_Github:
		s := "github:" + GitHubEventName(k.Github.Event)
		if k.Github.Event == pb.GitHubTrigger_EVENT_LABELED {
			s += "=" + k.Github.Label
		}
		return s
	case *pb.EventTrigger_Agent:
		var w []string
		if k.Agent.Finished {
			w = append(w, "finished")
		}
		if k.Agent.NeedsYou {
			w = append(w, "needs-you")
		}
		return "agent:" + strings.Join(w, ",") + target(k.Agent.PaneId, k.Agent.GroupId)
	case *pb.EventTrigger_Exit:
		return "exit" + target(k.Exit.PaneId, k.Exit.GroupId)
	case *pb.EventTrigger_Log:
		return "log:" + k.Log.Pattern + "@" + k.Log.PaneId
	case *pb.EventTrigger_Files:
		s := "files"
		if k.Files.Glob != "" {
			s += ":" + k.Files.Glob
		}
		if k.Files.Folder != "" {
			s += "@" + k.Files.Folder
		}
		return s
	case *pb.EventTrigger_Calendar:
		c := k.Calendar
		s := "calendar:" + strconv.Itoa(int(c.MinutesBefore))
		if c.Title != "" {
			s += ",title=" + c.Title
		}
		if c.Calendar != "" {
			s += ",calendar=" + c.Calendar
		}
		if c.WithAttendees {
			s += ",attendees"
		}
		return s
	case *pb.EventTrigger_Webhook:
		return "webhook"
	}
	return ""
}

// Kind is t's kind's word: "github", "agent", "exit", "log", "files",
// "calendar" or "webhook".
func Kind(t *pb.EventTrigger) string {
	switch t.GetKind().(type) {
	case *pb.EventTrigger_Github:
		return "github"
	case *pb.EventTrigger_Agent:
		return "agent"
	case *pb.EventTrigger_Exit:
		return "exit"
	case *pb.EventTrigger_Log:
		return "log"
	case *pb.EventTrigger_Files:
		return "files"
	case *pb.EventTrigger_Calendar:
		return "calendar"
	case *pb.EventTrigger_Webhook:
		return "webhook"
	}
	return ""
}

// CompileLog compiles a log trigger's pattern: case-insensitive unless it
// has a capital letter (as a search box is).
func CompileLog(pattern string) (*regexp.Regexp, error) {
	p := pattern
	if strings.ToLower(p) == p {
		p = "(?i)" + p
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("the pattern %q isn't a regular expression: %v", pattern, err)
	}
	return re, nil
}

// CheckGlob says what's wrong with a files trigger's glob, if anything.
func CheckGlob(glob string) error {
	if glob == "" {
		return nil
	}
	if strings.HasPrefix(glob, "/") || strings.Contains(glob, "..") {
		return fmt.Errorf("the glob %q is relative to the folder (give the folder separately)", glob)
	}
	if _, err := GlobRegexp(glob); err != nil {
		return err
	}
	return nil
}

// GlobRegexp is glob as a regular expression for slash-separated paths
// relative to the folder: * and ? within a name, ** across folders, {a,b}
// either. A glob with no / matches a file's name, wherever it is.
func GlobRegexp(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	if !strings.Contains(glob, "/") {
		b.WriteString("(?:.*/)?")
	}
	depth := 0
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString("(?:.*/)?")
			} else {
				b.WriteString(".*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '{':
			depth++
			b.WriteString("(?:")
		case c == '}' && depth > 0:
			depth--
			b.WriteString(")")
		case c == ',' && depth > 0:
			b.WriteString("|")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("the glob %q has an unclosed {", glob)
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

func cmpOr(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// tildeHome shortens a path in the home folder to ~/….
func tildeHome(p string) string {
	if home := homeDir(); home != "" && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// homeDir is the home folder ("" if it can't be told).
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// Cooldown is the least time between runs s's triggers start, in seconds
// (unset: 60; 0: none).
func Cooldown(s *pb.Schedule) int {
	if s.CooldownSeconds == nil {
		return 60
	}
	return int(max(s.GetCooldownSeconds(), 0))
}

// CooldownWords says a cooldown in words: "at most one run a minute",
// "no cooldown".
func CooldownWords(secs int) string {
	switch {
	case secs <= 0:
		return "no cooldown"
	case secs%3600 == 0:
		return plural(secs/3600, "hour") + " cooldown"
	case secs%60 == 0:
		return plural(secs/60, "minute") + " cooldown"
	}
	return plural(secs, "second") + " cooldown"
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
