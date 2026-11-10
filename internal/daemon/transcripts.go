package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/homegrownsw/hammock/internal/agents"
)

// Agents' hook events name their session's transcript, which the daemon
// then reads (agent view, switching) and appends to (session names). A
// hook payload is only a claim, so a transcript is used only when it is a
// regular session file inside an agent's own session folders.

// learnedRoots are the session folders agents in panes were started with
// ($CLAUDE_CONFIG_DIR, $CODEX_HOME set in your shell): the daemon, started
// by the app, doesn't see your shell's.
var learnedRoots sync.Map // dir → harness

// learnTranscriptRoots notes the session folders in an agent's environment
// (a hook sender's, which it inherits).
func learnTranscriptRoots(env []string) {
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if !filepath.IsAbs(v) {
			continue
		}
		for _, a := range agents.All {
			sub, ok := a.RootEnv[k]
			if !ok {
				continue
			}
			dir := filepath.Join(v, sub)
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				learnedRoots.Store(dir, a.ID)
			}
		}
	}
}

// agentRoots are where an agent keeps its sessions: its own folders (with
// the daemon's environment), and those agents in panes were seen using.
var agentRoots = func(harness string) []string {
	a := agents.Get(harness)
	if a == nil || a.Roots == nil {
		return nil
	}
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d = filepath.Clean(d); !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, d := range a.Roots() {
		add(d)
	}
	learnedRoots.Range(func(k, v any) bool {
		if v.(string) == harness {
			add(k.(string))
		}
		return true
	})
	return dirs
}

// transcriptRoots are every agent's session folders.
func transcriptRoots() []string {
	var dirs []string
	for _, a := range agents.All {
		dirs = append(dirs, agentRoots(a.ID)...)
	}
	return dirs
}

// harnessOfPath is the agent whose session folders path is in, and whose
// own file it is, or "".
func harnessOfPath(path string) string {
	if path == "" {
		return ""
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	if a := agentOfFile(path, real); a != nil {
		return a.ID
	}
	return ""
}

// agentOfFile is the agent whose session folders real (path, resolved) is
// in, and that owns it by both names.
func agentOfFile(path, real string) *agents.Agent {
	for _, a := range agents.All {
		if !ownsFile(a, path) || !ownsFile(a, real) {
			continue
		}
		for _, root := range agentRoots(a.ID) {
			if r, err := filepath.EvalSymlinks(root); err == nil && within(real, r) || within(path, root) {
				return a
			}
		}
	}
	return nil
}

// ownsFile: p is one of a's session files by its name (a JSONL file,
// unless the agent says otherwise).
func ownsFile(a *agents.Agent, p string) bool {
	if a.OwnsFile != nil {
		return a.OwnsFile(p)
	}
	return isTranscriptName(p)
}

// within: path is inside dir (both cleaned; neither resolved).
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// isTranscriptName: a JSONL session file, or a compressed one (Codex's
// rollouts are .jsonl.zst).
func isTranscriptName(p string) bool {
	return strings.HasSuffix(p, ".jsonl") || strings.HasSuffix(p, ".jsonl.zst")
}

// safeTranscript returns path resolved (symlinks followed) if it is a
// regular file under one of an agent's session folders that the agent
// owns (by default, a .jsonl file), else "".
func safeTranscript(path string) string { return safeTranscriptOf("", path) }

// safeTranscriptOf is safeTranscript, but only for harness's own session
// files (any agent's while harness is "").
func safeTranscriptOf(harness, path string) string {
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	if st, err := os.Lstat(real); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	for _, a := range agents.All {
		if (harness != "" && a.ID != harness) || !ownsFile(a, path) || !ownsFile(a, real) {
			continue
		}
		for _, root := range agentRoots(a.ID) {
			if r, err := filepath.EvalSymlinks(root); err == nil && within(real, r) {
				return real
			}
		}
	}
	return ""
}

// openTranscriptAppend opens a checked transcript to read and append to,
// refusing a symlink swapped in since it was checked.
func openTranscriptAppend(path string) (*os.File, error) {
	real := safeTranscriptOf("claude", path)              // only Claude's are named by appending
	if real == "" || !strings.HasSuffix(real, ".jsonl") { // never into a compressed one
		return nil, os.ErrPermission
	}
	return os.OpenFile(real, os.O_RDWR|os.O_APPEND|syscall.O_NOFOLLOW, 0)
}
