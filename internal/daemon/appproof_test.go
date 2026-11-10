package daemon

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A nonce is for its own connection, once, while it's fresh: a wrong one
// (another connection's, a guess) is ignored and leaves the right one
// waiting; a used one, or one for a connection that stopped waiting, is
// for nothing.
func TestAppProofNonces(t *testing.T) {
	var p appProofs
	now := time.Now()
	a, b := &appProofWait{}, &appProofWait{}
	p.issue(a, now)
	p.issue(b, now)
	if a.id == b.id || len(a.nonce) != 32 || bytes.Equal(a.nonce, b.nonce) {
		t.Fatalf("ids %d %d, nonces %x %x", a.id, b.id, a.nonce, b.nonce)
	}
	if w, _ := p.take(b.id, a.nonce, now); w != nil {
		t.Fatal("another connection's nonce taken")
	}
	if w, _ := p.take(b.id, b.nonce[:16], now); w != nil {
		t.Fatal("part of a nonce taken")
	}
	if w, _ := p.take(b.id+100, b.nonce, now); w != nil {
		t.Fatal("a nonce taken for no connection")
	}
	if w, fresh := p.take(b.id, b.nonce, now); w != b || !fresh {
		t.Fatalf("its own nonce: %v, fresh %v", w, fresh)
	}
	if w, _ := p.take(b.id, b.nonce, now); w != nil {
		t.Fatal("a nonce taken twice")
	}
	if p.drop(b) {
		t.Fatal("dropped one already taken")
	}
	// Too late: taken (so its verdict is sent), but not fresh.
	if w, fresh := p.take(a.id, a.nonce, now.Add(appProofFor+time.Millisecond)); w != a || fresh {
		t.Fatalf("an expired nonce: %v, fresh %v", w, fresh)
	}
	c := &appProofWait{}
	p.issue(c, now)
	if !p.drop(c) {
		t.Fatal("couldn't drop a waiting one")
	}
	if w, _ := p.take(c.id, c.nonce, now); w != nil {
		t.Fatal("a dropped nonce taken")
	}
}

// fakeAppProof is a process other than the test's, sending a proof
// (HMK_TEST_APP_PROOF: service, connection, nonce in hex), and printing
// the daemon's answer.
func fakeAppProof() int {
	p := strings.Split(os.Getenv("HMK_TEST_APP_PROOF"), ",")
	if len(p) != 3 {
		return 2
	}
	id, _ := strconv.ParseUint(p[1], 10, 64)
	nonce, _ := hex.DecodeString(p[2])
	got, err := sendAppProof(p[0], id, nonce, 2*time.Second)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	fmt.Println(got)
	return 0
}
