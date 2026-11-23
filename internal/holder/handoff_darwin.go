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

	mu     sync.Mutex
	at     fileID // the executable this image is
	target fileID // the one being handed over to, once the reader's woken
	tries  map[fileID]int
	woken  bool
}

var (
	// handoffEvery is how often a holder looks for an update to its
	// executable. Programs in the pane lose access in the meantime, so
	// short.
	handoffEvery = 500 * time.Millisecond
	// handoffTries is how many times a holder tries an executable that
	// fails its check, or the handoff (a first launch slowed by the Mac
	// assessing it), before it gives up on it.
	handoffTries = 3
)

// errBusy: not now (a waiting gate, input being written, a close asked
// for), next time.
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
	h.ptyFD = -1
	if s.pty != nil {
		if rc, err := s.pty.SyscallConn(); err == nil {
			_ = rc.Control(func(fd uintptr) { h.ptyFD = int(fd) })
		}
	}
	r, w, err := os.Pipe()
	if err != nil {
		logf("no handoff on update: %v", err)
		return
	}
	if int(r.Fd()) >= unix.FD_SETSIZE || h.ptyFD >= unix.FD_SETSIZE {
		// (select's limit: never, for a holder's few descriptors)
		r.Close()
		w.Close()
		logf("no handoff on update: descriptors past select's")
		return
	}
	h.wakeR, h.wakeW, h.at = r, w, id
	h.tries = map[fileID]int{}
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
		skip := err != nil || id == h.at || h.tries[id] >= handoffTries || h.woken
		h.mu.Unlock()
		if skip {
			continue // gone (an uninstall: idleWatch), or seen
		}
		if !takesHandoff(s.cfg.Exe, s.cfg.Args) {
			h.failed(id, "it doesn't take this holder's handoff")
			continue
		}
		s.mu.Lock()
		exited := s.exited
		s.mu.Unlock()
		if exited {
			// No reader to wake (or it's ending, and won't see the wake):
			// nothing more comes from the PTY.
			if err := s.reexec(s.cfg.Exe); !errors.Is(err, errBusy) {
				h.failed(id, err.Error())
			}
			continue
		}
		h.mu.Lock()
		h.target, h.woken = id, true
		h.mu.Unlock()
		_, _ = h.wakeW.Write([]byte{0})
	}
}

// failed counts a try at handing over to executable id that didn't work.
func (h *handoff) failed(id fileID, why string) {
	h.mu.Lock()
	h.tries[id]++
	n := h.tries[id]
	h.mu.Unlock()
	if n >= handoffTries {
		logf("not handing over to the updated holder: %s (tried %d times)", why, n)
	}
}

// takesHandoff runs exe with CheckArg and the holder's arguments: it must
// start, take them, and take this handoff.
func takesHandoff(exe string, args []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, append([]string{CheckArg}, args...)...).Output()
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
		id := h.target
		h.mu.Unlock()
		err = s.reexec(s.cfg.Exe)
		if !errors.Is(err, errBusy) {
			h.failed(id, err.Error())
		}
		h.mu.Lock()
		h.woken = false
		h.mu.Unlock()
		return false
	}
}

// readerEnded: the reader has stopped (the child's end). A wake it never
// saw is let go, so the watch takes the exited child's way.
func (h *handoff) readerEnded() {
	if h.wakeR == nil {
		return
	}
	h.mu.Lock()
	h.woken = false
	h.mu.Unlock()
}

// reexec execs exe in the holder's place with ResumeArg, handing the pane
// over. It returns only if it couldn't (errBusy: try again later), with
// the holder as it was but for its clients, which reconnect. The reader is
// stopped, or the child has exited.
func (s *Server) reexec(exe string) error {
	if !s.pauseInput(time.Second) {
		return errBusy
	}
	defer s.resumeInput()
	s.mu.Lock()
	if s.closing || s.closeAsked || s.gate != nil {
		s.mu.Unlock()
		return errBusy
	}
	s.handingOver = true
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.handingOver = false
		s.mu.Unlock()
	}()

	// What the daemon has sent is taken first: a close, a resize, input
	// (queued, and handed over). Each client's reader stops once it has
	// read what's there; the daemon reconnects to the new image.
	for _, c := range clients {
		_ = c.conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	}
	for _, c := range clients {
		select {
		case <-c.served:
		case <-time.After(time.Second):
			c.close() // (a frame cut off: the daemon sends it again on its next connection, or not)
			<-c.served
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.closeAsked {
		return errBusy // closed meanwhile: it ends instead
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
	// The daemons proven to it, attached or handed over to it before (one
	// may be reconnecting from an earlier handoff).
	for _, t := range s.daemonsHandedOver() {
		st.Daemons = append(st.Daemons, t.Words())
	}
	for _, c := range clients {
		if c.daemon.Valid() && !s.wasHandedOver(c.daemon) {
			st.Daemons = append(st.Daemons, c.daemon.Words())
		}
	}
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
	file := -1
	if rc, err := f.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { file = int(fd) })
	}
	if file < 0 {
		return errors.New("no handoff file")
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
	argv := append([]string{exe, ResumeArg, strconv.Itoa(file)}, s.cfg.Args...)
	err = syscall.Exec(exe, argv, os.Environ())
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
