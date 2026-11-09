package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// DevTools protocol calls on browser panes, for agents (hmk mcp's browser
// tools). Calls go to the browser host over its socket and through CEF's
// in-process DevTools channel; no debugging port is ever opened. The host
// also streams each page's console and network events, kept here so an
// agent can read what happened before it looked.

const (
	maxConsole = 500
	maxNetwork = 300
)

type cdpReply struct {
	ok     bool
	result json.RawMessage
}

type consoleEntry struct {
	Time   string `json:"time"`
	Level  string `json:"level"`
	Text   string `json:"text"`
	Source string `json:"source,omitempty"` // url:line
}

type networkEntry struct {
	id       string
	Time     string `json:"time"`
	Method   string `json:"method"`
	URL      string `json:"url"`
	Type     string `json:"type,omitempty"`
	Status   int    `json:"status,omitempty"`
	MIME     string `json:"mime,omitempty"`
	Failed   string `json:"failed,omitempty"`
	Bytes    int64  `json:"bytes,omitempty"`
	Finished bool   `json:"finished"`
}

type pageLog struct {
	console []consoleEntry
	network []*networkEntry
}

// call runs a DevTools method on pane id's page and returns its result.
func (h *browserHub) call(id, method string, params json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	h.ensure()
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ch := make(chan cdpReply, 1)
	h.mu.Lock()
	if h.calls == nil {
		h.calls = map[int]chan cdpReply{}
	}
	h.seq++
	seq := h.seq
	h.calls[seq] = ch
	connected := h.c != nil
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.calls, seq)
		h.mu.Unlock()
	}()
	if !connected {
		// The host may be starting: give it a moment.
		for i := 0; i < 50 && !connected; i++ {
			time.Sleep(100 * time.Millisecond)
			h.mu.Lock()
			connected = h.c != nil
			h.mu.Unlock()
		}
		if !connected {
			return nil, errors.New("the browser isn't running")
		}
	}
	h.send(map[string]any{"t": "cdp", "id": id, "seq": seq, "method": method, "params": params})
	select {
	case r := <-ch:
		if !r.ok {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(r.result, &e)
			if e.Message == "" {
				e.Message = string(r.result)
			}
			return nil, fmt.Errorf("%s: %s", method, e.Message)
		}
		return r.result, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("%s: no answer from the page after %s", method, timeout)
	}
}

// reply hands a call's result to its caller.
func (h *browserHub) reply(m hostMsg) {
	h.mu.Lock()
	ch := h.calls[m.Seq]
	h.mu.Unlock()
	if ch != nil {
		ch <- cdpReply{ok: m.OK, result: m.Result}
	}
}

// failCalls ends every call in flight (the host went away).
func (h *browserHub) failCalls() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for seq, ch := range h.calls {
		select {
		case ch <- cdpReply{result: json.RawMessage(`{"message":"the browser stopped"}`)}:
		default:
		}
		delete(h.calls, seq)
	}
}

// event records a page's console and network activity.
func (h *browserHub) event(m hostMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.logs == nil {
		h.logs = map[string]*pageLog{}
	}
	l := h.logs[m.ID]
	if l == nil {
		l = &pageLog{}
		h.logs[m.ID] = l
	}
	now := time.Now().Format("15:04:05.000")
	switch m.Method {
	case "Runtime.consoleAPICalled":
		var p struct {
			Type       string         `json:"type"`
			Args       []remoteObject `json:"args"`
			StackTrace *stackTrace    `json:"stackTrace"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		var parts []string
		for _, a := range p.Args {
			parts = append(parts, a.text())
		}
		l.addConsole(consoleEntry{Time: now, Level: p.Type, Text: strings.Join(parts, " "), Source: p.StackTrace.top()})
	case "Runtime.exceptionThrown":
		var p struct {
			Details struct {
				Text       string        `json:"text"`
				URL        string        `json:"url"`
				Line       int           `json:"lineNumber"`
				Exception  *remoteObject `json:"exception"`
				StackTrace *stackTrace   `json:"stackTrace"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		d := p.Details
		text := d.Text
		if d.Exception != nil {
			text = d.Exception.text()
		}
		src := d.StackTrace.top()
		if src == "" && d.URL != "" {
			src = fmt.Sprintf("%s:%d", d.URL, d.Line+1)
		}
		l.addConsole(consoleEntry{Time: now, Level: "exception", Text: text, Source: src})
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
				Line  int    `json:"lineNumber"`
			} `json:"entry"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		src := ""
		if p.Entry.URL != "" {
			src = fmt.Sprintf("%s:%d", p.Entry.URL, p.Entry.Line+1)
		}
		l.addConsole(consoleEntry{Time: now, Level: p.Entry.Level, Text: p.Entry.Text, Source: src})
	case "Network.requestWillBeSent":
		var p struct {
			ID      string `json:"requestId"`
			Type    string `json:"type"`
			Request struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
		}
		if json.Unmarshal(m.Params, &p) != nil || strings.HasPrefix(p.Request.URL, "data:") {
			return
		}
		l.network = append(l.network, &networkEntry{id: p.ID, Time: now, Method: p.Request.Method, URL: p.Request.URL, Type: p.Type})
		if len(l.network) > maxNetwork {
			l.network = l.network[len(l.network)-maxNetwork:]
		}
	case "Network.responseReceived":
		var p struct {
			ID       string `json:"requestId"`
			Response struct {
				Status int    `json:"status"`
				MIME   string `json:"mimeType"`
			} `json:"response"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			if e := l.request(p.ID); e != nil {
				e.Status, e.MIME = p.Response.Status, p.Response.MIME
			}
		}
	case "Network.loadingFinished":
		var p struct {
			ID    string  `json:"requestId"`
			Bytes float64 `json:"encodedDataLength"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			if e := l.request(p.ID); e != nil {
				e.Finished, e.Bytes = true, int64(p.Bytes)
			}
		}
	case "Network.loadingFailed":
		var p struct {
			ID       string `json:"requestId"`
			Error    string `json:"errorText"`
			Canceled bool   `json:"canceled"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			if e := l.request(p.ID); e != nil {
				e.Finished, e.Failed = true, p.Error
				if p.Canceled {
					e.Failed = "canceled"
				}
			}
		}
	}
}

func (l *pageLog) addConsole(e consoleEntry) {
	if len(e.Text) > 4000 {
		e.Text = e.Text[:4000] + "…"
	}
	l.console = append(l.console, e)
	if len(l.console) > maxConsole {
		l.console = l.console[len(l.console)-maxConsole:]
	}
}

// request finds a request by id, newest first (redirects reuse ids).
func (l *pageLog) request(id string) *networkEntry {
	for i := len(l.network) - 1; i >= 0; i-- {
		if l.network[i].id == id {
			return l.network[i]
		}
	}
	return nil
}

// readLogs returns pane id's console or network log as JSON.
func (h *browserHub) readLogs(id, kind string, clear bool) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.logs[id]
	if l == nil {
		l = &pageLog{}
	}
	var v any
	switch kind {
	case "console":
		v = l.console
		if clear {
			l.console = nil
		}
	case "network":
		v = l.network
		if clear {
			l.network = nil
		}
	default:
		return "", fmt.Errorf("unknown log %q (console or network)", kind)
	}
	b, err := json.Marshal(v)
	if string(b) == "null" {
		b = []byte("[]")
	}
	return string(b), err
}

func (h *browserHub) forget(id string) {
	h.mu.Lock()
	delete(h.logs, id)
	h.mu.Unlock()
}

type remoteObject struct {
	Type         string          `json:"type"`
	Value        json.RawMessage `json:"value"`
	Unserialized string          `json:"unserializableValue"`
	Description  string          `json:"description"`
}

// text renders a console argument the way DevTools would, roughly.
func (o remoteObject) text() string {
	if len(o.Value) > 0 {
		var s string
		if json.Unmarshal(o.Value, &s) == nil {
			return s
		}
		return string(o.Value)
	}
	if o.Unserialized != "" {
		return o.Unserialized
	}
	if o.Description != "" {
		return o.Description
	}
	return o.Type
}

type stackTrace struct {
	Frames []struct {
		URL  string `json:"url"`
		Line int    `json:"lineNumber"`
	} `json:"callFrames"`
}

func (s *stackTrace) top() string {
	if s == nil {
		return ""
	}
	for _, f := range s.Frames {
		if f.URL != "" {
			return fmt.Sprintf("%s:%d", f.URL, f.Line+1)
		}
	}
	return ""
}

// browserPane returns pane id if it's a browser pane.
func (c *conn) browserPane(id string) (*pane, error) {
	p, err := c.pane(id)
	if err != nil {
		return nil, err
	}
	if !p.isBrowser() {
		return nil, errors.New("not a browser pane")
	}
	return p, nil
}

// browserCall runs a DevTools call off the read loop (a page can take a
// while) and replies when it's done.
func (d *Daemon) browserCall(c *conn, reqID uint64, m *pb.BrowserCall) error {
	p, err := c.browserPane(m.PaneId)
	if err != nil {
		return err
	}
	var params json.RawMessage
	if m.ParamsJson != "" {
		if !json.Valid([]byte(m.ParamsJson)) {
			return errors.New("params_json isn't valid JSON")
		}
		params = json.RawMessage(m.ParamsJson)
	}
	// Replies are deferred, so the read loop doesn't audit this: do it here.
	if c.isAgent() && browserCallMutates(m.Method) {
		d.mu.Lock()
		d.audit(c.auditActor(), "browser "+m.Method, p.id, "")
		d.mu.Unlock()
	}
	go func() {
		r, err := d.browsers.call(p.id, m.Method, params, time.Duration(m.TimeoutMs)*time.Millisecond)
		if err != nil {
			c.replyErr(reqID, err)
			return
		}
		c.send(&pb.ServerMessage{ReplyTo: reqID, Msg: &pb.ServerMessage_BrowserResult{BrowserResult: &pb.BrowserResult{Json: string(r)}}})
	}()
	return errDeferred
}

// browserInput passes a remote viewer's input (your phone, a guest with
// Drive) to the page as DevTools Input events. It runs on the read loop and
// doesn't wait, so a drag's events stay in order.
func (d *Daemon) browserInput(c *conn, m *pb.BrowserInput) error {
	p, err := c.browserPane(m.PaneId)
	if err != nil {
		return err
	}
	if !slices.Contains([]string{"mouse", "wheel", "key", "text", "compose", "copy", "cut"}, m.Op) {
		return fmt.Errorf("unknown op %q (mouse, wheel, key, text, compose, copy, cut)", m.Op)
	}
	// Typed and re-marshalled, so the helper only ever sees the fields and
	// types it expects (a wrong type would throw in it and close every page).
	var a remoteInputArgs
	if m.ArgsJson != "" {
		if err := json.Unmarshal([]byte(m.ArgsJson), &a); err != nil {
			return fmt.Errorf("args_json: %w", err)
		}
	}
	if err := a.check(); err != nil {
		return err
	}
	b, _ := json.Marshal(a)
	args := map[string]any{}
	_ = json.Unmarshal(b, &args)
	// Clicks, special keys and pasted text, not every move or letter.
	notable := m.Op == "text" || m.Op == "cut" || (m.Op == "mouse" && a.Type == "down") || (m.Op == "key" && a.Type == "down" && a.Text == "")
	d.mu.Lock()
	peer := c.peer
	if notable && (peer != "" || c.isAgent()) {
		d.audit(c.auditActor(), "browser "+m.Op, p.id, "")
	}
	d.mu.Unlock()
	d.browsers.noteInput(p.id, c)
	args["t"], args["id"], args["op"] = "rinput", p.id, m.Op
	d.browsers.send(args)
	return nil
}

// remoteInputArgs is what a BrowserInput's args_json may carry.
type remoteInputArgs struct {
	Type     string   `json:"type,omitempty"` // mouse and key: down, up, move
	X        float64  `json:"x"`              // fractions of the page
	Y        float64  `json:"y"`
	DX       float64  `json:"dx,omitempty"`
	DY       float64  `json:"dy,omitempty"`
	Mods     []string `json:"mods,omitempty"`
	Button   string   `json:"button,omitempty"`
	Clicks   int      `json:"clicks,omitempty"`
	Pressed  bool     `json:"pressed,omitempty"`
	Key      string   `json:"key,omitempty"`
	Code     string   `json:"code,omitempty"`
	Text     string   `json:"text,omitempty"`
	VK       int      `json:"vk,omitempty"`
	Commands []string `json:"commands,omitempty"`
	Start    int      `json:"start,omitempty"`
	End      int      `json:"end,omitempty"`
}

// remoteKeyCommands are the editing commands a remote key may carry. Not
// copy, cut or paste: those would use the host Mac's clipboard (a viewer's
// copy goes back to the viewer, and pastes come as text).
var remoteKeyCommands = []string{"selectAll", "undo", "redo"}

func (a *remoteInputArgs) check() error {
	if !slices.Contains([]string{"", "down", "up", "move"}, a.Type) {
		return fmt.Errorf("unknown type %q", a.Type)
	}
	if !slices.Contains([]string{"", "left", "right", "middle"}, a.Button) {
		return fmt.Errorf("unknown button %q", a.Button)
	}
	for _, c := range a.Commands {
		if !slices.Contains(remoteKeyCommands, c) {
			return fmt.Errorf("command %q isn't allowed remotely", c)
		}
	}
	a.Clicks = min(max(a.Clicks, 0), 3)
	return nil
}

// Menus a page opens in answer to remote input (a <select>, a right-click)
// go to whoever gave that input, not to the Mac showing the page.

type hostMenuItem struct {
	Title   string `json:"title"`
	Enabled bool   `json:"enabled"`
	Sep     bool   `json:"sep"`
	Indent  int    `json:"indent"`
	Hidden  bool   `json:"hidden"`
	Checked bool   `json:"checked"`
}

type remoteMenu struct {
	c     *conn
	pane  string
	kind  string // select or context
	index []int  // shown item → the host's index
}

func (h *browserHub) noteInput(paneID string, c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inputs == nil {
		h.inputs = map[string]*conn{}
	}
	h.inputs[paneID] = c
}

// menu passes a menu from the host to the viewer whose input opened it.
func (h *browserHub) menu(m hostMsg) {
	kind := "select"
	if m.T == "context" {
		kind = "context"
	}
	h.mu.Lock()
	c := h.inputs[m.ID]
	h.mu.Unlock()
	if c == nil {
		h.answerMenu(kind, m.Seq, -1)
		return
	}
	bm := &pb.BrowserMenu{PaneId: m.ID, Seq: int32(m.Seq), Kind: kind, Selected: -1, X: m.Fx, Y: m.Fy, W: m.Fw, H: m.Fh}
	var index []int
	for i, it := range m.Items {
		if it.Hidden {
			continue
		}
		if kind == "select" && i == m.Index {
			bm.Selected = int32(len(index))
		}
		index = append(index, i)
		bm.Items = append(bm.Items, &pb.BrowserMenuItem{Title: it.Title, Enabled: it.Enabled, Separator: it.Sep, Indent: int32(it.Indent), Checked: it.Checked})
	}
	h.mu.Lock()
	if h.menus == nil {
		h.menus = map[int]*remoteMenu{}
	}
	h.menus[m.Seq] = &remoteMenu{c: c, pane: m.ID, kind: kind, index: index}
	h.mu.Unlock()
	c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_BrowserMenu{BrowserMenu: bm}})
}

func (h *browserHub) answerMenu(kind string, seq, index int) {
	t := "menuResult"
	if kind == "context" {
		t = "contextResult"
	}
	h.send(map[string]any{"t": t, "seq": seq, "index": index})
}

// menuChoice is a viewer's answer to a BrowserMenu.
func (h *browserHub) menuChoice(c *conn, m *pb.BrowserMenuChoice) error {
	h.mu.Lock()
	rm := h.menus[int(m.Seq)]
	if rm == nil || rm.c != c || rm.pane != m.PaneId {
		h.mu.Unlock()
		return errors.New("no such menu")
	}
	delete(h.menus, int(m.Seq))
	h.mu.Unlock()
	index := -1
	if i := int(m.Index); i >= 0 && i < len(rm.index) {
		index = rm.index[i]
	}
	h.answerMenu(rm.kind, int(m.Seq), index)
	return nil
}

// closeMenusFor dismisses the menus a closing connection was showing, so
// the page isn't left waiting.
func (h *browserHub) closeMenusFor(c *conn) {
	h.mu.Lock()
	var gone []*remoteMenu
	var seqs []int
	for seq, rm := range h.menus {
		if rm.c == c {
			gone = append(gone, rm)
			seqs = append(seqs, seq)
			delete(h.menus, seq)
		}
	}
	for p, ic := range h.inputs {
		if ic == c {
			delete(h.inputs, p)
		}
	}
	h.mu.Unlock()
	for i, rm := range gone {
		h.answerMenu(rm.kind, seqs[i], -1)
	}
}

// clipboard passes text a remote viewer copied to that viewer.
func (h *browserHub) clipboard(m hostMsg) {
	h.mu.Lock()
	c := h.inputs[m.ID]
	h.mu.Unlock()
	if c != nil && m.Text != "" {
		c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_BrowserClipboard{BrowserClipboard: &pb.BrowserClipboard{PaneId: m.ID, Text: m.Text}}})
	}
}
