package main

// hmk sessions and hmk resume: the session finder (every Claude Code and
// Codex session on this Mac) from the command line.

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// listSessions asks the daemon, waiting out a first index being built.
func listSessions(call caller, query string, limit int) ([]*pb.SessionInfo, error) {
	return findSessions(call, &pb.ListSessions{Query: query, Limit: int32(limit)})
}

// findSessions is listSessions with the rest of ListSessions (a folder,
// everywhere).
func findSessions(call caller, req *pb.ListSessions) ([]*pb.SessionInfo, error) {
	for wait := 0; ; wait++ {
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: req}})
		if err != nil {
			return nil, err
		}
		l := r.GetSessionList()
		if !l.Indexing || wait >= 40 {
			return l.Sessions, nil
		}
		if wait == 0 {
			fmt.Fprintln(os.Stderr, "Indexing your sessions (the first time only)…")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// runSessions: hmk sessions [QUERY…] [--limit N] [--json] [--folder DIR] [--group GROUP] [--everywhere],
// or hmk sessions read ID [QUERY…]. Run by an agent, it searches its own
// project's sessions; --everywhere (or another --folder, or --group) asks
// you first. --group: the sessions in the folders open in that Group.
func runSessions(call caller, w *world, args []string, on string, onGiven bool) error {
	if len(args) > 0 && args[0] == "read" {
		return runSessionRead(call, args[1:])
	}
	limit, asJSON := 20, false
	req := &pb.ListSessions{}
	var words []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			asJSON = true
		case a == "--everywhere" || a == "--all":
			req.Everywhere = true
		case a == "--folder" && i+1 < len(args):
			req.Folder = absDir(args[i+1])
			i++
		case a == "--group" && i+1 < len(args):
			gid, err := w.resolve(args[i+1], "group")
			if err != nil {
				return err
			}
			if call, err = groupsComputer(call, w, gid, on, onGiven); err != nil {
				return err
			}
			req.GroupId = gid
			i++
		case a == "--limit" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return fmt.Errorf("--limit wants a number")
			}
			limit = n
			i++
		case strings.HasPrefix(a, "--"):
			return fmt.Errorf("usage: hmk sessions [QUERY] [--limit N] [--json] [--folder DIR] [--group GROUP] [--everywhere]\n       hmk sessions read ID [QUERY] [--limit N]")
		default:
			words = append(words, a)
		}
	}
	req.Query, req.Limit = strings.Join(words, " "), int32(limit)
	ss, err := findSessions(call, req)
	if err != nil {
		return err
	}
	if asJSON {
		type out struct {
			Harness     string `json:"harness"`
			ID          string `json:"id"`
			Title       string `json:"title"`
			Cwd         string `json:"cwd"`
			FirstPrompt string `json:"first_prompt"`
			LastPrompt  string `json:"last_prompt"`
			Created     string `json:"created"`
			Updated     string `json:"updated"`
			Messages    int32  `json:"messages"`
			Snippet     string `json:"snippet,omitempty"`
			OpenPane    string `json:"open_pane,omitempty"`
		}
		var o []out
		for _, s := range ss {
			o = append(o, out{s.Harness, s.Id, s.Title, s.Cwd, s.FirstPrompt, s.LastPrompt,
				time.UnixMilli(s.CreatedUnixMs).Format(time.RFC3339), time.UnixMilli(s.UpdatedUnixMs).Format(time.RFC3339),
				s.Messages, s.Snippet, s.OpenPaneId})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(o)
	}
	if len(ss) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}
	// The match in bold in a terminal; as it came (in «») otherwise.
	hl := strings.NewReplacer()
	if st, err := os.Stdout.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		hl = strings.NewReplacer("«", "\x1b[1m", "»", "\x1b[22m")
	}
	for _, s := range ss {
		open := ""
		if s.OpenPaneId != "" {
			open = "  [open in " + s.OpenPaneId + "]"
		}
		printfSafe("%-6s %s  %s%s\n", s.Harness, shortID(s.Id), s.Title, open)
		printfSafe("       %s · %s · %s\n", homeShort(s.Cwd), ago(time.UnixMilli(s.UpdatedUnixMs)), count(int(s.Messages), "message"))
		if s.Snippet != "" {
			fmt.Printf("       %s\n", hl.Replace(termSafe(s.Snippet)))
		}
	}
	fmt.Println("\nResume one with: hmk resume ID [--in claude|codex]")
	return nil
}

// groupsComputer is call for a Group's sessions: its computer's, if it's on
// a linked one and --on wasn't given; an error if --on names another.
func groupsComputer(call caller, w *world, gid, on string, onGiven bool) (caller, error) {
	host := ""
	for _, g := range w.groups {
		if g.Id == gid {
			host = g.HostUserId
		}
	}
	switch {
	case onGiven && host != on:
		where := "this computer"
		if host != "" {
			where = cmp.Or(w.computerName(&pb.Group{HostUserId: host}), "another computer")
		}
		return nil, fmt.Errorf("that Group is on %s: leave out --on, or pick a Group there", where)
	case !onGiven && host != "" && w.computerName(&pb.Group{HostUserId: host}) != "":
		return onComputerCalls(call, host), nil // a linked computer's: its sessions, resumed there
	}
	return call, nil
}

// runResume: hmk resume ID [--in claude|codex] [--group GROUP]
// runResume is hmk resume. on: --on's computer ("" for this one), if
// given (call already asks it); the Group's computer must agree, and with
// no --on, a Group on another computer resumes there.
func runResume(call caller, w *world, args []string, on string, onGiven bool) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return fmt.Errorf("usage: hmk resume ID [--in claude|codex] [--group GROUP] [--on COMPUTER]")
	}
	req := &pb.ResumeSession{Id: args[0]}
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--in" && i+1 < len(args):
			req.TargetHarness = args[i+1]
			i++
		case args[i] == "--group" && i+1 < len(args):
			gid, err := w.resolve(args[i+1], "group")
			if err != nil {
				return err
			}
			req.GroupId = gid
			i++
		default:
			return fmt.Errorf("usage: hmk resume ID [--in claude|codex] [--group GROUP] [--on COMPUTER]")
		}
	}
	if req.GroupId != "" {
		var err error
		if call, err = groupsComputer(call, w, req.GroupId, on, onGiven); err != nil {
			return err
		}
	}
	// A short ID (as hmk sessions prints them): the session it starts.
	if len(req.Id) < 36 {
		ss, err := listSessions(call, "", 100000)
		if err != nil {
			return err
		}
		var match []*pb.SessionInfo
		for _, s := range ss {
			if strings.HasPrefix(s.Id, req.Id) {
				match = append(match, s)
			}
		}
		switch len(match) {
		case 0:
			return fmt.Errorf("no session starts %q", req.Id)
		case 1:
			req.Id, req.Harness = match[0].Id, match[0].Harness
		default:
			return fmt.Errorf("%q could be %d sessions: give more of its ID", req.Id, len(match))
		}
	}
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ResumeSession{ResumeSession: req}})
	if err != nil {
		return err
	}
	res := r.GetSessionResumed()
	if res == nil {
		return errors.New("no answer from the daemon")
	}
	if res.AlreadyOpen {
		printfSafe("Already open in pane %s.\n", res.PaneId)
		return nil
	}
	printfSafe("Resumed in a new tab (pane %s).\n", res.PaneId)
	if res.Report != "" {
		fmt.Println(termSafeText(res.Report))
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func homeShort(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ago is a time as people say it: "3 min ago", "yesterday", "12 Oct".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour && t.Day() == time.Now().Day():
		return t.Format("15:04")
	case d < 48*time.Hour && t.Day() == time.Now().AddDate(0, 0, -1).Day():
		return "yesterday " + t.Format("15:04")
	case t.Year() == time.Now().Year():
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// runSessionRead: hmk sessions read ID [QUERY…] [--limit N] [--json]: the
// parts of a session that matter (the messages matching QUERY, with their
// neighbours; without one, the latest), not its whole transcript.
func runSessionRead(call caller, args []string) error {
	usage := fmt.Errorf("usage: hmk sessions read ID [QUERY] [--limit N] [--json]")
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return usage
	}
	req := &pb.SessionExcerpts{Id: args[0]}
	asJSON := false
	var words []string
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			asJSON = true
		case a == "--limit" && i+1 < len(args):
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return fmt.Errorf("--limit wants a number")
			}
			req.Limit = int32(n)
			i++
		case strings.HasPrefix(a, "--"):
			return usage
		default:
			words = append(words, a)
		}
	}
	req.Query = strings.Join(words, " ")
	if len(req.Id) < 36 { // a short ID, as hmk sessions prints them
		id, err := fullSessionID(call, req.Id)
		if err != nil {
			return err
		}
		req.Id = id
	}
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: req}})
	if err != nil {
		return err
	}
	ex := r.GetSessionExcerpt()
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ex)
	}
	fmt.Print(termSafeText(formatExcerpt(ex, req.Query)))
	return nil
}

// fullSessionID finds the session a short ID starts (among those the
// caller may see).
func fullSessionID(call caller, short string) (string, error) {
	ss, err := findSessions(call, &pb.ListSessions{Limit: 100000})
	if err != nil {
		return "", err
	}
	var match []string
	for _, s := range ss {
		if strings.HasPrefix(s.Id, short) {
			match = append(match, s.Id)
		}
	}
	switch len(match) {
	case 0:
		return "", fmt.Errorf("no session %q (hmk sessions lists them)", short)
	case 1:
		return match[0], nil
	}
	return "", fmt.Errorf("%q could be %d sessions: give more of its ID", short, len(match))
}

// formatExcerpt is a session's excerpt as text, for the CLI and agents.
func formatExcerpt(ex *pb.SessionExcerpt, query string) string {
	var b strings.Builder
	s := ex.GetSession()
	fmt.Fprintf(&b, "%s\n%s session %s · %s · %s · updated %s\n", s.GetTitle(), s.GetHarness(), s.GetId(), homeShort(s.GetCwd()),
		count(int(s.GetMessages()), "message"), time.UnixMilli(s.GetUpdatedUnixMs()).Format("2006-01-02 15:04"))
	switch {
	case query == "":
		fmt.Fprintf(&b, "Its latest %s:\n", count(len(ex.Messages), "message"))
	case ex.Matched == 0:
		fmt.Fprintf(&b, "Nothing matches %q; its latest messages:\n", query)
	default:
		fmt.Fprintf(&b, "%s matching %q, with the messages around them:\n", count(int(ex.Matched), "message"), query)
	}
	last := int32(-2)
	for _, m := range ex.Messages {
		if last >= 0 && m.Index != last+1 {
			b.WriteString("\n   ⋯\n")
		}
		last = m.Index
		who := map[string]string{"user": "You", "assistant": "Agent"}[m.Role]
		fmt.Fprintf(&b, "\n— %s, %s (message %d):\n%s\n", who, time.UnixMilli(m.TimeUnixMs).Format("2006-01-02 15:04"), m.Index+1, m.Text)
	}
	return b.String()
}
