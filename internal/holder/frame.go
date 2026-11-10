// Package holder implements the pane-holder: a tiny long-lived process that
// owns one PTY and its child process, keeps a ring buffer of recent output
// with monotonic offsets, and serves it over a Unix socket. The daemon
// connects to holders and can reconnect after it restarts without losing
// output (see docs/adr/0003-per-pane-holder-processes.md).
package holder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// FrameType identifies a holder protocol frame.
type FrameType uint8

const (
	// FrameAttach (client → holder): payload u64 from_offset, then the
	// holder's token if it has one. The first frame a client sends, or on
	// macOS the first after its proof (FrameProve). The holder replies with
	// FrameInfo, then replays buffered output from max(from_offset, ring
	// start) and streams live output.
	FrameAttach FrameType = 1
	// FrameInfo (holder → client): see Info.
	FrameInfo FrameType = 2
	// FrameOutput (holder → client): u64 offset of the first byte, then bytes.
	FrameOutput FrameType = 3
	// FrameInput (client → holder): raw bytes written to the PTY.
	FrameInput FrameType = 4
	// FrameResize (client → holder): u16 cols, u16 rows.
	FrameResize FrameType = 5
	// FrameSignal (client → holder): i32 signal number sent to the child's process group.
	FrameSignal FrameType = 6
	// FrameExit (holder → client): i32 exit status (-1 when killed by a signal).
	FrameExit FrameType = 7
	// FrameClose (client → holder): ask the holder to exit. If the child is still
	// running it is sent SIGHUP first.
	FrameClose FrameType = 8
	// FrameProve (client → holder, macOS): no payload; the first frame a
	// daemon sends, asking to prove the connection is its own (proof.go).
	// Holders from before the proof close the connection on it.
	FrameProve FrameType = 9
	// FrameChallenge (holder → client): u64 the connection's number, its
	// 32-byte nonce, then the name of the holder's Mach service for the
	// proof. The holder reads nothing more until the proof is in.
	FrameChallenge FrameType = 10
)

// MaxFramePayload bounds a single frame to protect against corrupt streams.
const MaxFramePayload = 4 << 20

// Frame is one protocol message: 1 byte type, 4 byte big-endian length, payload.
type Frame struct {
	Type    FrameType
	Payload []byte
}

// WriteFrame writes f to w as a single Write call.
func WriteFrame(w io.Writer, t FrameType, payload []byte) error {
	if len(payload) > MaxFramePayload {
		return fmt.Errorf("holder: frame payload too large (%d)", len(payload))
	}
	buf := make([]byte, 5+len(payload))
	buf[0] = byte(t)
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// ReadFrame reads one frame from r.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > MaxFramePayload {
		return Frame{}, errors.New("holder: frame payload too large")
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return Frame{}, err
	}
	return Frame{Type: FrameType(hdr[0]), Payload: p}, nil
}

// Info describes the holder's state, sent in reply to FrameAttach.
type Info struct {
	PID        int32  // child process id
	Cols, Rows uint16 // current PTY size
	RingStart  uint64 // oldest offset still buffered
	RingEnd    uint64 // offset of the next byte to be produced
	Exited     bool
	ExitStatus int32
}

const infoLen = 4 + 2 + 2 + 8 + 8 + 1 + 4

func (i Info) marshal() []byte {
	b := make([]byte, infoLen)
	binary.BigEndian.PutUint32(b[0:], uint32(i.PID))
	binary.BigEndian.PutUint16(b[4:], i.Cols)
	binary.BigEndian.PutUint16(b[6:], i.Rows)
	binary.BigEndian.PutUint64(b[8:], i.RingStart)
	binary.BigEndian.PutUint64(b[16:], i.RingEnd)
	if i.Exited {
		b[24] = 1
	}
	binary.BigEndian.PutUint32(b[25:], uint32(i.ExitStatus))
	return b
}

// ParseInfo decodes a FrameInfo payload.
func ParseInfo(b []byte) (Info, error) {
	if len(b) < infoLen {
		return Info{}, errors.New("holder: short info frame")
	}
	return Info{
		PID:        int32(binary.BigEndian.Uint32(b[0:])),
		Cols:       binary.BigEndian.Uint16(b[4:]),
		Rows:       binary.BigEndian.Uint16(b[6:]),
		RingStart:  binary.BigEndian.Uint64(b[8:]),
		RingEnd:    binary.BigEndian.Uint64(b[16:]),
		Exited:     b[24] == 1,
		ExitStatus: int32(binary.BigEndian.Uint32(b[25:])),
	}, nil
}

func u64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

func i32(v int32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	return b[:]
}

func resizePayload(cols, rows uint16) []byte {
	var b [4]byte
	binary.BigEndian.PutUint16(b[0:], cols)
	binary.BigEndian.PutUint16(b[2:], rows)
	return b[:]
}
