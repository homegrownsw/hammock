package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// hmk key: this Mac's Hammock Key, an SSH key in its Secure Enclave
// that signs only when you approve (Touch ID here, or your phone).
func runKey(args []string) error {
	sub := "show"
	if len(args) > 0 {
		sub = args[0]
	}
	op := map[string]pb.HammockKeyCommand_Op{
		"show": pb.HammockKeyCommand_OP_STATUS, "create": pb.HammockKeyCommand_OP_CREATE, "delete": pb.HammockKeyCommand_OP_DELETE,
		"allow": pb.HammockKeyCommand_OP_ADD_APPROVER, "disallow": pb.HammockKeyCommand_OP_REMOVE_APPROVER,
	}
	switch sub {
	case "copy-id":
		if len(args) < 2 {
			return errors.New("usage: hmk key copy-id [USER@]HOST")
		}
		st, err := keyCommand(&pb.HammockKeyCommand{Op: pb.HammockKeyCommand_OP_STATUS})
		if err != nil {
			return err
		}
		if !st.Exists {
			return errors.New("there's no Hammock Key on this Mac: hmk key create")
		}
		// -f: there's no private key file beside the .pub (it's in the Secure Enclave).
		c := exec.Command("ssh-copy-id", append([]string{"-f", "-i", st.PublicKeyPath}, args[1:]...)...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		return c.Run()
	case "pub":
		st, err := keyCommand(&pb.HammockKeyCommand{Op: pb.HammockKeyCommand_OP_STATUS})
		if err != nil {
			return err
		}
		if !st.Exists {
			return errors.New("there's no Hammock Key on this Mac: hmk key create")
		}
		fmt.Println(st.PublicKey)
		return nil
	}
	o, ok := op[sub]
	if !ok {
		return errors.New("usage: hmk key [show|pub|create|delete|allow DEVICE|disallow DEVICE|copy-id HOST]")
	}
	m := &pb.HammockKeyCommand{Op: o}
	if o == pb.HammockKeyCommand_OP_ADD_APPROVER || o == pb.HammockKeyCommand_OP_REMOVE_APPROVER {
		if len(args) < 2 {
			return fmt.Errorf("usage: hmk key %s DEVICE (a phone's id or name: hmk key)", sub)
		}
		st, err := keyCommand(&pb.HammockKeyCommand{Op: pb.HammockKeyCommand_OP_STATUS})
		if err != nil {
			return err
		}
		id, err := approverID(st, args[1], o == pb.HammockKeyCommand_OP_ADD_APPROVER)
		if err != nil {
			return err
		}
		m.DeviceId = id
	}
	if o == pb.HammockKeyCommand_OP_CREATE || o == pb.HammockKeyCommand_OP_DELETE || o == pb.HammockKeyCommand_OP_ADD_APPROVER {
		fmt.Fprintln(os.Stderr, "Approve with Touch ID on this Mac…")
	}
	st, err := keyCommand(m)
	if err != nil {
		return err
	}
	printKey(st)
	return nil
}

func keyCommand(m *pb.HammockKeyCommand) (*pb.HammockKeyStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_HammockKeyCommand{HammockKeyCommand: m}})
	if err != nil {
		return nil, err
	}
	st := r.GetHammockKeyStatus()
	if st == nil {
		return nil, errors.New("no reply")
	}
	return st, nil
}

// approverID finds a phone by id or name: among those that can be added,
// or those that can approve already.
func approverID(st *pb.HammockKeyStatus, want string, adding bool) (string, error) {
	list := st.Approvers
	if adding {
		list = st.CanAdd
	}
	for _, a := range list {
		if a.DeviceId == want || strings.EqualFold(a.Name, want) {
			return a.DeviceId, nil
		}
	}
	if adding {
		return "", fmt.Errorf("no paired phone %q waiting to approve (open Hammock on it first)", want)
	}
	return "", fmt.Errorf("%q can't approve the Hammock Key", want)
}

func printKey(st *pb.HammockKeyStatus) {
	switch {
	case st.Error != "" && !st.Exists:
		printlnSafe("Hammock Key: " + st.Error)
		return
	case !st.Exists && !st.Available:
		fmt.Println("This Mac can't have a Hammock Key (no Secure Enclave).")
		return
	case !st.Exists:
		fmt.Println("No Hammock Key yet: hmk key create")
	default:
		fmt.Println(st.PublicKey)
		fmt.Println("Fingerprint: " + st.Fingerprint)
		fmt.Println("File:        " + st.PublicKeyPath)
		if len(st.Approvers) == 0 {
			fmt.Println("Approve with: Touch ID on this Mac")
		} else {
			var names []string
			for _, a := range st.Approvers {
				names = append(names, a.Name)
			}
			printlnSafe("Approve with: Touch ID on this Mac, or " + strings.Join(names, ", "))
		}
	}
	for _, a := range st.CanAdd {
		printfSafe("Can be allowed to approve: %s (hmk key allow %s)\n", a.Name, a.DeviceId)
	}
}

// hmk ssh setup: how your SSH setup reaches Hammock's agent; --apply adds
// Hammock's block to ~/.ssh/config.
func runSSH(args []string) error {
	if len(args) == 0 || args[0] != "setup" {
		return errors.New("usage: hmk ssh setup [--apply] [--apply-jj]")
	}
	apply, applyJJ := slices.Contains(args[1:], "--apply"), slices.Contains(args[1:], "--apply-jj")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	r, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SshSetup{SshSetup: &pb.SshSetup{Apply: apply, ApplyJj: applyJJ}}})
	if err != nil {
		return err
	}
	s := r.GetSshSetupReply()
	if s == nil {
		return errors.New("no reply")
	}
	if s.AgentName != "" {
		printfSafe("Hammock's agent passes requests on to %s (%s).\n", s.AgentName, s.AgentSocket)
	}
	if s.GitProgram != "" {
		printfSafe("Git signs with %s; in Hammock's panes, hmk-ssh-sign goes first.\n", s.GitProgram)
	}
	defer printJJSetup(s, applyJJ)
	switch {
	case s.Applied && apply:
		printfSafe("Added to %s (the old file is %s):\n\n", s.ConfigPath, s.BackupPath)
		fmt.Print(termSafeText(s.Change))
	case s.Routed:
		fmt.Println("ssh in Hammock's panes uses Hammock's agent.")
	default:
		printfSafe("%s points ssh straight at %s, so ssh in Hammock's panes skips Hammock's agent.\n", s.ConfigPath, s.AgentName)
		fmt.Printf("hmk ssh setup --apply adds this:\n\n%s", termSafeText(s.Change))
	}
	return nil
}

// printJJSetup says how jj signs in Hammock's panes (sshsetup_jj.go).
func printJJSetup(s *pb.SshSetupReply, applied bool) {
	switch {
	case s.JjPath == "":
		return // no jj, or it doesn't sign with SSH
	case s.JjProblem != "":
		printlnSafe(s.JjProblem)
	case s.JjRouted && applied:
		printfSafe("\nWrote %s:\n\n", s.JjConfigPath)
		fmt.Print(termSafeText(s.JjChange))
	case s.JjRouted:
		fmt.Println("jj in Hammock's panes signs through hmk-jj-ssh-sign.")
	default:
		printfSafe("\njj signs with %s, so in Hammock's panes it can't use keys from your other computers or the Hammock Key:\n", cmpOrStr(s.JjProgram, "ssh-keygen"))
		fmt.Println("while you're elsewhere, it waits on a prompt here that nobody sees.")
		printfSafe("hmk ssh setup --apply-jj writes %s:\n\n", s.JjConfigPath)
		fmt.Print(termSafeText(s.JjChange))
	}
}

// hmk-ssh-sign (a link to the CLI; git's gpg.ssh.program in Hammock's panes):
// what git signed with before, except while you're away from this Mac,
// when a key nobody's here to approve (1Password's) is passed over for the
// Hammock Key, approved on your phone. A key in a file, or one the agent can
// use, signs as it always did; at your desk anything else goes to your own
// gpg.ssh.program, then ssh-keygen. If your usual key can't sign (1Password
// said no, or nobody answered it), the Hammock Key is asked instead, as ssh
// goes on to the next key. hmk-jj-ssh-sign is the same for jj, orig being
// jj's own program.
func runSSHSign(args []string, orig string) int {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		keygen = "/usr/bin/ssh-keygen"
	}
	if !sshSignArgs(args) {
		return runPassthrough(keygen, args) // -Y verify, find-principals, check-novalidate
	}
	keyFile := expandTilde(flagValue(args, "-f"))
	pub, isPub := publicKeyFile(keyFile)
	ag := agentConnCLI()
	var keys []*agent.Key
	if ag != nil {
		keys, _ = ag.List()
	}
	var fk *agent.Key // the Hammock Key, if the agent lists it
	for _, k := range keys {
		if strings.HasPrefix(k.Comment, "Hammock-Key") {
			fk = k
		}
	}
	if isPub && fk != nil && bytes.Equal(fk.Blob, pub.Marshal()) {
		fk = nil // asked for the Hammock Key itself: nothing to fall back to
	}
	if !isPub || fileExistsCLI(strings.TrimSuffix(keyFile, ".pub")) && strings.HasSuffix(keyFile, ".pub") {
		// A key in a file: signed with it (by the agent, if it has it too).
		return signOrHammockKey(cmpOrStr(orig, keygen), keygen, args, fk)
	}
	for _, k := range keys {
		if bytes.Equal(k.Blob, pub.Marshal()) {
			return signOrHammockKey(keygen, keygen, args, fk) // the agent has it: the agent signs
		}
	}
	if ag != nil && fk != nil {
		if away, err := ag.Extension("away@hammock.homegrownsw.com", nil); err == nil && len(away) == 1 && away[0] == 1 {
			fmt.Fprintln(os.Stderr, "hammock: you're away from this Mac, so this is signed with your Hammock Key (approve on your phone)")
			return signWithHammockKey(keygen, args, fk)
		}
	}
	return signOrHammockKey(cmpOrStr(orig, keygen), keygen, args, fk)
}

// signOrHammockKey signs with prog as asked; if that fails (your usual key's
// agent refused, or nobody answered it), with the Hammock Key fk instead,
// which asks you here and on your phone. Without fk it's prog's answer.
func signOrHammockKey(prog, keygen string, args []string, fk *agent.Key) int {
	if fk == nil || len(signedFiles(args)) == 0 {
		return runPassthrough(prog, args) // (from stdin: read once, so not tried twice)
	}
	var errOut bytes.Buffer
	code := runSigner(prog, args, &errOut)
	if code == 0 {
		_, _ = os.Stderr.Write(errOut.Bytes())
		return 0
	}
	why := strings.TrimSpace(errOut.String())
	if i := strings.LastIndexByte(why, '\n'); i >= 0 {
		why = strings.TrimSpace(why[i+1:])
	}
	fmt.Fprintf(os.Stderr, "hammock: your usual key didn't sign (%s), so this is signed with your Hammock Key\n", cmpOrStr(why, fmt.Sprintf("exit %d", code)))
	if c := signWithHammockKey(keygen, args, fk); c != 0 {
		_, _ = os.Stderr.Write(errOut.Bytes()) // both failed: what the first said too
		return c
	}
	return 0
}

// signWithHammockKey signs with the Hammock Key fk (the agent asks you).
func signWithHammockKey(keygen string, args []string, fk *agent.Key) int {
	f, err := os.CreateTemp("", "hmk-key-*.pub")
	if err != nil {
		fmt.Fprintln(os.Stderr, "hmk-ssh-sign:", err)
		return 1
	}
	defer os.Remove(f.Name())
	_, _ = f.Write(ssh.MarshalAuthorizedKey(fk))
	f.Close()
	return runPassthrough(keygen, withFlag(args, "-f", f.Name()))
}

// runSigner runs prog as runPassthrough does, its stderr to errOut.
func runSigner(prog string, args []string, errOut *bytes.Buffer) int {
	c := exec.Command(prog, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, errOut
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(errOut, "hmk-ssh-sign: %v\n", err)
		return 1
	}
	return 0
}

func cmpOrStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func fileExistsCLI(p string) bool { _, err := os.Stat(p); return err == nil }

// agentConnCLI is the agent panes have (Hammock's), nil if there's none.
func agentConnCLI() agent.ExtendedAgent {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	c, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return nil
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	return agent.NewClient(c)
}

// sshSignArgs: ssh-keygen -Y sign.
func sshSignArgs(args []string) bool {
	for i, a := range args {
		if a == "-Y" && i+1 < len(args) && args[i+1] == "sign" {
			return true
		}
	}
	return false
}

func withFlag(args []string, flag, value string) []string {
	out := append([]string{}, args...)
	for i, a := range out {
		if a == flag && i+1 < len(out) {
			out[i+1] = value
		}
	}
	return out
}

// publicKeyFile reads a public key (an authorized_keys line, as git writes
// for "key::" and as .pub files are). false: a private key, or unreadable.
func publicKeyFile(path string) (ssh.PublicKey, bool) {
	b, err := os.ReadFile(expandTilde(path))
	if err != nil {
		return nil, false
	}
	pk, _, _, _, err := ssh.ParseAuthorizedKey(b)
	if err != nil {
		return nil, false
	}
	return pk, true
}

// ownSSHProgram is your own gpg.ssh.program (git's config without what
// the pane adds), unless it's this.
func ownSSHProgram() string {
	c := exec.Command("git", "config", "--get", "gpg.ssh.program")
	for _, e := range os.Environ() {
		// What the pane adds (GIT_CONFIG_COUNT/KEY_n/VALUE_n), not your
		// GIT_CONFIG_GLOBAL or GIT_CONFIG_SYSTEM.
		k, _, _ := strings.Cut(e, "=")
		if k == "GIT_CONFIG_COUNT" || k == "GIT_CONFIG_PARAMETERS" || strings.HasPrefix(k, "GIT_CONFIG_KEY_") || strings.HasPrefix(k, "GIT_CONFIG_VALUE_") {
			continue
		}
		c.Env = append(c.Env, e)
	}
	out, err := c.Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" || filepath.Base(p) == "hmk-ssh-sign" {
		return ""
	}
	return expandTilde(p)
}

// ownJJProgram is jj's own signing.backends.ssh.program: jj's config as
// it is outside Hammock's panes (Hammock's file applies only while
// HMK_PANE_ID is set), unless it's this.
func ownJJProgram() string {
	jj, err := exec.LookPath("jj")
	if err != nil {
		return ""
	}
	c := exec.Command(jj, "--ignore-working-copy", "config", "get", "signing.backends.ssh.program")
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "HMK_PANE_ID=") {
			c.Env = append(c.Env, e)
		}
	}
	out, err := c.Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" || strings.HasPrefix(filepath.Base(p), "hmk-") {
		return ""
	}
	return expandTilde(p)
}

func runPassthrough(prog string, args []string) int {
	c := exec.Command(prog, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "hmk-ssh-sign:", err)
		return 1
	}
	return 0
}

// signedFiles are the files an ssh-keygen -Y sign signs (git names one);
// none: it signs its stdin.
func signedFiles(args []string) []string {
	withValue := map[string]bool{"-f": true, "-n": true, "-O": true, "-Y": true, "-I": true, "-s": true}
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case withValue[a]:
			i++
		case strings.HasPrefix(a, "-") && a != "-":
		default:
			files = append(files, a)
		}
	}
	return files
}
