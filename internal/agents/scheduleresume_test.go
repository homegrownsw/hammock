package agents

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestScheduleResume(t *testing.T) {
	s := &pb.Schedule{AllowedTools: []string{"Read", "Bash(gh pr:*)"}, CodexSandbox: "workspace-write", CodexApproval: "on-request"}
	if got := Claude.ScheduleResume(s, "abc"); got != "claude --resume 'abc' --allowedTools 'Read,Bash(gh pr:*)'" {
		t.Errorf("claude: %s", got)
	}
	if got := Codex.ScheduleResume(s, "abc"); got != "codex resume 'abc' --sandbox 'workspace-write' --ask-for-approval 'on-request'" {
		t.Errorf("codex: %s", got)
	}
}

func TestWiderThan(t *testing.T) {
	s := &pb.Schedule{AllowedTools: []string{"Read"}}
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"claude"}, ""},
		{[]string{"claude", "--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
		{[]string{"claude", "--permission-mode", "bypassPermissions"}, "--permission-mode bypassPermissions"},
		{[]string{"claude", "--permission-mode=plan"}, ""},
		{[]string{"claude", "--allowedTools", "Read"}, ""},
		{[]string{"claude", "--allowedTools", "Read,Edit"}, "--allowedTools Edit"},
		{[]string{"codex", "--sandbox", "danger-full-access"}, "--sandbox danger-full-access"},
		{[]string{"codex", "-a", "never"}, "-a never"},
		{[]string{"codex", "--yolo"}, "--yolo"},
	} {
		if got := WiderThan(c.argv, s); got != c.want {
			t.Errorf("%v: %q, want %q", c.argv, got, c.want)
		}
	}
}
