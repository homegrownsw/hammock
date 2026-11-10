// Package client is a Go client for the hmkd v1 protocol, used by the
// hammock CLI/TUI, the MCP server and tests.
package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/wirezstd"
)

// Client is a connection to hmkd.
type Client struct {
	ws      *websocket.Conn
	zr      *wirezstd.Reader // the compressed subprotocol, when the daemon chose it
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan *pb.ServerMessage
	// approvals: calls whose request waits for the person (ApprovalPending).
	approvals map[uint64]chan *pb.ApprovalPending
	// OnApproval, when set, is told that a call's request waits for you to
	// allow it in the Hammock app or on your phone (hmk says so on stderr).
	// The call then waits for that answer, past its context's deadline.
	OnApproval func(*pb.ApprovalPending)
	// Welcome is the daemon's reply to Hello: its version, name, user.
	Welcome *pb.HelloReply
	// Events receives unsolicited messages: state updates, pane snapshots and output.
	Events chan *pb.ServerMessage
	err    error
	done   chan struct{}
}

// Dial connects to the daemon's Unix socket and says hello.
func Dial(ctx context.Context, socket, kind, name string) (*Client, error) {
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
	return dialURL(ctx, "ws://hmkd/v1", hc, kind, name)
}

// DialRemote connects to a daemon's remote listener (wss://host:port/v1),
// accepting only the TLS certificate whose SHA-256 is certSHA256 (hex).
// The connection must then Pair or Authenticate (see the AuthChallenge event).
func DialRemote(ctx context.Context, addr, certSHA256, kind, name string) (*Client, error) {
	want, err := hex.DecodeString(certSHA256)
	if err != nil {
		return nil, err
	}
	var seen string
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // self-signed; verified by pin below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			sum := sha256.Sum256(raw[0])
			if !bytes.Equal(sum[:], want) {
				seen = hex.EncodeToString(sum[:])
				return errors.New("certificate does not match the paired host")
			}
			return nil
		},
	}}}
	c, err := dialURL(ctx, "wss://"+addr+"/v1", hc, kind, name)
	if err != nil && seen != "" {
		return nil, &CertMismatchError{Seen: seen}
	}
	return c, err
}

// CertMismatchError: what answered there has another certificate than the
// one pinned (Seen, its SHA-256): set up again, or not the host at all.
type CertMismatchError struct{ Seen string }

func (e *CertMismatchError) Error() string { return "certificate does not match the paired host" }

// DialRemoteLearning connects to a daemon whose certificate isn't known
// yet (linking computers, before their code is checked), returning its
// certificate's fingerprint.
func DialRemoteLearning(ctx context.Context, addr, kind, name string) (*Client, string, error) {
	var seen string
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // learned here; the link's code check covers it
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no certificate")
			}
			sum := sha256.Sum256(raw[0])
			seen = hex.EncodeToString(sum[:])
			return nil
		},
	}}}
	c, err := dialURL(ctx, "wss://"+addr+"/v1", hc, kind, name)
	if err != nil {
		return nil, "", err
	}
	return c, seen, nil
}

func dialURL(ctx context.Context, url string, hc *http.Client, kind, name string) (*Client, error) {
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient: hc, Subprotocols: []string{wirezstd.Subprotocol, wirezstd.Plain},
	})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(64 << 20)
	var zr *wirezstd.Reader
	if ws.Subprotocol() == wirezstd.Subprotocol {
		zr = wirezstd.NewReader(64 << 20)
	}
	cctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		ws: ws, zr: zr, ctx: cctx, cancel: cancel,
		pending:   map[uint64]chan *pb.ServerMessage{},
		approvals: map[uint64]chan *pb.ApprovalPending{},
		Events:  make(chan *pb.ServerMessage, 4096),
		done:    make(chan struct{}),
	}
	go c.readLoop()
	hello := &pb.Hello{ProtocolVersion: 1, ClientKind: kind, ClientName: name}
	if kind == "cli" {
		// Run inside a pane (likely by an agent): kept out of Groups shared
		// with you unless their host allows agents.
		hello.FromPaneId = os.Getenv("HMK_PANE_ID")
	}
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: hello}})
	if err != nil {
		c.Close()
		return nil, err
	}
	c.Welcome = r.GetHelloReply()
	return c, nil
}

func (c *Client) readLoop() {
	defer close(c.done)
	defer close(c.Events)
	for {
		_, b, err := c.ws.Read(c.ctx)
		if err == nil && c.zr != nil {
			b, err = c.zr.Decode(b)
		}
		if err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		msg := &pb.ServerMessage{}
		if proto.Unmarshal(b, msg) != nil {
			continue
		}
		if ap := msg.GetApprovalPending(); ap != nil {
			c.mu.Lock()
			ch := c.approvals[ap.RequestId]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- ap:
				default:
				}
			}
			continue
		}
		if msg.ReplyTo != 0 {
			c.mu.Lock()
			ch := c.pending[msg.ReplyTo]
			delete(c.pending, msg.ReplyTo)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		select {
		case c.Events <- msg:
		case <-c.ctx.Done():
			return
		}
	}
}

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Call sends msg and waits for its reply. An Error reply is returned as err.
// A request only you may make, from outside the Hammock app, waits for you
// to allow it (ApprovalPending): then the call waits for your answer as
// long as the daemon does, whatever ctx's deadline (cancelling ctx still
// ends it).
func (c *Client) Call(ctx context.Context, msg *pb.ClientMessage) (*pb.ServerMessage, error) {
	ch := make(chan *pb.ServerMessage, 1)
	ach := make(chan *pb.ApprovalPending, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return nil, c.err
	}
	c.nextID++
	msg.Id = c.nextID
	id := msg.Id
	c.pending[id] = ch
	c.approvals[id] = ach
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.approvals, id)
		c.mu.Unlock()
	}()
	if err := c.write(ctx, msg); err != nil {
		return nil, err
	}
	done := ctx.Done()
	var asked <-chan time.Time
	for {
		select {
		case reply, ok := <-ch:
			if !ok {
				return nil, errors.New("connection closed")
			}
			if e := reply.GetError(); e != nil {
				return nil, errors.New(e.Message)
			}
			return reply, nil
		case ap := <-ach:
			if c.OnApproval != nil {
				c.OnApproval(ap)
			}
			t := time.NewTimer(time.Duration(ap.TimeoutMs)*time.Millisecond + 15*time.Second)
			defer t.Stop()
			asked = t.C
		case <-done:
			if asked != nil && ctx.Err() == context.DeadlineExceeded {
				done = nil // you're being asked: your answer is what's waited for
				continue
			}
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, ctx.Err()
		case <-asked:
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, errors.New("no answer from you in time")
		}
	}
}

// Send sends msg without waiting for a reply.
func (c *Client) Send(ctx context.Context, msg *pb.ClientMessage) error {
	msg.Id = 0
	return c.write(ctx, msg)
}

func (c *Client) write(ctx context.Context, msg *pb.ClientMessage) error {
	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}
	// Sent plain even on a compressed link: what goes to the daemon is small
	// (keys, requests), and writes from any goroutine need no shared stream.
	return c.ws.Write(ctx, websocket.MessageBinary, b)
}

// Compressed says whether the daemon sends compressed messages (wirezstd).
func (c *Client) Compressed() bool { return c.zr != nil }

// Close closes the connection.
func (c *Client) Close() error {
	c.cancel()
	return c.ws.Close(websocket.StatusNormalClosure, "")
}

// CreatedID extracts the id from a Created reply.
func CreatedID(m *pb.ServerMessage) string { return m.GetCreated().GetId() }
