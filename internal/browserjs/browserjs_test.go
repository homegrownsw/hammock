package browserjs

import (
	"encoding/json"
	"testing"
)

func params(expr string, extra map[string]any) string {
	p := map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true, "userGesture": true}
	for k, v := range extra {
		p[k] = v
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func TestReadOnly(t *testing.T) {
	for _, expr := range []string{Snapshot, DevicePixelRatio, WaitFor("Sign in", ""), WaitFor("", "#main > .x[data-y='\"z\"']"),
		WaitFor(`")); fetch("//evil"); ((`, "")} {
		if !ReadOnly(params(expr, nil)) {
			t.Errorf("not read-only: %.60s", expr)
		}
	}
	for _, p := range []string{
		params("document.cookie", nil),
		params(Snapshot+";fetch('//x')", nil),
		params(waitForJS+`"a","b"]);fetch("//x");([1])`, nil),
		params(waitForJS+`1,2])`, nil),
		params(waitForJS+`"a","b","c"])`, nil),
		params(Snapshot, map[string]any{"contextId": 2}),
		params(Snapshot, map[string]any{"includeCommandLineAPI": true}),
		`{"expression": 1}`,
		`nonsense`,
	} {
		if ReadOnly(p) {
			t.Errorf("read-only: %.80s", p)
		}
	}
}

// A key named twice: Go keeps the last, Chromium may keep the first, so
// it's refused; and what's sent on is re-encoded from what was checked.
func TestReadOnlyParamsDuplicateKeys(t *testing.T) {
	for _, p := range []string{
		`{"expression":"evil()","expression":"window.devicePixelRatio"}`,
		`{"expression":"evil()","\u0065xpression":"window.devicePixelRatio"}`,
		`{"expression":"window.devicePixelRatio","returnByValue":true,"returnByValue":true}`,
		`{"expression":"window.devicePixelRatio","returnByValue":"yes"}`,
		`{"expression":"window.devicePixelRatio","Expression":"evil()"}`,
		`{"expression":"window.devicePixelRatio"} {"expression":"evil()"}`,
	} {
		if out, ok := ReadOnlyParams(p); ok {
			t.Errorf("read-only: %s (sent as %s)", p, out)
		}
	}
	in := params(Snapshot, nil)
	out, ok := ReadOnlyParams(in)
	if !ok {
		t.Fatal("the snapshot isn't read-only")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["expression"] != Snapshot || got["returnByValue"] != true || len(got) != 4 {
		t.Fatalf("sent as %.80s (%v)", out, err)
	}
}
