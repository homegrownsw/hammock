package main

// hmk review: the comment threads on diff panes (the daemon's reviews), for
// agents answering what the user said and people at a terminal. Agents
// reply, mark threads addressed and start threads of their own; resolving
// them and sending comments are the user's.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const reviewUsage = `usage: hmk review [list] [--all] [--json]          the reviews for you (or --all), with their open threads
       hmk review show [REVIEW|PANE] [--all-threads] [--json]
       hmk review reply THREAD TEXT...
       hmk review address THREAD [NOTE...]           mark a thread addressed (the user resolves it)
       hmk review comment [REVIEW|PANE] FILE:LINE[-LINE] TEXT...   start a thread on the diff's new side
       hmk review wait [REVIEW|PANE] [--timeout SECONDS]           until the user sends comments or resolves threads`

// reviewView is a review as agents and hmk review --json see it.
type reviewView struct {
	ID      string       `json:"review_id"`
	Title   string       `json:"title"`
	Repo    string       `json:"repo"`
	PaneID  string       `json:"pane_id,omitempty"` // the diff pane showing it ("" closed)
	Target  string       `json:"comments_go_to,omitempty"`
	Threads []threadView `json:"threads"`
}

type threadView struct {
	ID       string        `json:"thread_id"`
	Path     string        `json:"path"`
	Line     uint32        `json:"line,omitempty"` // 0: the whole file
	EndLine  uint32        `json:"end_line,omitempty"`
	Side     string        `json:"side,omitempty"` // "old": on the lines as they were
	State    string        `json:"state"`          // open, addressed or resolved
	Outdated bool          `json:"outdated,omitempty"`
	Quote    string        `json:"lines,omitempty"`
	Comments []commentView `json:"comments"`
}

type commentView struct {
	ID     string `json:"comment_id"`
	Author string `json:"author"`
	Agent  bool   `json:"agent,omitempty"`
	Text   string `json:"text"`
	Status string `json:"status,omitempty"` // a user's: "not sent", "waiting to be sent"
	At     string `json:"at"`
}

func viewReview(rv *pb.Review, all bool) reviewView {
	v := reviewView{ID: rv.Id, Title: rv.Title, Repo: rv.Repo, PaneID: rv.PaneId, Target: rv.TargetPaneId, Threads: []threadView{}}
	for _, t := range rv.Threads {
		if !all && t.State == pb.ReviewThread_STATE_RESOLVED {
			continue
		}
		tv := threadView{ID: t.Id, Path: t.Path, Line: t.Line, Outdated: t.Outdated, Quote: t.Quote,
			State: strings.ToLower(strings.TrimPrefix(t.State.String(), "STATE_"))}
		if t.EndLine > t.Line {
			tv.EndLine = t.EndLine
		}
		if t.Side == pb.ReviewThread_SIDE_OLD {
			tv.Side = "old"
		}
		for _, c := range t.Comments {
			cv := commentView{ID: c.Id, Author: cmpOr(c.Author, "user"), Agent: c.Agent, Text: c.Text,
				At: time.UnixMilli(c.UnixMs).Format(time.RFC3339)}
			switch c.Delivery {
			case pb.ReviewComment_DELIVERY_UNSENT:
				cv.Status = "not sent"
			case pb.ReviewComment_DELIVERY_QUEUED:
				cv.Status = "waiting to be sent"
			}
			tv.Comments = append(tv.Comments, cv)
		}
		v.Threads = append(v.Threads, tv)
	}
	return v
}

func listReviews(call caller) ([]*pb.Review, error) {
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ListReviews{ListReviews: &pb.ListReviews{}}})
	if err != nil {
		return nil, err
	}
	return r.GetReviewList().GetReviews(), nil
}

// reviewsFor is the reviews self's comments come from: those sent to it, or
// on diffs it opened; the newest first.
func reviewsFor(w *world, list []*pb.Review, self string) []*pb.Review {
	var out []*pb.Review
	for _, rv := range list {
		p := w.panes[rv.PaneId]
		if self != "" && (rv.TargetPaneId == self || p != nil && p.CreatedByPaneId == self) {
			out = append(out, rv)
		}
	}
	return out
}

// findReview resolves ref (a review id, a diff pane; "" the caller's
// newest, else the only one) to a review.
func findReview(w *world, call caller, ref, self string) (*pb.Review, error) {
	list, err := listReviews(call)
	if err != nil {
		return nil, err
	}
	if ref != "" {
		for _, rv := range list {
			if rv.Id == ref || rv.PaneId != "" && rv.PaneId == ref {
				return rv, nil
			}
		}
		// A diff pane without comments yet.
		if p := w.panes[ref]; p != nil && p.Kind == pb.PaneKind_PANE_KIND_DIFF {
			r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadReview{ReadReview: &pb.ReadReview{PaneId: ref}}})
			if err != nil {
				return nil, err
			}
			return r.GetReview(), nil
		}
		return nil, fmt.Errorf("no review or diff pane %s", ref)
	}
	if mine := reviewsFor(w, list, self); len(mine) > 0 {
		return mine[0], nil
	}
	if len(list) == 1 {
		return list[0], nil
	}
	// A diff pane opened for this caller, with no comments yet.
	for _, p := range w.panes {
		if p.Kind == pb.PaneKind_PANE_KIND_DIFF && self != "" && (p.GetDiff().GetTargetPaneId() == self || p.CreatedByPaneId == self) {
			return &pb.Review{PaneId: p.Id, Title: p.GetDiff().GetTitle(), Repo: p.GetDiff().GetRepo()}, nil
		}
	}
	if len(list) == 0 {
		return nil, errors.New("there are no reviews: open a diff (hmk diff) for the user to comment on")
	}
	return nil, errors.New("there are several reviews: name one (hmk review list)")
}

// threadReview finds the review a thread is in.
func threadReview(call caller, threadID string) (*pb.Review, error) {
	list, err := listReviews(call)
	if err != nil {
		return nil, err
	}
	for _, rv := range list {
		for _, t := range rv.Threads {
			if t.Id == threadID {
				return rv, nil
			}
		}
	}
	return nil, fmt.Errorf("no thread %s", threadID)
}

func reviewCommand(call caller, rv *pb.Review, m *pb.ReviewCommand) (*pb.ReviewReply, error) {
	if rv.PaneId != "" {
		m.PaneId = rv.PaneId
	} else {
		m.ReviewId = rv.Id
	}
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReviewCommand{ReviewCommand: m}})
	if err != nil {
		return nil, err
	}
	return r.GetReviewReply(), nil
}

// replyThread answers a thread (addressed: and marks it so).
func replyThread(call caller, threadID, text string, addressed bool) (*pb.ReviewReply, error) {
	rv, err := threadReview(call, threadID)
	if err != nil {
		return nil, err
	}
	m := &pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_REPLY, ThreadId: threadID, Text: text}
	if addressed {
		m.Action, m.State = pb.ReviewCommand_ACTION_SET_STATE, pb.ReviewThread_STATE_ADDRESSED
	}
	return reviewCommand(call, rv, m)
}

// waitReview waits until the user sends comments on rv, resolves (or
// reopens) a thread, or closes its pane: what changed.
func waitReview(ctx context.Context, call caller, rv *pb.Review) (string, *pb.Review, error) {
	seen := map[string]bool{}
	states := map[string]pb.ReviewThread_State{}
	note := func(r *pb.Review) {
		for _, t := range r.Threads {
			states[t.Id] = t.State
			for _, c := range t.Comments {
				if c.Delivery == pb.ReviewComment_DELIVERY_SENT {
					seen[c.Id] = true
				}
			}
		}
	}
	note(rv)
	read := &pb.ReadReview{PaneId: rv.PaneId}
	if rv.PaneId == "" {
		read = &pb.ReadReview{ReviewId: rv.Id}
	}
	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(time.Second):
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadReview{ReadReview: read}})
		if err != nil {
			return "the diff was closed", rv, nil
		}
		now := r.GetReview()
		var sent []string
		resolved := 0
		for _, t := range now.Threads {
			if was, ok := states[t.Id]; ok && was != t.State && (t.State == pb.ReviewThread_STATE_RESOLVED || was == pb.ReviewThread_STATE_RESOLVED) {
				resolved++
			}
			for _, c := range t.Comments {
				if c.Delivery == pb.ReviewComment_DELIVERY_SENT && !seen[c.Id] {
					sent = append(sent, c.Id)
				}
			}
		}
		if len(sent) > 0 || resolved > 0 {
			return fmt.Sprintf("%d comments sent, %d threads resolved or reopened", len(sent), resolved), now, nil
		}
	}
}

func runReview(w *world, c *client.Client, call caller, args []string) error {
	usage := errors.New(reviewUsage)
	sub := "list"
	rest := args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		sub, rest = rest[0], rest[1:]
	}
	self := paneEnv("PANE_ID")
	asJSON := hasFlag(rest, "--json")
	rest = dropFlag(rest, "--json")
	print := func(v any, text func()) error {
		if asJSON {
			b, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		text()
		return nil
	}
	switch sub {
	case "list":
		list, err := listReviews(call)
		if err != nil {
			return err
		}
		if !hasFlag(rest, "--all") {
			if mine := reviewsFor(w, list, self); self != "" {
				list = mine
			}
		}
		views := []reviewView{}
		for _, rv := range list {
			views = append(views, viewReview(rv, false))
		}
		return print(views, func() {
			if len(views) == 0 {
				fmt.Println("No reviews.")
			}
			for _, v := range views {
				printReview(v)
			}
		})
	case "show":
		all := hasFlag(rest, "--all-threads")
		rest = dropFlag(rest, "--all-threads")
		ref := ""
		if len(rest) > 0 {
			ref = rest[0]
		}
		rv, err := findReview(w, call, ref, self)
		if err != nil {
			return err
		}
		v := viewReview(rv, all)
		return print(v, func() { printReview(v) })
	case "reply", "address":
		if len(rest) < 1 || sub == "reply" && len(rest) < 2 {
			return usage
		}
		r, err := replyThread(call, rest[0], strings.Join(rest[1:], " "), sub == "address")
		if err != nil {
			return err
		}
		fmt.Println(cmpOr(r.CommentId, rest[0]))
		return nil
	case "comment":
		ref := ""
		if len(rest) >= 3 && !strings.Contains(rest[0], ":") {
			ref, rest = rest[0], rest[1:]
		}
		if len(rest) < 2 {
			return usage
		}
		file, lines, ok := strings.Cut(rest[0], ":")
		if !ok {
			return usage
		}
		from, to, _ := strings.Cut(lines, "-")
		line, err := strconv.ParseUint(from, 10, 32)
		if err != nil {
			return fmt.Errorf("%q isn't LINE or LINE-LINE", lines)
		}
		end := line
		if to != "" {
			if end, err = strconv.ParseUint(to, 10, 32); err != nil || end < line {
				return fmt.Errorf("%q isn't LINE or LINE-LINE", lines)
			}
		}
		rv, err := findReview(w, call, ref, self)
		if err != nil {
			return err
		}
		r, err := reviewCommand(call, rv, &pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_ADD_THREAD, Path: file,
			Line: uint32(line), EndLine: uint32(end), Text: strings.Join(rest[1:], " ")})
		if err != nil {
			return err
		}
		fmt.Println(r.ThreadId)
		return nil
	case "wait":
		timeout := 0
		if v := flagValue(rest, "--timeout"); v != "" {
			timeout, _ = strconv.Atoi(v)
			rest = dropFlag(rest, "--timeout")
		}
		ref := ""
		if len(rest) > 0 {
			ref = rest[0]
		}
		rv, err := findReview(w, call, ref, self)
		if err != nil {
			return err
		}
		ctx := context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()
		}
		what, now, err := waitReview(ctx, call, rv)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, what)
		v := viewReview(now, false)
		return print(v, func() { printReview(v) })
	}
	return usage
}

func printReview(v reviewView) {
	where := "closed"
	if v.PaneID != "" {
		where = "in " + v.PaneID
	}
	fmt.Printf("%s  %s  (%s, %s)\n", v.ID, v.Title, v.Repo, where)
	if len(v.Threads) == 0 {
		fmt.Println("  no open threads")
	}
	for _, t := range v.Threads {
		loc := t.Path
		if t.Line > 0 {
			loc += ":" + strconv.Itoa(int(t.Line))
			if t.EndLine > 0 {
				loc += "-" + strconv.Itoa(int(t.EndLine))
			}
		}
		flags := []string{t.State}
		if t.Outdated {
			flags = append(flags, "outdated")
		}
		if t.Side == "old" {
			flags = append(flags, "old side")
		}
		fmt.Printf("  %s  %s  [%s]\n", t.ID, loc, strings.Join(flags, ", "))
		for _, c := range t.Comments {
			status := ""
			if c.Status != "" {
				status = " (" + c.Status + ")"
			}
			fmt.Printf("    %s%s: %s\n", c.Author, status, strings.ReplaceAll(c.Text, "\n", "\n      "))
		}
	}
}
