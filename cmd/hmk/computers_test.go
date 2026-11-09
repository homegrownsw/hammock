package main

import (
	"os"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestIsLinkAddress(t *testing.T) {
	for target, want := range map[string]bool{
		"":                         false,
		"Studio":                   false,
		"my-mac-studio":            false,
		"100.101.102.103":          true,
		"fd7a:115c:a1e0::1":        true,
		"[fd7a:115c:a1e0::1]:7433": true,
		"studio.tail1234.ts.net":   true,
		"studio.local":             true,
		"100.101.102.103:7433":     true,
		"hammock://link/abc":       true,
	} {
		if got := isLinkAddress(target); got != want {
			t.Errorf("isLinkAddress(%q) = %v, want %v", target, got, want)
		}
	}
}

func TestPlaceAgent(t *testing.T) {
	w := &world{
		groups: []*pb.Group{
			{Id: "gp_here", Name: "Here"},
			{Id: "gp_studio", Name: "Web", HostUserId: "us_studio", MyRole: pb.Role_ROLE_OWNER},
		},
		computers: &pb.Computers{List: []*pb.Computer{
			{UserId: "us_me", Name: "Laptop", ThisComputer: true},
			{UserId: "us_studio", Name: "Studio"},
		}},
	}
	place := func(group, on string) (*pb.StartAgent, error) {
		req := &pb.StartAgent{Harness: "claude"}
		return req, w.placeAgent(req, group, on, "gp_pane")
	}
	// A Group on the Studio: started there, in its home folder (not here's).
	if r, err := place("Web", ""); err != nil || r.GroupId != "gp_studio" || r.Cwd != "~" || r.ComputerId != "" {
		t.Fatalf("--group on the Studio: %v %v", r, err)
	}
	// --on that disagrees with --group: refused.
	if _, err := place("Here", "Studio"); err == nil {
		t.Fatal("--on Studio --group Here was taken")
	}
	if _, err := place("Web", "this"); err == nil {
		t.Fatal("--on this --group (a Studio Group) was taken")
	}
	// --on this is as no --on: here, in the pane's Group.
	if r, err := place("", "this"); err != nil || r.ComputerId != "" || r.GroupId != "gp_pane" || r.Cwd == "~" {
		t.Fatalf("--on this: %v %v", r, err)
	}
	// --on Studio alone: there, its home folder, the folder's Group there.
	if r, err := place("", "Studio"); err != nil || r.ComputerId != "us_studio" || r.GroupId != "" || r.Cwd != "~" {
		t.Fatalf("--on Studio: %v %v", r, err)
	}
}

func TestResumeGroupComputer(t *testing.T) {
	w := &world{
		groups: []*pb.Group{
			{Id: "gp_here", Name: "Here"},
			{Id: "gp_studio", Name: "Web", HostUserId: "us_studio", MyRole: pb.Role_ROLE_OWNER},
		},
		computers: &pb.Computers{List: []*pb.Computer{{UserId: "us_me", Name: "Laptop", ThisComputer: true}, {UserId: "us_studio", Name: "Studio"}}},
	}
	var sent *pb.ResumeSession
	call := func(m *pb.ClientMessage) (*pb.ServerMessage, error) {
		sent = m.GetResumeSession()
		return &pb.ServerMessage{Msg: &pb.ServerMessage_SessionResumed{SessionResumed: &pb.SessionResumed{}}}, nil
	}
	id := "0123456789abcdef0123456789abcdef0123" // full length: no lookup
	// --on (this computer) with a Studio Group: refused.
	if err := runResume(call, w, []string{id, "--group", "Web"}, "", true); err == nil {
		t.Fatal("--on this --group (a Studio Group) was taken")
	}
	// --on Studio with a Group here: refused.
	if err := runResume(call, w, []string{id, "--group", "Here"}, "us_studio", true); err == nil {
		t.Fatal("--on Studio --group Here was taken")
	}
	// No --on, a Studio Group: resumed on the Studio.
	if err := runResume(call, w, []string{id, "--group", "Web"}, "", false); err != nil || sent.GetComputerId() != "us_studio" || sent.GroupId != "gp_studio" {
		t.Fatalf("resume into a Studio Group: %v %v", sent, err)
	}
}

func TestPlaceAgentRemoteDir(t *testing.T) {
	w := &world{computers: &pb.Computers{List: []*pb.Computer{{UserId: "us_me", Name: "Laptop", ThisComputer: true}, {UserId: "us_studio", Name: "Studio"}}}}
	home, _ := os.UserHomeDir()
	for dir, want := range map[string]string{
		home + "/Developer/web": "~/Developer/web", // the shell expanded ~ here
		"~/web":                 "~/web",
		"/srv/app":              "/srv/app",
		"web":                   "", // relative: refused
	} {
		req := &pb.StartAgent{Cwd: dir}
		err := w.placeAgent(req, "", "Studio", "")
		if want == "" {
			if err == nil {
				t.Errorf("--dir %s on another computer was taken", dir)
			}
			continue
		}
		if err != nil || req.Cwd != want {
			t.Errorf("--dir %s: sent %q (%v), want %q", dir, req.Cwd, err, want)
		}
	}
}
