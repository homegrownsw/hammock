package daemon

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/state"
)

// fakeKeyHelper stands in for helpers/key (the Secure Enclave and Touch
// ID) when the test binary runs as it: the key is a P-256 key in a file,
// Touch ID answers as HMK_TEST_TOUCH says (allow, deny, or wait to be
// withdrawn), and a phone's approval is checked as the real helper does.
// Like the real one, it takes its request from the daemon that ran it
// (--daemon: keyHelperFetch), never from its arguments or stdin.
func fakeKeyHelper() int {
	dir := os.Getenv("HMK_TEST_KEY_HELPER")
	fail := func(code int, msg string) int { fmt.Fprintln(os.Stderr, msg); return code }
	if len(os.Args) != 3 || os.Args[1] != "--daemon" {
		return fail(1, "usage: --daemon <name>")
	}
	b, from, err := keyHelperFetch(os.Args[2], 5*time.Second)
	if err != nil {
		return fail(1, err.Error())
	}
	if from != os.Getppid() {
		return fail(1, "a request from something other than the daemon")
	}
	var req struct {
		V       int            `json:"v"`
		Command string         `json:"command"`
		Input   map[string]any `json:"input"`
	}
	if err := json.Unmarshal(b, &req); err != nil || req.V != keyHelperProtocol {
		return fail(1, "a request of another version")
	}
	cmd, in := req.Command, req.Input
	_ = os.WriteFile(filepath.Join(dir, "last-"+cmd), b, 0o600)
	keyPath, apprPath := filepath.Join(dir, "key.pem"), filepath.Join(dir, "approvers.json")
	touch := func() int {
		switch os.Getenv("HMK_TEST_TOUCH") {
		case "allow":
			return 0
		case "wait":
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, syscall.SIGTERM)
			<-ch
			return 2
		case "unavailable":
			return 1
		}
		if os.Getenv("HMK_TEST_TOUCH") == "deny" {
			return 2
		}
		return 2
	}
	loadKey := func() *ecdsa.PrivateKey {
		b, err := os.ReadFile(keyPath)
		if err != nil {
			return nil
		}
		blk, _ := pem.Decode(b)
		k, _ := x509.ParseECPrivateKey(blk.Bytes)
		return k
	}
	var approvers []map[string]any
	if b, err := os.ReadFile(apprPath); err == nil {
		_ = json.Unmarshal(b, &approvers)
	}
	status := func() map[string]any {
		k := loadKey()
		if k == nil {
			return map[string]any{"exists": false, "enclave": true}
		}
		raw, _ := k.PublicKey.ECDH()
		var list []any
		for _, a := range approvers {
			list = append(list, map[string]any{"id": a["id"], "name": a["name"], "key": a["key"]})
		}
		id := sha256.Sum256(raw.Bytes())
		return map[string]any{"exists": true, "enclave": true, "public_key": base64.StdEncoding.EncodeToString(raw.Bytes()),
			"key_id": hex.EncodeToString(id[:]), "approvers": list, "created": 1}
	}
	out := func(m map[string]any) int { _ = json.NewEncoder(os.Stdout).Encode(m); return 0 }
	switch cmd {
	case "status":
		return out(status())
	case "create":
		if c := touch(); c != 0 {
			return fail(c, "not allowed")
		}
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		der, _ := x509.MarshalECPrivateKey(k)
		_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600)
		for _, a := range anySlice(in["approvers"]) {
			approvers = append(approvers, a.(map[string]any))
		}
		b, _ := json.Marshal(approvers)
		_ = os.WriteFile(apprPath, b, 0o600)
		return out(status())
	case "add-approver":
		if c := touch(); c != 0 {
			return fail(c, "not allowed")
		}
		approvers = append(approvers, in["approver"].(map[string]any))
		b, _ := json.Marshal(approvers)
		_ = os.WriteFile(apprPath, b, 0o600)
		return out(status())
	case "sign":
		k := loadKey()
		if k == nil {
			return fail(1, "there's no Hammock Key")
		}
		data, _ := base64.StdEncoding.DecodeString(in["data"].(string))
		pane, _ := in["pane"].(string)
		window := false
		switch {
		case in["token"] != nil:
			if in["token"] != "tok:"+pane {
				return fail(2, "that approval has run out")
			}
		case in["approval"] != nil:
			a := in["approval"].(map[string]any)
			var key *ecdsa.PublicKey
			for _, ap := range approvers {
				if ap["id"] == a["approver"] {
					raw, _ := base64.StdEncoding.DecodeString(ap["key"].(string))
					key, _ = ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
				}
			}
			if key == nil {
				return fail(1, "that device can't approve the Hammock Key")
			}
			sum := sha256.Sum256(data)
			raw, _ := k.PublicKey.ECDH()
			id := sha256.Sum256(raw.Bytes())
			msg := approvalMessage(hex.EncodeToString(id[:]), a["request"].(string), hex.EncodeToString(sum[:]), a["scope"].(string), pane, int64(a["expires"].(float64)))
			sig, _ := base64.StdEncoding.DecodeString(a["signature"].(string))
			h := sha256.Sum256(msg)
			if !ecdsa.VerifyASN1(key, h[:], sig) {
				return fail(1, "the approval's signature doesn't check out")
			}
			window = a["scope"] == "window"
		default:
			if c := touch(); c != 0 {
				return fail(c, "not allowed")
			}
			window = in["window"] == true
		}
		h := sha256.Sum256(data)
		r, s, _ := ecdsa.Sign(rand.Reader, k, h[:])
		raw := make([]byte, 64)
		r.FillBytes(raw[:32])
		s.FillBytes(raw[32:])
		res := map[string]any{"signature": base64.StdEncoding.EncodeToString(raw)}
		if window && pane != "" {
			res["token"], res["until"] = "tok:"+pane, time.Now().Add(10*time.Minute).UnixMilli()
		}
		return out(res)
	}
	return fail(1, "unknown command "+cmd)
}

// approvalMessage is what a phone signs (the helper's approvalMessage,
// HammockKeyApproval.message on the phone: TestApprovalMessageVector and
// HammockKeyTests check the same vector).
func approvalMessage(key, request, dataHash, scope, pane string, expires int64) []byte {
	return fmt.Appendf(nil, "hmk-key-approval-v2\n%s\n%s\n%s\n%s\n%s\n%d", key, request, dataHash, scope, pane, expires)
}

func TestApprovalMessageVector(t *testing.T) {
	want := "hmk-key-approval-v2\nk1\nfk_1\nab\nwindow\npn_1\n123"
	if got := string(approvalMessage("k1", "fk_1", "ab", "window", "pn_1", 123)); got != want {
		t.Fatalf("%q", got)
	}
}

// useFakeKeyHelper points the daemon at fakeKeyHelper, with its key in a
// scratch dir, Touch ID answering as touch says.
func useFakeKeyHelper(t *testing.T, touch string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the Hammock Key is the Mac's")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HMK_TEST_KEY_HELPER", t.TempDir())
	t.Setenv("HMK_TEST_TOUCH", touch)
	oldPath, oldBeta := keyHelperPath, keyHelperBeta
	keyHelperPath = func() string { return exe }
	keyHelperBeta = func() bool { return false }
	t.Cleanup(func() { keyHelperPath, keyHelperBeta = oldPath, oldBeta })
}

func keyStatus(t *testing.T, d *Daemon, op pb.HammockKeyCommand_Op, device string) *pb.HammockKeyStatus {
	t.Helper()
	st, err := d.hammockKey.command(&pb.HammockKeyCommand{Op: op, DeviceId: device})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A phone that may approve: paired, its approval key sent, let approve.
func addPhone(t *testing.T, d *Daemon, id string) *ecdsa.PrivateKey {
	t.Helper()
	pk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw, _ := pk.PublicKey.ECDH()
	d.mu.Lock()
	if err := d.store.PutDevice(state.Device{ID: id, Name: "Test iPhone", Platform: "ios", PublicKey: []byte{1}, Paired: time.Now(), LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := d.hammockKey.setApprovalKey(&conn{deviceID: id}, &pb.SetApprovalKey{PublicKey: raw.Bytes()}); err != nil {
		t.Fatal(err)
	}
	d.mu.Unlock()
	return pk
}

func agentConn(t *testing.T, d *Daemon) agent.ExtendedAgent {
	t.Helper()
	c, err := net.Dial("unix", SSHAgentSocketPath(d.cfg.Dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return agent.NewClient(c)
}

func hammockAgentKey(t *testing.T, ag agent.ExtendedAgent) *agent.Key {
	t.Helper()
	keys, err := ag.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if strings.HasPrefix(k.Comment, "Hammock-Key") {
			return k
		}
	}
	t.Fatalf("no Hammock Key in %v", keys)
	return nil
}

func waitKeyRequest(t *testing.T, d *Daemon) *pb.HammockKeyRequest {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		d.mu.Lock()
		for _, r := range d.hammockKey.reqs {
			req := r.req
			d.mu.Unlock()
			return req
		}
		d.mu.Unlock()
	}
	t.Fatal("no Hammock Key request")
	return nil
}

// Creating the key lets the phones waiting to approve do so; panes' agent
// lists it; a phone's approval (its signature over the request) gets a
// signature that checks out, and the Touch ID prompt here is withdrawn.
func TestHammockKeyPhoneApproval(t *testing.T) {
	sshConfig(t, "")
	useFakeKeyHelper(t, "wait")
	d := startDaemon(t, shortDir(t))
	phone := addPhone(t, d, "dv_phone")
	if st := keyStatus(t, d, pb.HammockKeyCommand_OP_STATUS, ""); st.Exists || len(st.CanAdd) != 1 {
		t.Fatalf("before: %v", st)
	}
	// Creating asks with Touch ID: allow that one.
	t.Setenv("HMK_TEST_TOUCH", "allow")
	st := keyStatus(t, d, pb.HammockKeyCommand_OP_CREATE, "")
	if !st.Exists || len(st.Approvers) != 1 || st.Approvers[0].DeviceId != "dv_phone" || len(st.CanAdd) != 0 {
		t.Fatalf("after create: %v", st)
	}
	if b, err := os.ReadFile(st.PublicKeyPath); err != nil || !strings.HasPrefix(string(b), "ecdsa-sha2-nistp256 ") {
		t.Fatalf("pub file: %q %v", b, err)
	}
	t.Setenv("HMK_TEST_TOUCH", "wait")

	ag := agentConn(t, d)
	k := hammockAgentKey(t, ag)
	pub, _ := ssh.ParsePublicKey(k.Blob)
	data := []byte("SSHSIG\x00\x00\x00\x03git-whatever")
	type result struct {
		sig *ssh.Signature
		err error
	}
	done := make(chan result, 1)
	go func() {
		sig, err := ag.Sign(pub, data)
		done <- result{sig, err}
	}()
	req := waitKeyRequest(t, d)
	if req.What != "Sign for git (a commit or tag)" || req.WindowMs != 0 {
		t.Fatalf("request: %v", req)
	}
	expires := time.Now().Add(time.Minute).UnixMilli()
	h := sha256.Sum256(approvalMessage(req.KeyId, req.Id, req.DataHash, "once", req.PaneId, expires))
	asn, _ := ecdsa.SignASN1(rand.Reader, phone, h[:])
	// Someone else's phone can't: its signature doesn't check out.
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	h2 := sha256.Sum256(approvalMessage(req.KeyId, req.Id, req.DataHash, "once", req.PaneId, expires))
	bad, _ := ecdsa.SignASN1(rand.Reader, other, h2[:])
	if req.KeyId == "" {
		t.Fatal("request names no key")
	}
	verdict := make(chan error, 1)
	reply := func(err error) { verdict <- err }
	d.mu.Lock()
	err := d.hammockKey.respond(&conn{deviceID: "dv_phone"}, &pb.RespondHammockKey{Id: req.Id, Allow: true, Signature: bad, ExpiresUnixMs: expires}, reply)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// The phone hears it didn't take.
	if err := <-verdict; err == nil {
		t.Fatal("a forged approval was taken")
	}
	select {
	case r := <-done:
		t.Fatalf("signed with a bad approval: %v %v", r.sig, r.err)
	default:
	}
	d.mu.Lock()
	err = d.hammockKey.respond(&conn{deviceID: "dv_phone"}, &pb.RespondHammockKey{Id: req.Id, Allow: true, Signature: asn, ExpiresUnixMs: expires}, reply)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-verdict; err != nil {
		t.Fatal(err)
	}
	var r result
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("no signature")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	if err := pub.Verify(data, r.sig); err != nil {
		t.Fatalf("signature doesn't verify: %v", err)
	}
	d.mu.Lock()
	left := len(d.hammockKey.reqs)
	d.mu.Unlock()
	if left != 0 {
		t.Fatal("request still pending")
	}
}

// Touch ID here: "Don't Allow" refuses; an approval of a person's own
// program opens a window its pane's next signature uses without asking.
func TestHammockKeyTouchID(t *testing.T) {
	sshConfig(t, "")
	useFakeKeyHelper(t, "allow")
	d := startDaemon(t, shortDir(t))
	keyStatus(t, d, pb.HammockKeyCommand_OP_CREATE, "")
	ag := agentConn(t, d)
	pub, _ := ssh.ParsePublicKey(hammockAgentKey(t, ag).Blob)
	data := []byte("hello")
	sig, err := ag.Sign(pub, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Verify(data, sig); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HMK_TEST_TOUCH", "deny")
	if _, err := ag.Sign(pub, data); err == nil {
		t.Fatal("signed though you said no")
	}

	// Touch ID for a pane's own program opens a window: the next one in
	// that pane goes through the token, Touch ID not asked again.
	t.Setenv("HMK_TEST_TOUCH", "allow")
	if _, err := d.hammockKey.sign("pn_x", false, 0, data); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	w, ok := d.hammockKey.windows["pn_x"]
	d.mu.Unlock()
	if !ok || w.token != "tok:pn_x" {
		t.Fatalf("no window: %+v", w)
	}
	t.Setenv("HMK_TEST_TOUCH", "deny")
	if _, err := d.hammockKey.sign("pn_x", false, 0, data); err != nil {
		t.Fatalf("window: %v", err)
	}
	// Another pane has none.
	if _, err := d.hammockKey.sign("pn_y", false, 0, data); err == nil {
		t.Fatal("signed in another pane without asking")
	}
	// An expired window asks again.
	d.mu.Lock()
	d.hammockKey.windows["pn_x"] = keyWindow{token: "tok:pn_x", until: time.Now().Add(-time.Second)}
	d.mu.Unlock()
	if _, err := d.hammockKey.sign("pn_x", false, 0, data); err == nil {
		t.Fatal("an expired window signed")
	}
	// Agents are asked every time: the window isn't theirs.
	d.mu.Lock()
	d.hammockKey.windows["pn_x"] = keyWindow{token: "tok:pn_x", until: time.Now().Add(time.Minute)}
	d.mu.Unlock()
	if _, err := d.hammockKey.sign("pn_x", true, 0, data); err == nil {
		t.Fatal("an agent signed within your window")
	}

	// Nobody to ask (Touch ID can't, no phone may approve): it says so at
	// once rather than keeping ssh waiting.
	t.Setenv("HMK_TEST_TOUCH", "unavailable")
	start := time.Now()
	if _, err := d.hammockKey.sign("", false, 0, data); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

func TestRedactURLs(t *testing.T) {
	if got := redactURLs("git push https://alex:ghp_secret@github.com/x/y main"); got != "git push https://github.com/x/y main" {
		t.Fatal(got)
	}
	if got := redactURLs("ssh git@github.com"); got != "ssh git@github.com" {
		t.Fatal(got)
	}
}

// Agents can't approve, nor create or delete the key; a phone can't
// approve for a while what an agent asked.
func TestHammockKeyAgentsAndScope(t *testing.T) {
	sshConfig(t, "")
	useFakeKeyHelper(t, "allow")
	d := startDaemon(t, shortDir(t))
	keyStatus(t, d, pb.HammockKeyCommand_OP_CREATE, "")
	c := &conn{d: d, caller: &caller{pane: "pn_agent", agent: true}}
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_HammockKeyCommand{HammockKeyCommand: &pb.HammockKeyCommand{Op: pb.HammockKeyCommand_OP_DELETE}}},
		{Msg: &pb.ClientMessage_RespondHammockKey{RespondHammockKey: &pb.RespondHammockKey{Id: "fk_x", Allow: true}}},
		{Msg: &pb.ClientMessage_SshSetup{SshSetup: &pb.SshSetup{Apply: true}}},
	} {
		if err := c.agentCheck(m); err == nil {
			t.Fatalf("an agent may send %T", m.Msg)
		}
	}
	d.mu.Lock()
	d.hammockKey.reqs["fk_a"] = &keyReq{req: &pb.HammockKeyRequest{Id: "fk_a", Agent: true}, done: make(chan keyResult, 1), cancel: func() {}}
	err := d.hammockKey.respond(&conn{deviceID: "dv"}, &pb.RespondHammockKey{Id: "fk_a", Allow: true, Window: true, Signature: []byte{1}}, func(error) {})
	if err == nil {
		t.Fatal("approved an agent's request for a while")
	}
	// Approving needs a phone's signature: a client here can only deny.
	if err := d.hammockKey.respond(&conn{}, &pb.RespondHammockKey{Id: "fk_a", Allow: true}, func(error) {}); err == nil {
		t.Fatal("approved from a client without Touch ID or a phone")
	}
	delete(d.hammockKey.reqs, "fk_a")
	d.mu.Unlock()
}

// Away from the Mac, the Hammock Key comes first and an agent that asks you
// (1Password) isn't offered; here, it's last.
func TestHammockKeyListOrder(t *testing.T) {
	sshConfig(t, "")
	useFakeKeyHelper(t, "allow")
	d := startDaemon(t, shortDir(t))
	keyStatus(t, d, pb.HammockKeyCommand_OP_CREATE, "")
	sock, _ := testAgent(t, "own-key")
	d.mu.Lock()
	d.sshHub.own = sock
	d.mu.Unlock()
	oldLocked, oldIdle := screenLocked, idleTime
	t.Cleanup(func() { screenLocked, idleTime = oldLocked, oldIdle })
	idleTime = func() time.Duration { return 0 }

	screenLocked = func() bool { return false }
	keys, _ := agentConn(t, d).List()
	if len(keys) != 2 || keys[0].Comment != "own-key" || !strings.HasPrefix(keys[1].Comment, "Hammock-Key") {
		t.Fatalf("here: %v", keys)
	}
	screenLocked = func() bool { return true }
	keys, _ = agentConn(t, d).List()
	if len(keys) != 2 || !strings.HasPrefix(keys[0].Comment, "Hammock-Key") || keys[1].Comment != "own-key" {
		t.Fatalf("away: %v", keys)
	}
	// An agent that asks you, while you're away: left out.
	link := filepath.Join(shortDir(t), "com.1password.sock")
	if err := os.Symlink(sock, link); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	d.sshHub.own = link
	d.mu.Unlock()
	keys, _ = agentConn(t, d).List()
	if len(keys) != 1 || !strings.HasPrefix(keys[0].Comment, "Hammock-Key") {
		t.Fatalf("away, 1Password: %v", keys)
	}
	// At the Mac, untouched for a few minutes (reading what an agent does):
	// 1Password's keys are still offered first.
	screenLocked = func() bool { return false }
	idleTime = func() time.Duration { return 3 * time.Minute }
	keys, _ = agentConn(t, d).List()
	if len(keys) != 2 || keys[0].Comment != "own-key" {
		t.Fatalf("idle a little, 1Password: %v", keys)
	}
	// You've used Hammock on your phone since you last touched the Mac: away,
	// though its screen's on.
	d.mu.Lock()
	d.phoneUsed()
	d.mu.Unlock()
	keys, _ = agentConn(t, d).List()
	if len(keys) != 1 || !strings.HasPrefix(keys[0].Comment, "Hammock-Key") {
		t.Fatalf("on the phone, 1Password: %v", keys)
	}
	// Then the Mac again: here.
	idleTime = func() time.Duration { return 0 }
	keys, _ = agentConn(t, d).List()
	if len(keys) != 2 || keys[0].Comment != "own-key" {
		t.Fatalf("back at the Mac: %v", keys)
	}
	// Left a good while: away.
	idleTime = func() time.Duration { return sshAwayIdle }
	d.mu.Lock()
	d.phoneAt = time.Time{}
	d.mu.Unlock()
	keys, _ = agentConn(t, d).List()
	if len(keys) != 1 {
		t.Fatalf("left a while, 1Password: %v", keys)
	}
}

func TestDescribeSigning(t *testing.T) {
	login := ssh.Marshal(struct {
		Session []byte
		Type    byte
		User    string
		Service string
	}{[]byte("sid"), 50, "git", "ssh-connection"})
	if what, _ := describeSigning(login, 0); what != "Log in as git" {
		t.Fatal(what)
	}
	sig := append([]byte("SSHSIG"), ssh.Marshal(struct{ NS string }{"file"})...)
	if what, _ := describeSigning(sig, 0); what != "Sign a file" {
		t.Fatal(what)
	}
	if what, _ := describeSigning([]byte("x"), 0); what != "Sign with your Hammock Key" {
		t.Fatal(what)
	}
	if cmd := callerCommand(os.Getpid()); cmd == "" {
		t.Fatal("no command for this process")
	}
}

func TestSSHConfigSetup(t *testing.T) {
	cfg := `# Added by OrbStack: 'orb' SSH host for Linux machines
# This only works if it's at the top of ssh_config (before any Host blocks).
Include ~/.orbstack/ssh/config

Host *
  SendEnv ITERM_PROFILE
  IdentityAgent "~/Library/Group Containers/2BUA8C4S2C.com.1password/t/agent.sock"

Host 10.10.10.10
    User root
`
	s := parseSSHConfig(cfg)
	if !strings.HasSuffix(s.agent, "2BUA8C4S2C.com.1password/t/agent.sock") || s.routed {
		t.Fatalf("scan: %+v", s)
	}
	if name, asks := sshAgentName(s.agent); name != "1Password" || !asks {
		t.Fatal(name, asks)
	}
	out := insertSSHBlock(cfg, sshBlock(s.agent))
	i, j, k := strings.Index(out, "Match exec"), strings.Index(out, "Match all"), strings.Index(out, "Include ~/.orbstack")
	if i != strings.Index(out, "Match") || j < i || k < j {
		t.Fatalf("block in the wrong place:\n%s", out)
	}
	if !parseSSHConfig(out).routed {
		t.Fatal("not routed after")
	}
	// An IdentityAgent before any Host line, and a block after it: ssh
	// takes the first, so that's not routed; inserting puts it first.
	top := "IdentityAgent ~/.1password/agent.sock\nHost *\n  User x\n"
	if s := parseSSHConfig(top); s.agent == "" || s.routed {
		t.Fatalf("%+v", s)
	}
	if parseSSHConfig(top + sshBlock("")).routed {
		t.Fatal("a block after the agent counts as routed")
	}
	if !parseSSHConfig(insertSSHBlock(top, sshBlock(""))).routed {
		t.Fatal("not routed after inserting")
	}
	// Host-specific agents don't count; nor does SSH_AUTH_SOCK.
	if a := parseSSHConfig("Host work\n  IdentityAgent /x.sock\nHost *\n  IdentityAgent SSH_AUTH_SOCK\n").agent; a != "" {
		t.Fatal(a)
	}

	// Applying writes the block and keeps the old file.
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	old := sshConfigPath
	sshConfigPath = func() string { return path }
	t.Cleanup(func() { sshConfigPath = old })
	sshConfig(t, "")
	d := startDaemon(t, shortDir(t))
	r, err := d.sshSetup(&pb.SshSetup{})
	if err != nil || r.Routed || r.AgentName != "1Password" {
		t.Fatalf("%v %v", r, err)
	}
	r, err = d.sshSetup(&pb.SshSetup{Apply: true})
	if err != nil || !r.Applied {
		t.Fatalf("%v %v", r, err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte(`Match exec "test -n \"$HMK_PANE_ID\""`)) {
		t.Fatalf("%s", b)
	}
	if bk, _ := os.ReadFile(r.BackupPath); string(bk) != cfg {
		t.Fatal("backup differs")
	}
	if r, _ := d.sshSetup(&pb.SshSetup{Apply: true}); r.Applied {
		t.Fatal("applied twice")
	}
	// Hammock's agent passes requests on to 1Password's.
	if up := d.sshHub.upstream(); !strings.HasSuffix(up, "com.1password/t/agent.sock") {
		t.Fatal(up)
	}
}

// A request goes to your phones only once you're away from the Mac: at it,
// its Touch ID prompt is enough.
func TestHammockKeyPushWhenAway(t *testing.T) {
	sshConfig(t, "")
	useFakeKeyHelper(t, "allow")
	var idle atomic.Int64
	var locked atomic.Bool
	oldIdle, oldLocked, oldPoll := idleTime, screenLocked, pushPoll
	idleTime = func() time.Duration { return time.Duration(idle.Load()) }
	screenLocked, pushPoll = locked.Load, 20*time.Millisecond
	var pushes atomic.Int32
	pushed = func(event, _ string) {
		if event == "hammock_key" {
			pushes.Add(1)
		}
	}
	t.Cleanup(func() { idleTime, screenLocked, pushPoll, pushed = oldIdle, oldLocked, oldPoll, nil })

	d := startDaemon(t, shortDir(t))
	addPhone(t, d, "dv_phone")
	keyStatus(t, d, pb.HammockKeyCommand_OP_CREATE, "")
	t.Setenv("HMK_TEST_TOUCH", "wait")

	ag := agentConn(t, d)
	pub, _ := ssh.ParsePublicKey(hammockAgentKey(t, ag).Blob)
	done := make(chan error, 1)
	go func() { _, err := ag.Sign(pub, []byte("SSHSIG\x00\x00\x00\x03git-x")); done <- err }()
	req := waitKeyRequest(t, d)
	time.Sleep(200 * time.Millisecond)
	if n := pushes.Load(); n != 0 {
		t.Fatalf("pushed %d times while you're at the Mac", n)
	}
	locked.Store(true) // you've gone
	for deadline := time.Now().Add(5 * time.Second); pushes.Load() == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("not pushed once you'd gone")
		}
	}
	d.mu.Lock()
	_ = d.hammockKey.respond(&conn{}, &pb.RespondHammockKey{Id: req.Id}, func(error) {})
	d.mu.Unlock()
	if err := <-done; err == nil {
		t.Fatal("signed after a no")
	}
	if n := pushes.Load(); n != 1 {
		t.Fatalf("pushed %d times", n)
	}
}

// The helper's panel gets where a request is from as its own fields:
// the Group (and its emoji), the tab, the agent asking, the command.
func TestHammockKeyPrompt(t *testing.T) {
	d := &Daemon{panes: map[string]*pane{}, tabs: map[string]*pb.Tab{}, groups: map[string]*pb.Group{}, people: map[string]*pb.Person{}}
	d.groups["gp_1"] = &pb.Group{Id: "gp_1", Name: "Web", Icon: "🌴", TabIds: []string{"tb_1"}}
	d.tabs["tb_1"] = &pb.Tab{Id: "tb_1", GroupId: "gp_1", Name: "api", Layout: leaf("pn_1")}
	d.panes["pn_1"] = newPane(d, &pb.Pane{Id: "pn_1", TabId: "tb_1", Agent: &pb.Agent{Harness: "claude"}})

	p, id := d.keyPrompt("pn_1", true, "git push")
	want := map[string]any{"group": "Web", "group_icon": "🌴", "tab": "api", "agent": "Claude", "command": "git push"}
	if id != "gp_1" || len(p) != len(want) {
		t.Fatalf("%q %v", id, p)
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s: %v, want %v", k, p[k], v)
		}
	}
	// A person's own program: no agent.
	if p, _ := d.keyPrompt("pn_1", false, ""); p["agent"] != nil {
		t.Errorf("agent for a program: %v", p["agent"])
	}
	// Nowhere (a program outside Hammock's panes).
	if p, id := d.keyPrompt("", false, "ssh"); id != "" || p["group"] != "" || p["tab"] != "" {
		t.Errorf("%q %v", id, p)
	}
	if got := d.tabPath("pn_1"); got != "Web ▸ api" {
		t.Errorf("tabPath %q", got)
	}
}

// A prompt sent from the phone's agent view (not raw typing) counts as using
// the phone, so signing then goes to the Hammock Key, not 1Password.
func TestPhoneActionAgentView(t *testing.T) {
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_AgentSend{AgentSend: &pb.AgentSend{}}},
		{Msg: &pb.ClientMessage_AgentAnswer{AgentAnswer: &pb.AgentAnswer{}}},
		{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{}}},
	} {
		if !phoneAction(m) {
			t.Errorf("%T isn't a phone action", m.Msg)
		}
	}
	if phoneAction(&pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{}}}) {
		t.Error("watching an agent (sent on reconnect) counts as using the phone")
	}
}
