package holder

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// handoff watches for an update to the holder's executable and hands the
// pane over to it (handoff.go). The reader does the exec: woken by the
// watch, it's between reads, so no output it read is lost with it.
type handoff struct {
	wakeR, wakeW *os.File // nil: no handoff (Config.Exe unset)
	ptyFD        int

	mu      sync.Mutex
	at      fileID // the executable this image is
	refused fileID // one that failed or wouldn't take a handoff: not tried again
	exe     string // the executable to hand over to, once the reader is woken
	woken   bool
}

// handoffEvery is how often a holder looks for an update to its
// executable. Programs in the pane lose access in the meantime, so short.
var handoffEvery = 500 * time.Millisecond

// errBusy: not now (a waiting gate, input being written), next time.
var errBusy = errors.New("busy")

func (h *handoff) init(s *Server) {
	if s.cfg.Exe == "" {
		return
	}
	id, err := statID(s.cfg.Exe)
	if err != nil {
		logf("no handoff on update: %v", err)
		return
	}
	r, w, err := os.Pipe()
	if err != nil {
		logf("no handoff on update: %v", err)
		return
	}
	h.wakeR, h.wakeW, h.at = r, w, id
	h.ptyFD = -1
	if s.pty != nil {
		if rc, err := s.pty.SyscallConn(); err == nil {
			_ = rc.Control(func(fd uintptr) { h.ptyFD = int(fd) })
		}
	}
}

func statID(path string) (fileID, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return fileID{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return fileID{}, errors.New(path + " isn't a file")
	}
	return idOf(&st), nil
}

// watch looks for another executable at Config.Exe until the holder ends,
// and once one that takes the handoff is there, has it handed over to.
func (h *handoff) watch(s *Server) {
	if h.wakeR == nil {
		return
	}
	t := time.NewTicker(handoffEvery)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
		}
		id, err := statID(s.cfg.Exe)
		h.mu.Lock()
		skip := err != nil || id == h.at || id == h.refused || h.woken
		h.mu.Unlock()
		if skip {
			continue // gone (an uninstall: idleWatch), or seen
		}
		if !takesHandoff(s.cfg.Exe) {
			logf("not handing over to %s: it doesn't take this holder's handoff", s.cfg.Exe)
			h.refuse(id)
			continue
		}
		s.mu.Lock()
		exited := s.exited
		s.mu.Unlock()
		if exited {
			// No reader to wake (or it's ending, and won't see the wake):
			// nothing more comes from the PTY.
			if err := s.reexec(s.cfg.Exe); !errors.Is(err, errBusy) {
				logf("couldn't hand over to %s: %v", s.cfg.Exe, err)
				h.refuse(id)
			}
			continue
		}
		h.mu.Lock()
		h.exe, h.woken = s.cfg.Exe, true
		h.mu.Unlock()
		_, _ = h.wakeW.Write([]byte{0})
	}
}

func (h *handoff) refuse(id fileID) {
	h.mu.Lock()
	h.refused = id
	h.mu.Unlock()
}

// takesHandoff runs exe with CheckArg: it must start, and take this handoff.
func takesHandoff(exe string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, CheckArg).Output()
	return err == nil && string(out) == handoffHello()+"\n"
}

// awaitOutput waits until the PTY has something to read (true), or the
// reader has been woken to hand over, which it does, coming back (false)
// only if it couldn't.
func (h *handoff) awaitOutput(s *Server) bool {
	if h.wakeR == nil || h.ptyFD < 0 {
		return true
	}
	wake := int(h.wakeR.Fd())
	for {
		var rs unix.FdSet
		rs.Set(h.ptyFD)
		rs.Set(wake)
		_, err := unix.Select(max(h.ptyFD, wake)+1, &rs, nil, nil, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || !rs.IsSet(wake) {
			return true // output, the child's end, or no select: read
		}
		_, _ = h.wakeR.Read(make([]byte, 1))
		h.mu.Lock()
		exe := h.exe
		h.mu.Unlock()
		err = s.reexec(exe)
		h.mu.Lock()
		h.woken = false
		if !errors.Is(err, errBusy) {
			logf("couldn't hand over to %s: %v", exe, err)
			if id, e := statID(exe); e == nil {
				h.refused = id
			}
		}
		h.mu.Unlock()
		return false
	}
}

// reexec execs exe in the holder's place with ResumeArg, handing the pane
// over. It returns only if it couldn't (errBusy: try again later), with
// the holder as it was. The reader is stopped, or the child has exited.
func (s *Server) reexec(exe string) error {
	if !s.pauseInput(time.Second) {
		return errBusy
	}
	defer s.resumeInput()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.gate != nil {
		return errBusy
	}
	st := handoffState{
		Args: s.cfg.Args, PID: s.proc.Pid, Cols: s.cols, Rows: s.rows,
		RingCap: s.ring.Cap(), Exited: s.exited, ExitStatus: s.exitStatus, ExitSignal: s.exitSignal,
		Token: s.cfg.Token, Listener: -1, PTY: -1,
	}
	ring, start := s.ring.ReadFrom(0)
	st.RingStart = start
	if s.cfg.State != nil {
		st.Lock = s.cfg.State.handoff()
	}
	s.inMu.Lock()
	st.Input = s.inBuf
	s.inMu.Unlock()
	if ul, ok := s.ln.(*net.UnixListener); ok {
		if rc, err := ul.SyscallConn(); err == nil {
			_ = rc.Control(func(fd uintptr) { st.Listener = int(fd) })
		}
	}
	if st.Listener < 0 {
		return errors.New("no listener to hand over")
	}
	if !s.exited {
		st.PTY = s.handoff.ptyFD
		if st.PTY < 0 {
			return errors.New("no PTY to hand over")
		}
	}

	f, err := os.CreateTemp("", "pane-holder-handoff-")
	if err != nil {
		return err
	}
	defer f.Close()
	_ = os.Remove(f.Name()) // only its descriptor: it holds the token
	if err := writeHandoff(f, st, ring); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	var file int
	if rc, err := f.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { file = int(fd) })
	}
	fds := []int{st.Listener, file}
	if st.PTY >= 0 {
		fds = append(fds, st.PTY)
	}
	for _, fd := range fds {
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0); err != nil {
			keepFromExec(fds)
			return err
		}
	}
	logf("handing the pane over to the updated holder (%s)", exe)
	err = syscall.Exec(exe, []string{exe, ResumeArg, strconv.Itoa(file)}, os.Environ())
	keepFromExec(fds)
	return err
}

func keepFromExec(fds []int) {
	for _, fd := range fds {
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
	}
}

// pauseInput keeps input queued, once a batch being written is (false:
// still writing after wait, a program not reading its input).
func (s *Server) pauseInput(wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	s.inMu.Lock()
	defer s.inMu.Unlock()
	s.inPaused = true
	for s.inWriting {
		if time.Now().After(deadline) {
			s.inPaused = false
			s.inCond.Broadcast()
			return false
		}
		s.inMu.Unlock()
		time.Sleep(10 * time.Millisecond)
		s.inMu.Lock()
	}
	return true
}

func (s *Server) resumeInput() {
	s.inMu.Lock()
	s.inPaused = false
	s.inCond.Broadcast()
	s.inMu.Unlock()
}
