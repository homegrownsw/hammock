// agentreplay plays a Claude Code session into a Hammock pane, as if the
// agent were writing it now, for watching the agent view under a live
// session (smoothness, jank). Development only:
//
//	hmk tab new GROUP -- agentreplay -src SESSION.jsonl [-from N] [-turns N] [-speed X] [-max-gap D] [-log FILE] [-done FILE]
//
// It runs in the pane it plays into: the daemon takes hook events only from
// a pane's own processes (build it with go build -o … ./scripts/agentreplay).
//
// The session's first lines (all but its last -turns turns, or up to line
// -from) are written at once as history; the rest follow at their own
// pace (times -speed, no gap longer than -max-gap). The copy goes under
// ~/.claude/projects/-hmk-replay (where the daemon reads transcripts); it
// stays when the replay ends, so remove that folder when you're done.
package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/alexjsp/hammock/internal/daemon"
)

func main() {
	pane := flag.String("pane", os.Getenv("HMK_PANE_ID"), "the pane to play into (the one it runs in)")
	sock := flag.String("hook-socket", os.Getenv("HMK_HOOK_SOCKET"), "the daemon's hook socket")
	logFile := flag.String("log", "", "log here, not to the pane")
	doneFile := flag.String("done", "", "write this file when the replay ends")
	src := flag.String("src", "", "the Claude Code session to play")
	from := flag.Int("from", 0, "start playing at this line (0: by -turns)")
	turns := flag.Int("turns", 2, "play the last this many turns (with -from 0)")
	speed := flag.Float64("speed", 1, "play this many times faster")
	maxGap := flag.Duration("max-gap", 3*time.Second, "the longest pause between lines")
	loop := flag.Bool("loop", false, "play it again when it ends")
	flag.Parse()
	if *sock == "" || *pane == "" || *src == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			log.Fatal(err)
		}
		log.SetOutput(f)
	}
	if *doneFile != "" {
		defer os.WriteFile(*doneFile, []byte("done\n"), 0o600)
	}
	lines := readLines(*src)
	start := *from
	if start == 0 {
		start = turnStart(lines, *turns)
	}
	home, _ := os.UserHomeDir()
	out := filepath.Join(home, ".claude", "projects", "-hmk-replay")
	if err := os.MkdirAll(out, 0o700); err != nil {
		log.Fatal(err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	for {
		id := newID()
		path := filepath.Join(out, id+".jsonl")
		done := play(*sock, *pane, id, path, lines, start, *speed, *maxGap, stop)
		if !done || !*loop {
			// Kept for the agent view to go on showing: remove the folder
			// when done with it.
			return
		}
		os.Remove(path)
	}
}

func play(sock, pane, id, path string, lines [][]byte, start int, speed float64, maxGap time.Duration, stop chan os.Signal) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines[:start] {
		f.Write(append(l, '\n'))
	}
	hook(sock, pane, "UserPromptSubmit", map[string]any{"session_id": id, "transcript_path": path, "prompt": "replay"})
	log.Printf("playing %d of %d lines into %s", len(lines)-start, len(lines), pane)
	var prev time.Time
	for i, l := range lines[start:] {
		t := stamp(l)
		if !prev.IsZero() && !t.IsZero() {
			gap := time.Duration(float64(t.Sub(prev)) / speed)
			gap = min(max(gap, 0), maxGap)
			select {
			case <-time.After(gap):
			case <-stop:
				return false
			}
		}
		if !t.IsZero() {
			prev = t
		}
		f.Write(append(l, '\n'))
		if (i+1)%50 == 0 {
			log.Printf("line %d", start+i+1)
		}
	}
	hook(sock, pane, "Stop", map[string]any{"session_id": id, "transcript_path": path})
	log.Printf("done")
	select {
	case <-time.After(3 * time.Second):
	case <-stop:
		return false
	}
	return true
}

func hook(sock, pane, event string, payload map[string]any) {
	payload["hook_event_name"] = event
	b, _ := json.Marshal(payload)
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		log.Fatalf("hook socket: %v", err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	json.NewEncoder(c).Encode(daemon.HookEvent{PaneID: pane, Harness: "claude", Event: event, Payload: b})
	var sink [512]byte
	for {
		if _, err := c.Read(sink[:]); err != nil {
			return
		}
	}
}

func readLines(path string) [][]byte {
	f, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		out = append(out, bytes.Clone(sc.Bytes()))
	}
	return out
}

// turnStart is the line where the last n turns (your messages) begin.
func turnStart(lines [][]byte, n int) int {
	seen := 0
	for i := len(lines) - 1; i >= 0; i-- {
		var l struct {
			Type    string `json:"type"`
			IsMeta  bool   `json:"isMeta"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(lines[i], &l) != nil || l.Type != "user" || l.IsMeta {
			continue
		}
		var s string
		if json.Unmarshal(l.Message.Content, &s) == nil && len(s) > 0 && s[0] != '<' {
			seen++
			if seen == n {
				return i
			}
		}
	}
	return 0
}

func stamp(l []byte) time.Time {
	var v struct {
		Timestamp time.Time `json:"timestamp"`
	}
	json.Unmarshal(l, &v)
	return v.Timestamp
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:])
}
