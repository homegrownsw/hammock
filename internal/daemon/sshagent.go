package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// SSH agent forwarding between your linked computers, like ssh -A:
// a pane on Studio uses the SSH keys of the computer you're at.
//
// Panes' SSH_AUTH_SOCK is this daemon's socket (run/ssh-agent.sock). It
// serves this computer's own agent (the daemon's SSH_AUTH_SOCK) as it is:
// listing, signing, ssh-add, locking. When you're at another of your
// computers, that one's keys are listed too, their comments saying where
// they're from ("id_ed25519 (from MacBook)"), and signing with one asks
// that computer over the link: only listing and signing ever go there.
//
// Where you are: the computer a person last typed into the pane from
// (within awayAfter), else the computer used last, by the idle times
// linked computers share (the one that notifies). Never a guest's: Groups
// shared with others don't forward keys either way. A program under an
// agent asks you first (a grant: Needs You on all your computers, and the
// phone), and every signature made elsewhere goes in the audit log, here
// and on the computer whose key it was.
//
// ssh-agent-forwarding = false in ~/.config/hammock/config turns it off:
// panes get no keys from elsewhere, and this computer doesn't lend its
// keys. (Panes still use Hammock's agent: your own agent's keys pass through,
// and the Hammock Key is there, hammockkey.go.)

// SSHAgentSocketPath is the agent socket panes are given.
func SSHAgentSocketPath(dir string) string { return filepath.Join(dir, "run", "ssh-agent.sock") }

// sshForwarding reads ssh-agent-forwarding from the config file (on unless
// it says false). Read each time: Settings changes it while the daemon runs.
func sshForwarding() bool { return hammockconfig.Bool("ssh-agent-forwarding", true) }

// sshHub is the agent socket's listener and what it knows.
type sshHub struct {
	d   *Daemon
	ln  net.Listener
	own string // this computer's own agent (the daemon's SSH_AUTH_SOCK), "" if none

	signLink string // hmk-ssh-sign, for panes' git ("" if there's no CLI to link)

	// guarded by d.mu
	typed map[string]typedBy // pane → who a person last typed into it from
}

type typedBy struct {
	uid string // a computer: this one, or a linked one
	at  time.Time
}

func newSSHHub(d *Daemon) *sshHub {
	own := os.Getenv("SSH_AUTH_SOCK")
	if own != "" && filepath.Clean(own) == filepath.Clean(SSHAgentSocketPath(d.cfg.Dir)) {
		own = "" // (a daemon started from one of its own panes)
	}
	return &sshHub{d: d, own: own, typed: map[string]typedBy{}}
}

func (h *sshHub) listen(path string) error {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	h.ln = ln
	h.signLink = h.d.makeSSHSignLink()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go h.serve(c)
		}
	}()
	return nil
}

func (h *sshHub) close() {
	if h.ln != nil {
		h.ln.Close()
	}
}

// paneEnv is what a new pane gets: Hammock's agent socket (your own agent's
// keys and the Hammock Key; with ssh-agent-forwarding, the keys of the
// computer you're at too), and git's SSH signing through hmk-ssh-sign
// (sshsetup.go), which signs with your usual key when it can and with the
// Hammock Key when it can't. GIT_CONFIG_* adds to your git config for the
// pane's programs only.
func (h *sshHub) paneEnv() []string {
	if h == nil || h.ln == nil {
		return nil // (no socket: panes keep the daemon's own agent)
	}
	env := []string{"SSH_AUTH_SOCK=" + SSHAgentSocketPath(h.d.cfg.Dir)}
	if link := h.signLink; link != "" {
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=gpg.ssh.program", "GIT_CONFIG_VALUE_0="+link)
	}
	return env
}

// noteTyped records a person typing into a pane: from this computer (its
// app, TUI or CLI, not an agent or a phone) or from a linked one (its
// daemon, relaying what was typed there). Callers hold d.mu.
func (h *sshHub) noteTyped(c *conn, paneID string) {
	d := h.d
	uid := ""
	switch {
	case c.peer != "":
		if !d.isLinked(c.peer) {
			return // a guest: never a source of keys
		}
		uid = c.peer
	case c.remote || c.caller.isAgent():
		return
	default:
		uid = d.id.me.UserId
	}
	now := time.Now()
	h.typed[paneID] = typedBy{uid: uid, at: now}
	if len(h.typed) > 256 {
		for id, t := range h.typed {
			if now.Sub(t.at) > awayAfter {
				delete(h.typed, id) // (closed panes, and old news)
			}
		}
	}
}

// source is the linked computer whose keys a program in paneID gets: where
// you are, "" when that's here (or nobody's anywhere). Callers hold d.mu.
func (h *sshHub) source(paneID string) string {
	d := h.d
	now := time.Now()
	reachable := func(uid string) bool {
		l := d.linkOf(uid)
		return d.isLinked(uid) && l != nil && l.online
	}
	if t, ok := h.typed[paneID]; ok && now.Sub(t.at) < awayAfter {
		if t.uid == d.id.me.UserId {
			return ""
		}
		if reachable(t.uid) {
			return t.uid
		}
	}
	best, bestAt := "", d.comp.self.LastInputUnixMs
	for uid, sl := range d.comp.links {
		if r := sl.Record; r != nil && reachable(uid) && r.Notifies && r.LastInputUnixMs > bestAt {
			best, bestAt = uid, r.LastInputUnixMs
		}
	}
	if best == "" || bestAt < now.Add(-awayAfter).UnixMilli() {
		return ""
	}
	return best
}

// serve answers one connection to the socket (ssh opens one per run).
func (h *sshHub) serve(c net.Conn) {
	defer c.Close()
	d := h.d
	pane, isAgent, direct := d.locateProcess(peerPID(c), "")
	if pane != "" && !direct {
		// Found only by the terminal it kept or a multiplexer: it may be
		// what an agent left running (person.go), so it asks as one would.
		isAgent = true
	}
	if pane != "" && !isAgent {
		// A pane an agent opened (and what it runs) acts for that agent,
		// as caller.go's owners has it: git push in one asks too.
		d.mu.Lock()
		isAgent = d.agentMade[pane]
		d.mu.Unlock()
	}
	a := &sshAgentConn{h: h, pane: pane, agent: isAgent, pid: peerPID(c), remote: map[string]remoteKey{}}
	up := h.upstream()
	_, a.upAsks = sshAgentName(up)
	if up != "" {
		if oc, err := net.DialTimeout("unix", up, 2*time.Second); err == nil {
			defer oc.Close()
			a.own = agent.NewClient(oc)
		}
	}
	_ = agent.ServeAgent(a, c)
}

// sshAgentConn is the agent one connection talks to: this computer's own,
// plus, while you're elsewhere, the keys of the computer you're at.
type sshAgentConn struct {
	h     *sshHub
	pane  string // the pane the program runs in ("" outside Hammock's panes)
	agent bool   // run by an agent
	pid   int    // the program on the other end
	own   agent.ExtendedAgent
	// The upstream agent asks you before it signs (1Password and the
	// like): while you're away from this Mac its keys aren't offered.
	upAsks bool

	mu     sync.Mutex
	remote map[string]remoteKey // key blob → where it was listed from
}

type remoteKey struct {
	uid     string // the linked computer
	comment string // as its agent names it
}

var errNoOwnAgent = errors.New("no SSH agent on this computer")

// List offers your keys in the order ssh should try them (it gives up
// after a few: servers' MaxAuthTries): at this Mac, this computer's own
// agent's first and the Hammock Key last; away from it, the Hammock Key first,
// after the keys of the computer you're at (they ask you there), and the
// own agent's keys only if it doesn't need you here to approve.
func (a *sshAgentConn) List() ([]*agent.Key, error) {
	var keys []*agent.Key
	if a.own != nil {
		own, err := a.own.List()
		if err != nil {
			return nil, err
		}
		keys = own
	}
	d := a.h.d
	on := sshForwarding() // (reads the config file: not under d.mu)
	away := a.h.awayFrom(a.pane)
	d.mu.Lock()
	uid, label := "", ""
	// Only programs in Hammock's panes get keys from elsewhere: one it can't
	// place could be an agent nobody would be asked about.
	if a.pane != "" && on {
		uid, label = a.h.source(a.pane), d.tabPath(a.pane)
	}
	name := d.computerName(uid)
	fk := d.hammockKey.agentKey()
	d.mu.Unlock()
	if away && a.upAsks {
		keys = nil // nobody's here to approve them
	}
	var fkKey []*agent.Key
	if fk != nil {
		if pk, err := ssh.ParsePublicKey(fk.blob); err == nil {
			fkKey = []*agent.Key{{Format: pk.Type(), Blob: fk.blob, Comment: fk.comment}}
		}
	}
	keys = slices.DeleteFunc(keys, func(k *agent.Key) bool { return fk != nil && bytes.Equal(k.Blob, fk.blob) })
	var remote []*agent.Key
	if uid != "" {
		remote = a.remoteKeys(uid, name, label, keys)
	}
	if away {
		return slices.Concat(remote, fkKey, keys), nil
	}
	return slices.Concat(keys, remote, fkKey), nil
}

// remoteKeys lists the keys of the linked computer you're at, leaving
// out those listed here already (signed here).
func (a *sshAgentConn) remoteKeys(uid, name, label string, have []*agent.Key) []*agent.Key {
	d := a.h.d
	r, err := d.sshCall(uid, &pb.SshAgentCall{List: true, PaneLabel: label}, 5*time.Second)
	if err != nil {
		d.log.Debug("ssh agent: list", "computer", name, "err", err)
		return nil // this computer's own keys still work
	}
	seen := map[string]bool{}
	for _, k := range have {
		seen[string(k.Blob)] = true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var keys []*agent.Key
	for _, k := range r.Keys {
		if seen[string(k.Blob)] {
			continue // here too: signed here
		}
		pk, err := ssh.ParsePublicKey(k.Blob)
		if err != nil {
			continue
		}
		seen[string(k.Blob)] = true
		a.remote[string(k.Blob)] = remoteKey{uid: uid, comment: k.Comment}
		keys = append(keys, &agent.Key{Format: pk.Type(), Blob: k.Blob, Comment: strings.TrimSpace(k.Comment + " (from " + name + ")")})
	}
	return keys
}

// sshAwayIdle is how long this Mac goes untouched before its keys that ask
// you (1Password's) stop being offered. Longer than awayAfter: you may be
// sitting at it reading what an agent does, and 1Password's prompt is
// the one you'd answer then.
const sshAwayIdle = 10 * time.Minute

// awayFrom: you're not at this Mac, as far as signing for pane goes: its
// screen is locked, you've used Hammock on your phone since you last
// touched it, you're at another of your computers (typing into the pane
// from there, or using it since), or it's been left a good while (and you
// haven't just typed into the pane here).
func (h *sshHub) awayFrom(pane string) bool {
	locked := screenLocked()
	idle := idleTime() // (off d.mu: it asks IOKit)
	d := h.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if locked || d.usingPhone(idle) {
		return true
	}
	if t, ok := h.typed[pane]; ok && t.uid == d.id.me.UserId && time.Since(t.at) < awayAfter {
		return false
	}
	if pane != "" && h.source(pane) != "" {
		// Elsewhere: a key that asks here (1Password) would wait on a
		// prompt nobody sees. Its twin from where you are signs instead.
		return true
	}
	return idle >= sshAwayIdle
}

func (a *sshAgentConn) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return a.SignWithFlags(key, data, 0)
}

func (a *sshAgentConn) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	blob := key.Marshal()
	d0 := a.h.d
	d0.mu.Lock()
	isHammock := d0.hammockKey.isKey(blob)
	d0.mu.Unlock()
	if isHammock {
		// The Hammock Key: you approve each use (or a while of them in the
		// pane: an agent's only if you choose), so it needs no grant first.
		return d0.hammockKey.sign(a.pane, a.agent, a.pid, data)
	}
	a.mu.Lock()
	rk := a.remote[string(blob)]
	a.mu.Unlock()
	uid := rk.uid
	if uid == "" {
		if a.own == nil {
			return nil, errNoOwnAgent
		}
		return a.own.SignWithFlags(key, data, flags)
	}
	d := a.h.d
	on := sshForwarding()
	d.mu.Lock()
	name, label, keyName := d.computerName(uid), d.tabPath(a.pane), cmpOr(rk.comment, ssh.FingerprintSHA256(key))
	on = on && d.isLinked(uid)
	d.mu.Unlock()
	if !on {
		return nil, errors.New("SSH agent forwarding is off")
	}
	if a.agent && !d.askSSHGrant(a.pane, keyName, name) {
		return nil, errors.New("not allowed")
	}
	r, err := d.sshCall(uid, &pb.SshAgentCall{KeyBlob: blob, Data: data, Flags: uint32(flags), PaneLabel: label}, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	actor := "user" // a program you ran
	if a.agent {
		actor = "agent:" + a.pane
	}
	d.audit(actor, "ssh-sign", a.pane, keyName+" from "+name)
	d.mu.Unlock()
	return &ssh.Signature{Format: r.SigFormat, Blob: r.SigBlob, Rest: r.SigRest}, nil
}

// What changes the agent goes to this computer's own: keys elsewhere are
// only ever listed and used.
func (a *sshAgentConn) Add(k agent.AddedKey) error {
	if a.own == nil {
		return errNoOwnAgent
	}
	return a.own.Add(k)
}

func (a *sshAgentConn) Remove(key ssh.PublicKey) error {
	if a.own == nil {
		return errNoOwnAgent
	}
	return a.own.Remove(key)
}

func (a *sshAgentConn) RemoveAll() error {
	if a.own == nil {
		return errNoOwnAgent
	}
	return a.own.RemoveAll()
}

func (a *sshAgentConn) Lock(passphrase []byte) error {
	if a.own == nil {
		return errNoOwnAgent
	}
	return a.own.Lock(passphrase)
}

func (a *sshAgentConn) Unlock(passphrase []byte) error {
	if a.own == nil {
		return errNoOwnAgent
	}
	return a.own.Unlock(passphrase)
}

func (a *sshAgentConn) Signers() ([]ssh.Signer, error) { return nil, errors.New("not supported") }

// sshAwayExtension asks Hammock's agent whether you're away from this Mac as
// far as the asking program's pane goes (hmk-ssh-sign: sign with the Hammock
// Key rather than a key nobody's here to approve). Replies one byte, 1 or 0.
const sshAwayExtension = "away@hammock.homegrownsw.com"

func (a *sshAgentConn) Extension(typ string, contents []byte) ([]byte, error) {
	if typ == sshAwayExtension {
		if a.h.awayFrom(a.pane) {
			return []byte{1}, nil
		}
		return []byte{0}, nil
	}
	if a.own == nil {
		return nil, agent.ErrExtensionUnsupported
	}
	return a.own.Extension(typ, contents)
}

// computerName is a linked computer's name. Callers hold d.mu.
func (d *Daemon) computerName(uid string) string {
	if sl := d.comp.links[uid]; sl != nil && sl.Record != nil && sl.Record.Name != "" {
		return sl.Record.Name
	}
	return "another computer"
}

// sshCall asks a linked computer's daemon for its keys or a signature.
func (d *Daemon) sshCall(uid string, m *pb.SshAgentCall, timeout time.Duration) (*pb.SshAgentReply, error) {
	d.mu.Lock()
	l := d.linkOf(uid)
	d.mu.Unlock()
	if l == nil {
		return nil, errors.New("not linked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cl, err := l.connected(ctx, 2*time.Second)
	if err != nil {
		return nil, err
	}
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SshAgentCall{SshAgentCall: m}})
	if err != nil {
		return nil, err
	}
	if e := r.GetError(); e != nil {
		return nil, errors.New(e.Message)
	}
	rep := r.GetSshAgentReply()
	if rep == nil {
		return nil, errors.New("no reply")
	}
	return rep, nil
}

// askSSHGrant asks you whether an agent may sign with a key on another of
// your computers, waiting for the answer (or grantTimeout). An answer of
// "allow for this Group", or one allowed in the last couple of minutes,
// is remembered as other grants are.
func (d *Daemon) askSSHGrant(paneID, key, computer string) bool {
	d.mu.Lock()
	group := d.groupOfPane(paneID)
	if paneID == "" || d.panes[paneID] == nil {
		d.mu.Unlock()
		return false // (outside Hammock's panes there's nobody to name)
	}
	if d.granted(paneID, "ssh", paneID) {
		d.mu.Unlock()
		return true
	}
	headline := fmt.Sprintf("%s on %s wants to use your SSH key %s from %s",
		d.agentWho(paneID), cmpOr(d.id.me.DeviceName, "this computer"), key, computer)
	answer := make(chan bool, 1)
	d.openGrant(&pendingGrant{
		req: &pb.GrantRequest{
			Id: newID("gr"), RequesterPaneId: paneID, TargetPaneId: paneID,
			Action: "ssh", Headline: headline, CreatedUnixMs: time.Now().UnixMilli(),
		},
		group: group,
		done:  func(allowed bool) { answer <- allowed },
	})
	d.mu.Unlock()
	return <-answer
}

// sshAgentCall answers a linked computer asking for this computer's keys
// (you're here) or a signature with one: off d.mu, as the agent may ask
// you first (a hardware key, 1Password). Only from a linked computer's
// daemon (ownerCheck lets nothing else through; this checks again).
func (d *Daemon) sshAgentCall(c *conn, id uint64, m *pb.SshAgentCall) (*pb.ServerMessage, error) {
	own := d.sshHub.upstream() // (reads files: not under d.mu)
	d.mu.Lock()
	peer := c.peer
	linked := peer != "" && d.isLinked(peer)
	name := d.computerName(peer)
	me := cmpOr(d.id.me.DeviceName, "that computer")
	lastUsed := time.UnixMilli(d.comp.self.LastInputUnixMs)
	d.mu.Unlock()
	if !linked {
		return nil, errors.New("only for your other computers")
	}
	if !sshForwarding() {
		return nil, errors.New("SSH agent forwarding is off on " + me)
	}
	// This computer checks too that you're at it (a Mac knows; elsewhere
	// nothing says): a computer that's lost or taken can't sign with your
	// keys while you're away from this one.
	if !m.List && hardware().os == "macos" && time.Since(lastUsed) > awayAfter {
		return nil, errors.New("you're not at " + me)
	}
	if own == "" {
		return nil, errNoOwnAgent
	}
	go func() {
		reply := &pb.ServerMessage{ReplyTo: id}
		r, err := d.ownAgentDo(own, m)
		if err != nil {
			reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
		} else {
			reply.Msg = &pb.ServerMessage_SshAgentReply{SshAgentReply: r}
			if !m.List {
				key := "a key"
				if pk, err := ssh.ParsePublicKey(m.KeyBlob); err == nil {
					key = ssh.FingerprintSHA256(pk)
				}
				d.mu.Lock()
				d.audit("computer:"+peer, "ssh-lent", "", key+" for "+cmpOr(m.PaneLabel, "a pane")+" on "+name)
				d.mu.Unlock()
			}
		}
		c.send(reply)
	}()
	return nil, errDeferred
}

// ownAgentDo lists this computer's own agent's keys, or signs with one.
func (d *Daemon) ownAgentDo(own string, m *pb.SshAgentCall) (*pb.SshAgentReply, error) {
	oc, err := net.DialTimeout("unix", own, 2*time.Second)
	if err != nil {
		return nil, errNoOwnAgent
	}
	defer oc.Close()
	ac := agent.NewClient(oc)
	keys, err := ac.List()
	if err != nil {
		return nil, err
	}
	if m.List {
		r := &pb.SshAgentReply{}
		for _, k := range keys {
			r.Keys = append(r.Keys, &pb.SshAgentKey{Blob: k.Blob, Comment: k.Comment})
		}
		return r, nil
	}
	for _, k := range keys {
		if bytes.Equal(k.Blob, m.KeyBlob) {
			sig, err := ac.SignWithFlags(k, m.Data, agent.SignatureFlags(m.Flags))
			if err != nil {
				return nil, err
			}
			return &pb.SshAgentReply{SigFormat: sig.Format, SigBlob: sig.Blob, SigRest: sig.Rest}, nil
		}
	}
	return nil, errors.New("that key isn't on this computer")
}
