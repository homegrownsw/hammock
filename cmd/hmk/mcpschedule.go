package main

// MCP tools for scheduled tasks (an agent run on a schedule, or a command,
// each run a tab in its Group) and show_output (a file or page beside you).
// Creating, changing or deleting a schedule asks the user, in Hammock's words.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type scheduleSaveArgs struct {
	ID            string   `json:"id,omitempty" jsonschema:"to change an existing schedule (from schedule_list); empty creates one"`
	Name          string   `json:"name,omitempty" jsonschema:"a short name, e.g. 'Morning briefing' (default: from the prompt)"`
	Kind          string   `json:"kind,omitempty" jsonschema:"an agent ('claude', the default, 'codex', or another agent Hammock knows) or 'command'"`
	Prompt        string   `json:"prompt,omitempty" jsonschema:"the agent's first message (e.g. '/morning-briefing'), or for a command the shell command line. Required when creating"`
	Dir           string   `json:"dir,omitempty" jsonschema:"the folder it runs in (absolute; default: your pane's)"`
	At            string   `json:"at,omitempty" jsonschema:"time of day, HH:MM in local time, e.g. '08:30'"`
	Days          string   `json:"days,omitempty" jsonschema:"with at: 'daily' (default), 'weekdays', 'weekends' or days like 'mon,wed,fri'"`
	Cron          string   `json:"cron,omitempty" jsonschema:"instead of at/days: a 5-field cron expression in local time, e.g. '0 */2 * * *'"`
	Notify        string   `json:"notify,omitempty" jsonschema:"'ready', 'problems', 'never' or 'default' (agents when ready, commands only about problems)"`
	Overlap       string   `json:"overlap,omitempty" jsonschema:"if the last run is still going: 'skip' (default), 'stop' (stop it, start fresh) or 'both'"`
	GiveUpMinutes int      `json:"give_up_minutes,omitempty" jsonschema:"stop a run waiting on the user this long (default 240)"`
	KeepRuns      int      `json:"keep_runs,omitempty" jsonschema:"run tabs to keep (default 7)"`
	AllowedTools  []string `json:"allowed_tools,omitempty" jsonschema:"claude: tools allowed in every run, as --allowedTools rules, e.g. 'Read', 'Bash(gh pr view:*)', 'mcp__slack__read'"`
	CodexSandbox  string   `json:"codex_sandbox,omitempty" jsonschema:"codex: read-only, workspace-write or danger-full-access"`
	CodexApproval string   `json:"codex_approval,omitempty" jsonschema:"codex: untrusted, on-failure, on-request or never"`
	CloseOnOK     bool     `json:"close_on_success,omitempty" jsonschema:"command: close its tab after a clean exit"`
	Description   string   `json:"description,omitempty" jsonschema:"why, in a sentence, shown to the user with the request"`
}

type scheduleIDArgs struct {
	ID string `json:"id" jsonschema:"the schedule's id, from schedule_list"`
}

type scheduleDeleteArgs struct {
	ID          string `json:"id" jsonschema:"the schedule's id, from schedule_list"`
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
	Error     string         `json:"error,omitempty"`
	LastRuns  []runView      `json:"recent_runs,omitempty"`
	CreatedBy string         `json:"created_by,omitempty"`
	Extra     map[string]any `json:"settings,omitempty"`
}

type runView struct {
	When   string `json:"when"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
	Tab    string `json:"tab_id,omitempty"`
	Pane   string `json:"pane_id,omitempty"`
	Output string `json:"output,omitempty"`
}

func viewSchedule(w *world, s *pb.Schedule) scheduleView {
	v := scheduleView{ID: s.Id, Name: s.Name, Kind: s.Kind, Prompt: s.Prompt, Dir: s.Cwd, When: scheduleWhen(s), Paused: s.Paused,
		Group: s.GroupName, Computer: w.scheduleComputer(s.ComputerId), Allowed: s.AllowedTools, Error: s.Error, CreatedBy: s.CreatedBy,
		Extra: map[string]any{"notify": strings.ToLower(strings.TrimPrefix(s.Notify.String(), "NOTIFY_")),
			"overlap": strings.ToLower(strings.TrimPrefix(s.Overlap.String(), "OVERLAP_")), "give_up_minutes": s.GiveUpMinutes, "keep_runs": s.KeepRuns}}
	if s.NextRunUnixMs > 0 {
		v.NextRun = time.UnixMilli(s.NextRunUnixMs).Format(time.RFC3339)
	}
	for i, r := range s.Runs {
		if i == 5 {
			break
		}
		rv := runView{When: time.UnixMilli(max(r.StartedUnixMs, r.DueUnixMs)).Format(time.RFC3339), Status: runStatusWords[r.Status], Note: r.Note, Output: r.Output}
		if !r.TabClosed {
			rv.Tab, rv.Pane = r.TabId, r.PaneId
		}
		v.LastRuns = append(v.LastRuns, rv)
	}
	return v
}

func (s *mcpServer) registerSchedules(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "schedule_list",
		Description: "List the user's scheduled tasks in Hammock (agents or commands Hammock runs on a schedule, each run a new tab), " +
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

	saveDesc := "Create (no id) or change a scheduled task: Hammock's daemon then starts the agent (Claude Code by default, or Codex or another Hammock knows) with the prompt (or runs the command) " +
		"at that time, each run a new tab in the Group, the user told when it's ready. Use this instead of launchd or cron for agent tasks. " +
		"The user is asked to allow it first (this waits for their answer)."
	if hammockconfig.PaneKindOn(hammockconfig.Values(), "browser") { // hmk show opens a browser pane
		saveDesc += " To show a page from a run, have the prompt end with running `hmk show FILE`."
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "schedule_save",
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
			if a.At == "" && a.Cron == "" {
				return nil, nil, errors.New("say when: at (HH:MM, with days) or cron")
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
		if a.Notify != "" {
			args = append(args, "--notify", a.Notify)
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

	mcp.AddTool(server, &mcp.Tool{
		Name:        "schedule_run",
		Description: "Run a scheduled task now, in a new tab of its Group. Returns the tab id. One you didn't save yourself asks the user first (waits for their answer).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a scheduleIDArgs) (*mcp.CallToolResult, any, error) {
		r, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: a.ID}}})
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]string{"tab_id": client.CreatedID(r)}), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "schedule_pause",
		Description: "Pause (paused=true) or resume a scheduled task. One you didn't save yourself asks the user first (waits for their answer).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a schedulePauseArgs) (*mcp.CallToolResult, any, error) {
		if _, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: a.ID, Paused: a.Paused}}}); err != nil {
			return nil, nil, err
		}
		return textResult(map[string]bool{"paused": a.Paused}), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "schedule_delete",
		Description: "Delete a scheduled task (asks the user first; waits for their answer).",
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
