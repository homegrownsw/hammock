package holder

// Ring is a fixed-capacity byte ring buffer addressed by monotonic offsets.
// Offsets count every byte ever written; only the most recent Cap() bytes are
// retained. It is not safe for concurrent use.
type Ring struct {
	buf []byte
	end uint64 // offset of the next byte to be written
}

// NewRing returns a ring retaining up to capacity bytes.
func NewRing(capacity int) *Ring {
	return &Ring{buf: make([]byte, capacity)}
}

// Cap returns the ring capacity.
func (r *Ring) Cap() int { return len(r.buf) }

// End returns the offset of the next byte to be written.
func (r *Ring) End() uint64 { return r.end }

// Start returns the oldest offset still retained.
func (r *Ring) Start() uint64 {
	if r.end < uint64(len(r.buf)) {
		return 0
	}
	return r.end - uint64(len(r.buf))
}

// Write appends p, overwriting the oldest bytes when full.
func (r *Ring) Write(p []byte) {
	n := len(r.buf)
	if len(p) > n {
		// Only the last n bytes can be retained; account for the skipped ones.
		skip := len(p) - n
		r.end += uint64(skip)
		p = p[skip:]
	}
	for len(p) > 0 {
		pos := int(r.end % uint64(n))
		c := copy(r.buf[pos:], p)
		p = p[c:]
		r.end += uint64(c)
	}
}

// ReadFrom returns a copy of the retained bytes from offset from to End(),
// along with the offset the returned bytes actually start at (which is later
// than from if from has already been overwritten).
func (r *Ring) ReadFrom(from uint64) ([]byte, uint64) { return r.ReadFromMax(from, 0) }

// ReadFromMax is ReadFrom limited to at most max bytes (0 = no limit).
func (r *Ring) ReadFromMax(from uint64, max int) ([]byte, uint64) {
	start := r.Start()
	if from < start {
		from = start
	}
	if from >= r.end {
		return nil, r.end
	}
	end := r.end
	if max > 0 && end-from > uint64(max) {
		end = from + uint64(max)
	}
	n := uint64(len(r.buf))
	out := make([]byte, 0, end-from)
	for off := from; off < end; {
		pos := off % n
		chunk := n - pos
		if rem := end - off; rem < chunk {
			chunk = rem
		}
		out = append(out, r.buf[pos:pos+chunk]...)
		off += chunk
	}
	return out, from
}
