package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
)

// runDocs opens the docs site in the browser: `open` on a Mac, xdg-open on
// a Linux desktop. Over SSH, without a desktop, or with --print, it prints
// the address instead.
func runDocs(args []string) error {
	print := false
	for _, a := range args {
		if a != "--print" {
			return errors.New("usage: hmk docs [--print]")
		}
		print = true
	}
	opener := ""
	switch {
	case print:
	case runtime.GOOS == "darwin":
		opener = "open"
	case os.Getenv("SSH_CONNECTION") == "" && (os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""):
		opener, _ = exec.LookPath("xdg-open")
	}
	if opener == "" || exec.Command(opener, hammockconfig.DocsURL).Run() != nil {
		fmt.Println(hammockconfig.DocsURL)
	}
	return nil
}
