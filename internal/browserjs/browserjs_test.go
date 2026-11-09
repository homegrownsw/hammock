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
