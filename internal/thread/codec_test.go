package thread

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func decodeClaudeFixture(t *testing.T) (Thread, []Record) {
	t.Helper()
	f, err := os.Open("testdata/claude.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	th, recs, err := Claude{}.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return th, recs
}

func kinds(recs []Record) string {
	var s []string
	for _, r := range recs {
		if r.Kind == KindReasoning {
			continue
		}
		s = append(s, string(r.Kind)+":"+string(r.Tool))
	}
	return strings.Join(s, " ")
}

func TestClaudeDecode(t *testing.T) {
	th, recs := decodeClaudeFixture(t)
	if th.NativeID != "sess-claude-1" || th.Cwd != "/src/app" || th.Title != "Ring buffer fix" {
		t.Fatalf("thread %+v", th)
	}
	want := "user: assistant: tool_call:shell tool_result: tool_call:edit_file tool_result: assistant:"
	if got := kinds(recs); got != want {
		t.Fatalf("records\n got %s\nwant %s", got, want)
	}
	for _, r := range recs {
		if strings.Contains(r.Text, "secret") || strings.Contains(r.Text, "sidechain") || strings.Contains(r.Text, "system-reminder") {
			t.Fatalf("leaked hidden content: %+v", r)
		}
	}
	vis := visible(recs)
	if vis[4].Edit == nil || vis[4].Edit.Path != "/src/app/holder/ring.go" || vis[5].Text != "The file has been updated." || !vis[3].IsError {
		t.Fatalf("edit/results wrong: %+v %+v %+v", vis[4], vis[5], vis[3])
	}
	if recs[1].Kind != KindReasoning {
		t.Fatal("thinking not kept as a reasoning marker")
	}
}

// lines decodes JSONL into generic objects, failing on invalid JSON.
func lines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("invalid line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func TestClaudeToCodexRoundTrip(t *testing.T) {
	src, recs := decodeClaudeFixture(t)
	for _, paginated := range []bool{false, true} {
		p := paginated
		nt := Thread{ID: NewID(), Cwd: "/src/app", Parent: src.ID, Source: "claude"}
		id, path, data, err := Codex{Home: "/home/x/.codex", Paginated: &p}.Encode(nt, recs)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(path, "/home/x/.codex/sessions/") || !strings.HasSuffix(path, "-"+id+".jsonl") {
			t.Fatalf("path %s", path)
		}
		ls := lines(t, data)
		meta := ls[0]["payload"].(map[string]any)
		if ls[0]["type"] != "session_meta" || meta["id"] != id || meta["cwd"] != "/src/app" {
			t.Fatalf("meta %v", ls[0])
		}
		if _, has := ls[1]["ordinal"]; has != paginated {
			t.Fatalf("ordinal presence wrong for paginated=%v", paginated)
		}
		// Every function_call has exactly one output.
		calls, outputs := map[string]int{}, map[string]int{}
		for _, l := range ls {
			if pl, ok := l["payload"].(map[string]any); ok {
				switch pl["type"] {
				case "function_call":
					calls[pl["call_id"].(string)]++
					var args map[string]any
					if json.Unmarshal([]byte(pl["arguments"].(string)), &args) != nil {
						t.Fatalf("arguments not a JSON object: %v", pl)
					}
				case "function_call_output":
					outputs[pl["call_id"].(string)]++
				}
			}
		}
		for id, n := range calls {
			if n != 1 || outputs[id] != 1 {
				t.Fatalf("call %s: %d calls, %d outputs", id, n, outputs[id])
			}
		}
		_, back, err := Codex{}.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		// The Claude Edit has no Codex equivalent and stays a named function call.
		if got, want := kinds(back), "user: assistant: tool_call:shell tool_result: tool_call: tool_result: assistant:"; got != want {
			t.Fatalf("paginated=%v round trip\n got %s\nwant %s", paginated, got, want)
		}
		if vb := visible(back); vb[0].Text != "Fix the failing ring buffer test" || vb[6].Text != "Fixed the off-by-one; tests pass now." {
			t.Fatalf("texts lost: %+v", back)
		}
		if !strings.Contains(string(data), `"name":"Edit"`) || !strings.Contains(string(data), "go test ./holder/") {
			t.Fatalf("tool content lost:\n%s", data)
		}
	}
}

func TestCodexToClaude(t *testing.T) {
	_, recs := decodeClaudeFixture(t)
	p := false
	_, _, data, _ := Codex{Paginated: &p}.Encode(Thread{Cwd: "/src/app", Source: "claude"}, recs)
	_, crecs, _ := Codex{}.Decode(bytes.NewReader(data))
	sid, path, out, err := Claude{Home: "/h/.claude"}.Encode(Thread{Cwd: "/src/app", Source: "codex", Title: "Ring"}, crecs)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/h/.claude/projects/-src-app/"+sid+".jsonl" {
		t.Fatalf("path %s", path)
	}
	ls := lines(t, out)
	// Turns alternate (after the meta line), parentUuid chains, tool_uses answered.
	prevRole, prevUUID := "", ""
	pending := map[string]bool{}
	for i, l := range ls {
		typ, _ := l["type"].(string)
		if typ == "ai-title" {
			continue
		}
		if i > 0 && l["parentUuid"] != prevUUID {
			t.Fatalf("line %d breaks the parent chain", i)
		}
		prevUUID, _ = l["uuid"].(string)
		if l["isMeta"] == true {
			continue
		}
		if typ == prevRole {
			t.Fatalf("line %d: two %s lines in a row", i, typ)
		}
		prevRole = typ
		msg := l["message"].(map[string]any)
		if blocks, ok := msg["content"].([]any); ok {
			for _, b := range blocks {
				bm := b.(map[string]any)
				switch bm["type"] {
				case "tool_use":
					pending[bm["id"].(string)] = true
				case "tool_result":
					delete(pending, bm["tool_use_id"].(string))
				case "thinking":
					t.Fatal("thinking block written")
				}
			}
		}
	}
	if len(pending) != 0 {
		t.Fatalf("unanswered tool_use: %v", pending)
	}
	_, back, err := Claude{}.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := kinds(back), "user: assistant: tool_call:shell tool_result: tool_call: tool_result: assistant:"; got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestClaudeProjectDir(t *testing.T) {
	if got := claudeProjectDir("/Users/me/Developer/hammock"); got != "-Users-me-Developer-hammock" {
		t.Fatal(got)
	}
	if got := claudeProjectDir("/a/my.proj_x"); got != "-a-my-proj-x" {
		t.Fatal(got)
	}
	long := "/" + strings.Repeat("d", 250)
	if got := claudeProjectDir(long); len(got) <= 200 || !strings.HasPrefix(got, "-ddd") || got[200] != '-' {
		t.Fatal(got)
	}
}

func TestUnwrapShell(t *testing.T) {
	if unwrapShell([]string{"bash", "-lc", "ls -la"}) != "ls -la" || unwrapShell([]string{"ls", "-la"}) != "ls -la" {
		t.Fatal("unwrap")
	}
}

func visible(recs []Record) []Record {
	var out []Record
	for _, r := range recs {
		if r.Kind != KindReasoning {
			out = append(out, r)
		}
	}
	return out
}

func TestCodexDecodeAndToClaude(t *testing.T) {
	f, err := os.Open("testdata/codex.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	th, recs, err := Codex{}.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if th.NativeID != "0193a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b" || th.Cwd != "/src/app" {
		t.Fatalf("thread %+v", th)
	}
	vis := visible(recs)
	if got, want := kinds(recs), "user: tool_call:shell tool_result: tool_call: tool_result: assistant:"; got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	if vis[0].Text != "Add a README section on building" || !strings.Contains(string(vis[1].Input), `"ls"`) || vis[2].Text != "README.md\ngo.mod\n" {
		t.Fatalf("decoded wrong: %+v", vis[:3])
	}
	if vis[3].NativeTool != "apply_patch" || vis[3].Edit == nil || vis[3].Edit.Path != "README.md" {
		t.Fatalf("patch call: %+v", vis[3])
	}
	_, _, out, err := Claude{Home: "/h"}.Encode(Thread{Cwd: th.Cwd, Source: "codex"}, recs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"name":"Bash"`) || !strings.Contains(string(out), `"command":"ls"`) || !strings.Contains(string(out), `"name":"apply_patch"`) {
		t.Fatalf("tool mapping lost:\n%s", out)
	}
	if b := Brief(th, recs); !strings.Contains(b, "README.md") {
		t.Fatalf("brief lacks patched file:\n%s", b)
	}
}
