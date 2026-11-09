package main

// hmk schedule: scheduled tasks (an agent with a prompt, or a command, run
// by the daemon on a schedule, each run a tab in its Group), and hmk show
// (a file or URL in a browser pane beside this one).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/agents"
	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// grantWait is how long a save or delete may wait for your yes (an agent's
// ask you; the daemon gives up on a grant after two minutes).
const grantWait = 3 * time.Minute

const scheduleUsage = `usage:
  hmk schedule                             list scheduled tasks
  hmk schedule add [NAME] (--at HH:MM [--days D] | --cron EXPR) [OPTIONS] -- PROMPT|COMMAND
  hmk schedule edit SCHEDULE [OPTIONS] [-- PROMPT|COMMAND]
  hmk schedule show SCHEDULE [--json]      its settings and runs
  hmk schedule run SCHEDULE                run it now (in a new tab)
  hmk schedule pause|resume SCHEDULE
  hmk schedule skip SCHEDULE [--undo]      skip its next run
  hmk schedule rm SCHEDULE
options:
  --claude | --codex | --AGENT | --command  what it runs (default: claude)
  --days daily|weekdays|weekends|mon,tue,…  which days (with --at; default daily)
  --dir DIR  --group GROUP  --name NAME     where it runs and is shown (default: here)
  --notify ready|problems|never|default     when to tell you (default: agents when ready,
                                            commands only about problems)
  --overlap skip|stop|both                  if the last run is still going (default skip)
  --give-up MINUTES  --keep N               stop a run waiting on you; run tabs to keep
  --close-on-success | --keep-open          commands: close the tab after a clean exit
  --allow RULE (repeatable)  --no-allow     Claude: tools allowed in every run (--allowedTools rules)
  --sandbox MODE  --approval POLICY         Codex: --sandbox and --ask-for-approval
  --on COMPUTER                             add it on a linked computer`

func runSchedule(args []string) error {
	ctx := context.Background()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	w, err := fetchWorld(ctx, c)
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "ls" || args[0] == "list" {
		printSchedules(w)
		return nil
	}
	call := func(m *pb.ClientMessage, wait time.Duration) (*pb.ServerMessage, error) {
		ctx, cancel := context.WithTimeout(ctx, wait)
		defer cancel()
		return c.Call(ctx, m)
	}
	needOne := func() (*pb.Schedule, error) {
		if len(args) < 2 {
			return nil, errors.New(scheduleUsage)
		}
		return w.schedule(args[1])
	}
	switch args[0] {
	case "add", "new":
		s := &pb.Schedule{Kind: "claude"}
		rest := args[1:]
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			s.Name, rest = rest[0], rest[1:]
		}
		if err := scheduleFlags(w, s, rest, true); err != nil {
			return err
		}
		if s.Cwd == "" {
			s.Cwd, _ = os.Getwd()
		}
		if s.GroupId == "" && s.ComputerId == "" {
			s.GroupId = w.groupOfPane(os.Getenv("HMK_PANE_ID")) // the Group this runs in
		}
		if os.Getenv("HMK_PANE_ID") != "" {
			fmt.Fprintln(os.Stderr, "Asking for permission in Hammock…")
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}}, grantWait)
		if err != nil {
			return err
		}
		got := r.GetScheduleReply().GetSchedule()
		printfSafe("%s  %s, %s; next run %s\n", got.Id, got.Name, scheduleWhen(got), nextRunWords(got))
		return nil
	case "edit":
		s, err := needOne()
		if err != nil {
			return err
		}
		s = cloneSchedule(s)
		if err := scheduleFlags(w, s, args[2:], false); err != nil {
			return err
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}}, grantWait)
		if err != nil {
			return err
		}
		got := r.GetScheduleReply().GetSchedule()
		printfSafe("%s  %s, %s; next run %s\n", got.Id, got.Name, scheduleWhen(got), nextRunWords(got))
		return nil
	case "show":
		s, err := needOne()
		if err != nil {
			return err
		}
		if slices.Contains(args, "--json") {
			b, _ := json.MarshalIndent(s, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		printSchedule(w, s)
		return nil
	case "run":
		s, err := needOne()
		if err != nil {
			return err
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: s.Id}}}, 30*time.Second)
		if err != nil {
			return err
		}
		fmt.Println(client.CreatedID(r))
		return nil
	case "pause", "resume":
		s, err := needOne()
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: args[0] == "pause"}}}, 10*time.Second)
		return err
	case "skip":
		s, err := needOne()
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_SkipScheduleRun{SkipScheduleRun: &pb.SkipScheduleRun{ScheduleId: s.Id, Skip: !slices.Contains(args, "--undo")}}}, 10*time.Second)
		return err
	case "rm", "delete":
		s, err := needOne()
		if err != nil {
			return err
		}
		_, err = call(&pb.ClientMessage{Msg: &pb.ClientMessage_DeleteSchedule{DeleteSchedule: &pb.DeleteSchedule{ScheduleId: s.Id}}}, grantWait)
		return err
	}
	return errors.New(scheduleUsage)
}

func cloneSchedule(s *pb.Schedule) *pb.Schedule { return proto.CloneOf(s) }

// scheduleFlags applies command-line options to s. Everything after "--"
// is the prompt (or command).
func scheduleFlags(w *world, s *pb.Schedule, args []string, adding bool) error {
	when := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		val := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		var v string
		if ag := agents.Get(strings.TrimPrefix(a, "--")); ag != nil && ag.Schedulable && strings.HasPrefix(a, "--") {
			s.Kind = ag.ID // --claude, --codex, --gemini…
			continue
		}
		switch a {
		case "--":
			s.Prompt = strings.Join(args[i+1:], " ")
			i = len(args)
		case "--command":
			s.Kind = "command"
		case "--at":
			if v, err = val(); err == nil {
				var m int32
				if m, err = parseHHMM(v); err == nil {
					s.MinuteOfDay, s.Cron, when = m, "", true
				}
			}
		case "--days":
			if v, err = val(); err == nil {
				s.Weekdays, err = parseDays(v)
				s.Cron, when = "", true
			}
		case "--cron":
			if v, err = val(); err == nil {
				s.Cron, when = v, true
			}
		case "--dir":
			if v, err = val(); err == nil {
				s.Cwd, err = filepath.Abs(expandTilde(v))
			}
		case "--group":
			if v, err = val(); err == nil {
				s.GroupId, err = w.resolve(v, "group")
			}
		case "--name":
			s.Name, err = val()
		case "--notify":
			if v, err = val(); err == nil {
				n, ok := map[string]pb.Schedule_Notify{"default": pb.Schedule_NOTIFY_DEFAULT, "ready": pb.Schedule_NOTIFY_READY, "problems": pb.Schedule_NOTIFY_PROBLEMS, "never": pb.Schedule_NOTIFY_NEVER}[v]
				if !ok {
					err = fmt.Errorf("--notify %q: ready, problems, never or default", v)
				}
				s.Notify = n
			}
		case "--overlap":
			if v, err = val(); err == nil {
				o, ok := map[string]pb.Schedule_Overlap{"skip": pb.Schedule_OVERLAP_SKIP, "stop": pb.Schedule_OVERLAP_STOP_PREVIOUS, "both": pb.Schedule_OVERLAP_RUN_BOTH}[v]
				if !ok {
					err = fmt.Errorf("--overlap %q: skip, stop or both", v)
				}
				s.Overlap = o
			}
		case "--give-up", "--keep":
			if v, err = val(); err == nil {
				var n int
				if n, err = strconv.Atoi(v); err == nil && n < 0 {
					err = fmt.Errorf("%s can't be negative", a)
				}
				if a == "--keep" {
					s.KeepRuns = int32(n)
				} else {
					s.GiveUpMinutes = int32(n)
				}
			}
		case "--close-on-success", "--keep-open":
			s.CloseOnSuccess = a == "--close-on-success"
		case "--allow":
			if v, err = val(); err == nil && !slices.Contains(s.AllowedTools, v) {
				s.AllowedTools = append(s.AllowedTools, v)
			}
		case "--no-allow":
			s.AllowedTools = nil
		case "--sandbox":
			s.CodexSandbox, err = val()
		case "--approval":
			s.CodexApproval, err = val()
		case "--on":
			if v, err = val(); err == nil {
				if !adding {
					return errors.New("--on: a schedule stays on its computer")
				}
				var pc *pb.Computer
				if pc, err = w.computer(v); err == nil && !pc.ThisComputer {
					s.ComputerId = pc.UserId
				}
			}
		default:
			return fmt.Errorf("unknown option %s\n%s", a, scheduleUsage)
		}
		if err != nil {
			return err
		}
	}
	if adding && !when {
		return errors.New("say when: --at HH:MM [--days …] or --cron EXPR")
	}
	if adding && strings.TrimSpace(s.Prompt) == "" {
		return errors.New("give the prompt (or command) after --")
	}
	return nil
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

func parseHHMM(v string) (int32, error) {
	h, m, ok := strings.Cut(v, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("--at %q: a time like 08:30", v)
	}
	return int32(hh*60 + mm), nil
}

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func parseDays(v string) ([]int32, error) {
	switch strings.ToLower(v) {
	case "daily", "every", "everyday", "all":
		return nil, nil
	case "weekdays":
		return []int32{1, 2, 3, 4, 5}, nil
	case "weekends":
		return []int32{0, 6}, nil
	}
	var out []int32
	for _, d := range strings.Split(strings.ToLower(v), ",") {
		d = strings.TrimSpace(d)
		i := slices.IndexFunc(dayNames, func(n string) bool { return d != "" && strings.HasPrefix(d, n) })
		if i < 0 {
			return nil, fmt.Errorf("--days %q: daily, weekdays, weekends or days like mon,wed,fri", v)
		}
		out = append(out, int32(i))
	}
	return out, nil
}

// schedule finds a schedule by id, unique id prefix or name.
func (w *world) schedule(ref string) (*pb.Schedule, error) {
	var found []*pb.Schedule
	for _, s := range w.schedules {
		if s.Id == ref {
			return s, nil
		}
		if strings.HasPrefix(s.Id, ref) || strings.EqualFold(s.Name, ref) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("no schedule %q (hmk schedule lists them)", ref)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%q matches %d schedules: use its id", ref, len(found))
}

// scheduleComputer names the computer a schedule runs on ("" for this one).
func (w *world) scheduleComputer(id string) string {
	if w.computers == nil {
		return ""
	}
	for _, c := range w.computers.List {
		if c.UserId == id && !c.ThisComputer {
			return c.Name
		}
	}
	return ""
}

// scheduleWhen says when a schedule runs, in words.
func scheduleWhen(s *pb.Schedule) string {
	if s.Cron != "" {
		return "cron " + s.Cron
	}
	at := fmt.Sprintf("%02d:%02d", s.MinuteOfDay/60, s.MinuteOfDay%60)
	switch {
	case len(s.Weekdays) == 0:
		return "every day at " + at
	case slices.Equal(s.Weekdays, []int32{1, 2, 3, 4, 5}):
		return "weekdays at " + at
	case slices.Equal(s.Weekdays, []int32{0, 6}):
		return "weekends at " + at
	}
	var ds []string
	for _, d := range s.Weekdays {
		ds = append(ds, strings.ToUpper(dayNames[d][:1])+dayNames[d][1:])
	}
	return strings.Join(ds, ", ") + " at " + at
}

func nextRunWords(s *pb.Schedule) string {
	switch {
	case s.Paused:
		return "paused"
	case s.NextRunUnixMs == 0:
		return "never"
	}
	t := time.UnixMilli(s.NextRunUnixMs)
	out := when(t)
	if s.SkipUntilUnixMs > s.NextRunUnixMs {
		out += " (skipped)"
	}
	return out
}

// when is a time near now, briefly: "today 08:30", "Thu 08:30", "12 Nov 08:30".
func when(t time.Time) string {
	now := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "today " + t.Format("15:04")
	case t.Sub(now).Abs() < 6*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("2 Jan 15:04")
}

var runStatusWords = map[pb.ScheduleRun_Status]string{
	pb.ScheduleRun_STATUS_RUNNING: "running", pb.ScheduleRun_STATUS_WAITING: "waiting on you",
	pb.ScheduleRun_STATUS_READY: "ready", pb.ScheduleRun_STATUS_FAILED: "failed",
	pb.ScheduleRun_STATUS_GAVE_UP: "gave up", pb.ScheduleRun_STATUS_SKIPPED: "skipped",
	pb.ScheduleRun_STATUS_MISSED: "missed", pb.ScheduleRun_STATUS_STOPPED: "stopped",
}

func printSchedules(w *world) {
	if len(w.schedules) == 0 {
		fmt.Println("No scheduled tasks. Add one: hmk schedule add NAME --at 08:30 --days weekdays -- PROMPT")
		return
	}
	list := slices.Clone(w.schedules)
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	for _, s := range list {
		last := "never run"
		if len(s.Runs) > 0 {
			r := s.Runs[0]
			last = runStatusWords[r.Status] + " " + when(time.UnixMilli(max(r.StartedUnixMs, r.DueUnixMs)))
		}
		where := s.GroupName
		if c := w.scheduleComputer(s.ComputerId); c != "" {
			where += " on " + c
		}
		printfSafe("%s  %-24s %-8s %-22s next %-16s last %s  (%s)\n", s.Id, s.Name, s.Kind, scheduleWhen(s), nextRunWords(s), last, where)
		if s.Error != "" {
			printfSafe("    ! %s\n", s.Error)
		}
	}
}

func printSchedule(w *world, s *pb.Schedule) {
	printfSafe("%s  %s\n", s.Id, s.Name)
	printfSafe("  runs     %s in %s: %s\n", s.Kind, s.Cwd, s.Prompt)
	printfSafe("  when     %s; next %s\n", scheduleWhen(s), nextRunWords(s))
	printfSafe("  group    %s\n", cmpOr(s.GroupName, s.GroupId))
	if c := w.scheduleComputer(s.ComputerId); c != "" {
		printfSafe("  computer %s\n", c)
	}
	if len(s.AllowedTools) > 0 {
		printfSafe("  allowed  %s\n", strings.Join(s.AllowedTools, ", "))
	}
	if s.CodexSandbox != "" || s.CodexApproval != "" {
		printfSafe("  codex    sandbox %s, approval %s\n", cmpOr(s.CodexSandbox, "(yours)"), cmpOr(s.CodexApproval, "(yours)"))
	}
	printfSafe("  made by  %s\n", s.CreatedBy)
	if s.Error != "" {
		printfSafe("  error    %s\n", s.Error)
	}
	if len(s.Runs) == 0 {
		return
	}
	fmt.Println("  runs:")
	for _, r := range s.Runs {
		t := time.UnixMilli(max(r.StartedUnixMs, r.DueUnixMs))
		extra := []string{}
		if r.Manual {
			extra = append(extra, "run now")
		}
		if r.CatchUp {
			extra = append(extra, "caught up")
		}
		if r.EndedUnixMs > 0 && r.StartedUnixMs > 0 {
			extra = append(extra, (time.Duration(r.EndedUnixMs-r.StartedUnixMs) * time.Millisecond).Round(time.Second).String())
		}
		if r.TabId != "" && !r.TabClosed {
			extra = append(extra, "tab "+r.TabId)
		}
		if r.Output != "" {
			extra = append(extra, "showed "+r.Output)
		}
		printfSafe("    %-14s %-14s %s", when(t), runStatusWords[r.Status], r.Note)
		if len(extra) > 0 {
			printfSafe("  [%s]", strings.Join(extra, ", "))
		}
		fmt.Println()
	}
}

func cmpOr(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// runShow is hmk show: a file or URL in a browser pane beside this one
// (the one it showed in last time, or one in this tab already on that page),
// beside another pane (--split), or in a browser pane (--pane).
func runShow(args []string) error {
	const usage = "usage: hmk show FILE|URL [--pane PANE | --split PANE]"
	target, into, beside := "", "", ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case (a == "--pane" || a == "--split") && i+1 < len(args):
			if a == "--pane" {
				into = args[i+1]
			} else {
				beside = args[i+1]
			}
			i++
		case strings.HasPrefix(a, "-") && a != "-":
			return fmt.Errorf("unknown option %s\n%s", a, usage)
		case target != "":
			return errors.New(usage)
		default:
			target = a
		}
	}
	if target == "" {
		return errors.New(usage)
	}
	if into != "" && beside != "" {
		return errors.New("--pane shows it in a browser pane, --split opens one beside a pane: give one")
	}
	if !strings.Contains(target, "://") {
		if abs, err := filepath.Abs(expandTilde(target)); err == nil {
			target = abs
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if into != "" || beside != "" {
		w, err := fetchWorld(ctx, c)
		if err != nil {
			return err
		}
		if into != "" {
			if into, err = w.resolve(into, "pane"); err != nil {
				return err
			}
		} else if beside, err = w.resolve(beside, "pane"); err != nil {
			return err
		}
	}
	// This pane: what's shown beside it, and a scheduled run's output.
	if beside == "" {
		beside = os.Getenv("HMK_PANE_ID")
	}
	if beside == "" && into == "" {
		return errors.New("hmk show runs in a Hammock pane (or name one: --split PANE, or a browser pane with --pane)")
	}
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ShowOutput{ShowOutput: &pb.ShowOutput{Target: target, PaneId: beside, IntoPaneId: into}}})
	if err != nil {
		return err
	}
	fmt.Println(client.CreatedID(r))
	return nil
}
