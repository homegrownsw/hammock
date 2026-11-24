package agents

// Usage limits an agent's turn ended at: its plan's allowance for a while
// (Claude's 5-hour or weekly limit, Codex's), used up, so it stopped until
// the limit resets. The daemon sends it "continue" then (limitresume.go).
// Not a passing 429 or an overloaded API: agents retry those themselves,
// and when they give up, nothing says when trying again would work.

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/agentview"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Limit is a usage limit an agent's turn ended at.
type Limit struct {
	// Resets is when the limit resets, when the agent said (its error's
	// words, or its session file's limits); zero when it didn't.
	Resets time.Time
}

// HitLimit reads whether StopFailure h ended the agent's turn at its usage
// limit, and when the limit resets if the agent said. now is when it came:
// a reset given as a time of day is the next one after it.
func (a *Agent) HitLimit(h Hook, now time.Time) (Limit, bool) {
	if h.Event != "StopFailure" {
		return Limit{}, false
	}
	if a != nil && a.UsageLimit != nil {
		return a.UsageLimit(h, now)
	}
	return genericLimit(h, now)
}

// limitWords are how agents say a usage limit (not a passing rate limit):
// Claude Code's "You've hit your 5-hour limit", Codex's "You've hit your
// usage limit", Gemini's "exhausted your daily quota", Antigravity's
// "quota exceeded", "usage limit reached".
var limitWords = regexp.MustCompile(`(?i)\bhit your (?:[\w-]+ )*limit\b|\busage limit\b|\bout of usage\b|\bquota (?:exceeded|exhausted|reached)\b|\bexhausted (?:your |the )?(?:[\w-]+ )*quota\b|\blimit reached\b.*\bresets?\b`)

// transientWords are a rate limit that passes, which the agent retried
// before giving up: nothing to wait out.
var transientWords = regexp.MustCompile(`(?i)\btemporar(?:y|ily)\b|\boverloaded\b|\bcapacity\b|\brequest rejected \(429\)`)

// genericLimit reads a StopFailure's error (its kind and details: never
// the agent's own last message, whose prose could say anything) for a
// usage limit, in the words agents use, and when it resets, in theirs
// ("resets 3pm", "try again at 3:05 PM", "retry in 2h 10m").
func genericLimit(h Hook, now time.Time) (Limit, bool) {
	return limitIn(h, now, h.ErrorDetails, h.Error)
}

// limitIn reads texts, an error's words, for a usage limit and its reset.
func limitIn(h Hook, now time.Time, texts ...string) (Limit, bool) {
	text := strings.Join(texts, "\n")
	if !limitWords.MatchString(text) || transientWords.MatchString(text) {
		return Limit{}, false
	}
	return Limit{Resets: limitResetFrom(h, now, texts...)}, true
}

// limitResetFrom is when a usage limit resets, as h says: its own time
// (an agent's session file's), else in texts, its error's words, the
// first that says.
func limitResetFrom(h Hook, now time.Time, texts ...string) time.Time {
	if h.ResetsUnix > 0 {
		return time.Unix(h.ResetsUnix, 0)
	}
	for _, s := range texts {
		if t := ParseLimitReset(s, now); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// claudeLimit reads Claude Code's StopFailure: a rate_limit error whose
// message is its usage limit's ("You've hit your 5-hour limit · resets
// 3pm (Europe/London)", "You've hit your weekly limit · resets Nov 27,
// 10am (Europe/London)", "You're out of usage credits · resets …"), not a
// rejected request ("API Error: Request rejected (429) · this may be a
// temporary capacity issue").
func claudeLimit(h Hook, now time.Time) (Limit, bool) {
	if h.Error != "" && h.Error != "rate_limit" {
		return Limit{}, false
	}
	// Its StopFailure's last message is the error line it showed, not a
	// reply: read with the details.
	return limitIn(h, now, h.ErrorDetails, h.LastMessage, h.Error)
}

// codexLimit reads the StopFailure codexTurnFailed makes: a
// usage_limit_exceeded error that's its plan's limit ("You've hit your
// usage limit… try again at 3:05 PM."), with when its windows reset; not
// an API key's quota or a plan without Codex, which no time cures.
func codexLimit(h Hook, now time.Time) (Limit, bool) {
	if h.Error != "usage_limit_exceeded" || h.ResetsUnix == 0 && !strings.Contains(strings.ToLower(h.ErrorDetails), "hit your usage limit") {
		return Limit{}, false
	}
	return Limit{Resets: limitResetFrom(h, now, h.ErrorDetails)}, true
}

var (
	// "resets 3pm (Europe/London)", "resets at 3:30pm", "resets Nov 27,
	// 10am": up to a " · ", the end of a sentence, or the line's end; with
	// its time zone in brackets.
	resetsAt = regexp.MustCompile(`(?i)\bresets?(?: at| on)? ([^·()\n]+?)\s*(?:\(([^)]+)\))?\s*(?:·|\.(?:\s|$)|,?\s*$|\n)`)
	// Codex's "try again at 3:05 PM.", "try again at Nov 27th, 2026 3:05 PM."
	tryAt = regexp.MustCompile(`(?i)\b(?:try again|retry|available again)(?: at| after) ([^·()\n]+?)\s*(?:\(([^)]+)\))?\s*(?:·|\.(?:\s|$)|,?\s*$|\n)`)
	// "resets in 2h 10m", "try again in 3 hours", "retry in 45s".
	resetsIn = regexp.MustCompile(`(?i)\b(?:resets?|try again|retry|available again)(?: in| after) ((?:\d+(?:\.\d+)?\s*(?:days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b[\s,]*(?:and\s+)?)+)`)
	durPart  = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(days?|d|hours?|hrs?|h|minutes?|mins?|m|seconds?|secs?|s)\b`)
	ordinal  = regexp.MustCompile(`(?i)(\d)(?:st|nd|rd|th)\b`)
	ampm     = regexp.MustCompile(`(?i)(\d)\s*([ap])\.?m\.?\b`)
)

// ParseLimitReset finds when a usage limit resets in an agent's words, or
// zero. now is when they were said: a time of day is the next one after
// it, a date without a year the next one too. A time zone in brackets
// ("(Europe/London)") is the time's; otherwise it's this computer's.
func ParseLimitReset(s string, now time.Time) time.Time {
	if m := resetsIn.FindStringSubmatch(s); m != nil {
		var d time.Duration
		for _, p := range durPart.FindAllStringSubmatch(m[1], -1) {
			n, _ := strconv.ParseFloat(p[1], 64)
			switch u := strings.ToLower(p[2]); {
			case strings.HasPrefix(u, "d"):
				d += time.Duration(n * float64(24*time.Hour))
			case strings.HasPrefix(u, "h"):
				d += time.Duration(n * float64(time.Hour))
			case strings.HasPrefix(u, "m"):
				d += time.Duration(n * float64(time.Minute))
			default:
				d += time.Duration(n * float64(time.Second))
			}
		}
		if d > 0 {
			return now.Add(d)
		}
	}
	for _, re := range []*regexp.Regexp{resetsAt, tryAt} {
		if m := re.FindStringSubmatch(s); m != nil {
			if t := parseResetTime(m[1], m[2], now); !t.IsZero() {
				return t
			}
		}
	}
	return time.Time{}
}

// parseResetTime reads "3pm", "3:30pm", "Nov 27, 10am", "Nov 27, 2026,
// 10am", "Nov 27th, 2026 3:05 PM", "Nov 27" in zone tz ("" or unknown:
// now's).
func parseResetTime(s, tz string, now time.Time) time.Time {
	loc := now.Location()
	if tz != "" {
		if l, err := time.LoadLocation(strings.TrimSpace(tz)); err == nil {
			loc = l
		}
	}
	now = now.In(loc)
	s = strings.ToLower(strings.TrimSpace(s))
	s = ordinal.ReplaceAllString(s, "$1")
	s = ampm.ReplaceAllString(s, "$1${2}m")
	s = strings.Join(strings.Fields(s), " ")
	for _, l := range []string{"3pm", "3:04pm", "15:04"} {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if at.Before(now.Add(-time.Minute)) {
				at = at.AddDate(0, 0, 1)
			}
			return at
		}
	}
	for _, l := range []string{"Jan 2, 2006, 3pm", "Jan 2, 2006, 3:04pm", "Jan 2, 2006 3pm", "Jan 2, 2006 3:04pm", "Jan 2, 2006"} {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t
		}
	}
	for _, l := range []string{"Jan 2, 3pm", "Jan 2, 3:04pm", "Jan 2 3pm", "Jan 2 3:04pm", "Jan 2"} {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			at := time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if at.Before(now.AddDate(0, 0, -1)) {
				at = at.AddDate(1, 0, 0)
			}
			return at
		}
	}
	return time.Time{}
}

// ExhaustedReset is when the windows used up (at 100% or more) of an
// agent's usage reset: the latest of them, as all must have reset before
// it can go on. Zero when none is used up, or none says.
func ExhaustedReset(windows []*pb.UsageWindow, now time.Time) time.Time {
	var at time.Time
	for _, w := range windows {
		if w.GetUsedPercent() < 100 || w.GetResetsUnix() <= 0 {
			continue
		}
		if t := time.Unix(w.GetResetsUnix(), 0); t.After(now) && t.After(at) {
			at = t
		}
	}
	return at
}

// AgentWait is what an agent's screen says of its own wait for its usage
// limit to reset (Claude Code's "continue automatically"), read by its
// LimitWait.
type AgentWait struct {
	// Offer: the menu on screen offers to wait and continue by itself, as
	// this option (1-based); 0 when it doesn't.
	Offer int
	// Armed: it's set to continue by itself (or is continuing now); At is
	// when, if it says.
	Armed bool
	At    time.Time
	// Cancelled: its own wait was cancelled (Esc, or "Don't continue
	// automatically"), after anything that armed it.
	Cancelled bool
}

var (
	// Claude Code's own wait, as it says it (2.1.331): "Usage limit
	// reached · set to continue automatically at 6:35pm", "… reached
	// again · set to …", "Continuing automatically at 6:35pm · Esc to
	// cancel", "Usage limit reset · Continuing automatically", "Claude
	// Code will continue automatically at 6:35pm. Keep this session open".
	claudeArmed = regexp.MustCompile(`(?i)\bset to continue automatically\b|\bcontinuing automatically\b|\bwill continue automatically\b`)
	// "Automatic continue cancelled. Your session will wait for you
	// instead", "the automatic continue at the usage-limit reset was
	// cancelled".
	claudeUnarmed = regexp.MustCompile(`(?i)\bautomatic continue cancelled\b|\bautomatic continue at the usage-limit reset was cancelled\b|\bwill not resume on its own\b`)
	// Its menu's option: "Wait here, then continue automatically at
	// 6:35pm" (or "shortly", "when the limit resets").
	claudeWaitOption = regexp.MustCompile(`(?i)^wait here, then continue automatically\b`)
	// When, in its words: "continue automatically at 6:35pm".
	claudeWaitWhen = regexp.MustCompile(`(?i)\bcontinu(?:e|ing) automatically(?: at| on)? ([^·()\n.]+?)\s*(?:\(([^)]+)\))?\s*(?:·|\.(?:\s|$)|,?\s*$|\n)`)
)

// claudeWait reads Claude Code's screen for its own wait: its limit menu's
// "Wait here, then continue automatically …", and the last thing it said
// of that wait, armed or cancelled, from the bottom of the screen up.
func claudeWait(screen string, now time.Time) AgentWait {
	var w AgentWait
	sc := agentview.ReadScreen(screen)
	for i, o := range sc.Options {
		if claudeWaitOption.MatchString(strings.TrimSpace(o)) {
			w.Offer = i + 1
			break
		}
	}
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if claudeUnarmed.MatchString(l) {
			w.Cancelled = true
			break
		}
		if claudeArmed.MatchString(l) {
			w.Armed = true
			if m := claudeWaitWhen.FindStringSubmatch(l + "\n"); m != nil {
				w.At = parseResetTime(m[1], m[2], now)
			}
			break
		}
	}
	return w
}
