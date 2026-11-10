package daemon

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/alexjsp/hammock/internal/wirezstd"
)

// transport carries whole protobuf messages for a conn.
type transport interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, b []byte) error
	Close() error
}

type wsTransport struct {
	ws *websocket.Conn
	// With the compressed subprotocol (remote links that offer it): set
	// before the conn starts, then used only by its read and write loops.
	zw *wirezstd.Writer
	zr *wirezstd.Reader
}

func newWSTransport(ws *websocket.Conn, readLimit int) *wsTransport {
	t := &wsTransport{ws: ws}
	if ws.Subprotocol() == wirezstd.Subprotocol {
		t.zw, t.zr = wirezstd.NewWriter(), wirezstd.NewReader(readLimit)
	}
	return t
}

// setReadLimit bounds a message's size, compressed or not.
func (t *wsTransport) setReadLimit(n int) {
	t.ws.SetReadLimit(int64(n))
	if t.zr != nil {
		t.zr.SetMax(n)
	}
}

func (t *wsTransport) Read(ctx context.Context) ([]byte, error) {
	for {
		typ, b, err := t.ws.Read(ctx)
		if err != nil {
			return nil, err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		if t.zr != nil {
			return t.zr.Decode(b)
		}
		return b, nil
	}
}

func (t *wsTransport) Write(ctx context.Context, b []byte) error {
	return t.writeMsg(ctx, b, true)
}

// writeMsg sends b, compressed if the link is and compress is set (not for
// what's compressed already, like video).
func (t *wsTransport) writeMsg(ctx context.Context, b []byte, compress bool) error {
	if t.zw != nil {
		var err error
		if b, err = t.zw.Encode(b, compress); err != nil {
			return err
		}
	}
	return t.ws.Write(ctx, websocket.MessageBinary, b)
}

func (t *wsTransport) Close() error { return t.ws.Close(websocket.StatusNormalClosure, "") }

// frameTransport is the raw local transport: each message is a 4-byte
// big-endian length followed by the protobuf bytes. Native clients use it
// because Network.framework can't run WebSocket over a Unix socket.
type frameTransport struct {
	c  net.Conn
	r  *bufio.Reader
	mu sync.Mutex
}

const maxFrame = 64 << 20

func (t *frameTransport) Read(ctx context.Context) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(t.r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame {
		return nil, errors.New("frame too large")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(t.r, b)
	return b, err
}

func (t *frameTransport) Write(ctx context.Context, b []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		_ = t.c.SetWriteDeadline(dl)
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err := t.c.Write(buf)
	return err
}

func (t *frameTransport) Close() error { return t.c.Close() }

// frameMagic opens a raw-frame connection so it can't be mistaken for HTTP.
const frameMagic = "PRC1"

// sniffListener hands connections that start with frameMagic to onFrames and
// passes everything else (HTTP/WebSocket) through Accept.
type sniffListener struct {
	net.Listener
	onFrames func(net.Conn, *bufio.Reader, localPeer)
	conns    chan net.Conn
	errc     chan error
	done     chan struct{} // closed when the listener stops
}

func newSniffListener(ln net.Listener, onFrames func(net.Conn, *bufio.Reader, localPeer)) net.Listener {
	s := &sniffListener{Listener: ln, onFrames: onFrames, conns: make(chan net.Conn), errc: make(chan error, 1), done: make(chan struct{})}
	go s.loop()
	return s
}

func (s *sniffListener) loop() {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			s.errc <- err
			close(s.done)
			return
		}
		// Who connected, before anything is read, and again after: macOS
		// gives a socket's peer as the last process to use it, so a
		// program that connects and hands the socket to another (an exec,
		// a child) to speak would otherwise be taken for that one. Both
		// must be the one process (localPeer.same).
		at := localPeerOf(c)
		go func() {
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
			r := bufio.NewReader(c)
			head, err := r.Peek(len(frameMagic))
			if err != nil {
				c.Close()
				return
			}
			_ = c.SetReadDeadline(time.Time{})
			lp := localPeerOf(c)
			if !at.same(lp) {
				c.Close()
				return
			}
			if string(head) == frameMagic {
				_, _ = r.Discard(len(frameMagic))
				s.onFrames(c, r, lp)
				return
			}
			select {
			case s.conns <- &peekedConn{Conn: c, r: r, peer: lp}:
			case <-s.done:
				c.Close()
			}
		}()
	}
}

func (s *sniffListener) Accept() (net.Conn, error) {
	select {
	case c := <-s.conns:
		return c, nil
	case err := <-s.errc:
		return nil, err
	}
}

type peekedConn struct {
	net.Conn
	r    *bufio.Reader
	peer localPeer // who connected (and spoke first)
}

func (c *peekedConn) Read(p []byte) (int, error) { return c.r.Read(p) }
