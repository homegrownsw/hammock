package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeUsage(t *testing.T) {
	a := parseClaudeUsage([]byte(`{"numStartups":3,"cachedUsageUtilization":{"fetchedAtMs":1793626064226,"utilization":{
		"five_hour":{"utilization":12,"resets_at":"2026-11-02T18:10:00.119305+00:00"},
		"seven_day":{"utilization":55,"resets_at":"2026-11-05T12:00:00.119321+00:00"},
		"seven_day_opus":null}}}`))
	if a == nil || len(a.Windows) != 2 || a.Windows[0].Label != "5h" || a.Windows[0].UsedPercent != 12 || a.Windows[1].Label != "1w" {
		t.Fatalf("%v", a)
	}
	if a.Windows[0].ResetsUnix != 1793643000 || a.FetchedUnixMs != 1793626064226 {
		t.Errorf("resets %d fetched %d", a.Windows[0].ResetsUnix, a.FetchedUnixMs)
	}
	if parseClaudeUsage([]byte(`{"numStartups":3}`)) != nil {
		t.Error("usage from nothing")
	}
}

func TestCodexUsage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026", "11", "02")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := `{"timestamp":"2026-11-02T09:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":40,"window_minutes":300,"resets_at":1793640000},"secondary":null}}}
{"timestamp":"2026-11-02T10:00:00Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":45.5,"window_minutes":10080,"resets_at":1794000000},"secondary":{"used_percent":20,"window_minutes":300,"resets_at":1793650000}}}}
{"timestamp":"2026-11-02T10:01:00Z","type":"response_item","payload":{"type":"message"}}
`
	f := filepath.Join(dir, "rollout-x.jsonl")
	if err := os.WriteFile(f, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	files := newestCodexRollouts(filepath.Dir(filepath.Dir(filepath.Dir(dir))), 4)
	if len(files) != 1 {
		t.Fatalf("files %v", files)
	}
	a := lastCodexUsage(files[0])
	if a == nil || len(a.Windows) != 2 || a.Windows[0].Label != "5h" || a.Windows[0].UsedPercent != 20 || a.Windows[1].Label != "1w" || a.Windows[1].UsedPercent != 45.5 {
		t.Fatalf("%v", a)
	}
	for m, want := range map[int32]string{300: "5h", 10080: "1w", 43200: "30d", 90: "90m", 1440: "1d"} {
		if got := windowLabel(m); got != want {
			t.Errorf("%d: %q", m, got)
		}
	}
}
