package main

// MCP tools for files panes: a folder's tree beside the agent's pane, to
// point the user at files ("the config is here").

import (
	"context"
	"errors"
	"os"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type filesOpenArgs struct {
	Path      string `json:"path,omitempty" jsonschema:"a file or folder to show the user: selected, the folders down to it opened (relative paths are to your pane's directory)"`
	Folder    string `json:"folder,omitempty" jsonschema:"the folder the tree shows (default: the Group's folder, followed as it changes)"`
	SplitPane string `json:"split_pane_id,omitempty" jsonschema:"pane to open it beside, on its left; defaults to your own pane"`
}

func (s *mcpServer) registerFiles(server *mcp.Server) {
	addTool(s, server, &mcp.Tool{
		Name: "files_open",
		Description: "Show the user files in a files pane (a folder's tree, like an editor's file explorer): path is selected and scrolled to. " +
			"Uses the files pane in your tab if there is one, else opens one on your pane's left. " +
			"To show a file's contents, use editor_open instead. You change files on disk yourself; the pane follows. Returns the pane id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a filesOpenArgs) (*mcp.CallToolResult, any, error) {
		w, err := s.world(ctx)
		if err != nil {
			return nil, nil, err
		}
		call := s.callFunc(ctx)
		beside := a.SplitPane
		if beside == "" {
			beside = s.self
		} else if err := s.ensure(ctx, s.inScope(w, beside), "create", beside, "open a files pane beside it"); err != nil {
			return nil, nil, err
		}
		base := ""
		if me := w.panes[s.self]; me != nil {
			base = me.Cwd
		}
		if base == "" {
			base, _ = os.Getwd()
		}
		path, folder := "", ""
		if a.Path != "" {
			path = resolvePath(a.Path, base)
		}
		if a.Folder != "" {
			folder = resolvePath(a.Folder, base)
		}
		bp := w.panes[beside]
		if bp == nil {
			return nil, nil, errors.New("no pane to open it beside: give split_pane_id")
		}
		if id := w.filesInTab(bp.TabId); id != "" && folder == "" {
			if path != "" {
				if _, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_FilesCommand{FilesCommand: &pb.FilesCommand{
					PaneId: id, Action: pb.FilesCommand_ACTION_REVEAL, Path: path,
				}}}); err != nil {
					return nil, nil, err
				}
			}
			return textResult(map[string]any{"pane_id": id}), nil, nil
		}
		res, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
			SplitPaneId: beside, Direction: pb.Split_DIRECTION_HORIZONTAL, Before: true,
			Spec: &pb.PaneSpec{CreatedByPaneId: s.self, Files: &pb.FilesOpen{Root: folder, Reveal: path}},
		}}})
		if err != nil {
			return nil, nil, err
		}
		return textResult(map[string]any{"pane_id": client.CreatedID(res)}), nil, nil
	})
}
