package holder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
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
// one ignore it).
func Dial(socket string, from uint64, token []byte) (*Client, error) {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		return nil, err
	}
	if err := WriteFrame(conn, FrameAttach, append(u64(from), token...)); err != nil {
		conn.Close()
		return nil, err
	}
	f, err := ReadFrame(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if f.Type != FrameInfo {
		conn.Close()
		return nil, fmt.Errorf("holder: expected info frame, got %d", f.Type)
	}
	info, err := ParseInfo(f.Payload)
	if err != nil {
		conn.Close()
		return nil, err
	}
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
