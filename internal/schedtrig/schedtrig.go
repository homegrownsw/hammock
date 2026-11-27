// Package schedtrig reads and words a scheduled task's event triggers
// (pb.EventTrigger): what starts a run when it happens. hmk, its MCP tools,
// the TUI and the daemon share it, so a trigger reads the same everywhere.
//
// A trigger written out (hmk schedule --trigger, MCP schedule_save):
//
//	github:ci-failed | github:review-requested | github:reviewed |
//	github:label=NAME | github:assigned | github:merged |
//	github:commit[=BRANCH] | github:release[=TAGS] | github:issue-opened |
//	github:approved | github:workflow=NAME | github:notification
//	                                    [@OWNER/NAME: another repository]
//	agent:finished | agent:needs-you | agent:finished,needs-you  [@GROUP|PANE]
//	exit  [@GROUP|PANE]                 a pane's command exits non-zero
//	log:REGEX@PANE                      a log pane's line matches
//	files[:GLOB][@DIR]                  files change (DIR: the task's folder)
//	calendar:MINUTES|start|end|free[=MINUTES][,title=TEXT][,calendar=NAME][,attendees]
//	mac:disk<N%|disk<NGB[@VOLUME] | mac:battery<N% | mac:plugged-in |
//	mac:unplugged | mac:wake[=MIN] | mac:away=MIN | mac:back[=MIN] |
//	mac:drive[=NAME] | mac:wifi-joined[=NAME] | mac:wifi-left[=NAME] |
//	mac:app-opened=NAME | mac:app-quit=NAME | mac:display[=NAME]   (mac.go)
//	webhook
//	shortcut                            Shortcuts' Run Hammock Automation (Mac)
//
// A command check has a command in it, which can have any of : @ , in it,
// so it isn't written this way: hmk gives it flags of its own
// (--check-cmd), and Spec writes it as them.
package schedtrig

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Usage lists the forms Parse reads, for usage texts.
const Usage = `github:ci-failed|review-requested|reviewed|approved|label=NAME|assigned|merged|issue-opened|
  commit[=BRANCH]|release[=TAGS]|workflow=NAME|notification[@OWNER/NAME]
agent:finished|needs-you[,…][@GROUP|PANE]   exit[@GROUP|PANE]   log:REGEX@PANE   files[:GLOB][@DIR]
calendar:MINUTES|end|free[=MINUTES][,title=TEXT][,calendar=NAME][,attendees]
mac:disk<10%|disk<50GB[@VOLUME]|battery<20%|plugged-in|unplugged|wake[=MIN]|away=MIN|back[=MIN]|
  drive[=NAME]|wifi-joined[=NAME]|wifi-left[=NAME]|app-opened=NAME|app-quit=NAME|display[=NAME]
webhook   shortcut`

// Resolver finds the pane or Group ref names (an id, a name, an index), for
// a trigger that may be in either (groups) or a pane only.
type Resolver func(ref string, groups bool) (paneID, groupID string, err error)

var githubEvents = map[string]pb.GitHubTrigger_Event{
	"ci-failed": pb.GitHubTrigger_EVENT_CI_FAILED, "review-requested": pb.GitHubTrigger_EVENT_REVIEW_REQUESTED,
	"reviewed": pb.GitHubTrigger_EVENT_REVIEWED, "label": pb.GitHubTrigger_EVENT_LABELED,
	"assigned": pb.GitHubTrigger_EVENT_ASSIGNED, "merged": pb.GitHubTrigger_EVENT_MERGED,
	"commit": pb.GitHubTrigger_EVENT_COMMIT, "release": pb.GitHubTrigger_EVENT_RELEASE,
	"issue-opened": pb.GitHubTrigger_EVENT_ISSUE_OPENED, "approved": pb.GitHubTrigger_EVENT_APPROVED,
	"workflow": pb.GitHubTrigger_EVENT_WORKFLOW, "notification": pb.GitHubTrigger_EVENT_NOTIFICATION,
}

var githubRepoRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

// GitHubRepo is "owner/name" from s, written that way or as a github.com
// link (https, ssh or scp-like, .git or not), else "".
func GitHubRepo(s string) string {
	s = strings.TrimSpace(s)
	for _, p := range []string{"https://", "http://", "ssh://git@", "git@"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "www."), "github.com")
	s = strings.TrimPrefix(strings.TrimPrefix(s, ":"), "/")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "/"), ".git")
	if !githubRepoRE.MatchString(s) || strings.HasSuffix(s, "/.") || strings.HasSuffix(s, "/..") {
		return ""
	}
	return s
}

// CheckTags checks a release trigger's tags: a glob (path.Match's, as
// vt-*) or "".
func CheckTags(glob string) error {
	if _, err := path.Match(glob, ""); err != nil {
		return fmt.Errorf("the tags %q aren't a pattern like v* or vt-1.*", glob)
	}
	return nil
}

// TagMatches: tag matches glob, a release trigger's tags ("" matches any).
func TagMatches(glob, tag string) bool {
	if glob == "" {
		return true
	}
	ok, _ := path.Match(glob, tag)
	return ok
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
		// A repository to watch instead of the folder's: @OWNER/NAME.
		watch := ""
		if i := strings.LastIndex(rest, "@"); i >= 0 {
			if r := GitHubRepo(rest[i+1:]); r != "" {
				rest, watch = rest[:i], r
			}
		}
		ev, val, _ := strings.Cut(strings.TrimSpace(rest), "=")
		val = strings.TrimSpace(val)
		e, ok := githubEvents[strings.ToLower(ev)]
		if !ok {
			return nil, fmt.Errorf("%q: github:ci-failed, review-requested, reviewed, approved, label=NAME, assigned, merged, issue-opened, commit[=BRANCH], release[=TAGS], workflow=NAME or notification, then @OWNER/NAME for another repository", spec)
		}
		g := &pb.GitHubTrigger{Event: e, Watch: watch}
		switch e {
		case pb.GitHubTrigger_EVENT_LABELED:
			if val == "" {
				return nil, errors.New("github:label=NAME: which label?")
			}
			g.Label = val
		case pb.GitHubTrigger_EVENT_COMMIT:
			g.Branch = val
		case pb.GitHubTrigger_EVENT_RELEASE:
			if err := CheckTags(val); err != nil {
				return nil, err
			}
			g.Tags = val
		case pb.GitHubTrigger_EVENT_WORKFLOW:
			if val == "" {
				return nil, errors.New("github:workflow=NAME: which workflow (its name, or its file's, like ci.yml)?")
			}
			g.Workflow = val
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Github{Github: g}}, nil
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
			case i == 0 && (w == "start" || w == "starts"):
			case i == 0 && (w == "end" || w == "ends"):
				c.When = pb.CalendarTrigger_WHEN_END
			case i == 0 && k == "free":
				c.When = pb.CalendarTrigger_WHEN_FREE
				if v != "" {
					n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(v, "m"), "min"))
					if err != nil || n < 1 || n > 24*60 {
						return nil, fmt.Errorf("%q: calendar:free=MINUTES (free for at least that long, 1 to 1440)", spec)
					}
					c.FreeMinutes = int32(n)
				}
			case i == 0:
				n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(w, "m"), "min"))
				if err != nil || n < 0 || n > 24*60 {
					return nil, fmt.Errorf("%q: calendar:MINUTES (before the event, 0 to 1440), calendar:end or calendar:free", spec)
				}
				c.MinutesBefore = int32(n)
			case k == "title":
				c.Title = v
			case k == "calendar":
				c.Calendar = v
			case w == "attendees" || w == "with-attendees":
				c.WithAttendees = true
			default:
				return nil, fmt.Errorf("%q: calendar:MINUTES|end|free[=MINUTES][,title=TEXT][,calendar=NAME][,attendees]", spec)
			}
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Calendar{Calendar: c}}, nil
	case "webhook", "hook":
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Webhook{Webhook: &pb.WebhookTrigger{}}}, nil
	case "shortcut", "shortcuts":
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Shortcut{Shortcut: &pb.ShortcutTrigger{}}}, nil
	case "mac", "this-mac":
		m, err := parseMac(spec, rest)
		if err != nil {
			return nil, err
		}
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Mac{Mac: m}}, nil
	case "check", "command-check":
		return nil, errors.New("a command check is given with its own options: --check-cmd COMMAND [--check-every MINUTES] [--check-on fail|change|recover] [--check-timeout SECONDS]")
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
		if r := cmpOr(g.Repo, g.Watch); r != "" {
			in = " (" + r + ")"
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
		case pb.GitHubTrigger_EVENT_COMMIT:
			if g.Branch != "" {
				return "A new commit on " + g.Branch + in
			}
			return "A new commit on the default branch" + in
		case pb.GitHubTrigger_EVENT_RELEASE:
			if g.Tags != "" {
				return "A new release or tag matching " + g.Tags + in
			}
			return "A new release or tag" + in
		case pb.GitHubTrigger_EVENT_ISSUE_OPENED:
			return "A new issue is opened" + in
		case pb.GitHubTrigger_EVENT_APPROVED:
			return "My pull request is approved" + in
		case pb.GitHubTrigger_EVENT_WORKFLOW:
			return "The “" + g.Workflow + "” workflow finishes" + in
		case pb.GitHubTrigger_EVENT_NOTIFICATION:
			return "I get a GitHub notification" + in
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
		switch {
		case c.When == pb.CalendarTrigger_WHEN_END:
			out = "At the end of "
		case c.When == pb.CalendarTrigger_WHEN_FREE:
			out = "Free time of " + plural(FreeMinutes(c), "minute") + " or more begins in working hours"
			if c.Calendar != "" {
				out += " (" + c.Calendar + ")"
			}
			return out
		case c.MinutesBefore == 0:
			out = "At the start of "
		case c.MinutesBefore == 1:
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
		return "Something calls its webhook URL"
	case *pb.EventTrigger_Shortcut:
		return "Shortcuts runs it"
	case *pb.EventTrigger_Mac:
		return describeMac(k.Mac)
	case *pb.EventTrigger_Check:
		c := k.Check
		what := " starts failing"
		switch c.On {
		case pb.CommandCheckTrigger_ON_CHANGE:
			what = "’s output changes"
		case pb.CommandCheckTrigger_ON_RECOVER:
			what = " passes again"
		}
		return "“" + c.Command + "”" + what + ", checked " + EveryWords(CheckEvery(c))
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
		g := k.Github
		s := "github:" + GitHubEventName(g.Event)
		switch {
		case g.Event == pb.GitHubTrigger_EVENT_LABELED:
			s += "=" + g.Label
		case g.Event == pb.GitHubTrigger_EVENT_COMMIT && g.Branch != "":
			s += "=" + g.Branch
		case g.Event == pb.GitHubTrigger_EVENT_RELEASE && g.Tags != "":
			s += "=" + g.Tags
		case g.Event == pb.GitHubTrigger_EVENT_WORKFLOW:
			s += "=" + g.Workflow
		}
		if g.Watch != "" {
			s += "@" + g.Watch
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
		switch c.When {
		case pb.CalendarTrigger_WHEN_END:
			s = "calendar:end"
		case pb.CalendarTrigger_WHEN_FREE:
			s = "calendar:free"
			if c.FreeMinutes > 0 {
				s += "=" + strconv.Itoa(int(c.FreeMinutes))
			}
		}
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
	case *pb.EventTrigger_Shortcut:
		return "shortcut"
	case *pb.EventTrigger_Mac:
		return specMac(k.Mac)
	case *pb.EventTrigger_Check:
		return CheckFlags(k.Check)
	}
	return ""
}

// Brief is t in a few words, for a list: Spec, but a command check as
// "check:COMMAND".
func Brief(t *pb.EventTrigger) string {
	if c := t.GetCheck(); c != nil {
		return "check:" + c.Command
	}
	return Spec(t)
}

// CheckFlags writes a command check as hmk's options for it.
func CheckFlags(c *pb.CommandCheckTrigger) string {
	s := "--check-cmd " + shellQuote(c.Command) + " --check-every " + strconv.Itoa(CheckEvery(c)) + " --check-on " + CheckOnName(c.On)
	if c.TimeoutSeconds > 0 {
		s += " --check-timeout " + strconv.Itoa(int(c.TimeoutSeconds))
	}
	return s
}

var checkOns = map[string]pb.CommandCheckTrigger_On{
	"fail": pb.CommandCheckTrigger_ON_FAIL, "change": pb.CommandCheckTrigger_ON_CHANGE, "recover": pb.CommandCheckTrigger_ON_RECOVER,
}

// CheckOn reads fail, change or recover.
func CheckOn(s string) (pb.CommandCheckTrigger_On, error) {
	if on, ok := checkOns[strings.ToLower(strings.TrimSpace(s))]; ok {
		return on, nil
	}
	return 0, fmt.Errorf("%q: fail, change or recover", s)
}

// CheckOnName is the word CheckOn reads for on.
func CheckOnName(on pb.CommandCheckTrigger_On) string {
	for k, v := range checkOns {
		if v == on {
			return k
		}
	}
	return "fail"
}

// Command checks' bounds: how often (minutes) and how long one may run
// (seconds; never longer than how often).
const (
	CheckEveryDefault   = 5
	CheckEveryMax       = 7 * 24 * 60
	CheckTimeoutDefault = 60
	CheckTimeoutMax     = 60 * 60
)

// CheckEvery is how often c runs, in minutes.
func CheckEvery(c *pb.CommandCheckTrigger) int {
	if c.GetEveryMinutes() <= 0 {
		return CheckEveryDefault
	}
	return int(c.GetEveryMinutes())
}

// CheckTimeout is how long one check of c may run.
func CheckTimeout(c *pb.CommandCheckTrigger) time.Duration {
	secs := int(c.GetTimeoutSeconds())
	if secs <= 0 {
		secs = CheckTimeoutDefault
	}
	return time.Duration(min(secs, CheckTimeoutMax, CheckEvery(c)*60)) * time.Second
}

// EveryWords says how often, in minutes: "every minute", "every 5
// minutes", "every hour", "every 2 days".
func EveryWords(minutes int) string {
	switch {
	case minutes <= 1:
		return "every minute"
	case minutes%(24*60) == 0:
		if minutes == 24*60 {
			return "every day"
		}
		return "every " + plural(minutes/(24*60), "day")
	case minutes%60 == 0:
		if minutes == 60 {
			return "every hour"
		}
		return "every " + plural(minutes/60, "hour")
	}
	return "every " + plural(minutes, "minute")
}

// shellQuote quotes s for a shell, if it needs it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+,:@%=", r))
	}) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Status is the line under a trigger: when it last looked and fired, or
// what's holding back what it fired. "" when there's nothing to say yet.
// "Last checked 2 min ago · last fired yesterday 14:02".
func Status(t *pb.EventTrigger, now time.Time) string {
	var parts []string
	if t.CheckedUnixMs > 0 {
		parts = append(parts, "last checked "+Ago(time.UnixMilli(t.CheckedUnixMs), now))
	}
	switch {
	case t.FiredUnixMs > 0:
		parts = append(parts, "last fired "+When(time.UnixMilli(t.FiredUnixMs), now))
	case t.CheckedUnixMs > 0:
		parts = append(parts, "never fired")
	}
	if t.Held != "" {
		parts = append(parts, t.Held)
	}
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, " · ")
	return strings.ToUpper(out[:1]) + out[1:]
}

// Ago says how long ago: "just now", "2 min ago", "3 h ago", "2 days ago".
func Ago(at, now time.Time) string {
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " min ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " h ago"
	}
	return plural(int(d/(24*time.Hour)), "day") + " ago"
}

// When says when, near now: "14:02", "yesterday 14:02", "Mon 14:02",
// "3 Nov".
func When(at, now time.Time) string {
	at = at.In(now.Location())
	y1, m1, d1 := at.Date()
	day := func(t time.Time) time.Time { y, m, d := t.Date(); return time.Date(y, m, d, 0, 0, 0, 0, t.Location()) }
	days := int(day(now).Sub(day(at)).Hours() / 24)
	switch {
	case days <= 0:
		return at.Format("15:04")
	case days == 1:
		return "yesterday " + at.Format("15:04")
	case days < 7:
		return at.Format("Mon 15:04")
	case y1 == now.Year():
		return strconv.Itoa(d1) + " " + m1.String()[:3]
	}
	return strconv.Itoa(d1) + " " + m1.String()[:3] + " " + strconv.Itoa(y1)
}

// Kind is t's kind's word: "github", "agent", "exit", "log", "files",
// "calendar", "webhook", "shortcut", "check" or "mac".
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
	case *pb.EventTrigger_Shortcut:
		return "shortcut"
	case *pb.EventTrigger_Check:
		return "check"
	case *pb.EventTrigger_Mac:
		return "mac"
	}
	return ""
}

// FreeMinutes is how long free time must be for a calendar free trigger.
func FreeMinutes(c *pb.CalendarTrigger) int {
	if c.GetFreeMinutes() <= 0 {
		return 30
	}
	return int(c.GetFreeMinutes())
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
	case secs%(30*86400) == 0:
		return plural(secs/(30*86400), "month") + " cooldown"
	case secs%(7*86400) == 0:
		return plural(secs/(7*86400), "week") + " cooldown"
	case secs%86400 == 0:
		return plural(secs/86400, "day") + " cooldown"
	case secs%3600 == 0:
		return plural(secs/3600, "hour") + " cooldown"
	case secs%60 == 0:
		return plural(secs/60, "minute") + " cooldown"
	}
	return plural(secs, "second") + " cooldown"
}

// SessionWords says where s's runs go when they carry on a session
// ("Continues ‹name›"), "" for a new tab and session each run.
func SessionWords(s *pb.Schedule) string {
	name := cmpOr(s.SessionName, s.SessionId)
	switch s.Session {
	case pb.Schedule_SESSION_OWN:
		if name == "" {
			return "Continues its own session (from its first run)"
		}
		return "Continues its own session, " + name
	case pb.Schedule_SESSION_PICKED:
		return "Continues " + name
	}
	return ""
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
