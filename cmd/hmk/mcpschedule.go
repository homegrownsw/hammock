package main

// MCP tools for scheduled tasks (an agent run on a schedule, or a command,
// each run a tab in its Group) and show_output (a file or page beside you).
// Creating, changing or deleting a schedule asks the user, in Hammock's words.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type scheduleSaveArgs struct {
	ID            string      `json:"id,omitempty" jsonschema:"to change an existing schedule (from automation_list); empty creates one"`
	Name          string      `json:"name,omitempty" jsonschema:"a short name, e.g. 'Morning briefing' (default: from the prompt)"`
	Kind          string      `json:"kind,omitempty" jsonschema:"an agent ('claude', the default, 'codex', or another agent Hammock knows) or 'command'"`
	Prompt        string      `json:"prompt,omitempty" jsonschema:"the agent's first message (e.g. '/morning-briefing'), or for a command the shell command line. Required when creating"`
	Dir           string      `json:"dir,omitempty" jsonschema:"the folder it runs in (absolute; default: your pane's)"`
	At            string      `json:"at,omitempty" jsonschema:"time of day, HH:MM in local time, e.g. '08:30'"`
	Days          string      `json:"days,omitempty" jsonschema:"with at: 'daily' (default), 'weekdays', 'weekends' or days like 'mon,wed,fri'"`
	Cron          string      `json:"cron,omitempty" jsonschema:"instead of at/days: a 5-field cron expression in local time, e.g. '0 */2 * * *'"`
	Notify        string      `json:"notify,omitempty" jsonschema:"'ready', 'problems', 'never' or 'default' (agents when ready, commands only about problems)"`
	Overlap       string      `json:"overlap,omitempty" jsonschema:"if the last run is still going: 'skip' (default), 'stop' (stop it, start fresh) or 'both'; carrying on a session, 'wait' (default) or 'skip'"`
	Session       string      `json:"session,omitempty" jsonschema:"where runs go: 'new' (a new tab and session each run, the default), 'continue' (carry on this automation's own session) or a session id of the user's (its tab, or resumed if closed). Only agents that can resume"`
	GiveUpMinutes int         `json:"give_up_minutes,omitempty" jsonschema:"stop a run waiting on the user this long (default 240)"`
	KeepRuns      int         `json:"keep_runs,omitempty" jsonschema:"run tabs to keep (default 7)"`
	AllowedTools  []string    `json:"allowed_tools,omitempty" jsonschema:"claude: tools allowed in every run, as --allowedTools rules, e.g. 'Read', 'Bash(gh pr view:*)', 'mcp__slack__read'"`
	CodexSandbox  string      `json:"codex_sandbox,omitempty" jsonschema:"codex: read-only, workspace-write or danger-full-access"`
	CodexApproval string      `json:"codex_approval,omitempty" jsonschema:"codex: untrusted, on-failure, on-request or never"`
	CloseOnOK     bool        `json:"close_on_success,omitempty" jsonschema:"command: close its tab after a clean exit"`
	Once          string      `json:"once,omitempty" jsonschema:"instead of at/cron: run once, at this local date and time ('2026-12-23 17:00')"`
	Triggers      []string    `json:"triggers,omitempty" jsonschema:"run when something happens (replaces its triggers, but not its checks; [] drops them), e.g. github:ci-failed, github:workflow=CI@OWNER/NAME, agent:finished, exit, files:*.go, calendar:10, calendar:free, mac:plugged-in, mac:away=15, mac:wifi-joined=Office, place:arrive=Office, slack:message=#deploys,text=failed, slack:mention, webhook, shortcut; every form is in hmk automation's usage and https://homegrownsw.com/hammock/docs/automations/. What happened goes into the prompt, fenced as untrusted data, or where it says {{event}}"`
	Checks        []checkArgs `json:"checks,omitempty" jsonschema:"command checks: commands run every few minutes in its folder, with no pane, that start a run as they start failing, their output changes, or they pass again (replaces its checks; [] drops them)"`
	URLChecks     []urlArgs   `json:"url_checks,omitempty" jsonschema:"URL checks: URLs fetched every few minutes that start a run as they start failing, their page changes, or it starts containing a text (replaces its URL checks; [] drops them)"`
	OnlyIf        []string    `json:"only_if,omitempty" jsonschema:"conditions a run it starts itself must meet, or it's skipped (replaces them; [] drops them). Each one of: power, away[=MINUTES], wifi=NAME, place=NAME, hours=HH:MM-HH:MM, days=weekdays|mon,wed,…, free (not in a meeting), disk>N%"`
	EventsOnly    bool        `json:"events_only,omitempty" jsonschema:"run only on its triggers (and when run by hand), not at times (the default when creating one with triggers and no at, cron or once)"`
	Cooldown      *int        `json:"cooldown_seconds,omitempty" jsonschema:"runs its triggers start are at least this far apart (default 60; 0: none; at most 2592000, 30 days): events in between wait, and start one run together"`
	Description   string      `json:"description,omitempty" jsonschema:"why, in a sentence, shown to the user with the request"`
}

type checkArgs struct {
	Command string `json:"command" jsonschema:"the shell command line to run"`
	Every   int    `json:"every_minutes,omitempty" jsonschema:"how often (default 5, at least 1)"`
	On      string `json:"on,omitempty" jsonschema:"'fail' (it starts failing: the default), 'change' (its output or exit status changes) or 'recover' (it passes again)"`
	Timeout int    `json:"timeout_seconds,omitempty" jsonschema:"stop it after this long, as failing (default 60)"`
}

type urlArgs struct {
	URL     string `json:"url" jsonschema:"the http or https URL to fetch"`
	Every   int    `json:"every_minutes,omitempty" jsonschema:"how often (default 5, at least 1)"`
	On      string `json:"on,omitempty" jsonschema:"'fail' (not 2xx or unreachable: the default), 'change' (its page or status changes) or 'contains' (its page starts containing text)"`
	Text    string `json:"text,omitempty" jsonschema:"with on 'contains': what to look for (any case)"`
	Timeout int    `json:"timeout_seconds,omitempty" jsonschema:"give up after this long, as failing (default 30)"`
}

type scheduleIDArgs struct {
	ID string `json:"id" jsonschema:"the schedule's id, from automation_list"`
}

type scheduleRunArgs struct {
	ID      string `json:"id" jsonschema:"the schedule's id, from automation_list"`
	Early   bool   `json:"early,omitempty" jsonschema:"run it instead of at its next time, not as well"`
	Trigger bool   `json:"trigger,omitempty" jsonschema:"run it as an event (as hmk trigger does): its cooldown applies, and data goes into the run"`
	Data    string `json:"data,omitempty" jsonschema:"with trigger: what happened, given to the run (up to 64 KB)"`
}

type scheduleDeleteArgs struct {
	ID          string `json:"id" jsonschema:"the schedule's id, from automation_list"`
	Description string `json:"description,omitempty" jsonschema:"why, in a sentence, shown to the user with the request"`
}

type schedulePauseArgs struct {
	ID     string `json:"id" jsonschema:"the schedule's id"`
	Paused bool   `json:"paused" jsonschema:"true pauses it, false resumes it"`
}

type showArgs struct {
	Target string `json:"target" jsonschema:"a file (absolute, or relative to your pane's directory: HTML, Markdown (rendered), an image, a PDF) or an http(s) URL"`
}

type scheduleView struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Prompt    string         `json:"prompt"`
	Dir       string         `json:"dir"`
	When      string         `json:"when"`
	NextRun   string         `json:"next_run,omitempty"`
	Paused    bool           `json:"paused,omitempty"`
	Group     string         `json:"group"`
	Computer  string         `json:"computer,omitempty"`
	Allowed   []string       `json:"allowed_tools,omitempty"`
	Triggers  []triggerView  `json:"triggers,omitempty"`
	OnlyIf    []string       `json:"only_if,omitempty"`
	Error     string         `json:"error,omitempty"`
	LastRuns  []runView      `json:"recent_runs,omitempty"`
	CreatedBy string         `json:"created_by,omitempty"`
	Extra     map[string]any `json:"settings,omitempty"`
}

type triggerView struct {
	Spec  string `json:"spec"`
	Words string `json:"description"`
	Error string `json:"error,omitempty"`
	// When it last looked and fired, or what's holding back what it fired.
	Status    string `json:"status,omitempty"`
	LastCheck string `json:"last_check,omitempty"`
}

type runView struct {
	When    string `json:"when"`
	Trigger string `json:"triggered_by,omitempty"`
	Status  string `json:"status"`
	Note    string `json:"note,omitempty"`
	Tab     string `json:"tab_id,omitempty"`
	Pane    string `json:"pane_id,omitempty"`
	Output  string `json:"output,omitempty"`
}

func viewSchedule(w *world, s *pb.Schedule) scheduleView {
	v := scheduleView{ID: s.Id, Name: s.Name, Kind: s.Kind, Prompt: s.Prompt, Dir: s.Cwd, When: scheduleWhen(s), Paused: s.Paused,
		Group: s.GroupName, Computer: w.scheduleComputer(s.ComputerId), Allowed: s.AllowedTools, Error: s.Error, CreatedBy: s.CreatedBy,
		Extra: map[string]any{"notify": strings.ToLower(strings.TrimPrefix(s.Notify.String(), "NOTIFY_")),
			"overlap": strings.ToLower(strings.TrimPrefix(s.Overlap.String(), "OVERLAP_")), "give_up_minutes": s.GiveUpMinutes, "keep_runs": s.KeepRuns}}
	if w := sessionWords(s); w != "" {
		v.Extra["session"] = w
		if s.SessionId != "" {
			v.Extra["session_id"] = s.SessionId
		}
		if s.SessionWarning != "" {
			v.Extra["session_warning"] = s.SessionWarning
		}
	}
	if s.NextRunUnixMs > 0 {
		v.NextRun = time.UnixMilli(s.NextRunUnixMs).Format(time.RFC3339)
	}
	for _, t := range s.Events {
		v.Triggers = append(v.Triggers, triggerView{Spec: schedtrig.Spec(t), Words: schedtrig.Describe(t, w.triggerName), Error: t.Error,
			Status: schedtrig.Status(t, time.Now()), LastCheck: cmpOr(t.GetCheck().GetLastResult(), t.GetUrl().GetLastResult())})
	}
	if len(s.Events) > 0 {
		v.Extra["cooldown_seconds"] = schedtrig.Cooldown(s)
	}
	for _, c := range s.OnlyIf {
		v.OnlyIf = append(v.OnlyIf, schedtrig.SpecCondition(c))
	}
	for i, r := range s.Runs {
		if i == 5 {
			break
		}
		rv := runView{When: runAt(r).Format(time.RFC3339), Status: runStatusWords[r.Status], Note: r.Note, Output: r.Output, Trigger: r.Trigger}
		if !r.TabClosed {
			rv.Tab, rv.Pane = r.TabId, r.PaneId
		}
		v.LastRuns = append(v.LastRuns, rv)
	}
	return v
}

func (s *mcpServer) registerSchedules(server *mcp.Server) {
	addTool(s, server, &mcp.Tool{
		Name: "automation_list",
		Description: "List the user's automations in Hammock (agents or commands Hammock runs on a schedule or when something happens, each run a new tab), " +
			"with when they run next and how recent runs went.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listArgs) (*mcp.CallToolResult, any, error) {
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		out := []scheduleView{}
		for _, sc := range w.schedules {
			out = append(out, viewSchedule(w, sc))
		}
		return textResult(out), nil, nil
	})

	saveDesc := "Create (no id) or change an automation: the Hammock Server then starts the agent (Claude Code by default, or Codex or another Hammock knows) with the prompt (or runs the command) " +
		"at that time (or when its triggers fire), each run a new tab in the Group, the user told when it's ready. Use this instead of launchd or cron for agent tasks. " +
		"The user is asked to allow it first (this waits for their answer)."
	if hammockconfig.ToolOn(hammockconfig.Values(), "browser") { // hmk show opens a browser pane
		saveDesc += " To show a page from a run, have the prompt end with running `hmk show FILE`."
	}
	addTool(s, server, &mcp.Tool{
		Name:        "automation_save",
		Description: saveDesc,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a scheduleSaveArgs) (*mcp.CallToolResult, any, error) {
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		sc := &pb.Schedule{}
		if a.ID != "" {
			old, err := w.schedule(a.ID)
			if err != nil {
				return nil, nil, err
			}
			sc = cloneSchedule(old)
		} else {
			sc.Kind = "claude"
			if me := w.panes[s.self]; me != nil {
				sc.Cwd = me.Cwd
				sc.GroupId = w.groupOfPane(s.self)
			}
			if a.At == "" && a.Cron == "" && a.Once == "" && len(a.Triggers) == 0 && len(a.Checks) == 0 && len(a.URLChecks) == 0 {
				return nil, nil, errors.New("say when: at (HH:MM, with days), cron, once, or triggers")
			}
		}
		if a.Name != "" {
			sc.Name = a.Name
		}
		if a.Kind != "" {
			sc.Kind = a.Kind
		}
		if a.Prompt != "" {
			sc.Prompt = a.Prompt
		}
		if a.Dir != "" {
			sc.Cwd = expandTilde(a.Dir)
			if !filepath.IsAbs(sc.Cwd) {
				if me := w.panes[s.self]; me != nil && me.Cwd != "" {
					sc.Cwd = filepath.Join(me.Cwd, sc.Cwd)
				}
			}
		}
		if a.At != "" {
			m, err := parseHHMM(a.At)
			if err != nil {
				return nil, nil, err
			}
			sc.MinuteOfDay, sc.Cron = m, ""
			if sc.Weekdays, err = parseDays(cmpOr(a.Days, "daily")); err != nil {
				return nil, nil, err
			}
		} else if a.Days != "" {
			var err error
			if sc.Weekdays, err = parseDays(a.Days); err != nil {
				return nil, nil, err
			}
		}
		if a.Cron != "" {
			sc.Cron = a.Cron
		}
		args := []string{}
		if a.Once != "" {
			args = append(args, "--once", a.Once)
		}
		// Triggers replace its triggers, checks its checks, URL checks its URL checks.
		isCheck := func(t *pb.EventTrigger) bool { return t.GetCheck() != nil }
		isURL := func(t *pb.EventTrigger) bool { return t.GetUrl() != nil }
		if a.Triggers != nil {
			sc.Events = slices.DeleteFunc(sc.Events, func(t *pb.EventTrigger) bool { return !isCheck(t) && !isURL(t) })
			for _, t := range a.Triggers {
				args = append(args, "--trigger", t)
			}
		}
		if a.Checks != nil {
			sc.Events = slices.DeleteFunc(sc.Events, isCheck)
			for _, c := range a.Checks {
				args = append(args, "--check-cmd", c.Command)
				if c.Every != 0 {
					args = append(args, "--check-every", strconv.Itoa(c.Every))
				}
				if c.On != "" {
					args = append(args, "--check-on", c.On)
				}
				if c.Timeout != 0 {
					args = append(args, "--check-timeout", strconv.Itoa(c.Timeout))
				}
			}
		}
		if a.URLChecks != nil {
			sc.Events = slices.DeleteFunc(sc.Events, isURL)
			for _, u := range a.URLChecks {
				args = append(args, "--url", u.URL)
				if u.Every != 0 {
					args = append(args, "--url-every", strconv.Itoa(u.Every))
				}
				if u.On != "" {
					args = append(args, "--url-on", u.On)
				}
				if u.Text != "" {
					args = append(args, "--url-text", u.Text)
				}
				if u.Timeout != 0 {
					args = append(args, "--url-timeout", strconv.Itoa(u.Timeout))
				}
			}
		}
		news := len(a.Triggers) + len(a.Checks) + len(a.URLChecks)
		if (a.Triggers != nil || a.Checks != nil || a.URLChecks != nil) && len(sc.Events) == 0 && news == 0 && sc.Timing == pb.Schedule_TIMING_EVENTS {
			sc.Timing = pb.Schedule_TIMING_REPEAT
		}
		if a.EventsOnly {
			args = append(args, "--events-only")
		}
		if a.OnlyIf != nil {
			args = append(args, "--no-only-if")
			for _, c := range a.OnlyIf {
				args = append(args, "--only-if", c)
			}
		}
		if a.Cooldown != nil {
			args = append(args, "--cooldown", strconv.Itoa(*a.Cooldown))
		}
		if a.At != "" || a.Cron != "" {
			sc.Timing = pb.Schedule_TIMING_REPEAT
		}
		if a.ID == "" && a.At == "" && a.Cron == "" && a.Once == "" && news > 0 {
			args = append(args, "--events-only")
		}
		if a.Notify != "" {
			args = append(args, "--notify", a.Notify)
		}
		switch a.Session {
		case "":
		case "new":
			args = append(args, "--new-session")
		case "continue":
			args = append(args, "--continue")
		default:
			args = append(args, "--session", a.Session)
		}
		if a.Overlap != "" {
			args = append(args, "--overlap", a.Overlap)
		}
		if err := scheduleFlags(w, sc, args, false); err != nil {
			return nil, nil, err
		}
		if a.GiveUpMinutes > 0 {
			sc.GiveUpMinutes = int32(a.GiveUpMinutes)
		}
		if a.KeepRuns > 0 {
			sc.KeepRuns = int32(a.KeepRuns)
		}
		if a.AllowedTools != nil {
			sc.AllowedTools = a.AllowedTools
		}
		if a.CodexSandbox != "" {
			sc.CodexSandbox = a.CodexSandbox
		}
		if a.CodexApproval != "" {
			sc.CodexApproval = a.CodexApproval
		}
		if a.CloseOnOK {
			sc.CloseOnSuccess = true
		}
		ctx, cancel := context.WithTimeout(ctx, grantWait)
		defer cancel()
		r, err := s.send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: sc, Description: a.Description}}})
		if err != nil {
			return nil, nil, err
		}
		return textResult(viewSchedule(w, r.GetScheduleReply().GetSchedule())), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "automation_run",
		Description: "Run an automation now, in a new tab of its Group. Returns the tab id. With early, it runs instead of at its next time (which moves on to the one after), rather than as well. With trigger, it runs as an event, given data (its cooldown applies: it may be queued). One you didn't save yourself asks the user first (waits for their answer).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a scheduleRunArgs) (*mcp.CallToolResult, any, error) {
		if a.Data != "" && !a.Trigger {
			return nil, nil, errors.New("data goes with trigger")
		}
		r, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: a.ID, Early: a.Early, Trigger: a.Trigger, Data: a.Data, StatusReply: a.Trigger}}})
		if err != nil {
			return nil, nil, err
		}
		if tr := r.GetTriggerReply(); tr != nil {
			if tr.Status == pb.TriggerReply_STATUS_QUEUED {
				return textResult(map[string]any{"queued": true, "why": tr.Reason}), nil, nil
			}
			return textResult(map[string]string{"tab_id": tr.TabId}), nil, nil
		}
		id := client.CreatedID(r)
		if a.Trigger && id == "" {
			return textResult(map[string]bool{"queued": true}), nil, nil
		}
		return textResult(map[string]string{"tab_id": id}), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "automation_pause",
		Description: "Pause (paused=true) or resume an automation. One you didn't save yourself asks the user first (waits for their answer).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a schedulePauseArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: a.ID, Paused: a.Paused}}}); err != nil {
			return nil, nil, err
		}
		return textResult(map[string]bool{"paused": a.Paused}), nil, nil
	})

	addTool(s, server, &mcp.Tool{
		Name:        "automation_delete",
		Description: "Delete an automation (asks the user first; waits for their answer).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a scheduleDeleteArgs) (*mcp.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, grantWait)
		defer cancel()
		if _, err := s.send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_DeleteSchedule{DeleteSchedule: &pb.DeleteSchedule{ScheduleId: a.ID, Description: a.Description}}}); err != nil {
			return nil, nil, err
		}
		return textResult(map[string]bool{"deleted": true}), nil, nil
	})

}

// registerShowOutput adds show_output: it opens a browser pane, so it's
// one of the browser's tools (registerKind), there only while that's on.
func (s *mcpServer) registerShowOutput(server *mcp.Server) {
	addTool(s, server, &mcp.Tool{
		Name: "show_output",
		Description: "Show the user a file (HTML, Markdown, an image, a PDF) or a web page in a browser pane beside your pane, " +
			"reusing the one you showed in last time (or one in your tab already on that page), even after you restart. Use it for a report or page you made, e.g. at the end of a scheduled run.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a showArgs) (*mcp.CallToolResult, any, error) {
		t := strings.TrimSpace(a.Target)
		if t == "" {
			return nil, nil, errors.New("target is needed")
		}
		if !strings.Contains(t, "://") {
			t = expandTilde(t)
			if !filepath.IsAbs(t) {
				w, err := s.world(ctx)
				if err != nil {
					return nil, nil, err
				}
				if me := w.panes[s.self]; me != nil && me.Cwd != "" {
					t = filepath.Join(me.Cwd, t)
				} else if wd, err := os.Getwd(); err == nil {
					t = filepath.Join(wd, t)
				}
			}
		}
		r, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ShowOutput{ShowOutput: &pb.ShowOutput{Target: t, PaneId: s.self}}})
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]string{"pane_id": client.CreatedID(r), "showing": t}), nil, nil
	})
}
