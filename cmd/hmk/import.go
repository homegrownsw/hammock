package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/herdr"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const importUsage = "usage: hmk import herdr [--dry-run] [--json] [--only WORKSPACE,...]"

// runImport is hmk import: herdr's workspaces, as Groups. --dry-run says
// what it would make (--json: the plan, which onboarding shows); --only
// takes some of herdr's workspaces, by id.
func runImport(args []string) error {
	if len(args) == 0 || args[0] != "herdr" {
		return errors.New(importUsage)
	}
	flags := args[1:]
	for _, f := range flags {
		if strings.HasPrefix(f, "-") && f != "--dry-run" && f != "--json" && f != "--only" {
			return errors.New(importUsage)
		}
	}
	dry, asJSON := hasFlag(flags, "--dry-run"), hasFlag(flags, "--json")

	plan, err := herdr.Load(herdr.Dir())
	if errors.Is(err, herdr.ErrNone) && dry && asJSON {
		return printJSON(herdr.Plan{Path: herdr.SessionPath(herdr.Dir()), Groups: []herdr.Group{}})
	}
	if err != nil {
		return err
	}
	if only := flagValue(flags, "--only"); only != "" {
		plan.Only(strings.Split(only, ","))
		if len(plan.Groups) == 0 {
			return fmt.Errorf("herdr has no workspace %s", only)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if dry {
		// Which are here already, if Hammock is running to ask.
		dctx, dcancel := context.WithTimeout(ctx, 2*time.Second)
		if c, err := connect(dctx); err == nil {
			if w, err := fetchWorld(dctx, c); err == nil {
				markExisting(plan, w)
			}
			c.Close()
		}
		dcancel()
		if asJSON {
			return printJSON(plan)
		}
		printPlan(plan)
		return nil
	}

	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	call := func(m *pb.ClientMessage) (*pb.ServerMessage, error) { return c.Call(ctx, m) }
	tabs, resumed := 0, 0
	for _, g := range plan.Groups {
		t, r, err := herdr.Import(call, g)
		tabs, resumed = tabs+t, resumed+r
		if err != nil {
			return fmt.Errorf("importing %s: %w", g.Name, err)
		}
	}
	fmt.Printf("Imported %s and %s from herdr", plural(len(plan.Groups), "Group"), plural(tabs, "tab"))
	if resumed > 0 {
		fmt.Printf(", resuming %s", plural(resumed, "conversation"))
	}
	fmt.Println(".")
	if plan.Running {
		fmt.Println("herdr is still running: quit it, so its agents aren't running in both.")
	}
	return nil
}

func plural(n int, s string) string {
	if n == 1 {
		return "1 " + s
	}
	return fmt.Sprintf("%d %ss", n, s)
}

// markExisting marks the Groups Hammock has one of the same name as.
func markExisting(plan *herdr.Plan, w *world) {
	have := map[string]bool{}
	for _, g := range w.groups {
		have[strings.ToLower(client.GroupTitle(g, w.groups, w.panes))] = true
	}
	for i := range plan.Groups {
		plan.Groups[i].Exists = have[strings.ToLower(plan.Groups[i].Name)]
	}
}

func printPlan(plan *herdr.Plan) {
	fmt.Printf("From %s:\n", plan.Path)
	for _, g := range plan.Groups {
		note := ""
		if g.Exists {
			note = "  (a Group by this name is here already)"
		}
		fmt.Printf("\n%s  %s%s\n", termSafe(g.Name), termSafe(tildePath(g.Folder)), note)
		for i, t := range g.Tabs {
			name := t.Name
			if name == "" {
				name = fmt.Sprintf("Tab %d", i+1)
			}
			panes := ""
			if len(t.Panes) > 1 {
				panes = fmt.Sprintf("  %d panes", len(t.Panes))
			}
			fmt.Printf("  %s%s\n", termSafe(name), panes)
			for _, p := range t.Panes {
				switch {
				case p.Resume != "":
					fmt.Printf("    resumes %s: %s\n", p.AgentName, termSafe(p.Resume))
				case p.Session != "":
					fmt.Printf("    %s's conversation is gone: a shell\n", p.AgentName)
				}
			}
		}
	}
	if plan.Running {
		fmt.Println("\nherdr is still running: quit it first, so its agents aren't running in both.")
	}
}

func tildePath(p string) string {
	if home, _ := os.UserHomeDir(); home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}
