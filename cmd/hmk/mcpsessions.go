package main

// MCP tools over the session finder's index: an agent searches past
// agents' sessions on this Mac and reads the parts that
// matter, to answer "what did we decide about X" or pick up context from an
// earlier session. Its own project's sessions are open to it; other
// folders' ask the person first (a "sessions" grant).

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sessionSearchArgs struct {
	Query      string `json:"query,omitempty" jsonschema:"words to find (each as a prefix, all required) in a session's title, folder or conversation; empty lists the most recent"`
	Everywhere bool   `json:"everywhere,omitempty" jsonschema:"search every folder's sessions, not just this project's (asks the user the first time)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"at most this many (default 10)"`
}

type sessionReadArgs struct {
	ID      string `json:"id" jsonschema:"the session's ID, from session_search"`
	Harness string `json:"harness,omitempty" jsonschema:"claude or codex, if the ID could be either"`
	Query   string `json:"query,omitempty" jsonschema:"show the messages containing these words, with the one before and after each; empty shows its latest messages"`
	Limit   int    `json:"limit,omitempty" jsonschema:"at most this many messages (default 12)"`
}

func (s *mcpServer) registerSessions(server *mcp.Server) {
	call := func(ctx context.Context) caller {
		return func(m *pb.ClientMessage) (*pb.ServerMessage, error) { return s.send(ctx, m) }
	}
	// askedAgain runs f, and if it's out of the agent's scope, asks the
	// person (once; "always in this Group" lasts) and runs it again.
	askedAgain := func(ctx context.Context, what string, f func() (*pb.ServerMessage, error)) (*pb.ServerMessage, error) {
		r, err := f()
		if err == nil || !strings.HasPrefix(err.Error(), "out of scope") {
			return r, err
		}
		if err := s.ensure(ctx, false, "sessions", s.self, what); err != nil {
			return nil, err
		}
		return f()
	}

	mcp.AddTool(server, &mcp.Tool{
		Name: "session_search",
		Description: "Search past coding-agent sessions (Claude Code, Codex and others) on this Mac (titles, folders and what was said), best match first, " +
			"to find an earlier decision or pick up context. Searches this project's sessions; everywhere=true asks the user to look in other folders too. " +
			"Read one with session_read.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a sessionSearchArgs) (*mcp.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		limit := a.Limit
		if limit <= 0 {
			limit = 10
		}
		var ss []*pb.SessionInfo
		_, err := askedAgain(ctx, "search sessions from other folders", func() (*pb.ServerMessage, error) {
			var err error
			ss, err = findSessions(call(ctx), &pb.ListSessions{Query: a.Query, Limit: int32(limit), Everywhere: a.Everywhere})
			return nil, err
		})
		if err != nil {
			return nil, nil, err
		}
		return textResult(formatSessionList(ss, a.Query)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "session_read",
		Description: "Read the parts of a past session that matter, not its whole transcript: with a query, the messages containing those words " +
			"(each with the one before and after); without, its latest messages. Prompts and replies only, clipped.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a sessionReadArgs) (*mcp.CallToolResult, any, error) {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		r, err := askedAgain(ctx, "read a session from another folder", func() (*pb.ServerMessage, error) {
			return s.send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: &pb.SessionExcerpts{
				Id: a.ID, Harness: a.Harness, Query: a.Query, Limit: int32(a.Limit),
			}}})
		})
		if err != nil {
			return nil, nil, err
		}
		return textResult(formatExcerpt(r.GetSessionExcerpt(), a.Query)), nil, nil
	})
}

// formatSessionList is search results as text, for agents.
func formatSessionList(ss []*pb.SessionInfo, query string) string {
	if len(ss) == 0 {
		if query == "" {
			return "No sessions found."
		}
		return fmt.Sprintf("No sessions match %q.", query)
	}
	var b strings.Builder
	for i, s := range ss {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%s\n  id: %s (%s) · %s · %s · updated %s", s.Title, s.Id, s.Harness, homeShort(s.Cwd),
			count(int(s.Messages), "message"), time.UnixMilli(s.UpdatedUnixMs).Format("2006-01-02 15:04"))
		if s.OpenPaneId != "" {
			fmt.Fprintf(&b, " · running in pane %s", s.OpenPaneId)
		}
		if s.Snippet != "" {
			fmt.Fprintf(&b, "\n  %s", s.Snippet)
		}
		b.WriteString("\n")
	}
	return b.String()
}
