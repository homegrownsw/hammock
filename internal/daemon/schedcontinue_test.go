package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// continueSetup is a daemon with a fake claude (it says how it was started,
// then shows whatever's written to its screen file) and a tab of yours running
// it in session sess-mine.
type continueSetup struct {
	d      *Daemon
	c      *client.Client
	clock  *schedClock
	work   string
	gid    string // the automation's Group
	mine   string // your tab's pane
	screen string // written: shown in your tab
}

func newContinueSetup(t *testing.T) *continueSetup {
	bin := t.TempDir()
	screen := filepath.Join(t.TempDir(), "screen")
	fake := "#!/bin/sh\necho STARTED \"$@\"\nwhile :; do if [ -f '" + screen + "' ]; then cat '" + screen + "'; rm -f '" + screen + "'; fi; sleep 0.1; done\n"
	os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	plainShell(t)
	t.Setenv("ENV", "")
	cs := &continueSetup{work: t.TempDir(), screen: screen}
	cs.clock = useSchedClock(t, time.Date(2026, 11, 4, 7, 0, 0, 0, time.Local))
	dir := shortDir(t)
	cs.d = startDaemon(t, dir)
	t.Cleanup(cs.d.Shutdown)
	cs.c = dial(t, dir)
	call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	cs.gid = client.CreatedID(call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Assistant"}}}))
	mineGroup := client.CreatedID(call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Mine"}}}))
	tid := client.CreatedID(call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: mineGroup, Name: "My work", Pane: &pb.PaneSpec{Command: []string{"claude", "--dangerously-skip-permissions"}, Cwd: cs.work},
	}}}))
	cs.d.mu.Lock()
	cs.mine = cs.d.tabs[tid].FocusedPaneId
	cs.d.mu.Unlock()
	call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: cs.mine}}})
	waitOutput(t, cs.c, cs.mine, "STARTED")
	hook(cs.d, cs.mine, "SessionStart", map[string]any{"session_id": "sess-mine"})
	return cs
}

func (cs *continueSetup) save(t *testing.T, s *pb.Schedule) *pb.Schedule {
	t.Helper()
	s.Kind, s.Cwd, s.GroupId = "claude", cs.work, cs.gid
	if s.Prompt == "" {
		s.Prompt = "Check the inbox"
	}
	if s.Timing == pb.Schedule_TIMING_REPEAT && s.MinuteOfDay == 0 {
		s.Timing, s.OnceUnixMs = pb.Schedule_TIMING_ONCE, time.Date(2027, 6, 1, 9, 0, 0, 0, time.Local).UnixMilli() // (Run Now only, here)
	}
	r := call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}})
	return r.GetScheduleReply().GetSchedule()
}

func (cs *continueSetup) runNow(t *testing.T, id string) *pb.ScheduleRun {
	t.Helper()
	call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: id}}})
	return schedOf(cs.d, id).Runs[0]
}

// runIs waits for the newest run of id to be status (the typing is off the lock).
func (cs *continueSetup) runIs(t *testing.T, id string, status pb.ScheduleRun_Status) *pb.ScheduleRun {
	t.Helper()
	var r *pb.ScheduleRun
	waitFor(t, "run "+status.String(), func() bool {
		cs.d.mu.Lock()
		sending := len(cs.d.sched.sending) > 0
		cs.d.mu.Unlock()
		r = schedOf(cs.d, id).Runs[0]
		return r.Status == status && !sending
	})
	return r
}

func (cs *continueSetup) attention(pane string) bool {
	cs.d.mu.Lock()
	defer cs.d.mu.Unlock()
	_, ok := cs.d.attention["at_"+pane]
	return ok
}

// A run sent into a session you picked, open in your tab: typed in when
// its agent is free, never marked the automation's, and its tab is left
// alone: not closed, kept or stopped, and it can still set off automations.
func TestContinuePickedSession(t *testing.T) {
	cs := newContinueSetup(t)
	d := cs.d
	s := cs.save(t, &pb.Schedule{Name: "Inbox", Session: pb.Schedule_SESSION_PICKED, SessionId: "sess-mine", KeepRuns: 1, GiveUpMinutes: 30})
	if s.Overlap != pb.Schedule_OVERLAP_WAIT || s.SessionName != "My work" || !strings.Contains(s.SessionWarning, "--dangerously-skip-permissions") {
		t.Fatalf("saved: overlap %v, name %q, warning %q", s.Overlap, s.SessionName, s.SessionWarning)
	}
	// The approval an agent's save asks says so.
	d.mu.Lock()
	sum := d.scheduleSummary(s)
	d.mu.Unlock()
	if !strings.Contains(sum, "your session My work") || !strings.Contains(sum, "can do more than this automation allows") {
		t.Fatalf("summary: %s", sum)
	}
	// Commands, and new sessions without one picked, are refused.
	for _, bad := range []*pb.Schedule{
		{Kind: "command", Prompt: "ls", Cwd: cs.work, Session: pb.Schedule_SESSION_OWN},
		{Kind: "claude", Prompt: "x", Cwd: cs.work, Session: pb.Schedule_SESSION_PICKED},
	} {
		if _, err := try(cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: bad}}}); err == nil {
			t.Errorf("%v: saved", bad)
		}
	}

	// Its agent is working: the run waits, then goes in once it's done.
	hook(d, cs.mine, "UserPromptSubmit", map[string]any{"prompt": "my own work", "session_id": "sess-mine"})
	r := cs.runNow(t, s.Id)
	if r.Status != pb.ScheduleRun_STATUS_QUEUED || r.Note != "Its agent is working" {
		t.Fatalf("while working: %v", r)
	}
	hook(d, cs.mine, "Stop", map[string]any{"last_assistant_message": "Done mine.", "session_id": "sess-mine"})
	r = cs.runIs(t, s.Id, pb.ScheduleRun_STATUS_RUNNING)
	if !r.Borrowed || !r.Continued || r.PaneId != cs.mine {
		t.Fatalf("sent: %v", r)
	}
	waitOutput(t, cs.c, cs.mine, "[From the Hammock automation “Inbox”]")
	d.mu.Lock()
	info := d.panes[cs.mine].snapshotInfo()
	from := d.fromAutomation(d.panes[cs.mine])
	d.mu.Unlock()
	if info.ScheduleId != "" || info.ScheduleRunId != "" || from {
		t.Fatalf("your tab marked the automation's: %q %q %v", info.ScheduleId, info.ScheduleRunId, from)
	}
	// Your finished turn (before the run's) raised its usual item; the
	// run's turn ends when the agent next finishes, and your tab is marked
	// as a finished agent's.
	hook(d, cs.mine, "UserPromptSubmit", map[string]any{"prompt": "[From the Hammock automation", "session_id": "sess-mine"})
	hook(d, cs.mine, "Stop", map[string]any{"last_assistant_message": "Inbox is clear.", "session_id": "sess-mine"})
	r = schedOf(d, s.Id).Runs[0]
	if r.Status != pb.ScheduleRun_STATUS_READY || r.Note != "Inbox is clear." {
		t.Fatalf("done: %v", r)
	}
	if !cs.attention(cs.mine) {
		t.Fatal("your tab isn't marked finished")
	}

	// Asking you past the give-up time: the run is only marked; your
	// Needs You item stays (no Esc).
	cs.runNow(t, s.Id)
	cs.runIs(t, s.Id, pb.ScheduleRun_STATUS_RUNNING)
	hook(d, cs.mine, "UserPromptSubmit", map[string]any{"prompt": "x", "session_id": "sess-mine"})
	hook(d, cs.mine, "Notification", map[string]any{"notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	r = schedOf(d, s.Id).Runs[0]
	if r.Status != pb.ScheduleRun_STATUS_WAITING {
		t.Fatalf("waiting: %v", r)
	}
	tick(d, cs.clock, time.UnixMilli(r.WaitingSinceUnixMs).Add(31*time.Minute))
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_GAVE_UP {
		t.Fatalf("gave up: %v", r)
	}
	if !cs.attention(cs.mine) {
		t.Fatal("giving up cleared your Needs You item")
	}
	// Never closed for newer runs (keep 1), nor kept.
	d.mu.Lock()
	_, open := d.tabs[d.panes[cs.mine].tabID()]
	d.mu.Unlock()
	for _, r := range schedOf(d, s.Id).Runs {
		if r.Kept || r.TabClosed {
			t.Fatalf("your tab's run kept or closed: %v", r)
		}
	}
	if !open {
		t.Fatal("your tab was closed")
	}
}

// Something typed in the session's message box (the terminal's, or an
// app's composer): the run waits, and what's typed is never cleared.
func TestContinueWaitsForTyped(t *testing.T) {
	cs := newContinueSetup(t)
	d := cs.d
	s := cs.save(t, &pb.Schedule{Name: "Inbox", Session: pb.Schedule_SESSION_PICKED, SessionId: "sess-mine"})

	os.WriteFile(cs.screen, []byte(strings.Repeat("\n", 60)+"❯ half a thought\n"), 0o644)
	waitFor(t, "half a thought", func() bool { return strings.Contains(d.panes[cs.mine].text(false), "half a thought") })
	r := cs.runNow(t, s.Id)
	if r.Status != pb.ScheduleRun_STATUS_QUEUED || !strings.Contains(r.Note, "typed in its message box") {
		t.Fatalf("typed in the terminal: %v", r)
	}
	tick(d, cs.clock, cs.clock.now().Add(time.Minute))
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_QUEUED {
		t.Fatalf("sent over what's typed: %v", r)
	}
	d.mu.Lock()
	screen := d.panes[cs.mine].text(false)
	d.mu.Unlock()
	if !strings.Contains(screen, "❯ half a thought") || strings.Contains(screen, "Hammock automation") {
		t.Fatalf("what's typed was touched:\n%s", screen)
	}

	// Cleared there, but typed in an app's composer: still waits.
	os.WriteFile(cs.screen, []byte(strings.Repeat("output\n", 30)+"ready\n"), 0o644) // (its box scrolled away)
	waitFor(t, "its box scrolled away", func() bool { return strings.Contains(d.panes[cs.mine].text(false), "ready") })
	if b := d.panes[cs.mine].bottomText(20); strings.Contains(b, "half a thought") {
		t.Fatalf("not scrolled away:\n%s", b)
	}
	if err := d.setComposerDraft(&pb.SetComposerDraft{PaneId: cs.mine, Text: "from my phone"}); err != nil {
		t.Fatal(err)
	}
	tick(d, cs.clock, cs.clock.now().Add(time.Minute))
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_QUEUED {
		t.Fatalf("sent over the composer's draft: %v", r)
	}
	if err := d.setComposerDraft(&pb.SetComposerDraft{PaneId: cs.mine, Text: ""}); err != nil {
		t.Fatal(err)
	}
	tick(d, cs.clock, cs.clock.now().Add(time.Minute))
	cs.runIs(t, s.Id, pb.ScheduleRun_STATUS_RUNNING)
	waitFor(t, "the run typed in", func() bool { return strings.Contains(d.panes[cs.mine].text(false), "Check the inbox") })

	// Waiting longer than its give-up time: given up, nothing sent.
	hook(d, cs.mine, "UserPromptSubmit", map[string]any{"prompt": "x", "session_id": "sess-mine"})
	hook(d, cs.mine, "Stop", map[string]any{"session_id": "sess-mine"})
	os.WriteFile(cs.screen, []byte(strings.Repeat("\n", 60)+"❯ more\n"), 0o644)
	waitFor(t, "more typed", func() bool { return strings.Contains(d.panes[cs.mine].text(false), "❯ more") })
	r = cs.runNow(t, s.Id)
	tick(d, cs.clock, time.UnixMilli(r.WaitingSinceUnixMs).Add(4*time.Hour))
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_GAVE_UP || !strings.Contains(r.Note, "to send it") {
		t.Fatalf("gave up: %v", r)
	}
	d.mu.Lock()
	left := len(d.sched.queued)
	d.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d messages still queued", left)
	}
}

// The session's tab closed: resumed in a new tab of the automation's
// Group with the automation's own options, the run typed in there. That
// tab is the automation's; it isn't opened twice.
func TestContinueResumesClosedSession(t *testing.T) {
	cs := newContinueSetup(t)
	d := cs.d
	s := cs.save(t, &pb.Schedule{Name: "Inbox", Session: pb.Schedule_SESSION_PICKED, SessionId: "sess-gone", SessionName: "Old chat", AllowedTools: []string{"Read"}})
	if s.SessionName != "Old chat" || s.SessionWarning != "" {
		t.Fatalf("saved: %v", s)
	}
	r := cs.runNow(t, s.Id)
	if r.Status != pb.ScheduleRun_STATUS_RUNNING || r.Borrowed || r.SessionId != "sess-gone" {
		t.Fatalf("resumed: %v", r)
	}
	d.mu.Lock()
	tab := d.tabs[r.TabId]
	info := d.panes[r.PaneId].snapshotInfo()
	d.mu.Unlock()
	if tab.GroupId != cs.gid || info.ScheduleId != s.Id || info.ScheduleRunId != r.Id {
		t.Fatalf("its tab: %v %v", tab, info)
	}
	call(t, cs.c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: r.PaneId}}})
	waitOutput(t, cs.c, r.PaneId, "STARTED --resume sess-gone --allowedTools Read")
	hook(d, r.PaneId, "SessionStart", map[string]any{"session_id": "sess-gone"})
	waitOutput(t, cs.c, r.PaneId, "[From the Hammock automation “Inbox”]")
	hook(d, r.PaneId, "UserPromptSubmit", map[string]any{"prompt": "x", "session_id": "sess-gone"})
	hook(d, r.PaneId, "Stop", map[string]any{"session_id": "sess-gone"})
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_READY {
		t.Fatalf("done: %v", r)
	}
	// The next goes into that tab, not a new one.
	r2 := cs.runNow(t, s.Id)
	r2 = cs.runIs(t, s.Id, pb.ScheduleRun_STATUS_RUNNING)
	if r2.TabId != r.TabId || r2.Borrowed {
		t.Fatalf("second run: %v", r2)
	}
	d.mu.Lock()
	runID := d.panes[r.PaneId].snapshotInfo().ScheduleRunId
	d.mu.Unlock()
	if runID != r2.Id {
		t.Fatalf("its tab follows %q, not the new run %q", runID, r2.Id)
	}
}

// This automation's own session: its first run starts one, the rest go
// into it, following its tab to a new session (/clear). A run due while
// the last is still going waits for it.
func TestContinueOwnSession(t *testing.T) {
	cs := newContinueSetup(t)
	d := cs.d
	s := cs.save(t, &pb.Schedule{Name: "Inbox", Session: pb.Schedule_SESSION_OWN, Overlap: pb.Schedule_OVERLAP_STOP_PREVIOUS})
	if s.Overlap != pb.Schedule_OVERLAP_WAIT || s.SessionId != "" {
		t.Fatalf("saved: %v", s)
	}
	r := cs.runNow(t, s.Id)
	if r.Status != pb.ScheduleRun_STATUS_RUNNING || r.Borrowed {
		t.Fatalf("first: %v", r)
	}
	hook(d, r.PaneId, "UserPromptSubmit", map[string]any{"prompt": "x", "session_id": "sess-a"})
	if got := schedOf(d, s.Id).SessionId; got != "sess-a" {
		t.Fatalf("its session: %q", got)
	}
	// Still going: the next waits (Wait), then goes in after.
	r2 := cs.runNow(t, s.Id)
	if r2.Status != pb.ScheduleRun_STATUS_QUEUED || r2.Note != "Waiting for the last run to finish" {
		t.Fatalf("while going: %v", r2)
	}
	hook(d, r.PaneId, "Stop", map[string]any{"session_id": "sess-a"})
	r2 = cs.runIs(t, s.Id, pb.ScheduleRun_STATUS_RUNNING)
	if r2.PaneId != r.PaneId {
		t.Fatalf("not into its session: %v", r2)
	}
	hook(d, r.PaneId, "UserPromptSubmit", map[string]any{"prompt": "x", "session_id": "sess-a"})
	hook(d, r.PaneId, "Stop", map[string]any{"session_id": "sess-a"})
	// /clear in its tab: the session it carries on is the new one.
	hook(d, r.PaneId, "SessionStart", map[string]any{"session_id": "sess-b", "source": "clear"})
	if got := schedOf(d, s.Id); got.SessionId != "sess-b" {
		t.Fatalf("after /clear: %q", got.SessionId)
	}
	// Edits keep it.
	s = schedOf(d, s.Id)
	s.Prompt = "Check it again"
	if got := cs.save(t, s); got.SessionId != "sess-b" {
		t.Fatalf("edited: %q", got.SessionId)
	}
	// Skip: a run due while one's going is skipped.
	s = schedOf(d, s.Id)
	s.Overlap = pb.Schedule_OVERLAP_SKIP
	cs.save(t, s)
	cs.runNow(t, s.Id)
	hook(d, r.PaneId, "UserPromptSubmit", map[string]any{"prompt": "x", "session_id": "sess-b"})
	d.mu.Lock()
	_, err := d.startRun(d.sched.list[s.Id], time.Time{}, false)
	d.mu.Unlock()
	if r := schedOf(d, s.Id).Runs[0]; err != nil || r.Status != pb.ScheduleRun_STATUS_SKIPPED {
		t.Fatalf("skip: %v %v", r, err)
	}
}

// A session open on a linked computer isn't opened here as well.
func TestContinueNoSecondCopy(t *testing.T) {
	cs := newContinueSetup(t)
	d := cs.d
	s := cs.save(t, &pb.Schedule{Name: "Inbox", Session: pb.Schedule_SESSION_PICKED, SessionId: "sess-there"})
	d.mu.Lock()
	d.comp.links["us_studio"] = &pb.StoredLink{Record: &pb.Computer{Name: "Studio"}}
	d.links["test-studio"] = &peerLink{linked: true, tok: &pb.InviteToken{HostUserId: "us_studio"},
		panes: map[string]*pb.Pane{"p9": {Id: "p9", Agent: &pb.Agent{Harness: "claude", SessionId: "sess-there"}}}}
	d.mu.Unlock()
	t.Cleanup(func() { d.mu.Lock(); delete(d.links, "test-studio"); d.mu.Unlock() })
	r := cs.runNow(t, s.Id)
	if r.Status != pb.ScheduleRun_STATUS_FAILED || !strings.Contains(r.Note, "open on Studio") || r.TabId != "" {
		t.Fatalf("opened a second copy: %v", r)
	}
}
