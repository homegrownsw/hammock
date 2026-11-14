package agents

import (
	"slices"
	"testing"
)

func TestClaudeResumeFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{nil, nil},
		{[]string{"fix the tests"}, nil},
		{[]string{"--dangerously-skip-permissions", "fix the tests"}, []string{"--dangerously-skip-permissions"}},
		{[]string{"--model", "opus", "-r", "abc", "hello"}, []string{"--model", "opus"}},
		{[]string{"--resume", "--model=sonnet"}, []string{"--model=sonnet"}},
		{[]string{"-c", "--permission-mode", "plan"}, []string{"--permission-mode", "plan"}},
		{[]string{"--add-dir", "/a", "/b", "--verbose", "--", "-not an option"}, []string{"--add-dir", "/a", "/b", "--verbose"}},
		{[]string{"-p", "--output-format", "json", "say hi"}, nil},
		{[]string{"--session-id", "1234", "-n", "work", "--mcp-config", "x.json"}, []string{"--mcp-config", "x.json"}},
		{[]string{"--remote-control", "--chrome"}, []string{"--remote-control", "--chrome"}},
		{[]string{"-w", "feature", "--ide"}, []string{"--ide"}},
	} {
		if got := claudeResumeFlags(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("claudeResumeFlags(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestResumeWith(t *testing.T) {
	got := Claude.ResumeWith("s1", []string{"/Users/me/.local/share/claude/versions/2.1.3", "--model", "opus", "do it"})
	if want := "claude --resume 's1' '--model' 'opus'"; got != want {
		t.Errorf("ResumeWith = %q, want %q", got, want)
	}
	if got := Claude.StartWith([]string{"claude", "--model", "opus", "do it"}); got != "claude '--model' 'opus'" {
		t.Errorf("StartWith = %q", got)
	}
	if got := Codex.ResumeWith("s2", []string{"codex", "--yolo"}); got != Codex.Resume("s2") {
		t.Errorf("an agent without ResumeFlags keeps none: %q", got)
	}
}
