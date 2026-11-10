package holder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
)

// Client is a connection to a holder.
type Client struct {
	conn net.Conn
	Info Info
	wmu  sync.Mutex
}

// Event is something received from a holder after attaching.
type Event struct {
	Output     []byte
	Offset     uint64 // offset of Output[0]
	Exited     bool
	ExitStatus int32
}

// Dial connects to the holder at socket and attaches from offset from,
// proving itself with token (the holder's secret; holders started without
// one ignore it). On macOS it first proves over Mach that it's the process
// at its end of the connection (proof.go); a holder from before the proof
// closes the connection at that, and is attached to as it always was.
func Dial(socket string, from uint64, token []byte) (*Client, error) {
	if proofSupported {
		c, err := dial(socket, from, token, true)
		if !errors.Is(err, errOldHolder) {
			return c, err
		}
	}
	return dial(socket, from, token, false)
}

// errOldHolder: the holder closed the connection at FrameProve, as one from
// before the proof does.
var errOldHolder = errors.New("holder: closed at the proof (one from before it?)")

func dial(socket string, from uint64, token []byte, prove bool) (*Client, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	return attach(conn, from, token, prove)
}

// attach attaches on conn (proving it's this process's first, if prove),
// closing it if that fails.
func attach(conn net.Conn, from uint64, token []byte, prove bool) (*Client, error) {
	fail := func(err error) (*Client, error) {
		conn.Close()
		return nil, err
	}
	// Something that isn't a holder (put at its path) mustn't hold the
	// caller up for ever.
	_ = conn.SetDeadline(time.Now().Add(2*ProofFor + 5*time.Second))
	if prove {
		if err := WriteFrame(conn, FrameProve, nil); err != nil {
			return fail(err)
		}
		f, err := ReadFrame(conn)
		if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) {
			return fail(errOldHolder)
		}
		if err != nil {
			return fail(err)
		}
		if f.Type != FrameChallenge {
			return fail(fmt.Errorf("holder: expected a challenge, got frame %d", f.Type))
		}
		id, nonce, svc, err := parseChallenge(f.Payload)
		if err != nil {
			return fail(err)
		}
		answer, err := sendProof(svc, id, nonce, ProofFor)
		if err != nil {
			return fail(err)
		}
		if answer != "ok" {
			return fail(fmt.Errorf("holder: refused this process's proof: %s", answer))
		}
	}
	if err := WriteFrame(conn, FrameAttach, append(u64(from), token...)); err != nil {
		return fail(err)
	}
	f, err := ReadFrame(conn)
	if err != nil {
		return fail(err)
	}
	if f.Type != FrameInfo {
		return fail(fmt.Errorf("holder: expected info frame, got %d", f.Type))
	}
	info, err := ParseInfo(f.Payload)
	if err != nil {
		return fail(err)
	}
	_ = conn.SetDeadline(time.Time{})
	return &Client{conn: conn, Info: info}, nil
}

// Next blocks for the next event.
func (c *Client) Next() (Event, error) {
	for {
		f, err := ReadFrame(c.conn)
		if err != nil {
			return Event{}, err
		}
		switch f.Type {
		case FrameOutput:
			if len(f.Payload) < 8 {
				return Event{}, errors.New("holder: short output frame")
			}
			return Event{Offset: binary.BigEndian.Uint64(f.Payload), Output: f.Payload[8:]}, nil
		case FrameExit:
			if len(f.Payload) < 4 {
				return Event{}, errors.New("holder: short exit frame")
			}
			return Event{Exited: true, ExitStatus: int32(binary.BigEndian.Uint32(f.Payload))}, nil
		}
	}
}

func (c *Client) write(t FrameType, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return WriteFrame(c.conn, t, p)
}

// Input writes bytes to the PTY.
func (c *Client) Input(p []byte) error { return c.write(FrameInput, p) }

// Resize changes the PTY size.
func (c *Client) Resize(cols, rows uint16) error {
	return c.write(FrameResize, resizePayload(cols, rows))
}

// Signal sends sig to the child's process group.
func (c *Client) Signal(sig syscall.Signal) error { return c.write(FrameSignal, i32(int32(sig))) }

// CloseHolder asks the holder to hang up its child and exit.
func (c *Client) CloseHolder() error { return c.write(FrameClose, nil) }

// Close closes this connection; the holder keeps running.
func (c *Client) Close() error { return c.conn.Close() }
