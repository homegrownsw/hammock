package daemon

// Command checks (schedtriggers.go): a trigger that runs a command every
// few minutes, in its task's folder, with no pane (the login shell's -c, as
// a command task's run), and fires when it starts failing, its output
// changes, or it passes again. One check of a trigger runs at a time (one
// due meanwhile is skipped), for at most its time limit (then it's stopped,
// with what it started, and counts as failing). Its first check only notes
// how things are, so what's already so never fires. What each trigger last
// saw is kept in its trigState (its output cut to maxCheckKept), across
// restarts.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

var (
	// checkTick is how often checks are looked at to see which are due.
	checkTick = 15 * time.Second
	// checkWakeGrace is how long checks wait after the computer wakes.
	checkWakeGrace = 75 * time.Second
	// maxCheckOutput is how much of a check's output is read (the rest is
	// counted, not kept); maxCheckKept how much is kept for its trigger,
	// and goes into a run.
	maxCheckOutput = 256 << 10
	maxCheckKept   = 8 << 10
)

// maxCheckCommand is the longest command a check may run.
const maxCheckCommand = 1000

// checkState is what a command check last saw (in its trigState).
type checkState struct {
	Started int64  `json:"started"` // when its last check began (ms)
	Seen    bool   `json:"seen"`    // it's had a first check
	Failed  bool   `json:"failed"`
	Code    int    `json:"code"`           // its exit status (-1: timed out, or couldn't start)
	Hash    string `json:"hash,omitempty"` // of its output, all of it read
	Out     string `json:"out,omitempty"`  // the end of its output, cut to maxCheckKept
}

// checkJob is one check to run, as its trigger was when it began.
type checkJob struct {
	schedule, trigger, key string
	command, dir           string
	timeout                time.Duration
}

// watchChecks runs command checks as they're due, until the daemon stops.
func (d *Daemon) watchChecks() {
	wake := make(chan struct{}, 1)
	d.mu.Lock()
	d.sched.checkWake = wake
	d.mu.Unlock()
	t := time.NewTicker(checkTick)
	defer t.Stop()
	prev := time.Now()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		case <-wake:
		}
		now := time.Now()
		d.noteChecksWake(schedNow(), slept(now.Round(0).Sub(prev.Round(0)), now.Sub(prev)) || !schedAwake())
		prev = now
		for _, j := range d.dueChecks(schedNow()) {
			go d.runCheck(j)
		}
		for _, j := range d.dueURLs(schedNow()) { // (schedurl.go)
			go d.runURL(j)
		}
	}
}

// noteChecksWake notes that the computer has just woken, or isn't fully
// awake (woke), so checks wait a while: a Mac's network is often still
// coming back as it wakes, and a check then would say it's failing. Takes
// d.mu.
func (d *Daemon) noteChecksWake(now time.Time, woke bool) {
	if !woke {
		return
	}
	d.mu.Lock()
	d.sched.checksWoke = now
	d.mu.Unlock()
}

// checksHeld: checks wait now, the computer having just woken. Callers
// hold d.mu.
func (d *Daemon) checksHeld(now time.Time) bool {
	w := d.sched.checksWoke
	return !w.IsZero() && now.Sub(w) < checkWakeGrace
}

// checkSlept: a check that began at start (schedNow's, and the monotonic
// clock's monoStart) went on across a sleep: what it found is the network
// going or coming back, not the thing it checks, so it doesn't count (the
// trigger checks again once the wake's grace is over). Callers hold d.mu.
func (d *Daemon) checkSlept(start, monoStart time.Time) bool {
	now := time.Now()
	return slept(now.Round(0).Sub(monoStart.Round(0)), now.Sub(monoStart)) || d.sched.checksWoke.After(start)
}

// checkKey is what a check watches: a change starts it afresh.
func checkKey(s *pb.Schedule, c *pb.CommandCheckTrigger) string {
	return "check:" + s.Cwd + "\x00" + c.Command
}

// dueChecks are the checks to start now (each marked as running), while
// the daemon's state is in and the computer's fully awake. Takes d.mu.
func (d *Daemon) dueChecks(now time.Time) []checkJob {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || !d.sched.ready || !schedAwake() || d.checksHeld(now) {
		return nil
	}
	var jobs []checkJob
	for _, s := range d.sched.list {
		for _, t := range d.eventsOf(s) {
			c := t.GetCheck()
			if c == nil || d.sched.checking[t.Id] {
				continue
			}
			key := checkKey(s, c)
			st := d.trigStateFor(t.Id, key)
			if st.Check == nil {
				st.Check = &checkState{}
			}
			every := time.Duration(schedtrig.CheckEvery(c)) * time.Minute
			if st.Check.Started > 0 && now.Sub(time.UnixMilli(st.Check.Started)) < every-time.Second {
				continue
			}
			st.Check.Started = now.UnixMilli()
			d.saveTrigState()
			d.sched.checking[t.Id] = true
			jobs = append(jobs, checkJob{schedule: s.Id, trigger: t.Id, key: key, command: c.Command,
				dir: filepath.Clean(expandHome(s.Cwd)), timeout: schedtrig.CheckTimeout(c)})
		}
	}
	return jobs
}

// checkOutput keeps the end of a check's output, at least n bytes of it
// (dropped: how many before that), and hashes all of it.
type checkOutput struct {
	buf     bytes.Buffer
	n       int
	dropped int
	sum     hash.Hash
}

func (o *checkOutput) Write(p []byte) (int, error) {
	_, _ = o.sum.Write(p)
	o.buf.Write(p)
	if over := o.buf.Len() - 2*o.n; over > 0 {
		b := o.buf.Bytes()[over:]
		o.dropped += over
		o.buf = *bytes.NewBuffer(append([]byte(nil), b...))
	}
	return len(p), nil
}

// runCheckCommand runs a check's command (tests replace it): its output
// (stdout and stderr together), exit status (-1: it didn't finish), and
// why it failed in words ("" when it passed).
var runCheckCommand = func(ctx context.Context, j checkJob, out *checkOutput) (code int, why string) {
	if st, err := os.Stat(j.dir); err != nil || !st.IsDir() {
		return -1, "there's no folder " + j.dir
	}
	cmd := exec.CommandContext(ctx, loginShell(), "-l", "-c", j.command)
	cmd.Dir = j.dir
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(os.Environ(), "HMK_CHECK=1")
	// Its own process group, ended whole when it runs too long.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if cmd.Process != nil {
		// What it left running (in the background, still holding its
		// output) is ended with it: a check is over when its command is.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return -1, "timed out"
	case err == nil, errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success():
		// (ErrWaitDelay: it passed, but something it started held its
		// output open after it ended.)
		return 0, ""
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return exit.ExitCode(), fmt.Sprintf("exit %d", exit.ExitCode())
	}
	return -1, "couldn't run: " + err.Error()
}

// runCheck runs one check, then fires its trigger if it should.
func (d *Daemon) runCheck(j checkJob) {
	out := &checkOutput{n: maxCheckOutput, sum: sha256.New()}
	start, mono := schedNow(), time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), j.timeout)
	code, why := runCheckCommand(ctx, j, out)
	cancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sched.checking, j.trigger)
	if d.checkSlept(start, mono) {
		if st := d.sched.trig[j.trigger]; st != nil && st.Key == j.key && st.Check != nil {
			st.Check.Started = 0 // (again once the wake's grace is over)
		}
		return
	}
	d.checkDone(j, code, why, out.buf.String(), out.dropped > 0, hex.EncodeToString(out.sum.Sum(nil)))
}

// checkDone notes how a check went, and fires its trigger if it should:
// it started failing, its output or status changed, or it passes again
// (as its trigger says). Callers hold d.mu.
func (d *Daemon) checkDone(j checkJob, code int, why, output string, cutBefore bool, hash string) {
	s := d.sched.list[j.schedule]
	t := triggerOf(s, j.trigger)
	st := d.sched.trig[j.trigger]
	if s == nil || t == nil || t.GetCheck() == nil || st == nil || st.Key != j.key || st.Check == nil {
		return // changed or gone meanwhile
	}
	c := t.GetCheck()
	failed := why != ""
	result := "Passed"
	switch {
	case why == "timed out":
		result = "Timed out"
	case failed:
		result = "Failed (" + why + ")"
	}
	kept := trigTailLines(output, 200)
	if len(kept) > maxCheckKept || cutBefore {
		if len(kept) > maxCheckKept {
			kept = kept[len(kept)-maxCheckKept:]
		}
		for len(kept) > 0 && !validUTF8Start(kept) {
			kept = kept[1:]
		}
		kept = "[… cut]\n" + kept
	}
	prev := *st.Check
	st.Check.Seen, st.Check.Failed, st.Check.Code, st.Check.Hash, st.Check.Out = true, failed, code, hash, kept
	d.saveTrigState()
	// Saved (and the apps told) when how it went changes, else now and then.
	if due := d.noteChecked(t, schedNow()); due || c.LastResult != result {
		c.LastResult = result
		d.saveSchedule(s)
	}
	if !prev.Seen {
		return // the first look: how things are
	}
	var what string
	switch c.On {
	case pb.CommandCheckTrigger_ON_FAIL:
		if failed && !prev.Failed {
			what = "started failing (" + why + ")"
		}
	case pb.CommandCheckTrigger_ON_RECOVER:
		if !failed && prev.Failed {
			what = "passes again"
		}
	case pb.CommandCheckTrigger_ON_CHANGE:
		if hash != prev.Hash || code != prev.Code {
			what = "changed: " + result
		}
	}
	if what == "" || d.eventsOf(s) == nil {
		return
	}
	fields := [][2]string{{"command", c.Command}, {"result", result}, {"status", fmt.Sprint(code)}, {"previous_status", fmt.Sprint(prev.Code)}}
	_, _, _ = d.fireEvent(s, &schedEvent{
		Trigger: t.Id, Kind: "check", Summary: "A command check " + what, Source: "a command's output",
		Fields: fields, Body: kept,
	})
}

// validUTF8Start: s doesn't start partway through a character.
func validUTF8Start(s string) bool {
	return len(s) == 0 || s[0] < 0x80 || s[0] >= 0xC0
}
