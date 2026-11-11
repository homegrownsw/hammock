package daemon

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The Hammock Key: an SSH key of this Mac's own, in its Secure Enclave,
// that signs only when you approve. Your usual keys (1Password, Secretive,
// a YubiKey, files) go on working at your desk; the Hammock Key covers the
// rest: you're away from this Mac with only your phone, or an agent wants
// to push and you'd rather say yes to that push than to an app.
//
// The Hammock Key helper (helpers/key, Contents/Helpers/Hammock Key.app) holds
// it and decides: it signs with Touch ID here, a phone's approval it checks
// itself (a signature by that phone's Secure Enclave key over this very
// request, from a phone you let approve with Touch ID), or a token for a
// window one of those opened. The daemon only routes: it lists the key in
// panes' agent, asks here (the helper's Touch ID prompt) and on the phones
// at once, and gives the helper whichever answer comes first. So nothing
// that can talk to the daemon gets a signature you didn't approve. And the
// helper takes requests only from this daemon (runKeyHelper), so nothing
// can run it to skip the daemon's rules; what its prompt says is in its
// own words, beside where the daemon says a request is from.
//
// "For a while" is 10 minutes, in the pane that asked: a rebase signing
// many commits, a deploy script. Touch ID here opens one for your own
// programs. An agent's is 5 minutes (a script's few commits, a burst of ssh
// to a remote machine), and only if you choose it: a box on the Mac's
// prompt, a button on the phone. The two are kept apart: an agent never
// signs within a window you opened for your own programs, nor they within
// its. The helper fixes how long each lasts (keyWindowMs and
// agentKeyWindowMs in its main.swift), and its prompt says so.

const (
	hammockKeyTimeout     = 2 * time.Minute  // how long a request waits on you
	hammockKeyWindow      = 10 * time.Minute // "this pane for a while"
	hammockKeyAgentWindow = 5 * time.Minute  // an agent's while
)

// hammockKeyHub is the daemon's side of the Hammock Key: guarded by d.mu.
type hammockKeyHub struct {
	d      *Daemon
	status *pb.HammockKeyStatus
	blob   []byte // the public key, SSH wire format (nil: none)
	keyID  string // its id, as approvals name it
	// approvers' keys as the helper has them (base64 x9.63), by device
	approverKeys map[string]string
	retry        *time.Timer // a status check again, after one failed
	reqs         map[string]*keyReq
	windows      map[windowKey]keyWindow // a window you opened, by pane and whose

	// Requests' prompts and pushes end with the daemon (close).
	ctx    context.Context
	stop   context.CancelFunc
	pushes sync.WaitGroup
	closed bool // guarded by d.mu
}

type keyReq struct {
	req    *pb.HammockKeyRequest
	data   []byte
	actor  string
	done   chan keyResult // one result: the first answer
	cancel func()         // withdraws the Touch ID prompt
	pushed bool           // sent to your phones (guarded by d.mu)
}

type keyResult struct {
	sig   []byte // raw r‖s
	token string // a window it opened
	until int64
	by    string // who approved: "this Mac", a phone's name
	err   error
}

type keyWindow struct {
	token string
	until time.Time
}

// windowKey is whose a window is: a pane's own programs', or its agent's.
type windowKey struct {
	pane  string
	agent bool
}

// windowable: a request from pane may open a window (one in a pane of
// Hammock's, not from outside it).
func windowable(pane string) bool { return pane != "" && pane != outsidePane }

var (
	errNoHammockKey = errors.New("there's no Hammock Key on this Mac (Settings ▸ SSH)")
	errKeyDeclined  = errors.New("not allowed")
)

func newHammockKeyHub(d *Daemon) *hammockKeyHub {
	ctx, stop := context.WithCancel(context.Background())
	return &hammockKeyHub{d: d, status: &pb.HammockKeyStatus{}, reqs: map[string]*keyReq{}, windows: map[windowKey]keyWindow{}, ctx: ctx, stop: stop}
}

// close withdraws requests' prompts and waits for their pushes to stop.
// Off d.mu.
func (k *hammockKeyHub) close() {
	k.d.mu.Lock()
	k.closed = true
	ids := slices.Collect(maps.Keys(k.reqs))
	k.d.mu.Unlock()
	k.stop()
	for _, id := range ids {
		k.finish(id, keyResult{err: errors.New("Hammock quit before you answered")})
	}
	k.pushes.Wait()
}

// ---------- the helper ----------

// keyHelperPath finds the helper: $HMK_KEY_HELPER, else the one bundled in
// the app (Contents/Helpers), else one next to hmkd (bin/). Replaceable in
// tests.
var keyHelperPath = func() string {
	if p := helperEnv("HMK_KEY_HELPER"); p != "" { // (vetted: helperpath.go)
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	const app = "Hammock Key.app/Contents/MacOS/Hammock Key"
	for _, p := range []string{filepath.Join(filepath.Dir(exe), "..", "Helpers", app), filepath.Join(filepath.Dir(exe), app)} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// keyHelperBeta keeps Hammock β's key apart from the real one: anything but
// the prod app (β, a worktree's β, a daemon from bin/) uses β's.
var keyHelperBeta = func() bool { return bundleInfo("CFBundleIdentifier") != "com.homegrownsw.hammock" }

// keyHelperProtocol is the version of what runKeyHelper sends: the helper
// takes only its own, as the two ship together in the app.
const keyHelperProtocol = 1

// runKeyHelper runs one helper command, JSON in and out. Its exit 2 (you
// said no) is errKeyDeclined; cancelling ctx withdraws its prompt.
//
// The request doesn't go on the helper's command line or stdin, which
// anything that runs the helper chooses: the helper fetches it from this
// daemon over Mach (keyRendezvous) and takes it only if the kernel says
// Hammock's daemon sent it (helpers/key/rendezvous.h). The answer comes
// back on its stdout and exit code.
func runKeyHelper(ctx context.Context, cmd string, in map[string]any) (map[string]any, error) {
	path := keyHelperPath()
	if path == "" {
		return nil, errors.New("this build has no Hammock Key helper")
	}
	if in == nil {
		in = map[string]any{}
	}
	req, err := json.Marshal(map[string]any{"v": keyHelperProtocol, "command": cmd, "beta": keyHelperBeta(), "input": in})
	if err != nil {
		return nil, err
	}
	rv, err := newKeyRendezvous()
	if err != nil {
		return nil, err
	}
	defer rv.close()
	c := exec.CommandContext(ctx, path, "--daemon", rv.name)
	c.Cancel = func() error { return c.Process.Signal(syscall.SIGTERM) }
	c.WaitDelay = 2 * time.Second
	var out, stderr bytes.Buffer
	c.Stdout, c.Stderr = &out, &stderr
	if err := c.Start(); err != nil {
		return nil, err
	}
	stop, served := make(chan struct{}), make(chan error, 1)
	go func() { served <- rv.serve(c.Process.Pid, req, stop) }()
	err = c.Wait()
	close(stop)
	<-served
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 2 {
			if msg == "" {
				return nil, errKeyDeclined
			}
			return nil, fmt.Errorf("%w: %s", errKeyDeclined, msg)
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var res map[string]any
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return nil, fmt.Errorf("the Hammock Key helper said something odd: %w", err)
	}
	return res, nil
}

// ---------- status ----------

// refresh asks the helper about the key and tells your clients. Off d.mu.
func (k *hammockKeyHub) refresh() *pb.HammockKeyStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := runKeyHelper(ctx, "status", nil)
	st := &pb.HammockKeyStatus{}
	var blob []byte
	var keyID string
	approverKeys := map[string]string{}
	if err != nil {
		// Keep what was known (the key stays in panes' agent) and look
		// again in a while: a keychain hiccup isn't the key gone.
		d := k.d
		d.mu.Lock()
		defer d.mu.Unlock()
		st = proto.CloneOf(k.status)
		st.Error = err.Error()
		k.status = st
		if k.retry == nil && keyHelperPath() != "" {
			k.retry = time.AfterFunc(30*time.Second, func() {
				d.mu.Lock()
				k.retry = nil
				d.mu.Unlock()
				k.refresh()
			})
		}
		d.publish(&pb.StateUpdate{HammockKey: proto.CloneOf(st)})
		return proto.CloneOf(st)
	} else {
		st.Available, _ = res["enclave"].(bool)
		st.Exists, _ = res["exists"].(bool)
		if f, ok := res["created"].(float64); ok {
			st.CreatedUnixMs = int64(f)
		}
		if st.Exists {
			pub, perr := x963SSHKey(res["public_key"])
			if perr != nil {
				st.Error, st.Exists = perr.Error(), false
			} else {
				blob = pub.Marshal()
				st.Fingerprint = ssh.FingerprintSHA256(pub)
				keyID, _ = res["key_id"].(string)
			}
		}
		for _, a := range anySlice(res["approvers"]) {
			m, _ := a.(map[string]any)
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			key, _ := m["key"].(string)
			st.Approvers = append(st.Approvers, &pb.HammockKeyApprover{DeviceId: id, Name: name})
			approverKeys[id] = key
		}
	}
	d := k.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if st.Exists {
		pub, _ := ssh.ParsePublicKey(blob)
		st.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " " + k.keyComment()
		st.PublicKeyPath = filepath.Join(d.cfg.Dir, "hmk-key.pub")
		if cur, _ := os.ReadFile(st.PublicKeyPath); string(cur) != st.PublicKey+"\n" {
			_ = os.WriteFile(st.PublicKeyPath, []byte(st.PublicKey+"\n"), 0o644)
		}
	} else {
		_ = os.Remove(filepath.Join(d.cfg.Dir, "hmk-key.pub"))
		k.windows = map[windowKey]keyWindow{}
	}
	k.approverKeys = approverKeys
	st.CanAdd = k.addable(st.Approvers)
	k.status, k.blob, k.keyID = st, blob, keyID
	d.publish(&pb.StateUpdate{HammockKey: proto.CloneOf(st)})
	return proto.CloneOf(st)
}

// keyComment is the key's name in agents' lists and authorized_keys.
// Callers hold d.mu.
func (k *hammockKeyHub) keyComment() string {
	name := "Hammock Key"
	if keyHelperBeta() {
		name = "Hammock Key (β)"
	}
	if dn := k.d.id.me.DeviceName; dn != "" {
		name += " " + dn
	}
	return strings.ReplaceAll(name, " ", "-")
}

// addable: paired phones that sent an approval key and can't approve yet,
// or whose key has changed since they were let approve (a new phone, the
// app reinstalled). Callers hold d.mu.
func (k *hammockKeyHub) addable(approvers []*pb.HammockKeyApprover) []*pb.HammockKeyApprover {
	d := k.d
	recs, err := d.store.Records("approval-key")
	if err != nil || len(recs) == 0 {
		return nil
	}
	devs, _ := d.store.Devices()
	var out []*pb.HammockKeyApprover
	for _, dev := range devs {
		if recs[dev.ID] == nil {
			continue
		}
		if slices.ContainsFunc(approvers, func(a *pb.HammockKeyApprover) bool { return a.DeviceId == dev.ID }) {
			m := &pb.SetApprovalKey{}
			if proto.Unmarshal(recs[dev.ID], m) != nil || base64.StdEncoding.EncodeToString(m.PublicKey) == k.approverKeys[dev.ID] {
				continue
			}
		}
		out = append(out, &pb.HammockKeyApprover{DeviceId: dev.ID, Name: cmpOr(dev.Name, "a phone")})
	}
	return out
}

// approverRecord is a phone's approval key as the helper takes it.
// Callers hold d.mu.
func (k *hammockKeyHub) approverRecord(deviceID string) (map[string]any, error) {
	d := k.d
	recs, err := d.store.Records("approval-key")
	if err != nil {
		return nil, err
	}
	b := recs[deviceID]
	if b == nil {
		return nil, errors.New("that phone hasn't sent its approval key: open Hammock on it")
	}
	m := &pb.SetApprovalKey{}
	if err := proto.Unmarshal(b, m); err != nil {
		return nil, err
	}
	name := "a phone"
	devs, _ := d.store.Devices()
	for _, dev := range devs {
		if dev.ID == deviceID {
			name = cmpOr(dev.Name, name)
		}
	}
	return map[string]any{"id": deviceID, "name": name, "key": base64.StdEncoding.EncodeToString(m.PublicKey)}, nil
}

// command creates or deletes the key, or changes who may approve it
// (HammockKeyCommand). Off d.mu: the helper asks you with Touch ID.
func (k *hammockKeyHub) command(m *pb.HammockKeyCommand) (*pb.HammockKeyStatus, error) {
	d := k.d
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var err error
	switch m.Op {
	case pb.HammockKeyCommand_OP_STATUS, pb.HammockKeyCommand_OP_UNSPECIFIED:
	case pb.HammockKeyCommand_OP_CREATE:
		d.mu.Lock()
		var approvers []any
		names := []string{}
		for _, a := range k.addable(nil) {
			if r, err := k.approverRecord(a.DeviceId); err == nil {
				approvers = append(approvers, r)
				names = append(names, a.Name)
			}
		}
		d.mu.Unlock()
		// The helper words the prompt itself, naming each phone it lets
		// approve by its key.
		_, err = runKeyHelper(ctx, "create", map[string]any{"approvers": approvers})
		if err == nil {
			d.mu.Lock()
			d.audit("user", "hmk-key-created", "", strings.Join(names, ", "))
			d.mu.Unlock()
		}
	case pb.HammockKeyCommand_OP_DELETE:
		_, err = runKeyHelper(ctx, "delete", nil)
		if err == nil {
			d.mu.Lock()
			d.audit("user", "hmk-key-deleted", "", "")
			d.mu.Unlock()
		}
	case pb.HammockKeyCommand_OP_ADD_APPROVER:
		d.mu.Lock()
		r, rerr := k.approverRecord(m.DeviceId)
		d.mu.Unlock()
		if rerr != nil {
			return nil, rerr
		}
		_, err = runKeyHelper(ctx, "add-approver", map[string]any{"approver": r})
		if err == nil {
			d.mu.Lock()
			d.audit("user", "hmk-key-approver", m.DeviceId, "added "+r["name"].(string))
			d.mu.Unlock()
		}
	case pb.HammockKeyCommand_OP_REMOVE_APPROVER:
		// No Touch ID: it only narrows who can approve, and unpairing a
		// phone (forgetDevice) takes its say away without asking.
		_, err = runKeyHelper(ctx, "remove-approver", map[string]any{"id": m.DeviceId})
		if err == nil {
			d.mu.Lock()
			d.audit("user", "hmk-key-approver", m.DeviceId, "removed")
			d.mu.Unlock()
		}
	default:
		return nil, errors.New("unknown Hammock Key command")
	}
	st := k.refresh()
	if err != nil {
		return nil, err
	}
	return st, nil
}

// setApprovalKey keeps a paired phone's approval key (it can't approve
// until you let it, with Touch ID here). Callers hold d.mu.
func (k *hammockKeyHub) setApprovalKey(c *conn, m *pb.SetApprovalKey) error {
	d := k.d
	if c.deviceID == "" {
		return errors.New("only paired phones have approval keys")
	}
	if _, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), m.PublicKey); err != nil {
		return errors.New("not a P-256 key")
	}
	recs, _ := d.store.Records("approval-key")
	if old := recs[c.deviceID]; old != nil {
		prev := &pb.SetApprovalKey{}
		if proto.Unmarshal(old, prev) == nil && bytes.Equal(prev.PublicKey, m.PublicKey) {
			return nil
		}
	}
	if err := d.store.PutRecord("approval-key", c.deviceID, m); err != nil {
		return err
	}
	k.status.CanAdd = k.addable(k.status.Approvers)
	d.publish(&pb.StateUpdate{HammockKey: proto.CloneOf(k.status)})
	return nil
}

// forgetDevice drops a phone you unpaired: its approval key, and its say
// over the Hammock Key. Off d.mu.
func (k *hammockKeyHub) forgetDevice(deviceID string) {
	d := k.d
	d.mu.Lock()
	d.persist(d.store.DeleteRecord("approval-key", deviceID))
	approver := slices.ContainsFunc(k.status.Approvers, func(a *pb.HammockKeyApprover) bool { return a.DeviceId == deviceID })
	d.mu.Unlock()
	if approver {
		_, _ = k.command(&pb.HammockKeyCommand{Op: pb.HammockKeyCommand_OP_REMOVE_APPROVER, DeviceId: deviceID})
	} else {
		k.refresh()
	}
}

// x963SSHKey makes an SSH public key of the helper's base64 x9.63 one.
func x963SSHKey(v any) (ssh.PublicKey, error) {
	s, _ := v.(string)
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, errors.New("the Hammock Key's public key is damaged")
	}
	pk, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
	if err != nil {
		return nil, errors.New("the Hammock Key's public key is damaged")
	}
	return ssh.NewPublicKey(pk)
}

func anySlice(v any) []any { s, _ := v.([]any); return s }

// ---------- in the agent ----------

// agentKey is the Hammock Key as panes' agent lists it, nil if there's none.
// Callers hold d.mu.
func (k *hammockKeyHub) agentKey() *agentKeyEntry {
	if k.blob == nil {
		return nil
	}
	return &agentKeyEntry{blob: slices.Clone(k.blob), comment: k.keyComment()}
}

type agentKeyEntry struct {
	blob    []byte
	comment string
}

// isKey: blob is the Hammock Key. Callers hold d.mu.
func (k *hammockKeyHub) isKey(blob []byte) bool { return k.blob != nil && bytes.Equal(k.blob, blob) }

// sign signs data with the Hammock Key for a program in pane, once you
// approve: a window the pane has, else Touch ID here and your phones at
// once, the first answer winning. Off d.mu.
func (k *hammockKeyHub) sign(pane string, agent bool, pid int, data []byte) (*ssh.Signature, error) {
	d := k.d
	actor := "user"
	if agent {
		actor = "agent:" + pane
	}
	what, command := describeSigning(data, pid)

	// A window this pane has, for whoever's asking (an agent never uses
	// yours, nor your programs its): the helper checks the token.
	wk := windowKey{pane, agent}
	d.mu.Lock()
	w, ok := k.windows[wk]
	d.mu.Unlock()
	if ok && windowable(pane) && time.Now().Before(w.until) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		res, err := runKeyHelper(ctx, "sign", map[string]any{"data": base64.StdEncoding.EncodeToString(data), "pane": pane, "agent": agent, "token": w.token})
		cancel()
		if err == nil {
			if sig, err := keySignature(res); err == nil {
				d.mu.Lock()
				d.audit(actor, "hmk-key-sign", pane, what+" ("+cmpOr(command, "a program")+"), within a while you allowed")
				d.mu.Unlock()
				return sig, nil
			}
		}
		d.mu.Lock()
		delete(k.windows, wk)
		d.mu.Unlock()
	}

	d.mu.Lock()
	if k.blob == nil {
		d.mu.Unlock()
		return nil, errNoHammockKey
	}
	now := time.Now()
	sum := sha256.Sum256(data)
	label := d.tabPath(pane)
	var window int64
	if windowable(pane) {
		window = hammockKeyWindow.Milliseconds()
		if agent {
			window = hammockKeyAgentWindow.Milliseconds()
		}
	}
	who := "A program"
	if agent {
		who = d.agentWho(pane)
	} else if label != "" {
		who = "A program in " + label
	}
	headline := who + ": " + what
	if command != "" {
		headline += " (" + command + ")"
	}
	r := &keyReq{
		req: &pb.HammockKeyRequest{
			Id: newID("fk"), What: what, Headline: headline, Command: command, PaneId: pane, PaneLabel: label,
			Agent: agent, DataHash: hex.EncodeToString(sum[:]), CreatedUnixMs: now.UnixMilli(),
			ExpiresUnixMs: now.Add(hammockKeyTimeout).UnixMilli(), WindowMs: window, KeyId: k.keyID,
		},
		data: data, actor: actor, done: make(chan keyResult, 1),
	}
	ctx, cancel := context.WithCancel(k.ctx)
	r.cancel = cancel
	id := r.req.Id
	k.reqs[id] = r
	d.audit(actor, "hmk-key-requested", pane, headline)
	d.publish(&pb.StateUpdate{HammockKeyRequests: []*pb.HammockKeyRequest{proto.CloneOf(r.req)}})
	phones := len(k.status.Approvers)
	if phones > 0 && !k.closed {
		k.pushes.Add(1)
		go func() {
			defer k.pushes.Done()
			k.pushWhenAway(ctx, id)
		}()
	}
	prompt, groupID := d.keyPrompt(pane, agent, command)
	d.mu.Unlock()
	if pic := d.groupPicture(groupID); pic != nil {
		prompt["group_picture"] = base64.StdEncoding.EncodeToString(pic)
	}

	// Here: the helper's Touch ID prompt, which says what's signed in its
	// own words; this is the rest (who, where). A "Don't Allow" is your
	// answer; not being able to ask (the screen's locked) leaves it to your
	// phone, if one may approve.
	go func() {
		var detail []string
		if agent {
			detail = append(detail, "for "+d.agentWhoLocked(pane))
		}
		if command != "" {
			detail = append(detail, command)
		}
		if label != "" {
			detail = append(detail, "in "+label)
		}
		res, err := runKeyHelper(ctx, "sign", map[string]any{
			// "window": allowing also opens this pane's window (an agent's
			// only if you tick its box), as long as the helper lets one last
			// (hammockKeyWindow, hammockKeyAgentWindow), which its prompt says.
			"data": base64.StdEncoding.EncodeToString(data), "pane": pane, "context": strings.Join(detail, ", "), "window": window > 0,
			"agent":  agent,
			"prompt": prompt,
		})
		switch {
		case errors.Is(err, context.Canceled):
		case errors.Is(err, errKeyDeclined):
			k.finish(id, keyResult{err: errKeyDeclined, by: "this Mac"})
		case err != nil && phones == 0:
			k.finish(id, keyResult{err: fmt.Errorf("can't ask you on this Mac (%v), and no phone may approve the Hammock Key", err)})
		case err != nil:
			d.log.Debug("hmk key: can't ask here", "err", err)
			k.pushIfPending(id) // (your phone, then, straight away)
		default:
			k.finish(id, helperResult(res, "this Mac"))
		}
	}()

	timer := time.AfterFunc(hammockKeyTimeout, func() { k.finish(id, keyResult{err: errors.New("nobody answered")}) })
	res := <-r.done
	timer.Stop()
	if res.err != nil {
		return nil, res.err
	}
	return ecdsaSig(res.sig)
}

// keyPrompt is where a request comes from, for the helper's own panel
// : the Group, its emoji, the tab, the agent asking (its harness, or
// "an agent"), the command. Also the Group's id, for its picture. Callers
// hold d.mu.
func (d *Daemon) keyPrompt(pane string, agent bool, command string) (map[string]any, string) {
	g, group, tab := d.tabParts(pane)
	prompt := map[string]any{"group": group, "tab": tab, "command": command}
	id := ""
	if g != nil {
		id = g.Id
		prompt["group_icon"] = g.Icon
	}
	if agent {
		prompt["agent"] = d.agentName(pane)
	}
	return prompt, id
}

// groupPicture is the picture the daemon serves for a Group (checked and
// shrunk: groupicons.go), or nil. Off d.mu.
func (d *Daemon) groupPicture(groupID string) []byte {
	if groupID == "" {
		return nil
	}
	d.icons.mu.Lock()
	defer d.icons.mu.Unlock()
	if ic := d.icons.by[groupID]; ic != nil {
		return ic.png
	}
	return nil
}

// agentWhoLocked is agentWho, taking d.mu.
func (d *Daemon) agentWhoLocked(pane string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.agentWho(pane)
}

func helperResult(res map[string]any, by string) keyResult {
	sigB64, _ := res["signature"].(string)
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != 64 {
		return keyResult{err: errors.New("the Hammock Key helper gave no signature")}
	}
	r := keyResult{sig: sig, by: by}
	r.token, _ = res["token"].(string)
	if f, ok := res["until"].(float64); ok {
		r.until = int64(f)
	}
	return r
}

func keySignature(res map[string]any) (*ssh.Signature, error) {
	r := helperResult(res, "")
	if r.err != nil {
		return nil, r.err
	}
	return ecdsaSig(r.sig)
}

// ecdsaSig is an SSH signature of a raw P-256 one (r‖s).
func ecdsaSig(raw []byte) (*ssh.Signature, error) {
	if len(raw) != 64 {
		return nil, errors.New("not a P-256 signature")
	}
	r, s := new(big.Int).SetBytes(raw[:32]), new(big.Int).SetBytes(raw[32:])
	return &ssh.Signature{Format: ssh.KeyAlgoECDSA256, Blob: ssh.Marshal(struct{ R, S *big.Int }{r, s})}, nil
}

// finish resolves a request with its first answer; later ones are
// ignored. Off d.mu.
func (k *hammockKeyHub) finish(id string, res keyResult) {
	d := k.d
	d.mu.Lock()
	defer d.mu.Unlock()
	r := k.reqs[id]
	if r == nil {
		return
	}
	delete(k.reqs, id)
	r.cancel() // the other prompt goes
	detail := r.req.Headline
	switch {
	case res.err == nil:
		if res.token != "" && windowable(r.req.PaneId) {
			k.windows[windowKey{r.req.PaneId, r.req.Agent}] = keyWindow{token: res.token, until: time.UnixMilli(res.until)}
			if r.req.Agent {
				detail += ", and for a while for the agent in that pane"
			} else {
				detail += ", and for a while in that pane"
			}
		}
		d.audit(r.actor, "hmk-key-sign", r.req.PaneId, detail+" (approved on "+cmpOr(res.by, "a device")+")")
	case errors.Is(res.err, errKeyDeclined):
		d.audit("user", "hmk-key-denied", r.req.PaneId, detail+" (on "+cmpOr(res.by, "a device")+")")
	default:
		d.audit("user", "hmk-key-denied", r.req.PaneId, detail+" ("+res.err.Error()+")")
	}
	d.publish(&pb.StateUpdate{ResolvedHammockKeyIds: []string{id}})
	r.done <- res
}

// respond is your answer from a client: a deny from anywhere of yours; an
// approval only from a phone that may approve (its signature, which the
// helper checks: reply, the conn's reply, is called with the helper's
// verdict, so the phone can say if it didn't take). Callers hold d.mu.
func (k *hammockKeyHub) respond(c *conn, m *pb.RespondHammockKey, reply func(error)) error {
	d := k.d
	r := k.reqs[m.Id]
	if r == nil {
		return errors.New("that request has already been answered")
	}
	by := "this Mac"
	if c.deviceID != "" {
		by = d.pairedName(c.deviceID)
	}
	if !m.Allow {
		go k.finish(m.Id, keyResult{err: errKeyDeclined, by: by})
		reply(nil)
		return nil
	}
	if c.deviceID == "" || len(m.Signature) == 0 {
		return errors.New("approve the Hammock Key with Touch ID on this Mac, or on your phone")
	}
	scope := "once"
	if m.Window {
		if r.req.WindowMs == 0 {
			return errors.New("this request can only be approved once")
		}
		scope = "window"
	}
	in := map[string]any{
		"data": base64.StdEncoding.EncodeToString(r.data), "pane": r.req.PaneId, "agent": r.req.Agent,
		"approval": map[string]any{
			"request": r.req.Id, "approver": c.deviceID, "scope": scope,
			"signature": base64.StdEncoding.EncodeToString(m.Signature), "expires": m.ExpiresUnixMs,
		},
	}
	id := m.Id
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := runKeyHelper(ctx, "sign", in)
		if err != nil {
			d.log.Warn("hmk key: a phone's approval", "device", by, "err", err)
			d.mu.Lock()
			d.audit("device:"+c.deviceID, "hmk-key-refused", r.req.PaneId, "an approval from "+by+" didn't check out: "+err.Error())
			d.mu.Unlock()
			reply(err)
			return
		}
		k.finish(id, helperResult(res, by))
		reply(nil)
	}()
	return nil
}

// pairedName is a paired device's name. Callers hold d.mu.
func (d *Daemon) pairedName(id string) string {
	devs, _ := d.store.Devices()
	for _, dev := range devs {
		if dev.ID == id {
			return cmpOr(dev.Name, "your phone")
		}
	}
	return "your phone"
}

// pushWhenAway sends a request to your phones once you're away from this
// Mac (waitUntilAway: at another computer counts, as this Mac's Touch ID
// prompt is only here) and while nobody's answered it. At the Mac its
// Touch ID prompt is enough. Off d.mu.
func (k *hammockKeyHub) pushWhenAway(ctx context.Context, id string) {
	d := k.d
	if d.waitUntilAway(ctx, hammockKeyTimeout, true, func() bool { return k.reqs[id] != nil }) {
		k.pushIfPending(id)
	}
}

// pushIfPending sends request id to your phones now, if it's still
// waiting and hasn't been sent. Off d.mu.
func (k *hammockKeyHub) pushIfPending(id string) {
	k.d.mu.Lock()
	defer k.d.mu.Unlock()
	if r := k.reqs[id]; r != nil && !r.pushed {
		r.pushed = true
		k.pushRequest(r)
	}
}

// pushRequest tells the phones that may approve, Time Sensitive (the Mac
// asks too, and the first answer wins). Callers hold d.mu.
func (k *hammockKeyHub) pushRequest(r *keyReq) {
	d := k.d
	var to []string
	for _, a := range k.status.Approvers {
		if !d.phoneShowing(a.DeviceId) {
			to = append(to, a.DeviceId)
		}
	}
	if len(to) == 0 {
		return
	}
	n := pushNote{Title: "Hammock Key", Subtitle: r.req.PaneLabel, Body: r.req.Headline, Category: "hammockkey", Key: r.req.Id, Pane: r.req.PaneId}
	if pushed != nil {
		pushed("hammock_key", r.req.Id)
	}
	d.pushNow(to, n, r.req.Id)
}

// ---------- describing a request ----------

// describeSigning says what's being signed, from the data (an SSH login,
// an ssh-keygen -Y signature) and the program that asked (pid, the agent
// socket's peer, and its parents: git push, not ssh).
func describeSigning(data []byte, pid int) (what, command string) {
	what = "Sign with your Hammock Key"
	if ns, ok := sshsigNamespace(data); ok {
		switch ns {
		case "git":
			what = "Sign for git (a commit or tag)"
		case "file":
			what = "Sign a file"
		default:
			what = "Sign for " + oneLine(ns, 40)
		}
	} else if user, ok := userauthUser(data); ok {
		what = "Log in as " + oneLine(user, 40)
	}
	return what, callerCommand(pid)
}

// sshsigNamespace reads an ssh-keygen -Y sign request's namespace
// (PROTOCOL.sshsig: "SSHSIG", then the namespace).
func sshsigNamespace(data []byte) (string, bool) {
	if !bytes.HasPrefix(data, []byte("SSHSIG")) {
		return "", false
	}
	s, _, ok := sshString(data[6:])
	return string(s), ok
}

// userauthUser reads the user from an SSH login's signed data (RFC 4252:
// session id, SSH_MSG_USERAUTH_REQUEST, user, …).
func userauthUser(data []byte) (string, bool) {
	_, rest, ok := sshString(data)
	if !ok || len(rest) < 1 || rest[0] != 50 {
		return "", false
	}
	user, _, ok := sshString(rest[1:])
	return string(user), ok
}

func sshString(b []byte) (s, rest []byte, ok bool) {
	if len(b) < 4 {
		return nil, nil, false
	}
	n := binary.BigEndian.Uint32(b)
	if uint64(len(b)-4) < uint64(n) {
		return nil, nil, false
	}
	return b[4 : 4+n], b[4+n:], true
}

// callerCommand names the program behind an agent connection: the first
// git above it if there is one (git push, not the ssh git runs), else the
// program itself.
func callerCommand(pid int) string {
	if pid <= 0 {
		return ""
	}
	first := ""
	for p, i := pid, 0; p > 1 && i < 5; i++ {
		args := procinfo.Args(p)
		if len(args) > 0 {
			name := filepath.Base(args[0])
			if first == "" {
				first = strings.Join(append([]string{name}, args[1:]...), " ")
			}
			if name == "git" {
				return oneLine(redactURLs(strings.Join(append([]string{"git"}, args[1:]...), " ")), 80)
			}
		}
		pp, err := procinfo.Parent(p)
		if err != nil {
			break
		}
		p = pp
	}
	return oneLine(redactURLs(first), 80)
}

var urlUserinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)

// redactURLs drops the user:password@ from URLs in a command line (git
// push https://user:token@…): what's shown on your phone and audited.
func redactURLs(s string) string { return urlUserinfo.ReplaceAllString(s, "$1") }
