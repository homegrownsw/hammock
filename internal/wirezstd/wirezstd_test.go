package wirezstd

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	w, r := NewWriter(), NewReader(4<<20)
	rng := rand.New(rand.NewSource(1))
	total, sent := 0, 0
	for i := 0; i < 2000; i++ {
		var msg []byte
		switch rng.Intn(4) {
		case 0:
			msg = []byte(fmt.Sprintf("\x1b[32mline %d\x1b[0m ~/Developer/Fika $ go test ./...\r\n", i))
		case 1:
			msg = []byte{byte(i)}
		case 2:
			msg = make([]byte, 200_000)
			rng.Read(msg[:1000])
		default:
			msg = make([]byte, rng.Intn(3000))
			rng.Read(msg)
		}
		b, err := w.Encode(msg, i%5 != 0)
		if err != nil {
			t.Fatal(err)
		}
		total += len(msg)
		sent += len(b)
		got, err := r.Decode(append([]byte(nil), b...))
		if err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	t.Logf("%d bytes sent as %d", total, sent)
}

func TestPlainPassesThrough(t *testing.T) {
	for _, m := range [][]byte{{}, {0x08, 0x01}, {0x0a, 0x00}} {
		if got, err := NewReader(1 << 20).Decode(m); err != nil || string(got) != string(m) {
			t.Errorf("%x: %x %v", m, got, err)
		}
	}
}

func TestRefusesBadMessages(t *testing.T) {
	w := NewWriter()
	b, _ := w.Encode([]byte("hello hello hello"), true)
	for name, m := range map[string][]byte{
		"too large": {1, 0xff, 0xff, 0xff, 0xff, 0x0f},
		"lying":     append([]byte{1, 40}, b[2:]...),
		"truncated": b[:len(b)-2],
	} {
		if _, err := NewReader(1 << 20).Decode(m); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestFixture prints messages for the Swift decoder's test
// (apple/Tests/HammockKitTests/WireZstdTests.swift) with WIREZSTD_FIXTURE=1.
func TestFixture(t *testing.T) {
	if os.Getenv("WIREZSTD_FIXTURE") == "" {
		t.Skip()
	}
	w := NewWriter()
	big := bytes.Repeat([]byte("agent view: a long transcript line\n"), 5000)
	for _, m := range []struct {
		b        []byte
		compress bool
	}{
		{[]byte("\x08\x01hello terminal\r\n"), true},
		{[]byte("\x08\x01hello terminal\r\n"), true},
		{[]byte("\x0a\x03raw"), false},
		{big, true},
		{[]byte{}, true},
	} {
		b, _ := w.Encode(m.b, m.compress)
		fmt.Printf("%q,\n", base64.StdEncoding.EncodeToString(b))
	}
}
