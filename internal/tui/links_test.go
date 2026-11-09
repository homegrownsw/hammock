package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyLink(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "notes.txt")
	tool := filepath.Join(dir, "run")
	_ = os.WriteFile(doc, []byte("hi"), 0o644)
	_ = os.WriteFile(tool, []byte("#!/bin/sh"), 0o755)
	for _, tc := range []struct {
		url  string
		want linkAction
	}{
		{"https://example.com", linkOpen},
		{"HTTP://example.com", linkOpen},
		{"mailto:a@b.c", linkOpen},
		{"file://" + doc, linkOpen},
		{"file://" + tool, linkReveal},
		{"file:///Applications/Calculator.app", linkReveal},
		{"file://" + filepath.Join(dir, "x.command"), linkReveal},
		{"file://otherhost/etc/hosts", linkAsk},
		{"vscode://file/etc/hosts", linkAsk},
		{"javascript:alert(1)", linkAsk},
		{"x-apple.systempreferences:com.apple", linkAsk},
		{"/etc/hosts", linkAsk},
	} {
		if got, _ := classifyLink(tc.url); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.url, got, tc.want)
		}
	}
}
