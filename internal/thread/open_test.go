package thread

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// Codex writes its rollouts compressed (.jsonl.zst): they're found and read
// like plain ones.
func TestCodexCompressedRollout(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "11", "01")
	os.MkdirAll(dir, 0o755)
	id := "01a19623-5ee1-73d5-95d1-a3f769975038"
	rollout := `{"timestamp":"2026-11-01T08:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"/tmp/x"}}
{"timestamp":"2026-11-01T08:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"add a --top flag"}]}}
{"timestamp":"2026-11-01T08:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Done."}]}}
`
	f, _ := os.Create(filepath.Join(dir, "rollout-2026-11-01T08-00-00-"+id+".jsonl.zst"))
	w, _ := zstd.NewWriter(f)
	w.Write([]byte(rollout))
	w.Close()
	f.Close()

	c := Codex{Home: home}
	path, ok := c.Locate(id)
	if !ok {
		t.Fatal("not found")
	}
	r, err := OpenTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	th, recs, err := c.Decode(r)
	if err != nil || th.NativeID != id || th.Cwd != "/tmp/x" || len(recs) != 2 || recs[0].Text != "add a --top flag" {
		t.Fatalf("%v %+v %+v", err, th, recs)
	}
}
