//go:build !darwin

package daemon

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnHolder starts a pane holder in its own session, with extra (if any)
// as its fd 3 and its output on out (if any). See spawn_darwin.go.
func spawnHolder(path string, args, env []string, dir string, out, extra *os.File) (*os.Process, error) {
	cmd := exec.Command(path, args...)
	if extra != nil {
		cmd.ExtraFiles = []*os.File{extra}
	}
	cmd.Env = env
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if out != nil {
		cmd.Stdout, cmd.Stderr = out, out
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}
