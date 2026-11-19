package agentview

import "testing"

func TestBangOutput(t *testing.T) {
	const rule = "──────────────────────────────"
	tail := rule + "\n❯ \n" + rule + "\n   ~/app\n  ⏵⏵ auto mode on (shift+tab to cycle) · esc to interrupt\n"
	for _, c := range []struct {
		name, screen, want string
		running            bool
	}{
		{"running, printed", "✻ Crunched for 16s\n\n! echo one && sleep 4 && echo two\n  ⎿  one\n     two\n     (7s)\n     (ctrl+b to run in background)\n\n" + tail, "one\ntwo", true},
		// As Claude Code draws it: a no-break space after the ⎿.
		{"as drawn", "! echo one && sleep 4\n  ⎿ \u00a0one\n     (2s)\n     (ctrl+b to run in background)\n\n" + rule + "\n❯\u00a0\n" + rule + "\n", "one", true},
		{"nothing yet", "! sleep 100\n  ⎿  Running…\n\n" + tail, "", true},
		{"wrapped command", "! echo a long\n  command\n  ⎿  hi\n     (1m 2s)\n\n" + tail, "hi", true},
		{"done, a reply after", "! echo hi\n  ⎿  hi\n\n⏺ It printed hi.\n\n" + tail, "", false},
		{"done, no reply yet", "! echo hi\n  ⎿  hi\n\n" + tail, "", false},
		{"none", "⏺ Hello\n\n" + tail, "", false},
	} {
		if got, running := BangOutput(c.screen); got != c.want || running != c.running {
			t.Errorf("%s: BangOutput = %q, %v; want %q, %v", c.name, got, running, c.want, c.running)
		}
	}
}
