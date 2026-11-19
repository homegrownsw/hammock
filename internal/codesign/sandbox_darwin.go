package codesign

/*
#include <sys/types.h>
#include <mach/message.h>

// libsystem_sandbox (part of libSystem), not in the SDK's headers. With no
// operation and SANDBOX_FILTER_NONE (0) it asks only whether the process
// is sandboxed at all: 1 if it is, 0 if not, -1 if it can't tell.
int sandbox_check(pid_t pid, const char *operation, int type, ...);
int sandbox_check_by_audit_token(audit_token_t token, const char *operation, int type, ...);

static int hmk_sandboxed_token(const audit_token_t *tok) { return sandbox_check_by_audit_token(*tok, NULL, 0); }
static int hmk_sandboxed_pid(int pid) { return sandbox_check((pid_t)pid, NULL, 0); }
*/
import "C"

// Sandboxed reports whether the token's process runs in a sandbox (Seatbelt:
// sandbox-exec, the App Sandbox, an agent harness's sandbox for the commands
// it runs). A sandbox is inherited by every process started under it and
// can't be left, so anything a sandboxed program runs or execs is
// sandboxed too. One it can't tell about (gone, or not the process the
// token was taken from) counts as sandboxed.
func (t Token) Sandboxed() bool {
	if !t.ok {
		return false
	}
	return C.hmk_sandboxed_token(&t.t) != 0
}

// PIDSandboxed is Sandboxed for whatever process has pid now (no token to
// go by).
func PIDSandboxed(pid int) bool {
	if pid <= 0 {
		return false
	}
	return C.hmk_sandboxed_pid(C.int(pid)) != 0
}
