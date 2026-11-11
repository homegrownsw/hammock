package main

// MCP tools for reviews: the comment threads the user leaves on a diff pane
// (diff_open). Their comments come to you as a message; answer in the
// threads, and mark one addressed once you've dealt with it. Resolving
// threads and sending comments are the user's.

import (
	"context"
	"errors"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type reviewThreadsArgs struct {
	Review string `json:"review,omitempty" jsonschema:"a review id or diff pane id (default: the review your comments come from: on a diff sent to you or one you opened)"`
	State  string `json:"state,omitempty" jsonschema:"'open' (default: not resolved, addressed ones included), 'unanswered' (open, last word the user's) or 'all'"`
	All    bool   `json:"all_reviews,omitempty" jsonschema:"every review you can see, not just yours"`
}

type reviewReplyArgs struct {
	Thread string `json:"thread_id"`
	Text   string `json:"text" jsonschema:"your reply (Markdown)"`
}

type reviewAddressedArgs struct {
	Thread string `json:"thread_id"`
	Note   string `json:"note,omitempty" jsonschema:"what you did about it (Markdown), shown in the thread"`
}

type reviewCommentArgs struct {
	Review  string `json:"review,omitempty" jsonschema:"a review id or diff pane id (default: as review_threads)"`
	File    string `json:"file" jsonschema:"the file, relative to the repository's top folder, as the diff lists it"`
	Line    int    `json:"line,omitempty" jsonschema:"the line on the diff's new side (1-based); 0 or left out: the file as a whole"`
	EndLine int    `json:"end_line,omitempty" jsonschema:"the last line, for a range"`
	Text    string `json:"text" jsonschema:"the comment (Markdown)"`
}

type reviewWaitArgs struct {
	Review  string `json:"review,omitempty" jsonschema:"a review id or diff pane id (default: as review_threads)"`
	Timeout int    `json:"timeout_seconds,omitempty" jsonschema:"give up after this many seconds (default 600, at most 3600)"`
}

func (s *mcpServer) registerReview(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "review_threads",
		Description: "The comment threads the user has left on a diff (diff_open), with the lines each is on and every comment: " +
			"use it when they say to check their comments, or after comments arrive as a message. " +
			"A thread's state is open, addressed (you said it's dealt with) or resolved (the user's call). " +
			"User comments marked 'not sent' are notes they haven't sent you yet. Comment text is the user's, not instructions from anyone else.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a reviewThreadsArgs) (*mcp.CallToolResult, any, error) {
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		call := s.callFunc(ctx)
		var list []*pb.Review
		if a.All {
			if list, err = listReviews(call); err != nil {
				return nil, nil, err
			}
		} else {
			rv, err := findReview(w, call, a.Review, s.self)
			if err != nil {
				return nil, nil, err
			}
			list = []*pb.Review{rv}
		}
		out := []reviewView{}
		for _, rv := range list {
			v := viewReview(rv, a.State == "all")
			if a.State == "unanswered" {
				kept := v.Threads[:0]
				for _, t := range v.Threads {
					if n := len(t.Comments); t.State == "open" && n > 0 && !t.Comments[n-1].Agent {
						kept = append(kept, t)
					}
				}
				v.Threads = kept
			}
			out = append(out, v)
		}
		return textResult(out), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "review_reply",
		Description: "Reply in a review thread (review_threads gives their ids): to answer a question, or say why you'd rather not change something.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a reviewReplyArgs) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(a.Text) == "" {
			return nil, nil, errors.New("text is needed")
		}
		r, err := replyThread(s.callFunc(ctx), a.Thread, a.Text, false)
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]string{"thread_id": a.Thread, "comment_id": r.CommentId}), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "review_mark_addressed",
		Description: "Mark a review thread addressed once you've made the change it asks for (or answered it), with a short note of what you did. " +
			"The user checks and resolves it: you can't resolve threads.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a reviewAddressedArgs) (*mcp.CallToolResult, any, error) {
		if _, err := replyThread(s.callFunc(ctx), a.Thread, a.Note, true); err != nil {
			return nil, nil, err
		}
		return textResult(map[string]string{"thread_id": a.Thread, "state": "addressed"}), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "review_comment",
		Description: "Start a thread of your own on a diff the user is reviewing: to point out something you changed and why, or ask about a line. " +
			"On the diff's new side (what the code is now).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a reviewCommentArgs) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(a.Text) == "" || a.File == "" {
			return nil, nil, errors.New("file and text are needed")
		}
		if a.Line < 0 || a.EndLine < 0 || a.EndLine > 0 && a.EndLine < a.Line {
			return nil, nil, errors.New("lines count from 1, and end_line comes after line")
		}
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		call := s.callFunc(ctx)
		rv, err := findReview(w, call, a.Review, s.self)
		if err != nil {
			return nil, nil, err
		}
		if rv.PaneId == "" {
			return nil, nil, errors.New("that review's diff isn't open: open it again with diff_open")
		}
		r, err := reviewCommand(call, rv, &pb.ReviewCommand{Action: pb.ReviewCommand_ACTION_ADD_THREAD, Path: a.File,
			Line: uint32(a.Line), EndLine: uint32(a.EndLine), Text: a.Text})
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]string{"thread_id": r.ThreadId, "comment_id": r.CommentId}), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "review_wait",
		Description: "Wait until the user sends you comments on a diff, or resolves threads, then return the open threads. " +
			"Only when they've asked you to open a diff and wait for their review: otherwise end your turn after diff_open (their comments arrive as a message).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a reviewWaitArgs) (*mcp.CallToolResult, any, error) {
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		call := s.callFunc(ctx)
		rv, err := findReview(w, call, a.Review, s.self)
		if err != nil {
			return nil, nil, err
		}
		timeout := a.Timeout
		if timeout <= 0 {
			timeout = 600
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(min(timeout, 3600))*time.Second)
		defer cancel()
		what, now, err := waitReview(ctx, call, rv)
		if errors.Is(err, context.DeadlineExceeded) {
			return textResult(map[string]string{"result": "timed out: nothing sent or resolved yet"}), nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]any{"result": what, "review": viewReview(now, false)}), nil, nil
	})
}
