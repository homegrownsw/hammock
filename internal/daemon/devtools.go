package daemon

// DevTools as a pane: a browser pane at hmk-devtools:<page> loads
// Chromium's own DevTools frontend (devtools://devtools/bundled/…), pointed
// at a WebSocket here; what it says goes to the page over the browser
// host's DevTools channel for it (the one agents' browser tools use), and
// what the page says comes back. Chromium's ShowDevTools, which would do
// this itself, fails a check when asked to draw into a pane's window.
//
// The socket listens on 127.0.0.1 only, at a path with a secret per daemon
// run, which only the frontend's address carries.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type devtoolsBridge struct {
	h *browserHub

	once  sync.Once
	port  int
	token string
	err   error

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
	return fmt.Sprintf("devtools://devtools/bundled/devtools_app.html?ws=127.0.0.1:%d/devtools/%s/%s", b.port, page, b.token)
}

func (b *devtoolsBridge) listen() {
	t := make([]byte, 16)
	_, _ = rand.Read(t)
	b.token = hex.EncodeToString(t)
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
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "devtools" || subtle.ConstantTimeCompare([]byte(parts[2]), []byte(b.token)) != 1 {
		http.NotFound(w, r)
		return
	}
	page := parts[1]
	d := b.h.d
	d.mu.Lock()
	p := d.panes[page]
	d.mu.Unlock()
	if p == nil || !p.isBrowser() {
		http.NotFound(w, r)
		return
	}
	// The frontend's origin is devtools://devtools.
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
