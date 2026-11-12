package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// A hook payload bigger than hookPayloadMax (a Write of a large file) still
// says its event, session and tool, with the long strings cut.
func TestReadHookPayloadSlimsLargePayloads(t *testing.T) {
	big := strings.Repeat("é", hookPayloadMax)
	in, _ := json.Marshal(map[string]any{
		"session_id":      "s1",
		"hook_event_name": "PermissionRequest",
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": "/w/big.txt", "content": big},
		"n":               3,
		"list":            []any{1, "a", nil, true},
	})
	out := readHookPayload(strings.NewReader(string(in)))
	if !json.Valid(out) || len(out) > hookPayloadMax {
		t.Fatalf("payload %d bytes, valid %v", len(out), json.Valid(out))
	}
	var p struct {
		SessionID string `json:"session_id"`
		Event     string `json:"hook_event_name"`
		Tool      string `json:"tool_name"`
		Input     struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		} `json:"tool_input"`
		N    int   `json:"n"`
		List []any `json:"list"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	if p.SessionID != "s1" || p.Event != "PermissionRequest" || p.Tool != "Write" || p.Input.FilePath != "/w/big.txt" || p.N != 3 || len(p.List) != 4 {
		t.Fatalf("slimmed to %+v", p)
	}
	if n := len(p.Input.Content); n == 0 || n > hookStringMax || !strings.HasPrefix(big, p.Input.Content) {
		t.Fatalf("content cut to %d bytes", n)
	}

	small := `{"hook_event_name":"Stop"}`
	if got := readHookPayload(strings.NewReader(small)); string(got) != small {
		t.Fatalf("small payload changed: %s", got)
	}
}

// JSON that ends early keeps what came before it, closed off.
func TestSlimJSONTruncated(t *testing.T) {
	for in, want := range map[string]string{
		`{"hook_event_name":"PreToolUse","tool_input":{"file_path":"/a","content":"abc`: `{"hook_event_name":"PreToolUse","tool_input":{"file_path":"/a"}}`,
		`{"a":[1,2,{"b":`: `{"a":[1,2,{}]}`,
		`{"a":"x","b"`:     `{"a":"x"}`,
		`[`:                `[]`,
		``:                 ``,
	} {
		if got := string(slimJSON(strings.NewReader(in), 10)); got != want {
			t.Errorf("slimJSON(%q) = %s, want %s", in, got, want)
		}
	}
}
