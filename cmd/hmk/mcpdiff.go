package main

// MCP tools for diff panes: show the user a git diff to review, in a pane
// like a pull request's Files Changed. When they ask to see a diff, review
// changes, or look over your work, open one with diff_open, then end your
// turn: don't wait for them.

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/homegrownsw/hammock/internal/gitdiff"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type diffOpenArgs struct {
	Source          string `json:"source,omitempty" jsonschema:"what to compare: 'branch' (this branch against its base, a pull request's diff), 'uncommitted', a commit ('HEAD', 'a1b2c3d'), or a range as git writes them ('main..HEAD', 'main...feature'); empty: the branch with uncommitted changes, or on the default branch what's uncommitted"`
	Base            string `json:"base,omitempty" jsonschema:"for 'branch': the branch it's compared with (default: the repository's default branch)"`
	WithUncommitted *bool  `json:"with_uncommitted,omitempty" jsonschema:"for 'branch': include changes not yet committed (default true)"`
	Repo            string `json:"repo,omitempty" jsonschema:"a folder in the repository (default: your pane's directory)"`
	Style           string `json:"style,omitempty" jsonschema:"'split' or 'unified' (default: the user's setting)"`
	SplitPane       string `json:"split_pane_id,omitempty" jsonschema:"open it split beside this pane instead of in a new tab after yours"`
	Name            string `json:"tab_name,omitempty" jsonschema:"the new tab's name (default 'Review')"`
}

func (s *mcpServer) registerDiff(server *mcp.Server) {
	addTool(s, server, &mcp.Tool{
		Name: "diff_open",
		Description: "Show the user a git diff to review, in a diff pane laid out like a pull request's Files Changed (a new tab just after yours, unless split_pane_id). " +
			"Use it when they ask to see the diff, review your changes or look over your work before a pull request. " +
			"With no source it shows what a pull request from this branch would (uncommitted changes included), or on the default branch what's uncommitted. " +
			"It returns at once: tell them it's open and end your turn rather than waiting. Returns the pane id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a diffOpenArgs) (*mcp.CallToolResult, any, error) {
		plan := &diffPlan{open: &pb.DiffOpen{Repo: a.Repo, View: &pb.DiffView{Style: a.Style}}, name: a.Name, beside: a.SplitPane}
		if a.Style != "" && a.Style != "split" && a.Style != "unified" {
			return nil, nil, errors.New("style is 'split' or 'unified'")
		}
		switch src := strings.TrimSpace(a.Source); src {
		case "branch":
			with := a.WithUncommitted == nil || *a.WithUncommitted
			plan.open.Source = &pb.DiffSource{Kind: pb.DiffSource_KIND_BRANCH, Base: a.Base, WithUncommitted: with}
		case "uncommitted":
			plan.open.Source = &pb.DiffSource{Kind: pb.DiffSource_KIND_UNCOMMITTED}
		default:
			if a.Base != "" {
				return nil, nil, errors.New("base goes with source 'branch'")
			}
			plan.open.Source = gitdiff.ParseRev(src)
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
		if plan.beside != "" {
			if err := s.ensure(ctx, s.inScope(w, plan.beside), "create", plan.beside, "open a diff beside it"); err != nil {
				return nil, nil, err
			}
		}
		id, err := openDiff(w, s.callFunc(ctx), plan, s.self, cwd)
		if err != nil {
			return nil, nil, err
		}
		out := map[string]any{"pane_id": id}
		if w2, err := s.world(ctx); err == nil {
			if p := w2.panes[id]; p != nil && p.Diff != nil {
				out["title"], out["repo"] = p.Diff.Title, p.Diff.Repo
			}
		}
		return textResult(out), nil, nil
	})
}
