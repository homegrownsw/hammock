package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The tokens come from stdin, one a line, in either order.
func TestReadSlackTokens(t *testing.T) {
	read := func(in string) (string, string, error) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		go func() { _, _ = w.WriteString(in); w.Close() }()
		defer r.Close()
		return readSlackTokens(r, &bytes.Buffer{})
	}
	if u, a, err := read("xapp-1-A\n\n  xoxp-2-B \n"); err != nil || u != "xoxp-2-B" || a != "xapp-1-A" {
		t.Fatalf("%q %q %v", u, a, err)
	}
	if _, _, err := read("xoxp-only\n"); err == nil {
		t.Fatal("one token was enough")
	}
}

func TestPrintSlack(t *testing.T) {
	var b bytes.Buffer
	printSlack(&b, "status", &pb.SlackReply{ManifestUrl: "https://api.slack.com/apps?new_app=1"}, "")
	if !strings.Contains(b.String(), "new_app=1") {
		t.Fatal(b.String())
	}
	b.Reset()
	printSlack(&b, "status", &pb.SlackReply{Link: &pb.SlackLink{Team: "Acme", User: "me", Url: "https://acme.slack.com/", Idle: true}}, "")
	if !strings.HasPrefix(b.String(), "Connected to Acme as me (https://acme.slack.com): not listening") {
		t.Fatal(b.String())
	}
	b.Reset()
	printSlack(&b, "status", &pb.SlackReply{}, "Studio")
	if !strings.Contains(b.String(), "connected on Studio") {
		t.Fatal(b.String())
	}
}
