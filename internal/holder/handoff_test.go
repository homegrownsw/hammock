package holder

import (
	"bytes"
	"io"
	"testing"
)

// What a holder hands over comes back as it was, the output kept at the
// same offsets: the daemon reconnects from the offset it had.
func TestHandoffRoundTrip(t *testing.T) {
	r := NewRing(16)
	r.Write([]byte("0123456789abcdefghijklmnopqrstuvwxyzABCD")) // 40 bytes: wrapped
	kept, start := r.ReadFrom(0)
	st := handoffState{
		Args: []string{"--socket", "/tmp/p.sock", "--", "/bin/sh"}, PID: 42, Cols: 120, Rows: 40,
		RingCap: r.Cap(), RingStart: start, Exited: true, ExitStatus: 3, ExitSignal: 0,
		Token: []byte("secret"), Input: [][]byte{[]byte("ls\r")}, Listener: 5, PTY: -1,
		Lock: &lockState{Dir: "/state", Lock: "/state/run/daemon.lock", DirDev: 1, DirIno: 2, LockDev: 1, LockIno: 3},
	}
	var buf bytes.Buffer
	if err := writeHandoff(&buf, st, kept); err != nil {
		t.Fatal(err)
	}
	h, err := readHandoff(&buf)
	if err != nil {
		t.Fatal(err)
	}
	got := h.st
	if got.PID != 42 || got.Cols != 120 || got.Rows != 40 || !got.Exited || got.ExitStatus != 3 ||
		string(got.Token) != "secret" || len(got.Input) != 1 || string(got.Input[0]) != "ls\r" ||
		got.Listener != 5 || got.PTY != -1 || *got.Lock != *st.Lock || len(h.Args()) != 4 {
		t.Fatalf("handed over %+v, got %+v", st, got)
	}

	ring := NewRing(got.RingCap)
	ring.end = got.RingStart
	ring.Write(h.ring)
	if ring.Start() != r.Start() || ring.End() != r.End() {
		t.Fatalf("output at %d–%d, was %d–%d", ring.Start(), ring.End(), r.Start(), r.End())
	}
	a, at := ring.ReadFrom(30)
	b, bt := r.ReadFrom(30)
	if !bytes.Equal(a, b) || at != bt {
		t.Fatalf("from 30: %q at %d, was %q at %d", a, at, b, bt)
	}
}

// Queued input of any size up to the bound goes with the output (not in the
// JSON, which has a limit of its own); more than that is never written, so a
// holder never execs into a handoff the new image would refuse.
func TestHandoffInput(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 3<<20) // a paste
	var buf bytes.Buffer
	if err := writeHandoff(&buf, handoffState{RingCap: 16, Input: [][]byte{[]byte("a"), big}}, []byte("out")); err != nil {
		t.Fatal(err)
	}
	h, err := readHandoff(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(h.ring) != "out" || len(h.st.Input) != 2 || string(h.st.Input[0]) != "a" || !bytes.Equal(h.st.Input[1], big) {
		t.Fatalf("input %d pieces, output %q", len(h.st.Input), h.ring)
	}
	tooMuch := handoffState{RingCap: 16, Input: [][]byte{make([]byte, maxHandoffInput+1)}}
	if err := writeHandoff(io.Discard, tooMuch, nil); err == nil {
		t.Fatal("wrote a handoff with more input than one is taken with")
	}
}

// A handoff from a holder of another version is refused, not misread.
func TestHandoffOtherVersion(t *testing.T) {
	var buf bytes.Buffer
	if err := writeHandoff(&buf, handoffState{RingCap: 16}, nil); err != nil {
		t.Fatal(err)
	}
	b := bytes.Replace(buf.Bytes(), []byte(`"Version":1`), []byte(`"Version":9`), 1)
	if _, err := readHandoff(bytes.NewReader(b)); err == nil {
		t.Fatal("took a handoff of another version")
	}
}
