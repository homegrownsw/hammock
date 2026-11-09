package thread

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*.records.json from the codecs' output")

// Golden files, one per harness version (testdata/golden/<harness>-<version>.jsonl):
// sessions in the shapes that version writes, hand-made (no real
// conversations), and what Decode reads from them. When a new version
// changes its format, add its fixture beside the old ones; the old ones
// keep sessions from before readable. `go test ./internal/thread -run
// Golden -update` rewrites the expected output; check its diff.
func TestGolden(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "golden", "*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no golden fixtures")
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		t.Run(name, func(t *testing.T) {
			harness, _, _ := strings.Cut(name, "-")
			var codec Codec
			switch harness {
			case "claude":
				codec = Claude{}
			case "codex":
				codec = Codex{Home: t.TempDir()} // no session names from this Mac
			default:
				t.Fatalf("unknown harness in %s", name)
			}
			r, err := os.Open(f)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			th, recs, err := codec.Decode(r)
			if err != nil {
				t.Fatal(err)
			}
			got := goldenView(th, recs)
			want := filepath.Join("testdata", "golden", name+".records.json")
			if *update {
				os.WriteFile(want, got, 0o644)
			}
			b, err := os.ReadFile(want)
			if err != nil {
				t.Fatalf("%v (run with -update to write it)", err)
			}
			if !bytes.Equal(b, got) {
				t.Errorf("%s decodes differently from %s:\n%s", f, want, got)
			}

			// And it converts into the other harness and back with every
			// message intact.
			other := map[string]Codec{"claude": Codex{Home: t.TempDir()}, "codex": Claude{Home: t.TempDir()}}[harness]
			th.Cwd = "/work/tally"
			_, _, data, err := other.Encode(th, recs)
			if err != nil {
				t.Fatal(err)
			}
			_, back, err := other.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if a, b := texts(recs), texts(back); !equalStrings(a, b) {
				t.Errorf("through %s and back:\n%q\nbecame\n%q", other.Harness(), a, b)
			}
		})
	}
}

// goldenView is what the golden files pin down: the session's identity and
// each record's kind, tool and gist.
func goldenView(th Thread, recs []Record) []byte {
	type rec struct {
		Kind   Kind   `json:"kind"`
		Tool   Tool   `json:"tool,omitempty"`
		Native string `json:"native,omitempty"`
		Text   string `json:"text,omitempty"`
		Input  string `json:"input,omitempty"`
		Edit   string `json:"edit,omitempty"`
		Call   string `json:"call,omitempty"`
		Error  bool   `json:"error,omitempty"`
	}
	out := struct {
		ID      string `json:"id"`
		Cwd     string `json:"cwd"`
		Title   string `json:"title,omitempty"`
		Named   bool   `json:"named,omitempty"`
		Records []rec  `json:"records"`
	}{ID: th.NativeID, Cwd: th.Cwd, Title: th.Title, Named: th.Named}
	for _, r := range recs {
		g := rec{Kind: r.Kind, Tool: r.Tool, Native: r.NativeTool, Text: r.Text, Input: string(r.Input), Call: r.CallID, Error: r.IsError}
		if r.Edit != nil {
			g.Edit = r.Edit.Path
		}
		out.Records = append(out.Records, g)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return append(b, '\n')
}

// texts are what was said: your prompts and the agent's replies.
func texts(recs []Record) []string {
	var out []string
	for _, r := range recs {
		if r.Kind == KindUser || r.Kind == KindAssistant {
			out = append(out, string(r.Kind)+": "+strings.TrimSpace(r.Text))
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
