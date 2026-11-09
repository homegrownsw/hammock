package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// hmk-ssh-sign: when your usual key doesn't sign (1Password refused, or
// nobody answered it), the Hammock Key is asked instead.
func TestSSHSignFallsBackToHammockKey(t *testing.T) {
	dir, err := os.MkdirTemp("", "sshsign")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	// An agent with your usual key and the Hammock Key.
	_, usual, _ := ed25519.GenerateKey(rand.Reader)
	hk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: usual, Comment: "GitHub SSH Key"}); err != nil {
		t.Fatal(err)
	}
	if err := kr.Add(agent.AddedKey{PrivateKey: hk, Comment: "Hammock-Key-Test"}); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _ = agent.ServeAgent(kr, c) }()
		}
	}()

	// Your usual key's public half, as git names it.
	sp, _ := ssh.NewSignerFromKey(usual)
	pubFile := filepath.Join(dir, "usual.pub")
	if err := os.WriteFile(pubFile, ssh.MarshalAuthorizedKey(sp.PublicKey()), 0o644); err != nil {
		t.Fatal(err)
	}

	// An ssh-keygen whose agent refuses your usual key: it signs only with a
	// key file hmk-ssh-sign writes for the Hammock Key (hmk-key-*.pub).
	bin := filepath.Join(dir, "bin")
	_ = os.Mkdir(bin, 0o755)
	script := `#!/bin/sh
f=""; last=""
while [ $# -gt 0 ]; do case "$1" in -f|-n|-Y) [ "$1" = -f ] && f="$2"; shift 2;; *) last="$1"; shift;; esac; done
case "$(basename "$f")" in hmk-key-*) cat "$f" > "$last.sig"; exit 0;; esac
echo "Couldn't sign message (signer): agent refused operation?" >&2
exit 255
`
	if err := os.WriteFile(filepath.Join(bin, "ssh-keygen"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("SSH_AUTH_SOCK", sock)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(dir, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	data := filepath.Join(dir, "buffer")
	_ = os.WriteFile(data, []byte("tree …"), 0o644)
	if code := runSSHSign([]string{"-Y", "sign", "-n", "git", "-f", pubFile, data}, ownSSHProgram()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	sig, err := os.ReadFile(data + ".sig")
	hpub, _ := ssh.NewPublicKey(&hk.PublicKey)
	if err != nil || strings.TrimSpace(string(sig)) != strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hpub))) {
		t.Fatalf("signature %q, %v", sig, err)
	}

	// From stdin it can't be tried twice: the first answer stands.
	if code := runSSHSign([]string{"-Y", "sign", "-n", "file", "-f", pubFile}, ownSSHProgram()); code == 0 {
		t.Fatal("stdin: fell back")
	}
}

func TestSignedFiles(t *testing.T) {
	if f := signedFiles([]string{"-Y", "sign", "-n", "git", "-f", "/k.pub", "/tmp/buf"}); len(f) != 1 || f[0] != "/tmp/buf" {
		t.Fatal(f)
	}
	if f := signedFiles([]string{"-Y", "sign", "-n", "file", "-f", "/k.pub"}); len(f) != 0 {
		t.Fatal(f)
	}
}

// hmk-jj-ssh-sign's own program is jj's, as jj sees it outside Hammock's
// panes (its file there is scoped to HMK_PANE_ID).
func TestOwnJJProgram(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
if [ -n "$HMK_PANE_ID" ]; then echo /x/run/hmk-jj-ssh-sign; else echo /Applications/1Password.app/Contents/MacOS/op-ssh-sign; fi
`
	if err := os.WriteFile(filepath.Join(bin, "jj"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("HMK_PANE_ID", "p_1")
	if got := ownJJProgram(); got != "/Applications/1Password.app/Contents/MacOS/op-ssh-sign" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("PATH", t.TempDir()) // no jj
	if got := ownJJProgram(); got != "" {
		t.Fatalf("without jj: %q", got)
	}
}
