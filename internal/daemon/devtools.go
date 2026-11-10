package daemon

// DevTools as a pane: a browser pane at hmk-devtools:<page> loads
// Chromium's own DevTools frontend (devtools://devtools/bundled/…), pointed
// at a WebSocket here; what it says goes to the page over the browser
// host's DevTools channel for it (the one agents' browser tools use), and
// what the page says comes back. Chromium's ShowDevTools, which would do
// this itself, fails a check when asked to draw into a pane's window.
//
// A DevTools pane is as powerful as its page: its frontend can run anything
// in it. So, for agents, a DevTools pane is its page (caller.go): opening one,
// and anything done in one (reads included), needs control of the page.
//
// The socket listens on 127.0.0.1 only. Each page has its own token, an
// HMAC of its pane id under a secret per daemon run: only that page's
// frontend address carries it, it opens only that page, and only for a
// client that says it's the DevTools frontend (its Origin). The address is
// kept out of the pane's URL and title; reading it otherwise (the page's
// own location) needs control of the page.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

type devtoolsBridge struct {
	h *browserHub

	once   sync.Once
	port   int
	secret []byte // the per-run key page tokens are made with
	err    error

	mu    sync.Mutex
	conns map[string]*devtoolsConn // page pane → its frontend
}

type devtoolsConn struct {
	ws  *websocket.Conn
	out chan string
}

// frontendURL is the DevTools frontend's address for page, starting the
// socket the first time.
func (b *devtoolsBridge) frontendURL(page string) string {
	b.once.Do(b.listen)
	if b.err != nil {
		return "about:blank"
	}
	return fmt.Sprintf("devtools://devtools/bundled/devtools_app.html?ws=127.0.0.1:%d/devtools/%s/%s", b.port, page, b.token(page))
}

// devtoolsOrigin is the Origin Chromium's DevTools frontend connects with.
const devtoolsOrigin = "devtools://devtools"

// token is page's: it opens that page's DevTools only.
func (b *devtoolsBridge) token(page string) string {
	m := hmac.New(sha256.New, b.secret)
	m.Write([]byte(page))
	return hex.EncodeToString(m.Sum(nil))
}

func (b *devtoolsBridge) listen() {
	b.secret = make([]byte, 32)
	_, _ = rand.Read(b.secret)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.err = err
		b.h.d.log.Warn("devtools bridge", "err", err)
		return
	}
	b.port = ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: http.HandlerFunc(b.serve), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	go func() {
		<-b.h.d.done
		srv.Close()
	}()
}

// serve takes a frontend's connection: /devtools/<page>/<token>.
func (b *devtoolsBridge) serve(w http.ResponseWriter, r *http.Request) {
	d := b.h.d
	// Only the frontend: a page in a browser sends its own origin, and a
	// program none (coder/websocket lets a missing one through).
	if o := r.Header.Get("Origin"); o != devtoolsOrigin {
		d.log.Warn("devtools bridge: refused a connection", "origin", o)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "devtools" || subtle.ConstantTimeCompare([]byte(parts[2]), []byte(b.token(parts[1]))) != 1 {
		http.NotFound(w, r)
		return
	}
	page := parts[1]
	d.mu.Lock()
	p := d.panes[page]
	d.mu.Unlock()
	if p == nil || !p.isBrowser() {
		http.NotFound(w, r)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"devtools"}})
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20)
	dc := &devtoolsConn{ws: ws, out: make(chan string, 4096)}
	b.mu.Lock()
	if b.conns == nil {
		b.conns = map[string]*devtoolsConn{}
	}
	if old := b.conns[page]; old != nil {
		old.ws.Close(websocket.StatusGoingAway, "another DevTools opened for this page")
	}
	b.conns[page] = dc
	b.mu.Unlock()
	b.h.send(map[string]any{"t": "dtBridge", "id": page, "on": true})

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-dc.out:
				wctx, c := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Write(wctx, websocket.MessageText, []byte(m))
				c()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	for {
		_, msg, err := ws.Read(ctx)
		if err != nil {
			break
		}
		b.h.send(map[string]any{"t": "dtIn", "id": page, "msg": string(msg)})
	}
	b.mu.Lock()
	if b.conns[page] == dc {
		delete(b.conns, page)
		b.h.send(map[string]any{"t": "dtBridge", "id": page, "on": false})
	}
	b.mu.Unlock()
	ws.CloseNow()
}

// deliver passes the page's reply or event to its frontend; a frontend
// that's far behind is dropped (it reconnects when reopened).
func (b *devtoolsBridge) deliver(page, msg string) {
	b.mu.Lock()
	dc := b.conns[page]
	b.mu.Unlock()
	if dc == nil {
		return
	}
	select {
	case dc.out <- msg:
	default:
		dc.ws.Close(websocket.StatusPolicyViolation, "too far behind")
	}
}

// DevToolsPage is the page a DevTools pane's address (hmk-devtools:<page>)
// inspects, as the pane would be made from it.
func DevToolsPage(u string) (string, bool) {
	if u == "" {
		return "", false
	}
	if page, ok := strings.CutPrefix(BrowserURL(u), DevToolsURL); ok {
		return page, true
	}
	return "", false
}

// specDevTools is the page a new pane made from spec would inspect ("" if
// it wouldn't be a DevTools pane: startPane makes a browser of any URL).
func specDevTools(spec *pb.PaneSpec) string {
	if spec == nil {
		return ""
	}
	if page, ok := DevToolsPage(spec.Url); ok {
		return page
	}
	return ""
}

// devtoolsPage is the page DevTools pane id inspects, "" if it isn't one.
// Callers hold d.mu.
func (d *Daemon) devtoolsPage(id string) string {
	p := d.panes[id]
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.info.Kind != pb.PaneKind_PANE_KIND_BROWSER {
		return ""
	}
	if page, ok := strings.CutPrefix(p.info.Url, DevToolsURL); ok {
		return page
	}
	return ""
}

// isDevToolsAddress reports whether a page's address is a DevTools pane's
// (as Chromium writes it: schemes in lower case).
func isDevToolsAddress(u string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(u)), DevToolsURL)
}

// devtoolsTitle is what a DevTools pane is called: the frontend's own
// title ("DevTools - example.com"), never its address (which carries the
// page's token: Chromium titles an untitled page by its address).
func devtoolsTitle(title string) string {
	if strings.HasPrefix(title, "DevTools") && !strings.Contains(title, "devtools://") && !strings.Contains(title, "/devtools/") {
		return title
	}
	return "DevTools"
}

// hostURL is what the browser host loads for a pane: a DevTools pane's
// frontend, else the pane's own address.
func (h *browserHub) hostURL(u string) string {
	if page, ok := strings.CutPrefix(u, DevToolsURL); ok {
		return h.devtools.frontendURL(page)
	}
	return u
}

// closeOrphanDevTools closes DevTools panes whose page has closed.
func (d *Daemon) closeOrphanDevTools() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, p := range d.panes {
		if page, ok := strings.CutPrefix(p.snapshotInfo().Url, DevToolsURL); ok && p.isBrowser() {
			if _, open := d.panes[page]; !open {
				_ = d.closePane(id)
			}
		}
	}
}
