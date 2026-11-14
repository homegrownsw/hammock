package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// hmk report: a script tells its terminal what it's doing, with the
// Program Status Protocol (OSC 7501,
// https://www.superlogical.com/rex/docs/build/program-status). It needs no
// daemon, and works in any terminal that supports the protocol (others
// ignore it): Hammock shows it as the pane's state, in Needs You and as
// notifications.
//
//	hmk report working "Syncing photos" --progress 40
//	rsync … && hmk report done "Photos synced" || hmk report error "rsync failed"

const reportUsage = `usage: hmk report STATE [MESSAGE] [--progress N] [--id ID] [--title TITLE] [--app NAME] [--kind KIND]
  STATE: idle, working, done, blocked, error, or clear (removes the record, and those beneath it)
  --progress 0-100 (working, blocked)   --kind permission|question|auth (blocked)
  --id names one of several records ("/" nests: build/test); --app is the program's own name (brew)`

var (
	reportIDRE  = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}(/[A-Za-z0-9_.+-]{1,32}){0,7}$`)
	reportAppRE = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}$`)
)

// runReport is hmk report: it writes the report to the terminal.
func runReport(args []string) error {
	seq, err := reportSequence(args)
	if err != nil {
		return err
	}
	return writeToTerminal(seq)
}

// reportSequence builds the OSC 7501 report args ask for.
func reportSequence(args []string) (string, error) {
	var pos []string
	flags := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, inline := strings.Cut(a, "=")
		switch name {
		case "--progress", "--id", "--title", "--app", "--kind":
			if !inline {
				if i+1 >= len(args) {
					return "", fmt.Errorf("%s needs a value\n%s", name, reportUsage)
				}
				i++
				val = args[i]
			}
			flags[name] = val
		case "-h", "--help":
			return "", errors.New(reportUsage)
		default:
			if strings.HasPrefix(a, "--") {
				return "", fmt.Errorf("unknown option %s\n%s", a, reportUsage)
			}
			pos = append(pos, a)
		}
	}
	if len(pos) == 0 || len(pos) > 2 {
		return "", errors.New(reportUsage)
	}
	state := pos[0]
	switch state {
	case "idle", "working", "done", "blocked", "error", "clear":
	default:
		return "", fmt.Errorf("unknown state %q\n%s", state, reportUsage)
	}
	pairs := []string{"state=" + state}
	if id := flags["--id"]; id != "" {
		if len(id) > 128 || !reportIDRE.MatchString(id) {
			return "", fmt.Errorf("--id %q: up to 8 parts of A-Z a-z 0-9 _ . + - (32 each), separated by /", id)
		}
		pairs = append(pairs, "id="+id)
	}
	if kind := flags["--kind"]; kind != "" {
		if state != "blocked" {
			return "", errors.New("--kind is for blocked")
		}
		switch kind {
		case "permission", "question", "auth":
		default:
			return "", fmt.Errorf("--kind %q: permission, question or auth", kind)
		}
		pairs = append(pairs, "kind="+kind)
	}
	if p := flags["--progress"]; p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 100 {
			return "", fmt.Errorf("--progress %q: a whole number from 0 to 100", p)
		}
		if state != "working" && state != "blocked" {
			return "", errors.New("--progress is for working and blocked")
		}
		pairs = append(pairs, "progress="+p)
	}
	if app := flags["--app"]; app != "" {
		if !reportAppRE.MatchString(app) {
			return "", fmt.Errorf("--app %q: up to 32 of A-Z a-z 0-9 _ . + -", app)
		}
		pairs = append(pairs, "app="+app)
	}
	text := func(key, s string, limit int) error {
		if s == "" {
			return nil
		}
		if len(s) > limit {
			return fmt.Errorf("the %s is %d bytes; at most %d", key, len(s), limit)
		}
		if strings.IndexFunc(s, unicode.IsControl) >= 0 {
			return fmt.Errorf("the %s is one line, without control characters", key)
		}
		pairs = append(pairs, key+"="+base64.StdEncoding.EncodeToString([]byte(s)))
		return nil
	}
	if err := text("title", flags["--title"], 192); err != nil {
		return "", err
	}
	if len(pos) == 2 {
		if err := text("msg", pos[1], 2048); err != nil {
			return "", err
		}
	}
	if state == "clear" && len(pairs) > 1 && !(len(pairs) == 2 && strings.HasPrefix(pairs[1], "id=")) {
		return "", errors.New("clear takes only --id")
	}
	return "\x1b]7501;" + strings.Join(pairs, ":") + "\x1b\\", nil
}

// writeToTerminal writes s to the controlling terminal, so it works with
// output piped or captured; else to stderr or stdout, if one's a terminal.
func writeToTerminal(s string) error {
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		defer f.Close()
		_, err = io.WriteString(f, s)
		return err
	}
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			_, err = io.WriteString(f, s)
			return err
		}
	}
	return errors.New("hmk report: not in a terminal")
}
