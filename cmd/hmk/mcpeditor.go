package main

// MCP tools for editor panes: show the user a file, or lines in it, and
// read what they have open and selected. Agents edit files on disk, never
// the pane's buffer (the daemon refuses that): the pane reloads the file,
// or flags a conflict when the user has unsaved edits.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// editorMaxLines is how much of a file editor_read gives without a range.
const editorMaxLines = 2000

type editorOpenArgs struct {
	Path      string `json:"path" jsonschema:"the file (absolute, or relative to your pane's directory); one that doesn't exist yet opens empty"`
	Line      int    `json:"line,omitempty" jsonschema:"first line to select (1-based)"`
	Column    int    `json:"column,omitempty" jsonschema:"column in line (1-based); with no column and no end_line, the whole line is selected"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"last line to select (inclusive)"`
	EndColumn int    `json:"end_column,omitempty" jsonschema:"column in end_line to select up to; by default the end of end_line"`
	Direction string `json:"direction,omitempty" jsonschema:"'right' (default) or 'down': where to split relative to split_pane_id"`
	SplitPane string `json:"split_pane_id,omitempty" jsonschema:"pane to split; defaults to your own pane"`
	NewTab    string `json:"new_tab_name,omitempty" jsonschema:"if set, open it in a new tab with this name instead of splitting"`
	Large     bool   `json:"large,omitempty" jsonschema:"open a file over 5 MB (refused without it)"`
}

type editorReadArgs struct {
	PaneID      string `json:"pane_id" jsonschema:"the editor pane"`
	LineNumbers bool   `json:"with_line_numbers,omitempty" jsonschema:"prefix each line with its number and a tab"`
	StartLine   int    `json:"start_line,omitempty" jsonschema:"only from this line (1-based)"`
	EndLine     int    `json:"end_line,omitempty" jsonschema:"only up to this line (inclusive); without a range at most 2000 lines are returned"`
}

type editorSelectArgs struct {
	PaneID    string `json:"pane_id" jsonschema:"the editor pane"`
	Line      int    `json:"line" jsonschema:"first line to select (1-based)"`
	Column    int    `json:"column,omitempty" jsonschema:"column in line (1-based); with no column and no end_line, the whole line is selected"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"last line to select (inclusive)"`
	EndColumn int    `json:"end_column,omitempty" jsonschema:"column in end_line to select up to; by default the end of end_line"`
}

// mcpRange is the selection an agent asked for: a line on its own is
// highlighted whole, since the point is to show it.
func mcpRange(line, col, endLine, endCol int) (textRange, error) {
	if line < 0 || col < 0 || endLine < 0 || endCol < 0 {
		return textRange{}, errors.New("lines and columns count from 1")
	}
	if line == 0 {
		if col > 0 || endLine > 0 || endCol > 0 {
			return textRange{}, errors.New("line is needed with column or end_line")
		}
		return textRange{}, nil
	}
	if endLine == 0 && col == 0 {
		endLine = line
	}
	if endLine > 0 && endLine < line {
		return textRange{}, fmt.Errorf("end_line %d is before line %d", endLine, line)
	}
	r := textRange{Line: uint32(line), Col: uint32(col), EndLine: uint32(endLine), EndCol: uint32(endCol)}
	if r.EndLine > 0 && r.EndCol == 0 {
		r.EndCol = wholeLine
	}
	return r, nil
}

func (s *mcpServer) callFunc(ctx context.Context) caller {
	return func(m *pb.ClientMessage) (*pb.ServerMessage, error) { return s.call(ctx, m) }
}

// editorPane finds an editor pane the caller may read, asking if it's out of scope.
func (s *mcpServer) editorPane(ctx context.Context, id, what string) (*world, *pb.Pane, error) {
	w, err := s.world(ctx)
	if err != nil {
		return nil, nil, err
	}
	p := w.panes[id]
	if p == nil {
		return nil, nil, fmt.Errorf("no pane %s", id)
	}
	if p.Kind != pb.PaneKind_PANE_KIND_EDITOR {
		return nil, nil, fmt.Errorf("%s isn't an editor pane", id)
	}
	if err := s.ensure(ctx, s.inScope(w, id), "read", id, what); err != nil {
		return nil, nil, err
	}
	return w, p, nil
}

func (s *mcpServer) registerEditor(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "editor_open",
		Description: "Show the user a file in an editor pane beside yours (or in a new tab), with a line or range selected and scrolled into view: " +
			"use it to point them at code you're talking about. An editor pane in that tab already on the file is reused. " +
			"It's for showing, not editing: change files on disk as you usually do, and the pane reloads them (or, if the user has unsaved edits there, offers to). " +
			"Returns the pane id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a editorOpenArgs) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(a.Path) == "" {
			return nil, nil, errors.New("path is needed")
		}
		r, err := mcpRange(a.Line, a.Column, a.EndLine, a.EndColumn)
		if err != nil {
			return nil, nil, err
		}
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		cwd := ""
		if me := w.panes[s.self]; me != nil {
			cwd = me.Cwd
		}
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		path := resolvePath(a.Path, cwd)
		spec := &pb.PaneSpec{Cwd: cwd, CreatedByPaneId: s.self, Editor: &pb.EditorOpen{
			Path: path, Line: r.Line, Column: r.Col, EndLine: r.EndLine, EndColumn: r.EndCol, Large: a.Large, Cwd: cwd,
		}}
		largeErr := func(err error) error { return largeFileError(err, "large: true") }
		if a.NewTab != "" {
			group := w.groupOfPane(s.self)
			if group == "" {
				return nil, nil, errors.New("no Group to open a tab in")
			}
			res, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group, Name: a.NewTab, Pane: spec}}})
			if err != nil {
				return nil, nil, largeErr(err)
			}
			tab := client.CreatedID(res)
			return textResult(map[string]string{"pane_id": tabPane(s.callFunc(ctx), tab), "tab_id": tab, "path": path}), nil, nil
		}
		target := a.SplitPane
		if target == "" {
			target = s.self
		}
		tp := w.panes[target]
		if tp == nil {
			return nil, nil, fmt.Errorf("no pane %s", target)
		}
		if id := w.editorInTab(tp.TabId, path); id != "" && s.inScope(w, id) {
			if r.Line > 0 {
				if _, err := s.call(ctx, selectMsg(id, r)); err != nil {
					return nil, nil, err
				}
			}
			return textResult(map[string]any{"pane_id": id, "path": path, "reused": true}), nil, nil
		}
		if err := s.ensure(ctx, s.inScope(w, target), "create", target, "open "+path+" in an editor pane"); err != nil {
			return nil, nil, err
		}
		dir := pb.Split_DIRECTION_HORIZONTAL
		if a.Direction == "down" {
			dir = pb.Split_DIRECTION_VERTICAL
		}
		res, err := s.call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: target, Direction: dir, Spec: spec}}})
		if err != nil {
			return nil, nil, largeErr(err)
		}
		return textResult(map[string]string{"pane_id": client.CreatedID(res), "path": path}), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "editor_read",
		Description: "Read an editor pane: its file's path, its text as the user sees it (unsaved edits included), whether it's dirty (unsaved) " +
			"or in conflict (the file changed on disk under unsaved edits), and the user's selection (from/to as LINE:COL, and the selected text), " +
			"so you can act on \"look at what I've selected\". The selection is left out once the text has changed since it was made. " +
			"Without start_line/end_line at most 2000 lines come back (showing says which).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a editorReadArgs) (*mcp.CallToolResult, any, error) {
		_, p, err := s.editorPane(ctx, a.PaneID, "read the file open there")
		if err != nil {
			return nil, nil, err
		}
		from, to := a.StartLine, a.EndLine
		if from <= 0 && to <= 0 {
			to = editorMaxLines
		}
		v, err := readEditor(s.callFunc(ctx), p, from, to, a.LineNumbers)
		if err != nil {
			return nil, nil, err
		}
		return textResult(v), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "editor_select",
		Description: "Select a range in an editor pane and scroll it into view, to highlight something for the user. " +
			"It moves their cursor, so use it when you're showing them something, not while they're typing there.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a editorSelectArgs) (*mcp.CallToolResult, any, error) {
		if a.Line <= 0 {
			return nil, nil, errors.New("line is needed (1-based)")
		}
		r, err := mcpRange(a.Line, a.Column, a.EndLine, a.EndColumn)
		if err != nil {
			return nil, nil, err
		}
		if _, _, err := s.editorPane(ctx, a.PaneID, "select lines in it"); err != nil {
			return nil, nil, err
		}
		if _, err := s.call(ctx, selectMsg(a.PaneID, r)); err != nil {
			return nil, nil, err
		}
		return textResult(map[string]string{"pane_id": a.PaneID, "selected": fmt.Sprintf("%d-%d", r.Line, max(r.EndLine, r.Line))}), nil, nil
	})
}
