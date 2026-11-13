package daemon

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// A helper that owns a pane's pixels (Hammock Simulator, Hammock Browser) as a
// frameSource: a second connection to its socket switched to "media", which
// takes encode/rate/keyframe/hold/stop lines and answers with binary frame
// records (helpers/simulator/host.m, helpers/browser/host.mm).
type hostMedia struct {
	d      *Daemon
	name   string        // for logs
	socket func() string // the helper's socket
	ensure func()        // starts the helper if it isn't running
	// greet, if set, is what the helper must hear before anything else on a
	// connection (the screen host's challenge: screenauth.go).
	greet func(c net.Conn, r *bufio.Reader) error

	start sync.Once
	mu    sync.Mutex
	c     net.Conn
	out   chan []byte // the connection's control lines, written in order by writeLoop
}

// send queues a control line. It never blocks: it's called while frames are
// being read from the same socket, and a helper blocked writing frames would
// otherwise stall both sides.
func (h *hostMedia) send(m map[string]any) {
	b, _ := json.Marshal(m)
	b = append(b, '\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.c == nil {
		return // sessions are restarted on connect
	}
	select {
	case h.out <- b:
	default:
		h.c.Close() // hopelessly behind: reconnect, and every session restarts
	}
}

func (h *hostMedia) writeLoop(c net.Conn, out <-chan []byte) {
	for b := range out {
		_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := c.Write(b); err != nil {
			c.Close()
			for range out {
			}
			return
		}
	}
}

func (h *hostMedia) startEncode(paneID, sid string, bitrate, fps, maxPixels int) {
	h.start.Do(func() { go h.run() })
	h.send(map[string]any{"t": "encode", "id": paneID, "sid": sid, "bitrate": bitrate, "fps": fps, "maxPixels": maxPixels})
}

// encodeWith and setCodec: the screen host's two tiers (codecSource).
// Only it takes them; pane helpers encode H.264 whatever they're told.
func (h *hostMedia) encodeWith(paneID, sid string, bitrate, fps, maxPixels int, codec string) {
	h.start.Do(func() { go h.run() })
	h.send(map[string]any{"t": "encode", "id": paneID, "sid": sid, "bitrate": bitrate, "fps": fps, "maxPixels": maxPixels, "codec": codec})
}

func (h *hostMedia) setCodec(sid, codec string) {
	h.send(map[string]any{"t": "codec", "sid": sid, "codec": codec})
}

func (h *hostMedia) setView(sid string, x, y, w, h2, aspect float32) {
	h.send(map[string]any{"t": "view", "sid": sid, "x": x, "y": y, "w": w, "h": h2, "aspect": aspect})
}

func (h *hostMedia) setSize(sid string, maxPixels int) {
	h.send(map[string]any{"t": "size", "sid": sid, "maxPixels": maxPixels})
}

func (h *hostMedia) setRate(sid string, bitrate, fps int) {
	h.send(map[string]any{"t": "rate", "sid": sid, "bitrate": bitrate, "fps": fps})
}

func (h *hostMedia) keyframe(sid string) { h.send(map[string]any{"t": "keyframe", "sid": sid}) }

func (h *hostMedia) refresh(sid string) { h.send(map[string]any{"t": "refresh", "sid": sid}) }

func (h *hostMedia) ackLTR(sid string, tokens []uint64) {
	h.send(map[string]any{"t": "ltr", "sid": sid, "tokens": tokens})
}

func (h *hostMedia) hold(sid string, on bool) {
	h.send(map[string]any{"t": "hold", "sid": sid, "on": on})
}

func (h *hostMedia) stopEncode(sid string) { h.send(map[string]any{"t": "stop", "sid": sid}) }

func (h *hostMedia) run() {
	log := h.d.log.With("component", h.name+"-media")
	for {
		h.ensure()
		c, err := net.DialTimeout("unix", h.socket(), time.Second)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if err := h.serve(c); err != nil && err != io.EOF {
			log.Warn("media connection", "err", err)
		}
		h.mu.Lock()
		if h.c == c {
			h.c = nil
			close(h.out)
			h.out = nil
		}
		h.mu.Unlock()
		c.Close()
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *hostMedia) serve(c net.Conn) error {
	r := bufio.NewReaderSize(c, 1<<20)
	if h.greet != nil {
		if err := h.greet(c, r); err != nil {
			return err
		}
	}
	if _, err := c.Write([]byte(`{"t":"media"}` + "\n")); err != nil {
		return err
	}
	// The host's greeting lines, then mediaReady, then frames. A helper from
	// before video never says it: give up, and say why.
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				return errors.New("the helper didn't start video (an old build still running?)")
			}
			return err
		}
		var m struct {
			T string `json:"t"`
		}
		if json.Unmarshal(line, &m) == nil && m.T == "mediaReady" {
			break
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	out := make(chan []byte, 1024)
	go h.writeLoop(c, out)
	h.mu.Lock()
	h.c, h.out = c, out
	h.mu.Unlock()
	h.d.media.restart(h)
	hdr := make([]byte, 4)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(hdr)
		if n < 22 || n > mediaMaxFrame {
			return io.ErrUnexpectedEOF
		}
		rec := make([]byte, n)
		if _, err := io.ReadFull(r, rec); err != nil {
			return err
		}
		sl := int(rec[0])
		if 1+sl+21 > len(rec) {
			return io.ErrUnexpectedEOF
		}
		sid := string(rec[1 : 1+sl])
		b := rec[1+sl:]
		f := &mediaFrame{
			key:     b[0]&1 != 0,
			refresh: b[0]&2 != 0,
			hevc:    b[0]&4 != 0,
			pts:     binary.BigEndian.Uint64(b[1:]),
			w:       int(binary.BigEndian.Uint16(b[9:])),
			h:       int(binary.BigEndian.Uint16(b[11:])),
			ltr:     binary.BigEndian.Uint64(b[13:]),
			data:    b[21:],
		}
		if b[0]&8 != 0 && len(b) >= 29 {
			f.cropped = true
			for i := range f.crop {
				f.crop[i] = binary.BigEndian.Uint16(b[21+2*i:])
			}
			f.data = b[29:]
		}
		h.d.media.deliver(sid, f)
	}
}
