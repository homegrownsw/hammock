package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
)

// runTheme lists Hammock's themes, or picks one: app-theme in the config
// file, which the Mac app (and through the daemon, the iPhone) follows.
func runTheme(args []string) error { return theme(args, os.Stdout) }

const themeUsage = `usage: hmk theme [NAME]
  With no NAME, lists the themes (* the one in use). NAME is one of them, or
  custom: your own terminal themes, window theme, agent colour, focused pane
  border, sidebar and toolbar colours (theme, window-theme, agent-color,
  active-pane-border-color, sidebar-color and toolbar-color in the config).
  Custom picks up the ones you set before; with none set yet, it starts as
  the theme you had.`

func theme(args []string, w io.Writer) error {
	if len(args) == 0 {
		current := hammockconfig.AppTheme(hammockconfig.Values())
		for _, n := range append(hammockconfig.AppThemes, hammockconfig.CustomTheme) {
			mark := "  "
			if n == current {
				mark = "* "
			}
			note := ""
			if n == hammockconfig.CustomTheme {
				note = "   (your own theme, window-theme, agent-color, active-pane-border-color, sidebar-color and toolbar-color)"
			}
			fmt.Fprintf(w, "%s%s%s\n", mark, n, note)
		}
		return nil
	}
	if strings.HasPrefix(args[0], "-") {
		if args[0] == "-h" || args[0] == "--help" {
			fmt.Fprintln(w, themeUsage)
			return nil
		}
		return fmt.Errorf("%s", themeUsage)
	}
	arg := strings.Join(args, " ") // hmk theme tokyo night
	name, ok := hammockconfig.ThemeName(arg)
	if !ok {
		return fmt.Errorf("no theme named %q (hmk theme lists them)", arg)
	}
	changes := []hammockconfig.Change{{Key: "app-theme", Value: name}}
	if name == hammockconfig.CustomTheme {
		changes = hammockconfig.CustomChanges(hammockconfig.Values())
	}
	if err := hammockconfig.SetAll(changes...); err != nil {
		return err
	}
	fmt.Fprintf(w, "Theme: %s\n", name)
	return nil
}
