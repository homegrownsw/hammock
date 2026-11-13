package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/holder"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/thread"
)

// switchAgent continues a pane's agent session in another harness (ADR-0006).
// It runs without d.mu held for its slow parts: interrupting the agent,
// waiting for it to exit, and converting the transcript.
func (d *Daemon) switchAgent(paneID, target string) (string, error) {
	d.mu.Lock()
	p, ok := d.panes[paneID]
	d.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no such pane %q", paneID)
	}
	if err := d.wakeAndWait(p, "user"); err != nil { // asleep: its process is needed to stop
		return "", err
	}
	p.mu.Lock()
	a := p.info.Agent
	var harness, transcript, cwd, sessionID string
	if a != nil {
		harness, transcript, sessionID = a.Harness, safeTranscript(a.TranscriptPath), a.SessionId
	}
	cwd = p.info.Cwd
	pid := p.pid
	p.mu.Unlock()
	if harness == "" {
		return "", errors.New("no agent detected in this pane")
	}
	if harness == target {
		return "", fmt.Errorf("this pane is already running %s", target)
	}
	if _, ok := thread.CodecFor(target); !ok {
		return "", fmt.Errorf("switching to %s isn't supported yet", target)
	}

	// Read the source session (before stopping, so we fail fast).
	var th thread.Thread
	var recs []thread.Record
	var decodeErr error
	if src, ok := thread.CodecFor(harness); !ok {
		decodeErr = fmt.Errorf("reading %s sessions isn't supported yet", harness)
	} else if transcript == "" && sessionID != "" && locate(src, sessionID, &transcript) {
		decodeErr = readSession(src, transcript, &th, &recs)
	} else if transcript == "" {
		decodeErr = errors.New("the agent's transcript location is unknown (install hooks with hmk hooks install)")
	} else {
		decodeErr = readSession(src, transcript, &th, &recs)
	}
	if decodeErr != nil {
		return "", fmt.Errorf("can't read the current session: %w", decodeErr)
	}
	th.Cwd = cwd
	if existing, found, _ := d.store.ThreadByNative(harness, th.NativeID); found {
		th.ID = existing.ID
	} else if th.ID == "" {
		th.ID = thread.NewID()
	}
	thread.Seal(recs)
	d.persist(d.store.PutThread(th, recs))

	// Stop the current agent and wait for the shell to come back.
	if err := d.stopAgent(p, pid, harness); err != nil {
		return "", err
	}

	cmd, nt, convertedPath, report := d.convert(th, recs, target, cwd, paneID)
	d.mu.Lock()
	d.audit("user", "switch-agent", paneID, harness+" → "+target+" ("+report.audit()+")")
	d.mu.Unlock()
	p.mu.Lock()
	// The converted session is known now, so switching again works even
	// before (or without) the new agent's hooks saying so. Heuristic until a
	// hook arrives: without hooks, its screen says what it's doing.
	p.info.Agent = &pb.Agent{Harness: target, State: pb.AgentState_AGENT_STATE_STARTING, StateSinceUnixMs: time.Now().UnixMilli(),
		SessionId: nt.NativeID, TranscriptPath: convertedPath, Heuristic: true}
	ok = p.postLocked(func(hc *holder.Client) { _ = hc.Input([]byte(cmd + "\r")) })
	p.mu.Unlock()
	if !ok {
		return "", errors.New("pane is not running")
	}
	d.mu.Lock()
	d.paneChangedIfLive(p)
	d.mu.Unlock()
	return report.String(target), nil
}

// stopAgent interrupts the agent until it exits back to the shell.
func (d *Daemon) stopAgent(p *pane, shellPID int, harness string) error {
	deadline := time.Now().Add(10 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		if findAgent(shellPID) == "" {
			return nil
		}
		if attempt%3 == 0 {
			// Ctrl-C twice exits both Claude Code and Codex from an idle prompt.
			p.mu.Lock()
			p.postLocked(func(hc *holder.Client) {
				_ = hc.Input([]byte{0x03})
				time.Sleep(150 * time.Millisecond)
				_ = hc.Input([]byte{0x03})
			})
			p.mu.Unlock()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("%s didn't exit; stop it and try again", harness)
}

func shellJoin(argv []string) string {
	var b bytes.Buffer
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		if strings.ContainsAny(a, " \t\n'\"$`\\*?[]{}()<>|&;#~") {
			b.WriteString(shellQuote(a))
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}

func locate(c thread.Codec, id string, path *string) bool {
	if l, ok := c.(thread.Locator); ok {
		if p, ok := l.Locate(id); ok {
			*path = p
			return true
		}
	}
	return false
}

func readSession(c thread.Codec, path string, th *thread.Thread, recs *[]thread.Record) error {
	f, err := agents.Get(c.Harness()).OpenSession(path)
	if err != nil {
		return err
	}
	defer f.Close()
	*th, *recs, err = c.Decode(f)
	return err
}

// carryOver is what a conversion took across (the carry-over report).
type carryOver struct {
	messages, toolCalls, reasoning int
	brief                          error // converting failed: handed over as a brief, for this reason
}

func countCarry(recs []thread.Record) carryOver {
	var c carryOver
	for _, r := range recs {
		switch r.Kind {
		case thread.KindUser, thread.KindAssistant:
			c.messages++
		case thread.KindToolCall:
			c.toolCalls++
		case thread.KindReasoning:
			c.reasoning++
		}
	}
	return c
}

// String is the report for people: what came across into target.
func (c carryOver) String(target string) string {
	name := target
	if a := agents.Get(target); a != nil {
		name = a.Short
	}
	if c.brief != nil {
		return fmt.Sprintf("Started %s with a summary of the conversation: it couldn't be converted (%v).", name, c.brief)
	}
	s := fmt.Sprintf("Continued in %s with %s and %s", name, plural(c.messages, "message"), plural(c.toolCalls, "tool call"))
	if c.reasoning > 0 {
		s += "; the previous agent's private reasoning stays behind"
	}
	return s + "."
}

func (c carryOver) audit() string {
	if c.brief != nil {
		return fmt.Sprintf("handoff brief: %v", c.brief)
	}
	return fmt.Sprintf("converted, %d messages, %d tool calls", c.messages, c.toolCalls)
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// convert writes th as a new target session and returns the command that
// resumes it, the new thread and its file; if converting fails, the command
// starts target with a handoff brief instead. key names the brief's file.
func (d *Daemon) convert(th thread.Thread, recs []thread.Record, target, cwd, key string) (cmd string, nt thread.Thread, path string, report carryOver) {
	report = countCarry(recs)
	dst, _ := thread.CodecFor(target)
	nt = thread.Thread{ID: thread.NewID(), Harness: target, Cwd: cwd, Title: th.Title, Named: th.Named, Created: time.Now(), Parent: th.ID, Source: th.Harness}
	nativeID, p, data, err := dst.Encode(nt, recs)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(p), 0o700); err == nil {
			err = os.WriteFile(p, data, 0o600)
		}
	}
	if err == nil {
		nt.NativeID = nativeID
		d.persist(d.store.PutThread(nt, recs))
		return shellJoin(dst.ResumeCommand(nativeID)), nt, p, report
	}
	report.brief = err
	bf := filepath.Join(d.cfg.Dir, "run", "brief-"+key+".md")
	if werr := os.WriteFile(bf, []byte(thread.Brief(th, recs)), 0o600); werr != nil {
		report.brief = werr
	}
	return fmt.Sprintf("%s \"$(cat %s)\"", target, shellQuote(bf)), nt, "", report
}
