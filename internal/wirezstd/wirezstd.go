// Package wirezstd compresses a remote link's messages (the phone's, a
// guest daemon's): one zstd stream per direction for the whole connection,
// flushed after each message, so each message reuses what earlier ones said
// (small terminal chunks and agent view updates compress well only this
// way). It's negotiated as the WebSocket subprotocol Subprotocol; links that
// don't offer it carry plain protobuf messages as before.
//
// Each compressed-link message from the daemon starts with a byte saying
// how it's sent:
//
//	0, the protobuf bytes as they are (video frames, already compressed)
//	1, the uncompressed length (uvarint), then the next flushed piece of
//	   the stream
//
// A protobuf message never starts with 0 or 1 (its first byte is a field
// tag, at least 8), so anything else is a plain message: clients send
// theirs plain, and Decode passes them through.
package wirezstd

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Subprotocol is offered by clients that can read compressed messages;
// Plain is the one without.
const (
	Subprotocol = "hammock.v1+zstd"
	Plain       = "hammock.v1"
)

const (
	raw        = 0
	compressed = 1
	window     = 1 << 20 // the decoder's memory per link
)

// Writer frames outgoing messages. Not safe for concurrent use.
type Writer struct {
	buf bytes.Buffer
	enc *zstd.Encoder
}

func NewWriter() *Writer {
	w := &Writer{}
	w.enc, _ = zstd.NewWriter(&w.buf, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(window),
		zstd.WithLowerEncoderMem(true), zstd.WithEncoderLevel(zstd.SpeedDefault))
	return w
}

// Encode frames b, compressed unless compress is false. The result is only
// valid until the next call.
func (w *Writer) Encode(b []byte, compress bool) ([]byte, error) {
	w.buf.Reset()
	if !compress {
		w.buf.WriteByte(raw)
		w.buf.Write(b)
		return w.buf.Bytes(), nil
	}
	w.buf.WriteByte(compressed)
	var n [binary.MaxVarintLen64]byte
	w.buf.Write(n[:binary.PutUvarint(n[:], uint64(len(b)))])
	if _, err := w.enc.Write(b); err != nil {
		return nil, err
	}
	if err := w.enc.Flush(); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}

// Reader unframes incoming messages. Not safe for concurrent use.
type Reader struct {
	in  chunk
	dec *zstd.Decoder
	max int
}

// NewReader reads messages of at most max bytes uncompressed.
func NewReader(max int) *Reader {
	r := &Reader{max: max}
	r.dec, _ = zstd.NewReader(&r.in, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(window),
		zstd.WithDecoderLowmem(true))
	return r
}

// SetMax changes the largest message accepted (after authentication).
func (r *Reader) SetMax(max int) { r.max = max }

var errShort = errors.New("compressed message ended early")

// chunk hands the decoder one message's piece of the stream; it never
// waits for the next (that would block the read loop that brings it).
type chunk struct{ b []byte }

func (c *chunk) Read(p []byte) (int, error) {
	if len(c.b) == 0 {
		return 0, errShort
	}
	n := copy(p, c.b)
	c.b = c.b[n:]
	return n, nil
}

// Decode returns a message's protobuf bytes. After an error the link is
// unusable (its stream is out of step): close it.
func (r *Reader) Decode(m []byte) ([]byte, error) {
	if len(m) == 0 {
		return m, nil
	}
	switch m[0] {
	case raw:
		return m[1:], nil
	case compressed:
		n, k := binary.Uvarint(m[1:])
		if k <= 0 || n > uint64(r.max) {
			return nil, fmt.Errorf("compressed message too large or malformed")
		}
		r.in.b = m[1+k:]
		out := make([]byte, n)
		if _, err := io.ReadFull(r.dec, out); err != nil {
			return nil, fmt.Errorf("decompress: %w", err)
		}
		if len(r.in.b) != 0 {
			return nil, errors.New("compressed message has trailing bytes")
		}
		return out, nil
	}
	return m, nil // a plain message
}
