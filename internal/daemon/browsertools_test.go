package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBrowserLogs(t *testing.T) {
	h := &browserHub{}
	ev := func(method, params string) {
		h.event(hostMsg{ID: "pn1", Method: method, Params: json.RawMessage(params)})
	}
	ev("Runtime.consoleAPICalled", `{"type":"log","args":[{"type":"string","value":"clicked"},{"type":"number","value":42}],"stackTrace":{"callFrames":[{"url":"http://x/app.js","lineNumber":9}]}}`)
	ev("Runtime.exceptionThrown", `{"exceptionDetails":{"text":"Uncaught","exception":{"description":"Error: boom"}}}`)
	ev("Network.requestWillBeSent", `{"requestId":"1","type":"Fetch","request":{"url":"http://x/api","method":"POST"}}`)
	ev("Network.responseReceived", `{"requestId":"1","response":{"status":500,"mimeType":"application/json"}}`)
	ev("Network.loadingFinished", `{"requestId":"1","encodedDataLength":120}`)
	ev("Network.requestWillBeSent", `{"requestId":"2","request":{"url":"data:image/png;base64,xx","method":"GET"}}`)

	c, _ := h.readLogs("pn1", "console", false)
	if !strings.Contains(c, `"text":"clicked 42"`) || !strings.Contains(c, `"source":"http://x/app.js:10"`) || !strings.Contains(c, `Error: boom`) {
		t.Fatalf("console %s", c)
	}
	n, _ := h.readLogs("pn1", "network", true)
	if !strings.Contains(n, `"status":500`) || !strings.Contains(n, `"bytes":120`) || strings.Contains(n, "data:") {
		t.Fatalf("network %s", n)
	}
	if n, _ := h.readLogs("pn1", "network", false); n != "[]" {
		t.Fatalf("not cleared: %s", n)
	}
}
