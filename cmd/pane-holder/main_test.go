package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallOf(t *testing.T) {
	for exe, want := range map[string]string{
		"/Applications/Hammock.app/Contents/Helpers/Hammock Pane.app/Contents/MacOS/Hammock Pane":             "/Applications/Hammock.app",
		"/x/dist/Hammock β 77-pane-holders.app/Contents/Helpers/Hammock Pane.app/Contents/MacOS/Hammock Pane": "/x/dist/Hammock β 77-pane-holders.app",
		"/Applications/Hammock.app/Contents/Helpers/Hammock Terminal.app/Contents/MacOS/Hammock":              "/Applications/Hammock.app",
		"/Users/me/Developer/hammock/bin/pane-holder":                                                         "/Users/me/Developer/hammock/bin/pane-holder",
	} {
		if got := installOf(exe); got != want {
			t.Errorf("installOf(%q) = %q, want %q", exe, got, want)
		}
	}
}

// A holder with --state serves only a socket directly in that state dir's
// run/panes, and not one that's a link: the rule the daemon reconnects by.
func TestInPanes(t *testing.T) {
	state := t.TempDir()
	panes := filepath.Join(state, "run", "panes")
	os.MkdirAll(panes, 0o700)
	if !inPanes(filepath.Join(panes, "pa_1.sock"), state) {
		t.Error("its own run/panes refused")
	}
	if inPanes(filepath.Join(state, "run", "pa_1.sock"), state) || inPanes(filepath.Join(t.TempDir(), "pa_1.sock"), state) {
		t.Error("a socket elsewhere taken")
	}
	link := filepath.Join(panes, "pa_2.sock")
	os.Symlink(filepath.Join(t.TempDir(), "x.sock"), link)
	if inPanes(link, state) {
		t.Error("a link taken")
	}
}
