package agents

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHitLimit(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip("no time zone data")
	}
	now := time.Date(2026, 11, 24, 12, 10, 0, 0, london)
	at := func(mo time.Month, d, h, m int) time.Time { return time.Date(2026, mo, d, h, m, 0, 0, london) }
	stop := func(kind, msg string) Hook { return Hook{Event: "StopFailure", Error: kind, LastMessage: msg} }
	for _, c := range []struct {
		name   string
		agent  *Agent
		h      Hook
		hit    bool
		resets time.Time
	}{
		{"claude 5-hour", Claude, stop("rate_limit", "You've hit your 5-hour limit · resets 3pm (Europe/London)"), true, at(11, 24, 15, 0)},
		{"claude minutes", Claude, stop("rate_limit", "You've hit your limit · resets 3:30pm (Europe/London) · progress saved"), true, at(11, 24, 15, 30)},
		{"claude past time is tomorrow", Claude, stop("rate_limit", "You've hit your 5-hour limit · resets 9am (Europe/London)"), true, at(11, 25, 9, 0)},
		{"claude weekly", Claude, stop("rate_limit", "You've hit your weekly limit · resets Nov 27, 10am (Europe/London)"), true, at(11, 27, 10, 0)},
		{"claude with year", Claude, stop("rate_limit", "You've hit your weekly limit · resets Jan 2, 2027, 10am (Europe/London)"), true, time.Date(2027, 1, 2, 10, 0, 0, 0, london)},
		{"claude next year", Claude, stop("rate_limit", "You've hit your Opus limit · resets Jan 2, 10am (Europe/London)"), true, time.Date(2027, 1, 2, 10, 0, 0, 0, london)},
		{"claude another zone", Claude, stop("rate_limit", "You've hit your 5-hour limit · resets 3pm (America/New_York)"), true, at(11, 24, 20, 0)},
		{"claude no reset", Claude, stop("rate_limit", "You've hit your usage limit"), true, time.Time{}},
		{"claude out of credits", Claude, stop("rate_limit", "You're out of usage credits · resets 6pm (Europe/London)"), true, at(11, 24, 18, 0)},
		{"claude transient 429", Claude, stop("rate_limit", "API Error: Request rejected (429) · this may be a temporary capacity issue."), false, time.Time{}},
		{"claude overloaded", Claude, stop("overloaded", "API Error: 529 Overloaded"), false, time.Time{}},
		{"claude billing", Claude, stop("billing_error", "Credit balance too low · usage limit reached"), false, time.Time{}},
		{"claude connection", Claude, stop("server_error", "API Error: Connection lost mid-response."), false, time.Time{}},
		{"not a StopFailure", Claude, Hook{Event: "Stop", LastMessage: "You've hit your 5-hour limit · resets 3pm"}, false, time.Time{}},
		{"codex today", Codex, Hook{Event: "StopFailure", Error: "usage_limit_exceeded", ErrorDetails: "You've hit your usage limit. Upgrade to Pro (https://chatgpt.com/explore/pro), visit https://chatgpt.com/codex/settings/usage to purchase more credits or try again at 3:05 PM."}, true, at(11, 24, 15, 5)},
		{"codex date", Codex, Hook{Event: "StopFailure", Error: "usage_limit_exceeded", ErrorDetails: "You've hit your usage limit. Try again at Nov 27th, 2026 3:05 PM."}, true, at(11, 27, 15, 5)},
		{"codex its own time", Codex, Hook{Event: "StopFailure", Error: "usage_limit_exceeded", ErrorDetails: "You've hit your usage limit.", ResetsUnix: at(11, 24, 16, 0).Unix()}, true, at(11, 24, 16, 0)},
		{"codex quota", Codex, Hook{Event: "StopFailure", Error: "usage_limit_exceeded", ErrorDetails: "Quota exceeded. Check your plan and billing details."}, false, time.Time{}},
		{"codex prose isn't its error", Codex, Hook{Event: "StopFailure", Error: "usage_limit_exceeded", LastMessage: "I'll retry in 5 minutes.", ErrorDetails: "You've hit your usage limit. Try again at 3:05 PM."}, true, at(11, 24, 15, 5)},
		{"another agent's prose", Gemini, Hook{Event: "StopFailure", LastMessage: "Your usage limit for the API is fine.", ErrorDetails: "connection reset"}, false, time.Time{}},
		{"codex dropped", Codex, Hook{Event: "StopFailure", Error: "response_stream_disconnected", ErrorDetails: "stream disconnected before completion"}, false, time.Time{}},
		{"gemini daily quota", Gemini, Hook{Event: "StopFailure", ErrorDetails: "You have exhausted your daily quota on this model."}, true, time.Time{}},
		{"quota retry in", Antigravity, Hook{Event: "StopFailure", ErrorDetails: "quota exceeded, retry in 2h 30m"}, true, now.Add(150 * time.Minute)},
		{"copilot 429", Copilot, Hook{Event: "StopFailure", Error: "RateLimitError", ErrorDetails: "429 Too Many Requests"}, false, time.Time{}},
		{"cline rate limited", Cline, Hook{Event: "StopFailure", ErrorDetails: "429 rate limited"}, false, time.Time{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			l, hit := c.agent.HitLimit(c.h, now)
			if hit != c.hit || !l.Resets.Equal(c.resets) {
				t.Errorf("got %v %v, want %v %v", hit, l.Resets, c.hit, c.resets)
			}
		})
	}
}

func TestCodexTurnTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-11-24T12-00-00-x.jsonl")
	limits := `{"timestamp":"2026-11-24T12:01:00.000Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":100.0,"window_minutes":300,"resets_at":1795968000},"secondary":{"used_percent":41.0,"window_minutes":10080,"resets_at":1796428800}}}}`
	stale := `{"timestamp":"2026-11-24T12:01:00.000Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":100.0,"window_minutes":300,"resets_at":1764003600}}}}`
	started := `{"timestamp":"2026-11-24T12:00:00.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t2"}}`
	failed := `{"timestamp":"2026-11-24T12:01:01.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t2","last_agent_message":"I'll retry in 5 minutes.","error":{"message":"You've hit your usage limit. Try again at 4:20 PM.","codex_error_info":"usage_limit_exceeded"}}}`
	done := `{"timestamp":"2026-11-24T11:00:00.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":"Done."}}`
	dropped := `{"timestamp":"2026-11-24T12:01:01.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"t2","error":{"message":"stream disconnected","codex_error_info":{"response_stream_disconnected":{"http_status_code":null}}}}}`
	since := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	lines := func(ls ...string) []byte {
		var b []byte
		for _, l := range ls {
			b = append(b, l+"\n"...)
		}
		return b
	}
	fresh := func(ls ...string) TurnTail {
		if err := os.WriteFile(path, lines(ls...), 0o600); err != nil {
			t.Fatal(err)
		}
		return codexTurnTail(path)
	}

	h, ok := fresh(done, started, limits, failed).Next(since)
	if !ok || h.Event != "StopFailure" || h.Error != "usage_limit_exceeded" || h.ResetsUnix != 1795968000 || h.ErrorDetails == "" {
		t.Fatalf("limit: got %+v %v", h, ok)
	}
	// Its windows' reset, not the model's "retry in 5 minutes".
	if l, hit := Codex.HitLimit(h, since); !hit || l.Resets.Unix() != 1795968000 {
		t.Errorf("its limit: %v %v", l, hit)
	}
	// A window whose reset is before the turn ended had reset already:
	// the error's own words say.
	h, _ = fresh(done, started, stale, failed).Next(since)
	if h.ResetsUnix != 0 {
		t.Errorf("a stale window's reset: %d", h.ResetsUnix)
	}
	if l, hit := Codex.HitLimit(h, since); !hit || l.Resets.Hour() != 16 || l.Resets.Minute() != 20 {
		t.Errorf("its words' reset: %v %v", l, hit)
	}

	h, ok = fresh(done, started, limits, dropped).Next(since)
	if !ok || h.Error != "response_stream_disconnected" {
		t.Errorf("another error: got %+v %v", h, ok)
	}
	if _, hit := Codex.HitLimit(h, since); hit {
		t.Error("a dropped stream isn't a limit")
	}
	if _, ok := fresh(done).Next(time.Time{}); ok {
		t.Error("a turn that finished didn't fail")
	}
	if _, ok := fresh(done, started, limits, failed).Next(since.Add(time.Hour)); ok {
		t.Error("an error before since is an earlier turn's")
	}
	if _, ok := fresh(started, limits, failed, started).Next(since); ok {
		t.Error("a turn under way hasn't failed")
	}

	// Followed as it grows: only what's appended is read, a line split
	// across two reads included.
	tail := fresh(done, started)
	if _, ok := tail.Next(since); ok {
		t.Fatal("working: nothing failed")
	}
	if _, ok := tail.Next(since); ok {
		t.Fatal("unchanged: nothing new")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	all := lines(limits, failed)
	f.Write(all[:len(all)-20])
	if _, ok := tail.Next(since); ok {
		t.Fatal("half a line isn't its end")
	}
	f.Write(all[len(all)-20:])
	f.Close()
	if h, ok := tail.Next(since); !ok || h.Error != "usage_limit_exceeded" {
		t.Fatalf("appended: got %+v %v", h, ok)
	}
}

// Claude Code's own wait for its limit: offered in its menu, armed, then
// cancelled, as its screen says (2.1.331's words).
func TestClaudeWait(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skip("no time zone data")
	}
	now := time.Date(2026, 11, 24, 12, 10, 0, 0, london)
	menu := "  ⎿  You've hit your 5-hour limit · resets 6:35pm (Europe/London)\n\n" +
		" What do you want to do?\n\n" +
		" ❯ 1. Stop and wait for limit to reset\n" +
		"   2. Wait here, then continue automatically at 6:35pm\n" +
		"   3. Upgrade your plan\n\n" +
		" Enter to confirm · Esc to cancel\n"
	if w := claudeWait(menu, now); w.Offer != 2 || w.Armed {
		t.Errorf("its menu: %+v", w)
	}
	armed := "  ⎿  You've hit your 5-hour limit · resets 6:35pm (Europe/London)\n" +
		"  ⎿  Usage limit reached · set to continue automatically at 6:35pm\n\n" +
		"────────\n❯ \n────────\n  Continuing automatically at 6:35pm · Esc to cancel\n"
	if w := claudeWait(armed, now); !w.Armed || w.Offer != 0 || w.At.Hour() != 18 || w.At.Minute() != 35 {
		t.Errorf("armed: %+v", w)
	}
	cancelled := armed + "  ⎿  Automatic continue cancelled. Your session will wait for you instead; /rate-limit-options can arm it again\n"
	if w := claudeWait(cancelled, now); w.Armed || !w.Cancelled {
		t.Errorf("cancelled: %+v", w)
	}
	if w := claudeWait("❯ \n", now); w != (AgentWait{}) {
		t.Errorf("nothing: %+v", w)
	}
}
