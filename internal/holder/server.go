package holder

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Config configures a holder.
type Config struct {
	Socket   string   // Unix socket path to listen on
	Command  []string // argv of the child process
	Dir      string   // working directory for the child
	Env      []string // environment for the child (nil = inherit)
	Cols     uint16
	Rows     uint16
	RingSize int // bytes of output retained for replay (default 8 MiB)
	// Token, when set, must follow the offset in every client's FrameAttach:
	// only the daemon that started the holder knows it, so nothing else
	// that can reach the socket can type into or read the pane.
	Token []byte
	// Peer, when set, must also accept the process at the other end of each
	// connection: the token is in the daemon's state, which anything of
	// yours can read, so it alone doesn't make one the daemon.
	Peer func(PeerProc) bool
	// State, when set, must be locked by the process at the other end of
	// each connection: the daemon of the holder's own state directory, not
	// another hmkd started on a copy of it (statelock.go).
	State *StateLock
	// IdleExit makes the holder exit once the child has exited and no client
	// has been attached for this long. Zero means wait for FrameClose forever.
	IdleExit time.Duration
	// Install is where the holder was started from: the app it's in, or its
	// own executable outside one. When that has been deleted (a development
	// build's worktree removed, the app put in the Bin) and no client has
	// been attached for OrphanExit, no daemon is coming back for this pane:
	// the holder ends it rather than run on unseen. Either unset turns this
	// off.
	Install    string
	OrphanExit time.Duration
	// AdoptOrphans (Linux) makes the holder a child subreaper: what the
	// child's programs leave behind (a double fork, setsid, nohup) is
	// reparented to the holder, not init, so the daemon still finds it
	// under the pane (reap_linux.go). Only for a holder of its own process:
	// it reaps every exited child but the command's.
	AdoptOrphans bool
}

// maxChunk bounds a single output frame.
const maxChunk = 256 << 10

// client is one attached connection. Its writer streams straight from the
// ring at its own offset, so a slow client never blocks the PTY or other
// clients; it only loses data if it falls a whole ring behind (it then sees
// the offset jump).
type client struct {
	conn   net.Conn
	offset uint64        // next offset to send; guarded by Server.mu
	wake   chan struct{} // cap 1: new output or exit available
	done   chan struct{}
	once   sync.Once
}

func (c *client) close() { c.once.Do(func() { close(c.done); c.conn.Close() }) }

func (c *client) notify() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Server is a running holder.
type Server struct {
	cfg  Config
	ln   net.Listener
	pty  *os.File
	cmd  *exec.Cmd
	done chan struct{}

	mu         sync.Mutex
	ring       *Ring
	clients    map[*client]struct{}
	cols, rows uint16
	exited     bool
	exitStatus int32
	lastDetach time.Time
	closing    bool

	inMu   sync.Mutex
	inCond *sync.Cond
	inBuf  [][]byte
}

// Start launches the child on a new PTY and begins serving the socket.
func Start(cfg Config) (*Server, error) {
	if len(cfg.Command) == 0 {
		return nil, errors.New("holder: no command")
	}
	if cfg.RingSize == 0 {
		cfg.RingSize = 8 << 20
	}
	if cfg.Cols == 0 || cfg.Rows == 0 {
		cfg.Cols, cfg.Rows = 80, 24
	}
	_ = os.Remove(cfg.Socket)
	ln, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return nil, fmt.Errorf("holder: listen: %w", err)
	}
	if err := os.Chmod(cfg.Socket, 0o600); err != nil {
		ln.Close()
		return nil, err
	}

	if cfg.AdoptOrphans {
		becomeSubreaper() // before the child starts: nothing it leaves gets past
	}
	cmd := exec.Command(cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cfg.Cols, Rows: cfg.Rows})
	if err != nil {
		ln.Close()
		os.Remove(cfg.Socket)
		return nil, fmt.Errorf("holder: start: %w", err)
	}

	s := &Server{
		cfg: cfg, ln: ln, pty: f, cmd: cmd, done: make(chan struct{}),
		ring: NewRing(cfg.RingSize), clients: map[*client]struct{}{},
		cols: cfg.Cols, rows: cfg.Rows, lastDetach: time.Now(),
	}
	s.inCond = sync.NewCond(&s.inMu)
	if cfg.AdoptOrphans {
		go reapOrphans(cmd.Process.Pid, s.done)
	}
	go s.writePTY()
	go s.readPTY()
	go s.accept()
	if cfg.IdleExit > 0 || (cfg.Install != "" && cfg.OrphanExit > 0) {
		go s.idleWatch()
	}
	return s, nil
}

// PID returns the child's process id.
func (s *Server) PID() int { return s.cmd.Process.Pid }

// Done is closed when the holder has shut down.
func (s *Server) Done() <-chan struct{} { return s.done }

// Shutdown stops the holder, hanging up the child if it is still running.
func (s *Server) Shutdown() { s.shutdown("asked") }

// shutdown stops it, saying why on stderr (the holder's log), so a pane
// whose holder went can be explained.
func (s *Server) shutdown(why string) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	exited := s.exited
	logf("shutting down (%s); child exited=%v status=%d", why, s.exited, s.exitStatus)
	clients := s.clients
	s.clients = map[*client]struct{}{}
	s.mu.Unlock()

	if !exited {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
	}
	s.ln.Close()
	os.Remove(s.cfg.Socket)
	for c := range clients {
		c.close()
	}
	close(s.done)
	s.inMu.Lock()
	s.inBuf = nil
	s.inCond.Broadcast() // wake writePTY so it sees done
	s.inMu.Unlock()
}

func (s *Server) readPTY() {
	buf := make([]byte, 64<<10)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.ring.Write(buf[:n])
			for c := range s.clients {
				c.notify()
			}
			s.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	status := int32(0)
	if err := s.cmd.Wait(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			status = int32(ee.ExitCode())
		} else {
			status = -1
		}
	}
	logf("child exited, status %d (%v)", status, s.cmd.ProcessState)
	s.pty.Close()
	s.mu.Lock()
	s.exited, s.exitStatus = true, status
	for c := range s.clients {
		c.notify()
	}
	s.mu.Unlock()
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		// Who connected, before anything is read: macOS gives a socket's
		// peer as the last process to use it, which could otherwise be one
		// it was handed to that only wrote the first frame.
		var at PeerProc
		if uc, ok := conn.(*net.UnixConn); ok && (s.cfg.Peer != nil || s.cfg.State != nil) {
			at = sockPeer(uc)
		}
		go s.serve(conn, at)
	}
}

func (s *Server) serve(conn net.Conn, at PeerProc) {
	first, err := ReadFrame(conn)
	if err != nil || first.Type != FrameAttach || len(first.Payload) < 8 {
		conn.Close()
		return
	}
	if len(s.cfg.Token) > 0 && subtle.ConstantTimeCompare(first.Payload[8:], s.cfg.Token) != 1 {
		conn.Close()
		return
	}
	if s.cfg.Peer != nil || s.cfg.State != nil {
		var p PeerProc
		if uc, ok := conn.(*net.UnixConn); ok {
			p = sockPeer(uc)
		}
		if p.PID <= 0 || !sameProc(at, p) {
			logf("refused a client (pid %d, connected as pid %d): not the process that connected", p.PID, at.PID)
			conn.Close()
			return
		}
		if s.cfg.Peer != nil && !s.cfg.Peer(p) {
			logf("refused a client (pid %d): not the daemon", p.PID)
			conn.Close()
			return
		}
		if s.cfg.State != nil {
			err := s.cfg.State.HeldBy(p.PID)
			if err == nil && !peerStill(p) {
				err = errors.New("it went, or isn't the process that connected")
			}
			if err != nil {
				logf("refused a client (pid %d): not the daemon of this pane's state: %v", p.PID, err)
				conn.Close()
				return
			}
		}
	}
	c := &client{conn: conn, offset: binary.BigEndian.Uint64(first.Payload), wake: make(chan struct{}, 1), done: make(chan struct{})}

	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		conn.Close()
		return
	}
	info := Info{
		PID: int32(s.cmd.Process.Pid), Cols: s.cols, Rows: s.rows,
		RingStart: s.ring.Start(), RingEnd: s.ring.End(),
		Exited: s.exited, ExitStatus: s.exitStatus,
	}
	s.clients[c] = struct{}{}
	s.mu.Unlock()

	if WriteFrame(conn, FrameInfo, info.marshal()) != nil {
		s.detach(c)
		return
	}
	go s.write(c)
	defer s.detach(c)

	for {
		f, err := ReadFrame(conn)
		if err != nil {
			return
		}
		switch f.Type {
		case FrameInput:
			// Queued: a program that isn't reading stdin must not stop us
			// handling Signal and Close frames.
			s.queueInput(f.Payload)
		case FrameResize:
			if len(f.Payload) >= 4 {
				cols := binary.BigEndian.Uint16(f.Payload[0:])
				rows := binary.BigEndian.Uint16(f.Payload[2:])
				s.mu.Lock()
				s.cols, s.rows = cols, rows
				exited := s.exited
				s.mu.Unlock()
				if !exited {
					_ = pty.Setsize(s.pty, &pty.Winsize{Cols: cols, Rows: rows})
				}
			}
		case FrameSignal:
			if len(f.Payload) >= 4 {
				s.signal(syscall.Signal(int32(binary.BigEndian.Uint32(f.Payload))))
			}
		case FrameClose:
			go s.shutdown("the daemon closed the pane")
			return
		}
	}
}

// write streams ring output (then the exit status) to c until it detaches.
func (s *Server) write(c *client) {
	defer s.detach(c)
	sentExit := false
	for {
		s.mu.Lock()
		data, at := s.ring.ReadFromMax(c.offset, maxChunk)
		c.offset = at + uint64(len(data))
		exited, status := s.exited, s.exitStatus
		s.mu.Unlock()

		switch {
		case len(data) > 0:
			if WriteFrame(c.conn, FrameOutput, append(u64(at), data...)) != nil {
				return
			}
			continue
		case exited && !sentExit:
			if WriteFrame(c.conn, FrameExit, i32(status)) != nil {
				return
			}
			sentExit = true
		}
		select {
		case <-c.wake:
		case <-c.done:
			return
		}
	}
}

func (s *Server) detach(c *client) {
	s.mu.Lock()
	if _, ok := s.clients[c]; ok {
		delete(s.clients, c)
		s.lastDetach = time.Now()
	}
	s.mu.Unlock()
	c.close()
}

func (s *Server) idleWatch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			alone := len(s.clients) == 0
			since := time.Since(s.lastDetach)
			idle := s.cfg.IdleExit > 0 && s.exited && alone && since > s.cfg.IdleExit
			orphaned := s.cfg.Install != "" && s.cfg.OrphanExit > 0 && alone && since > s.cfg.OrphanExit
			s.mu.Unlock()
			if idle {
				s.shutdown("idle: the child exited and no one was attached")
				return
			}
			if orphaned && gone(s.cfg.Install) {
				s.shutdown("orphaned: this build was deleted and no one has been attached since")
				return
			}
		}
	}
}

// gone reports whether path no longer exists. Only a
// definite "not there" counts: a disk that can't be read right now isn't a
// reason to end someone's pane.
func gone(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

func (s *Server) queueInput(p []byte) {
	s.inMu.Lock()
	s.inBuf = append(s.inBuf, p)
	s.inCond.Signal()
	s.inMu.Unlock()
}

// writePTY feeds queued input to the PTY; it may block while the program
// isn't reading, without holding anything else up.
func (s *Server) writePTY() {
	for {
		s.inMu.Lock()
		for len(s.inBuf) == 0 {
			select {
			case <-s.done:
				s.inMu.Unlock()
				return
			default:
			}
			s.inCond.Wait()
		}
		batch := s.inBuf
		s.inBuf = nil
		s.inMu.Unlock()
		for _, p := range batch {
			if _, err := s.pty.Write(p); err != nil && (errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF)) {
				return
			}
		}
	}
}

// signal sends sig to the PTY's foreground process group (the running job,
// not just the shell), falling back to the child's group.
func (s *Server) signal(sig syscall.Signal) {
	pgrp := 0
	if rc, err := s.pty.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) {
			if v, err := unix.IoctlGetInt(int(fd), unix.TIOCGPGRP); err == nil {
				pgrp = v
			}
		})
	}
	if pgrp <= 0 {
		pgrp = s.cmd.Process.Pid
	}
	_ = syscall.Kill(-pgrp, sig)
}

// logf writes a timestamped line to stderr (the holder's log file).
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s pane-holder: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
