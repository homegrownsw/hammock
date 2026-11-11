package daemon

// Reviews: comment threads on a diff pane's diff (diff.go). A review is
// kept apart from its pane, keyed by what the diff compares, so closing
// the pane needn't lose it: one whose threads are all resolved goes with
// its pane; one with threads still open stays (two weeks untouched, or
// until what it compares is gone), and opening the same diff again brings
// it back.
//
// Your comments go to the pane's agent (DiffInfo.target_pane_id) typed as a
// message, as the agent view's composer sends one: when you post them
// (send), or all at once later (ACTION_SEND). While the agent is waiting on
// you (a permission prompt, a question), typing would answer it: they're
// held (queued) until it isn't. Agents reply, mark threads addressed and
// start threads of their own with the review tools (cmd/hmk/mcpreview.go);
// only you resolve them.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/gitdiff"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

const (
	reviewRecord = "review"
	// reviewKeep is how long a review with no pane open is kept untouched.
	reviewKeep = 14 * 24 * time.Hour
	// reviewContext is how many lines either side of a thread's are kept,
	// to find them again.
	reviewContext = 3
)

// reviewer is who's acting on a review.
type reviewer struct {
	name  string // "" you; a guest's name; an agent's harness
	agent bool
	pane  string // the agent's pane
	guest bool
}

// reviewKey is what a review belongs to: the repository and what's compared.
func reviewKey(r *gitdiff.Resolved) string {
	s := r.Source
	parts := []string{r.Repo, s.GetKind().String()}
	switch s.GetKind() {
	case pb.DiffSource_KIND_UNCOMMITTED:
		parts = append(parts, r.Branch)
	case pb.DiffSource_KIND_BRANCH:
		parts = append(parts, r.Branch, s.Base, fmt.Sprint(s.WithUncommitted))
	case pb.DiffSource_KIND_COMMIT:
		parts = append(parts, s.Rev)
	case pb.DiffSource_KIND_RANGE:
		parts = append(parts, s.Base, s.Rev, fmt.Sprint(s.MergeBase))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:12])
}

// loadReviews reads the kept reviews. Called once, as the daemon starts.
func (h *diffHub) loadReviews() {
	recs, err := h.d.store.Records(reviewRecord)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, b := range recs {
		rv := &pb.Review{}
		if proto.Unmarshal(b, rv) != nil || rv.Id != id {
			continue
		}
		rv.PaneId = "" // panes claim theirs as they're restored
		h.reviews[id] = rv
	}
	if len(h.reviews) > 0 {
		h.start() // to prune them in time
	}
}

// keyOf finds a review by its key. Callers hold h.mu.
func (h *diffHub) keyOf(key string) *pb.Review {
	for id, k := range h.reviewKeys {
		if k == key {
			return h.reviews[id]
		}
	}
	return nil
}

func (h *diffHub) saveReviewLocked(rv *pb.Review) {
	rv.UpdatedUnixMs = time.Now().UnixMilli()
	h.d.persist(h.d.store.PutRecord(reviewRecord, rv.Id, rv))
}

func (h *diffHub) deleteReviewLocked(id string) {
	delete(h.reviews, id)
	delete(h.reviewKeys, id)
	h.d.persist(h.d.store.DeleteRecord(reviewRecord, id))
}

// attachReviewLocked gives s the review kept for what it compares now
// (none: it gets one with its first comment), letting go of one it had for
// something else. Callers hold h.mu.
func (h *diffHub) attachReviewLocked(s *diffState) {
	if s.resolved == nil {
		return
	}
	key := reviewKey(s.resolved)
	if s.reviewID != "" {
		switch h.reviewKeys[s.reviewID] {
		case key:
			return
		case "":
			// A pane restored with its review (keys aren't kept): it's this.
			h.reviewKeys[s.reviewID] = key
			return
		}
		h.releaseReviewLocked(s.reviewID)
		s.reviewID = ""
	}
	rv := h.keyOf(key)
	if rv == nil {
		// One kept from before the daemon restarted, not yet keyed: it
		// matches by what it compares.
		for _, x := range h.reviews {
			if h.reviewKeys[x.Id] == "" && x.Repo == s.resolved.Repo && proto.Equal(x.Source, s.resolved.Source) {
				h.reviewKeys[x.Id] = key
				rv = x
				break
			}
		}
	}
	if rv == nil {
		return
	}
	s.reviewID = rv.Id
	rv.PaneId = s.paneID
	if s.target == "" {
		s.target = rv.TargetPaneId
	}
}

// releaseReviewLocked is a pane letting go of its review: kept while it
// has threads open, else gone. Callers hold h.mu.
func (h *diffHub) releaseReviewLocked(id string) {
	rv := h.reviews[id]
	if rv == nil {
		return
	}
	open := false
	for _, t := range rv.Threads {
		if t.State != pb.ReviewThread_STATE_RESOLVED {
			open = true
			break
		}
	}
	if !open {
		h.deleteReviewLocked(id)
		return
	}
	rv.PaneId = ""
	h.saveReviewLocked(rv)
}

type reviewCounts struct{ open, unsent, unread, queued uint32 }

func countReview(rv *pb.Review) (c reviewCounts) {
	if rv == nil {
		return
	}
	for _, t := range rv.Threads {
		if t.State != pb.ReviewThread_STATE_RESOLVED {
			c.open++
		}
		for _, m := range t.Comments {
			switch {
			case m.Delivery == pb.ReviewComment_DELIVERY_UNSENT:
				c.unsent++
			case m.Delivery == pb.ReviewComment_DELIVERY_QUEUED:
				c.queued++
			}
			if m.Unread {
				c.unread++
			}
		}
	}
	return
}

// publish puts s's review's counts on its pane, and sends the review to
// the clients showing the pane. Not under h.mu or d.mu.
func (h *diffHub) publish(s *diffState) {
	h.mu.Lock()
	if h.st[s.paneID] != s {
		h.mu.Unlock()
		return
	}
	rv := h.reviews[s.reviewID]
	snap := &pb.Review{PaneId: s.paneID, TargetPaneId: s.target}
	if rv != nil {
		snap = proto.CloneOf(rv)
		snap.TargetPaneId = s.target
	}
	counts := countReview(rv)
	reviewID, target := s.reviewID, s.target
	watchers := make([]*conn, 0, len(s.watchers))
	for c := range s.watchers {
		watchers = append(watchers, c)
	}
	h.mu.Unlock()

	d := h.d
	d.mu.Lock()
	if p := d.panes[s.paneID]; p != nil {
		p.mu.Lock()
		di := proto.CloneOf(p.info.Diff)
		if di == nil {
			di = &pb.DiffInfo{}
		}
		was := proto.CloneOf(di)
		di.ReviewId, di.TargetPaneId = reviewID, target
		di.OpenThreads, di.Unsent, di.Unread, di.Queued = counts.open, counts.unsent, counts.unread, counts.queued
		p.info.Diff = di
		p.mu.Unlock()
		if !proto.Equal(was, di) {
			d.paneChangedIfLive(p)
		}
	}
	d.mu.Unlock()
	for _, c := range watchers {
		c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_Review{Review: proto.CloneOf(snap)}})
	}
}

// readReview answers ReadReview.
func (h *diffHub) readReview(m *pb.ReadReview) (*pb.Review, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m.PaneId != "" {
		s := h.st[m.PaneId]
		if s == nil {
			return nil, fmt.Errorf("%s isn't a diff pane", m.PaneId)
		}
		if rv := h.reviews[s.reviewID]; rv != nil {
			out := proto.CloneOf(rv)
			out.TargetPaneId = s.target
			return out, nil
		}
		return &pb.Review{PaneId: m.PaneId, TargetPaneId: s.target}, nil
	}
	rv := h.reviews[m.ReviewId]
	if rv == nil {
		return nil, fmt.Errorf("no review %s", m.ReviewId)
	}
	return proto.CloneOf(rv), nil
}

// listReviews answers ListReviews: every review, the newest first.
func (h *diffHub) listReviews() []*pb.Review {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*pb.Review, 0, len(h.reviews))
	for _, rv := range h.reviews {
		x := proto.CloneOf(rv)
		if s := h.st[rv.PaneId]; s != nil {
			x.TargetPaneId = s.target
		}
		out = append(out, x)
	}
	slices.SortFunc(out, func(a, b *pb.Review) int { return int(b.UpdatedUnixMs - a.UpdatedUnixMs) })
	return out
}

// reviewOf finds the review (and pane, if one shows it) a command is for.
// Callers hold h.mu.
func (h *diffHub) reviewOf(m *pb.ReviewCommand) (*diffState, *pb.Review, error) {
	if m.PaneId != "" {
		s := h.st[m.PaneId]
		if s == nil {
			return nil, nil, fmt.Errorf("%s isn't a diff pane", m.PaneId)
		}
		return s, h.reviews[s.reviewID], nil
	}
	rv := h.reviews[m.ReviewId]
	if rv == nil {
		return nil, nil, fmt.Errorf("no review %s", m.ReviewId)
	}
	return h.st[rv.PaneId], rv, nil
}

func findThread(rv *pb.Review, id string) *pb.ReviewThread {
	if rv == nil {
		return nil
	}
	for _, t := range rv.Threads {
		if t.Id == id {
			return t
		}
	}
	return nil
}

func findComment(rv *pb.Review, id string) (*pb.ReviewThread, *pb.ReviewComment) {
	if rv == nil {
		return nil, nil
	}
	for _, t := range rv.Threads {
		for _, c := range t.Comments {
			if c.Id == id {
				return t, c
			}
		}
	}
	return nil, nil
}

// reviewCommand answers ReviewCommand, as who. groupID is the pane's Group
// (a new review's).
func (h *diffHub) reviewCommand(m *pb.ReviewCommand, who reviewer, groupID string) (*pb.ReviewReply, error) {
	if who.agent {
		switch m.Action {
		case pb.ReviewCommand_ACTION_ADD_THREAD, pb.ReviewCommand_ACTION_REPLY:
		case pb.ReviewCommand_ACTION_SET_STATE:
			if m.State != pb.ReviewThread_STATE_ADDRESSED {
				return nil, errors.New("agents mark threads addressed; resolving them is the user's")
			}
		default:
			return nil, errors.New("sending, resolving and choosing who comments go to are the user's")
		}
		m.Send = false
	}
	if who.guest && (m.Action == pb.ReviewCommand_ACTION_SEND || m.Action == pb.ReviewCommand_ACTION_SET_TARGET || m.Send) {
		return nil, errors.New("only the Group's owner sends comments to their agents")
	}
	now := time.Now().UnixMilli()
	comment := func(text string) *pb.ReviewComment {
		c := &pb.ReviewComment{Id: newID("rc"), Author: who.name, Agent: who.agent, Text: strings.TrimSpace(text), UnixMs: now}
		if who.agent {
			c.Unread, c.AuthorPaneId = true, who.pane
		} else {
			c.Delivery = pb.ReviewComment_DELIVERY_UNSENT
		}
		return c
	}
	reply := &pb.ReviewReply{}
	var s *diffState
	var toSend []string

	switch m.Action {
	case pb.ReviewCommand_ACTION_ADD_THREAD:
		if strings.TrimSpace(m.Text) == "" {
			return nil, errors.New("the comment is empty")
		}
		t, ps, err := h.newThread(m)
		if err != nil {
			return nil, err
		}
		s = ps
		c := comment(m.Text)
		t.Comments = []*pb.ReviewComment{c}
		t.CreatedUnixMs = now
		h.mu.Lock()
		if h.st[s.paneID] != s {
			h.mu.Unlock()
			return nil, errors.New("the pane has closed")
		}
		rv := h.reviews[s.reviewID]
		if rv == nil {
			r := s.resolved
			rv = &pb.Review{Id: newID("rv"), Repo: r.Repo, Source: proto.CloneOf(r.Source), Title: r.Title, GroupId: groupID, PaneId: s.paneID}
			h.reviews[rv.Id] = rv
			h.reviewKeys[rv.Id] = reviewKey(r)
			s.reviewID = rv.Id
		}
		rv.TargetPaneId = s.target
		rv.Threads = append(rv.Threads, t)
		h.saveReviewLocked(rv)
		reply.ThreadId, reply.CommentId = t.Id, c.Id
		if m.Send {
			toSend = []string{c.Id}
		}
		h.mu.Unlock()

	case pb.ReviewCommand_ACTION_REPLY, pb.ReviewCommand_ACTION_SET_STATE:
		h.mu.Lock()
		ps, rv, err := h.reviewOf(m)
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		s = ps
		t := findThread(rv, m.ThreadId)
		if t == nil {
			h.mu.Unlock()
			return nil, fmt.Errorf("no thread %s", m.ThreadId)
		}
		if strings.TrimSpace(m.Text) != "" {
			c := comment(m.Text)
			t.Comments = append(t.Comments, c)
			reply.CommentId = c.Id
			if m.Send {
				toSend = []string{c.Id}
			}
		} else if m.Action == pb.ReviewCommand_ACTION_REPLY {
			h.mu.Unlock()
			return nil, errors.New("the reply is empty")
		}
		if m.Action == pb.ReviewCommand_ACTION_SET_STATE {
			t.State = m.State
		} else if !who.agent && t.State == pb.ReviewThread_STATE_ADDRESSED {
			t.State = pb.ReviewThread_STATE_OPEN // you replying: not done yet
		}
		reply.ThreadId = t.Id
		h.saveReviewLocked(rv)
		h.mu.Unlock()

	case pb.ReviewCommand_ACTION_DELETE_COMMENT, pb.ReviewCommand_ACTION_EDIT_COMMENT:
		h.mu.Lock()
		ps, rv, err := h.reviewOf(m)
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		s = ps
		t, c := findComment(rv, m.CommentId)
		if c == nil {
			h.mu.Unlock()
			return nil, fmt.Errorf("no comment %s", m.CommentId)
		}
		if c.Agent || c.Delivery != pb.ReviewComment_DELIVERY_UNSENT {
			h.mu.Unlock()
			return nil, errors.New("only your own comments, before they're sent, can be changed")
		}
		if m.Action == pb.ReviewCommand_ACTION_EDIT_COMMENT {
			if strings.TrimSpace(m.Text) == "" {
				h.mu.Unlock()
				return nil, errors.New("the comment is empty")
			}
			c.Text = strings.TrimSpace(m.Text)
		} else {
			t.Comments = slices.DeleteFunc(t.Comments, func(x *pb.ReviewComment) bool { return x == c })
			if len(t.Comments) == 0 {
				rv.Threads = slices.DeleteFunc(rv.Threads, func(x *pb.ReviewThread) bool { return x == t })
			}
		}
		h.saveReviewLocked(rv)
		h.mu.Unlock()

	case pb.ReviewCommand_ACTION_MARK_READ:
		h.mu.Lock()
		ps, rv, err := h.reviewOf(m)
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		s = ps
		changed := false
		if rv != nil {
			for _, t := range rv.Threads {
				for _, c := range t.Comments {
					if c.Unread && (len(m.CommentIds) == 0 || slices.Contains(m.CommentIds, c.Id)) {
						c.Unread, changed = false, true
					}
				}
			}
			if changed {
				h.saveReviewLocked(rv)
			}
		}
		h.mu.Unlock()

	case pb.ReviewCommand_ACTION_SET_TARGET:
		h.mu.Lock()
		ps, rv, err := h.reviewOf(m)
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		if ps == nil {
			h.mu.Unlock()
			return nil, errors.New("open the diff to choose its agent")
		}
		s = ps
		s.target = m.TargetPaneId
		if rv != nil {
			rv.TargetPaneId = s.target
			h.saveReviewLocked(rv)
		}
		h.mu.Unlock()

	case pb.ReviewCommand_ACTION_SEND:
		h.mu.Lock()
		ps, rv, err := h.reviewOf(m)
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		s = ps
		for _, t := range rv.GetThreads() {
			for _, c := range t.Comments {
				if c.Delivery == pb.ReviewComment_DELIVERY_UNSENT && (len(m.CommentIds) == 0 || slices.Contains(m.CommentIds, c.Id)) {
					toSend = append(toSend, c.Id)
				}
			}
		}
		h.mu.Unlock()
		if len(toSend) == 0 {
			return nil, errors.New("there's nothing to send")
		}

	default:
		return nil, fmt.Errorf("unknown review command %v", m.Action)
	}

	var sendErr error
	if len(toSend) > 0 && s != nil {
		reply.Sent, reply.Queued, sendErr = h.send(s, toSend)
	}
	if s != nil {
		h.publish(s)
		h.mu.Lock()
		if rv := h.reviews[s.reviewID]; rv != nil {
			reply.Review = proto.CloneOf(rv)
			reply.Review.TargetPaneId = s.target
		}
		h.mu.Unlock()
	}
	if reply.Review == nil {
		reply.Review, _ = h.readReview(&pb.ReadReview{PaneId: m.PaneId, ReviewId: m.ReviewId})
	}
	if sendErr != nil {
		// The comment is kept, unsent: say why it didn't go.
		return nil, fmt.Errorf("saved, but not sent: %w", sendErr)
	}
	return reply, nil
}

// newThread makes a thread on m's lines, with what they say now (to find
// them again as the code changes). Not under h.mu.
func (h *diffHub) newThread(m *pb.ReviewCommand) (*pb.ReviewThread, *diffState, error) {
	if m.PaneId == "" {
		return nil, nil, errors.New("a new thread is on a diff pane: name it")
	}
	s, err := h.state(m.PaneId)
	if err != nil {
		return nil, nil, err
	}
	if err := h.compute(s, false); err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	r := s.resolved
	var f *pb.DiffFile
	for _, x := range s.files {
		if x.Path == m.Path {
			f = proto.CloneOf(x)
		}
	}
	h.mu.Unlock()
	if f == nil || r == nil {
		return nil, nil, fmt.Errorf("%s isn't in this diff", m.Path)
	}
	t := &pb.ReviewThread{Id: newID("rt"), Path: f.Path, Side: m.Side}
	if m.Line == 0 {
		return t, s, nil // the file as a whole
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ft, err := gitdiff.FileText(ctx, r, f)
	if err != nil {
		return nil, nil, err
	}
	text := ft.NewText
	if m.Side == pb.ReviewThread_SIDE_OLD {
		text = ft.OldText
	}
	lines := splitLines(text)
	end := max(m.EndLine, m.Line)
	if int(end) > len(lines) {
		return nil, nil, fmt.Errorf("%s has %d lines on that side, not %d", f.Path, len(lines), end)
	}
	t.Line, t.EndLine = m.Line, end
	t.Quote = strings.Join(lines[m.Line-1:end], "\n")
	t.Before = strings.Join(lines[max(int(m.Line)-1-reviewContext, 0):m.Line-1], "\n")
	t.After = strings.Join(lines[end:min(int(end)+reviewContext, len(lines))], "\n")
	return t, s, nil
}

// splitLines is text's lines, without their endings.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
}

// send delivers comments to s's agent as one message, or holds them while
// it's waiting on you. Not under h.mu or d.mu.
func (h *diffHub) send(s *diffState, ids []string) (sent, queued uint32, err error) {
	h.mu.Lock()
	if h.sending[s.paneID] {
		h.mu.Unlock()
		return 0, 0, errors.New("comments are on their way already: try again in a moment")
	}
	target := s.target
	rv := h.reviews[s.reviewID]
	if rv == nil {
		h.mu.Unlock()
		return 0, 0, errors.New("there's nothing to send")
	}
	if target == "" {
		h.mu.Unlock()
		return 0, 0, errors.New("choose an agent to send comments to")
	}
	h.sending[s.paneID] = true
	snap := proto.CloneOf(rv)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.sending, s.paneID)
		h.mu.Unlock()
	}()

	d := h.d
	d.mu.Lock()
	p := d.panes[target]
	var state pb.AgentState
	if p != nil {
		p.mu.Lock()
		if a := p.info.Agent; a != nil {
			state = a.State
		}
		p.mu.Unlock()
	}
	d.mu.Unlock()
	mark := func(to pb.ReviewComment_Delivery) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if rv := h.reviews[snap.Id]; rv != nil {
			for _, t := range rv.Threads {
				for _, c := range t.Comments {
					if slices.Contains(ids, c.Id) {
						c.Delivery = to
					}
				}
			}
			h.saveReviewLocked(rv)
		}
	}
	switch state {
	case pb.AgentState_AGENT_STATE_UNSPECIFIED, pb.AgentState_AGENT_STATE_EXITED:
		if p == nil {
			return 0, 0, errors.New("the agent comments go to has closed: choose another")
		}
		return 0, 0, errors.New("there's no agent running in the pane comments go to")
	case pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		// Typing now would answer what it's asking you.
		mark(pb.ReviewComment_DELIVERY_QUEUED)
		return 0, uint32(len(ids)), nil
	}
	if err := typeToAgent(p, nil, reviewMessage(snap, ids), nil); err != nil {
		return 0, 0, err
	}
	mark(pb.ReviewComment_DELIVERY_SENT)
	return uint32(len(ids)), 0, nil
}

// reviewQuoteWidth is how much of each quoted line an agent's message
// carries: a minified file's one line can be many kilobytes.
const reviewQuoteWidth = 200

// clip cuts s to n characters (runes), with an ellipsis if it was longer.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// reviewMessage is the message an agent gets: each comment with where it
// is and the lines it's about, and how to answer in the threads.
func reviewMessage(rv *pb.Review, ids []string) string {
	var b strings.Builder
	where := filepath.Base(rv.Repo)
	fmt.Fprintf(&b, "Review comments on %s (%s):\n", cmpOr(rv.Title, "the diff"), where)
	n := 0
	for _, t := range rv.Threads {
		for i, c := range t.Comments {
			if !slices.Contains(ids, c.Id) {
				continue
			}
			n++
			loc := t.Path
			switch {
			case t.Line > 0 && t.EndLine > t.Line:
				loc += fmt.Sprintf(":%d-%d", t.Line, t.EndLine)
			case t.Line > 0:
				loc += fmt.Sprintf(":%d", t.Line)
			}
			if t.Side == pb.ReviewThread_SIDE_OLD {
				loc += " (before the change)"
			}
			if t.Outdated {
				loc += " (outdated: the lines have changed since)"
			}
			kind := ""
			if i > 0 {
				kind = "reply "
			}
			fmt.Fprintf(&b, "\n%d. %s%s [thread %s]\n", n, kind, loc, t.Id)
			if i == 0 && t.Quote != "" {
				q := strings.Split(t.Quote, "\n")
				if len(q) > 8 {
					q = append(q[:8], "…")
				}
				for _, l := range q {
					fmt.Fprintf(&b, "   > %s\n", clip(l, reviewQuoteWidth))
				}
			}
			for _, l := range strings.Split(c.Text, "\n") {
				fmt.Fprintf(&b, "   %s\n", l)
			}
		}
	}
	b.WriteString("\nAnswer in each thread: reply (review_reply), or once it's dealt with mark it addressed (review_mark_addressed); " +
		"review_threads lists them. Without Hammock's tools: hmk review reply|address THREAD TEXT.")
	return b.String()
}

// flushQueued sends comments held while their agent was waiting on you,
// once it isn't. From diffHub.run.
func (h *diffHub) flushQueued() {
	h.mu.Lock()
	type due struct {
		s   *diffState
		ids []string
	}
	var list []due
	for _, s := range h.st {
		rv := h.reviews[s.reviewID]
		if rv == nil || s.target == "" || h.sending[s.paneID] {
			continue
		}
		var ids []string
		for _, t := range rv.Threads {
			for _, c := range t.Comments {
				if c.Delivery == pb.ReviewComment_DELIVERY_QUEUED {
					ids = append(ids, c.Id)
				}
			}
		}
		if len(ids) > 0 {
			list = append(list, due{s, ids})
		}
	}
	h.mu.Unlock()
	for _, x := range list {
		h.d.mu.Lock()
		p := h.d.panes[x.s.target]
		waiting := true
		if p != nil {
			p.mu.Lock()
			waiting = p.info.Agent != nil && p.info.Agent.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT
			p.mu.Unlock()
		}
		h.d.mu.Unlock()
		if waiting {
			continue
		}
		go func() {
			if _, _, err := h.send(x.s, x.ids); err != nil {
				h.d.log.Warn("review comments held for the agent couldn't be sent", "pane", x.s.paneID, "err", err)
			}
			h.publish(x.s)
		}()
	}
}

// reanchor finds each of s's threads' lines again in the diff as it is
// now: where they've moved to, or outdated if they've changed. Not under
// h.mu or d.mu.
func (h *diffHub) reanchor(s *diffState) {
	h.mu.Lock()
	rv := h.reviews[s.reviewID]
	if rv == nil || s.resolved == nil {
		h.mu.Unlock()
		return
	}
	r := s.resolved
	files := map[string]*pb.DiffFile{}
	for _, f := range s.files {
		files[f.Path] = proto.CloneOf(f)
	}
	threads := make([]*pb.ReviewThread, 0, len(rv.Threads))
	for _, t := range rv.Threads {
		threads = append(threads, proto.CloneOf(t))
	}
	rvID := rv.Id
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	texts := map[string]*pb.DiffFileText{}
	for _, t := range threads {
		f := files[t.Path]
		if f == nil {
			t.Outdated = true
			continue
		}
		if t.Line == 0 || t.Quote == "" {
			t.Outdated = false
			continue
		}
		ft := texts[t.Path]
		if ft == nil {
			x, err := gitdiff.FileText(ctx, r, f)
			if err != nil {
				continue
			}
			ft, texts[t.Path] = x, x
		}
		text := ft.NewText
		if t.Side == pb.ReviewThread_SIDE_OLD {
			text = ft.OldText
		}
		if at := findLines(splitLines(text), t); at > 0 {
			n := t.EndLine - t.Line
			t.Line, t.EndLine, t.Outdated = uint32(at), uint32(at)+n, false
		} else {
			t.Outdated = true
		}
	}

	h.mu.Lock()
	if rv := h.reviews[rvID]; rv != nil {
		changed := false
		for _, n := range threads {
			if t := findThread(rv, n.Id); t != nil && (t.Line != n.Line || t.EndLine != n.EndLine || t.Outdated != n.Outdated) {
				t.Line, t.EndLine, t.Outdated = n.Line, n.EndLine, n.Outdated
				changed = true
			}
		}
		if changed {
			h.saveReviewLocked(rv)
		}
	}
	h.mu.Unlock()
}

// findLines finds t's lines in lines (1-based; 0 if they aren't there): of
// the places they are, the one whose surroundings match best, then the
// nearest to where they were.
func findLines(lines []string, t *pb.ReviewThread) int {
	quote := strings.Split(t.Quote, "\n")
	before, after := splitLines(t.Before), splitLines(t.After)
	best, bestScore, bestDist := 0, -1, 0
	for i := 0; i+len(quote) <= len(lines); i++ {
		if !slices.Equal(lines[i:i+len(quote)], quote) {
			continue
		}
		score := 0
		for k := 1; k <= len(before) && i-k >= 0; k++ {
			if lines[i-k] == before[len(before)-k] {
				score++
			}
		}
		for k := 0; k < len(after) && i+len(quote)+k < len(lines); k++ {
			if lines[i+len(quote)+k] == after[k] {
				score++
			}
		}
		dist := i + 1 - int(t.Line)
		if dist < 0 {
			dist = -dist
		}
		if score > bestScore || score == bestScore && dist < bestDist {
			best, bestScore, bestDist = i+1, score, dist
		}
	}
	return best
}

// pruneReviews lets go of reviews no pane has open that have gone two
// weeks untouched, or whose diff is gone (its commits, its branch).
func (h *diffHub) pruneReviews() {
	h.mu.Lock()
	var check []*pb.Review
	for id, rv := range h.reviews {
		if rv.PaneId != "" && h.st[rv.PaneId] != nil {
			continue
		}
		if time.Since(time.UnixMilli(rv.UpdatedUnixMs)) > reviewKeep {
			h.deleteReviewLocked(id)
			continue
		}
		check = append(check, proto.CloneOf(rv))
	}
	h.mu.Unlock()
	for _, rv := range check {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := gitdiff.Resolve(ctx, rv.Repo, rv.Source)
		cancel()
		if err == nil {
			continue
		}
		h.mu.Lock()
		if x := h.reviews[rv.Id]; x != nil && x.PaneId == "" {
			h.deleteReviewLocked(rv.Id)
		}
		h.mu.Unlock()
	}
}

// reviewGroup is the Group a review's diff was in ("" no such review).
func (h *diffHub) reviewGroup(id string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rv := h.reviews[id]; rv != nil {
		return rv.GroupId
	}
	return ""
}
