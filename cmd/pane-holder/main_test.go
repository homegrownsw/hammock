package main

import "testing"

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
