package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A tab you name also names its Claude session: Hammock appends the same
// "custom-title" line to the session's transcript that /rename writes, so
// the name shows in Claude's resume list. Claude re-writes its own title
// (if it has one) at the end of each turn, so after each turn the tab's
// name is written again if it isn't the transcript's latest. The running
// Claude only sees the name when the session is next resumed.

type customTitle struct {
	Type        string `json:"type"`
	CustomTitle string `json:"customTitle"`
	SessionID   string `json:"sessionId"`
}

// nameSessions writes tab t's name into the transcripts of its Claude
// panes (off the lock: it reads and writes files). Callers hold d.mu.
func (d *Daemon) nameSessions(t *pb.Tab) {
	name := strings.Join(strings.Fields(t.Name), " ")
	if name == "" {
		return
	}
	for _, id := range tabPanes(t) {
		if p := d.panes[id]; p != nil {
			if a := p.snapshotInfo().Agent; a.GetHarness() == "claude" && a.GetSessionId() != "" && a.GetTranscriptPath() != "" {
				go func() {
					if err := nameTranscript(a.TranscriptPath, a.SessionId, name); err != nil {
						d.log.Warn("name claude session", "pane", id, "err", err)
					}
				}()
			}
		}
	}
}

// nameTranscript appends a custom-title line unless the transcript's latest
// already says name.
func nameTranscript(path, session, name string) error {
	f, err := openTranscriptAppend(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if latestCustomTitle(f) == name {
		return nil
	}
	b, _ := json.Marshal(customTitle{Type: "custom-title", CustomTitle: name, SessionID: session})
	_, err = f.Write(append(b, '\n'))
	return err
}

// latestCustomTitle reads the last custom-title in the file's tail (where
// Claude keeps it, re-writing it each turn).
func latestCustomTitle(f *os.File) string {
	const tail = 256 << 10
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := max(0, st.Size()-tail)
	b, _ := io.ReadAll(io.NewSectionReader(f, off, st.Size()-off))
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], []byte(`"custom-title"`)) {
			continue
		}
		var ct customTitle
		if json.Unmarshal(lines[i], &ct) == nil && ct.Type == "custom-title" {
			return ct.CustomTitle
		}
	}
	return ""
}
