package agents

// Agents' subscription usage limits, for the sidebar (off by default, per
// agent, in the apps). Read from what the CLIs keep on disk; nothing here
// asks a server or reads credentials (each agent's LiveUsage, live*.go,
// asks its service, when usage-live is on: the daemon's usage.go):
//
//   - Claude Code caches its /usage numbers in .claude.json
//     (cachedUsageUtilization: five_hour and seven_day, each a utilization
//     percentage and an RFC 3339 resets_at), but only now and then: it can
//     be hours behind what claude.ai shows.
//   - Codex writes rate_limits into its session files with each token
//     count: primary and secondary windows, each used_percent,
//     window_minutes and resets_at (Unix seconds).

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fileUsage is a reader of usage kept in one file (path, asked each time),
// read again only once the file changes.
func fileUsage(path func() string, maxSize int64, parse func([]byte) *pb.AgentUsage) func() *pb.AgentUsage {
	var mod time.Time
	var size int64
	var last *pb.AgentUsage
	return func() *pb.AgentUsage {
		st, err := os.Stat(path())
		if err != nil || !st.Mode().IsRegular() {
			last = nil
			return nil
		}
		if st.ModTime().Equal(mod) && st.Size() == size {
			return last
		}
		mod, size = st.ModTime(), st.Size()
		if st.Size() > maxSize {
			last = nil
			return nil
		}
		data, err := os.ReadFile(path())
		if err != nil {
			return last
		}
		last = parse(data)
		return last
	}
}

func claudeUsage() func() *pb.AgentUsage {
	return fileUsage(claudeConfigFile, 64<<20, parseClaudeUsage)
}

// codexUsage reads the newest rate_limits in Codex's latest sessions.
func codexUsage() func() *pb.AgentUsage {
	var path string
	var mod time.Time
	var last *pb.AgentUsage
	return func() *pb.AgentUsage {
		for _, f := range newestCodexRollouts(codexSessionsDir(), 8) {
			st, err := os.Stat(f)
			if err != nil {
				continue
			}
			if f == path && st.ModTime().Equal(mod) {
				return last
			}
			if a := lastCodexUsage(f); a != nil {
				path, mod, last = f, st.ModTime(), a
				return a
			}
		}
		return last // none newer has any: what the last one said
	}
}

// claudeConfigFile is Claude Code's .claude.json: in CLAUDE_CONFIG_DIR
// when that's set, else the home folder.
func claudeConfigFile() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// parseClaudeUsage reads the usage Claude Code cached in .claude.json.
func parseClaudeUsage(data []byte) *pb.AgentUsage {
	var f struct {
		Cached *struct {
			FetchedAtMs int64           `json:"fetchedAtMs"`
			Utilization json.RawMessage `json:"utilization"`
		} `json:"cachedUsageUtilization"`
	}
	if json.Unmarshal(data, &f) != nil || f.Cached == nil {
		return nil
	}
	return ParseClaudeWindows(f.Cached.Utilization, f.Cached.FetchedAtMs)
}

// ParseClaudeWindows reads Claude's usage windows (five_hour, seven_day),
// as both the usage endpoint and Claude Code's cache of it give them.
func ParseClaudeWindows(data []byte, fetchedMs int64) *pb.AgentUsage {
	type window struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    *string  `json:"resets_at"`
	}
	var u struct {
		FiveHour *window `json:"five_hour"`
		SevenDay *window `json:"seven_day"`
	}
	if json.Unmarshal(data, &u) != nil {
		return nil
	}
	a := &pb.AgentUsage{Harness: "claude", FetchedUnixMs: fetchedMs}
	for _, w := range []struct {
		label   string
		minutes int32
		w       *window
	}{{"5h", 300, u.FiveHour}, {"1w", 10080, u.SevenDay}} {
		if w.w == nil || w.w.Utilization == nil {
			continue
		}
		uw := &pb.UsageWindow{Label: w.label, UsedPercent: *w.w.Utilization, WindowMinutes: w.minutes}
		if w.w.ResetsAt != nil {
			if t, err := time.Parse(time.RFC3339Nano, *w.w.ResetsAt); err == nil {
				uw.ResetsUnix = t.Unix()
			}
		}
		a.Windows = append(a.Windows, uw)
	}
	if len(a.Windows) == 0 {
		return nil
	}
	return a
}

func codexSessionsDir() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return filepath.Join(d, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex", "sessions")
}

// newestCodexRollouts are up to n session files, newest first, from the
// latest days' folders (sessions/YYYY/MM/DD).
func newestCodexRollouts(dir string, n int) []string {
	type file struct {
		path string
		mod  time.Time
	}
	var days []string
	years, _ := os.ReadDir(dir)
	for i := len(years) - 1; i >= 0 && len(days) < 3; i-- {
		months, _ := os.ReadDir(filepath.Join(dir, years[i].Name()))
		for j := len(months) - 1; j >= 0 && len(days) < 3; j-- {
			ds, _ := os.ReadDir(filepath.Join(dir, years[i].Name(), months[j].Name()))
			for k := len(ds) - 1; k >= 0 && len(days) < 3; k-- {
				days = append(days, filepath.Join(dir, years[i].Name(), months[j].Name(), ds[k].Name()))
			}
		}
	}
	var files []file
	for _, day := range days {
		es, _ := os.ReadDir(day)
		for _, e := range es {
			if filepath.Ext(e.Name()) != ".jsonl" {
				continue // compressed ones are old
			}
			if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
				files = append(files, file{filepath.Join(day, e.Name()), info.ModTime()})
			}
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	out := make([]string, 0, n)
	for _, f := range files {
		if len(out) == n {
			break
		}
		out = append(out, f.path)
	}
	return out
}

// lastCodexUsage is the last rate_limits a Codex session file reported, read
// from its end.
func lastCodexUsage(path string) *pb.AgentUsage {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	const tail = 2 << 20
	off := max(st.Size()-tail, 0)
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil
	}
	var last []byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		if bytes.Contains(sc.Bytes(), []byte(`"rate_limits":{`)) {
			last = slices.Clone(sc.Bytes())
		}
	}
	if last == nil {
		return nil
	}
	return parseCodexUsage(last)
}

func parseCodexUsage(line []byte) *pb.AgentUsage {
	type window struct {
		UsedPercent   *float64 `json:"used_percent"`
		WindowMinutes int32    `json:"window_minutes"`
		ResetsAt      int64    `json:"resets_at"`
	}
	var l struct {
		Timestamp string `json:"timestamp"`
		Payload   struct {
			RateLimits *struct {
				Primary   *window `json:"primary"`
				Secondary *window `json:"secondary"`
			} `json:"rate_limits"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &l) != nil || l.Payload.RateLimits == nil {
		return nil
	}
	a := &pb.AgentUsage{Harness: "codex"}
	if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
		a.FetchedUnixMs = t.UnixMilli()
	}
	for _, w := range []*window{l.Payload.RateLimits.Primary, l.Payload.RateLimits.Secondary} {
		if w == nil || w.UsedPercent == nil {
			continue
		}
		a.Windows = append(a.Windows, &pb.UsageWindow{Label: windowLabel(w.WindowMinutes), UsedPercent: *w.UsedPercent,
			ResetsUnix: w.ResetsAt, WindowMinutes: w.WindowMinutes})
	}
	if len(a.Windows) == 0 {
		return nil
	}
	sort.SliceStable(a.Windows, func(i, j int) bool { return a.Windows[i].WindowMinutes < a.Windows[j].WindowMinutes })
	return a
}

// windowLabel names a window by its length: "5h", "1w", "30d".
func windowLabel(minutes int32) string {
	switch {
	case minutes <= 0:
		return "?"
	case minutes == 10080:
		return "1w"
	case minutes%1440 == 0:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	}
	return fmt.Sprintf("%dm", minutes)
}
