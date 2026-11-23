package holder

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// A handoff passes a pane from a holder to the executable an update put in
// its place (Config.Exe), in the same process: the holder execs it with
// ResumeArg and a file of what it holds, and the new image serves on
// (Resume). The child, the PTY and the socket are the same ones, so the
// daemon only sees its connection drop and reconnects (from the offset it
// had); the output kept, the size and an exit already seen carry over.
//
// Why: macOS judges a pane's programs by their holder (the daemon disclaims
// it, spawn_darwin.go), and takes the holder for the app it's in only while
// the code it runs is the app's. Once an update has replaced it, the holder
// is a stranger, and every program in the pane loses the app's privacy
// grants: Full Disk Access, the Documents folder. A refusal then seems to
// be kept for the process, even after it execs the new executable, so the
// holder hands over as soon as it sees the update, not when access fails.
// handoff_darwin.go does it; elsewhere a holder runs on as it is.

const (
	// ResumeArg, first in its arguments, has a holder's executable take a
	// handoff: then come the descriptor of the handoff's file and the
	// holder's own arguments (Config.Args), so the daemon still knows it
	// by them as its pane's holder (its --socket).
	ResumeArg = "--pane-holder-resume"
	// CheckArg, first in its arguments, has a holder's executable say which
	// handoff it takes (RunCheck), if it takes the holder's arguments that
	// follow: asked of an update's executable before a holder execs it, so
	// a holder never hands over to one that can't start, or can't serve
	// the pane (a downgrade, a build whose arguments differ).
	CheckArg = "--pane-holder-check"
	// handoffVersion is the handoff's format: change it with handoffState.
	handoffVersion = 1
)

// handoffHello is what RunCheck prints.
func handoffHello() string { return fmt.Sprintf("pane-holder handoff %d", handoffVersion) }

// RunCheck prints the handoff this executable takes, and exits.
func RunCheck() {
	fmt.Println(handoffHello())
	os.Exit(0)
}

// handoffState is what a holder hands over, besides the output it kept (the
// handoff's file holds this as JSON, then the output). Descriptors are the
// ones the exec keeps open.
type handoffState struct {
	Version    int
	Args       []string // Config.Args: the new image rebuilds its Config from them
	PID        int      // the child
	Cols, Rows uint16
	RingCap    int
	RingStart  uint64 // the offset of the output's first byte
	RingLen    int
	Exited     bool
	ExitStatus int32
	ExitSignal int32
	Token      []byte
	Lock       *lockState
	// Input is queued for the child, not yet written: after the output in
	// the file (a paste can be megabytes), InputLens long.
	Input     [][]byte `json:"-"`
	InputLens []int
	// Daemons are the audit tokens of the daemons proven to the holder
	// (Server.handedOver): the update replaced theirs too, and they must
	// reconnect.
	Daemons  [][8]uint32
	Listener int
	PTY      int // -1 once the child has exited
}

// lockState is a StateLock, handed over.
type lockState struct {
	Dir, Lock        string
	DirDev, DirIno   uint64
	LockDev, LockIno uint64
}

func (l *StateLock) handoff() *lockState {
	return &lockState{Dir: l.dir, Lock: l.lock, DirDev: l.dirID.dev, DirIno: l.dirID.ino, LockDev: l.lockID.dev, LockIno: l.lockID.ino}
}

// Handoff is a pane handed over by the holder this process was before its
// exec (ReadHandoff).
type Handoff struct {
	st   handoffState
	ring []byte
}

// Bounds on what readHandoff takes, which writeHandoff keeps to, so a
// holder never execs into a handoff it would refuse.
const (
	maxHandoffHeader = 1 << 20
	maxHandoffInput  = 64 << 20
)

// writeHandoff writes st, then the output kept, then the input queued.
func writeHandoff(w io.Writer, st handoffState, ring []byte) error {
	st.Version, st.RingLen = handoffVersion, len(ring)
	st.InputLens = st.InputLens[:0]
	total := 0
	for _, p := range st.Input {
		st.InputLens = append(st.InputLens, len(p))
		total += len(p)
	}
	if total > maxHandoffInput {
		return fmt.Errorf("%d bytes of input queued", total)
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if len(b) > maxHandoffHeader {
		return fmt.Errorf("a handoff of %d bytes", len(b))
	}
	bw := bufio.NewWriter(w)
	_, _ = bw.Write(binary.BigEndian.AppendUint32(nil, uint32(len(b))))
	_, _ = bw.Write(b)
	_, _ = bw.Write(ring)
	for _, p := range st.Input {
		_, _ = bw.Write(p)
	}
	return bw.Flush()
}

// readHandoff reads what writeHandoff wrote.
func readHandoff(r io.Reader) (*Handoff, error) {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > maxHandoffHeader {
		return nil, fmt.Errorf("a handoff of %d bytes", size)
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	h := &Handoff{}
	if err := json.Unmarshal(b, &h.st); err != nil {
		return nil, err
	}
	if h.st.Version != handoffVersion {
		return nil, fmt.Errorf("handoff version %d, not %d", h.st.Version, handoffVersion)
	}
	if h.st.RingCap <= 0 || h.st.RingLen < 0 || h.st.RingLen > h.st.RingCap {
		return nil, fmt.Errorf("a handoff of %d bytes of output for a buffer of %d", h.st.RingLen, h.st.RingCap)
	}
	h.ring = make([]byte, h.st.RingLen)
	if _, err := io.ReadFull(r, h.ring); err != nil {
		return nil, err
	}
	total := 0
	for _, l := range h.st.InputLens {
		if l < 0 || total+l > maxHandoffInput {
			return nil, errors.New("a handoff's input doesn't add up")
		}
		total += l
	}
	for _, l := range h.st.InputLens {
		p := make([]byte, l)
		if _, err := io.ReadFull(r, p); err != nil {
			return nil, err
		}
		h.st.Input = append(h.st.Input, p)
	}
	return h, nil
}

// ReadHandoff reads the handoff in the file at descriptor fd (ResumeArg's
// argument), and closes it.
func ReadHandoff(fd int) (*Handoff, error) {
	f := os.NewFile(uintptr(fd), "handoff")
	if f == nil {
		return nil, errors.New("no handoff file")
	}
	defer f.Close()
	h, err := readHandoff(f)
	if err != nil {
		return nil, fmt.Errorf("the handoff: %w", err)
	}
	return h, nil
}

// Args are the arguments the holder was started with (Config.Args).
func (h *Handoff) Args() []string { return h.st.Args }

// Resume serves the pane handed over in h: cfg is the holder's Config,
// rebuilt from h.Args(), and its Token and State are h's.
func Resume(cfg Config, h *Handoff) (*Server, error) {
	st := h.st
	cfg.Token = st.Token
	cfg.State = nil
	if l := st.Lock; l != nil {
		cfg.State = &StateLock{dir: l.Dir, lock: l.Lock, dirID: fileID{l.DirDev, l.DirIno}, lockID: fileID{l.LockDev, l.LockIno}}
	}
	cfg.RingSize = st.RingCap
	lf := os.NewFile(uintptr(st.Listener), "listener")
	if lf == nil {
		return nil, errors.New("holder: no listener handed over")
	}
	ln, err := net.FileListener(lf)
	lf.Close()
	if err != nil {
		return nil, fmt.Errorf("holder: the listener handed over: %w", err)
	}
	var pt *os.File
	if st.PTY >= 0 {
		// Close-on-exec again (cleared for the exec): the holder's other
		// children (a gate, the next update's check) mustn't hold it open.
		syscall.CloseOnExec(st.PTY)
		if pt = os.NewFile(uintptr(st.PTY), "pty"); pt == nil {
			ln.Close()
			return nil, errors.New("holder: no PTY handed over")
		}
	}
	proc, err := os.FindProcess(st.PID) // still this process's child: the exec kept it
	if err != nil {
		ln.Close()
		return nil, err
	}
	ring := NewRing(st.RingCap)
	ring.end = st.RingStart
	ring.Write(h.ring)
	s := &Server{
		cfg: cfg, ln: ln, pty: pt, proc: proc, done: make(chan struct{}),
		ring: ring, clients: map[*client]struct{}{},
		cols: st.Cols, rows: st.Rows, lastDetach: time.Now(),
		exited: st.Exited, exitStatus: st.ExitStatus, exitSignal: st.ExitSignal,
		inBuf: st.Input,
	}
	daemons := make([]codesign.Token, 0, len(st.Daemons))
	for _, w := range st.Daemons {
		daemons = append(daemons, codesign.TokenFromWords(w))
	}
	s.handedOver.Store(&daemons)
	s.inCond = sync.NewCond(&s.inMu)
	s.run()
	return s, nil
}
