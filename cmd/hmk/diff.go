package main

// hmk diff: open a git diff in a diff pane to review (the daemon works it
// out). By default a new tab just after this pane's, with what a pull
// request would show: the branch against its base, uncommitted changes
// included, or on the default branch, what's uncommitted.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/gitdiff"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const diffUsage = `usage: hmk diff [REV | FROM..TO | FROM...TO] [--uncommitted | --branch [--base B] [--with-uncommitted]]
                [--repo DIR] [--split | --unified] [--ignore-whitespace]
                [--beside PANE [--down] | --group G] [--name NAME] [--wait]`

// diffPlan is what to open and where: the parts hmk diff and diff_open share.
type diffPlan struct {
	open   *pb.DiffOpen
	name   string
	beside string // split this pane; else a new tab
	down   bool
	group  string // the tab's Group (else the caller's)
}

// parseDiffArgs reads hmk diff's arguments.
func parseDiffArgs(args []string) (*diffPlan, bool, error) {
	usage := errors.New(diffUsage)
	plan := &diffPlan{open: &pb.DiffOpen{Source: &pb.DiffSource{}, View: &pb.DiffView{}}}
	var rev, base string
	var branch, uncommitted, with, wait bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s wants a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--branch":
			branch = true
		case "--base":
			base, err = value()
		case "--with-uncommitted":
			with = true
		case "--uncommitted":
			uncommitted = true
		case "--repo":
			plan.open.Repo, err = value()
		case "--split":
			plan.open.View.Style = "split"
		case "--unified":
			plan.open.View.Style = "unified"
		case "--ignore-whitespace", "-w":
			plan.open.View.Whitespace = "ignore"
		case "--beside":
			plan.beside, err = value()
		case "--down":
			plan.down = true
		case "--group":
			plan.group, err = value()
		case "--name":
			plan.name, err = value()
		case "--wait":
			wait = true
		case "-h", "--help":
			return nil, false, usage
		default:
			if strings.HasPrefix(a, "-") || rev != "" {
				return nil, false, usage
			}
			rev = a
		}
		if err != nil {
			return nil, false, err
		}
	}
	switch {
	case rev != "" && (branch || uncommitted):
		return nil, false, errors.New("a commit or range, or --branch, or --uncommitted: one of them")
	case branch && uncommitted:
		return nil, false, errors.New("--branch or --uncommitted, not both")
	case (base != "" || with) && !branch:
		return nil, false, errors.New("--base and --with-uncommitted go with --branch")
	case rev != "":
		plan.open.Source = gitdiff.ParseRev(rev)
	case branch:
		plan.open.Source = &pb.DiffSource{Kind: pb.DiffSource_KIND_BRANCH, Base: base, WithUncommitted: with}
	case uncommitted:
		plan.open.Source = &pb.DiffSource{Kind: pb.DiffSource_KIND_UNCOMMITTED}
	}
	return plan, wait, nil
}

// openDiff opens plan's diff pane for the caller in pane self ("" outside
// one), returning its pane id. cwd is where a repository is looked for
// when none is named.
func openDiff(w *world, call caller, plan *diffPlan, self, cwd string) (string, error) {
	if plan.open.Repo != "" {
		plan.open.Repo = resolvePath(plan.open.Repo, cwd)
	}
	spec := &pb.PaneSpec{Cwd: cwd, CreatedByPaneId: self, Diff: plan.open}
	if plan.beside != "" {
		target, err := w.resolve(plan.beside, "pane")
		if err != nil {
			return "", err
		}
		dir := pb.Split_DIRECTION_HORIZONTAL
		if plan.down {
			dir = pb.Split_DIRECTION_VERTICAL
		}
		r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: target, Direction: dir, Spec: spec}}})
		if err != nil {
			return "", err
		}
		return client.CreatedID(r), nil
	}
	// A new tab, just after the caller's when it's in that Group.
	gid := ""
	after := ""
	if plan.group != "" {
		id, err := w.resolve(plan.group, "group")
		if err != nil {
			return "", err
		}
		gid = id
	}
	if p := w.panes[self]; p != nil {
		if t := w.tabs[p.TabId]; t != nil && (gid == "" || gid == t.GroupId) {
			gid, after = t.GroupId, t.Id
		}
	}
	if gid == "" {
		if len(w.groups) == 0 {
			return "", errors.New("no Groups yet; create one with: hmk group new NAME")
		}
		gid = w.groups[0].Id
	}
	r, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Name: plan.name, Pane: spec, AfterTabId: after,
	}}})
	if err != nil {
		return "", err
	}
	return tabPane(call, client.CreatedID(r)), nil
}

func runDiff(w *world, c *client.Client, call caller, args []string) error {
	plan, wait, err := parseDiffArgs(args[1:])
	if err != nil {
		return err
	}
	wd, _ := os.Getwd()
	id, err := openDiff(w, call, plan, paneEnv("PANE_ID"), wd)
	if err != nil {
		return err
	}
	fmt.Println(id)
	if !wait {
		return nil
	}
	// Until it's closed.
	return waitForExit(c, id)
}
