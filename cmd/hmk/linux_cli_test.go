package main

import (
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// hmk link confirm takes a request's id, or the code shown on the other
// computer (typed with or without its space).
func TestFindLinkRequest(t *testing.T) {
	reqs := []*pb.LinkRequest{
		{Id: "lr_1", Name: "Studio", Code: "482 913", Incoming: true},
		{Id: "lr_2", Name: "Old", Code: "111 222", AcceptedHere: true},
		{Id: "lr_3", Name: "Waiting"},
	}
	for in, want := range map[string]string{"lr_1": "lr_1", "482913": "lr_1", "482 913": "lr_1"} {
		r, err := findLinkRequest(reqs, in)
		if err != nil || r.Id != want {
			t.Errorf("%q: %v %v", in, r, err)
		}
	}
	for _, in := range []string{"111222", "lr_2", "lr_3", "000000"} {
		if _, err := findLinkRequest(reqs, in); err == nil {
			t.Errorf("%q: found", in)
		}
	}
}

// The unit keeps pane holders running across restarts and names the
// state dir that hmk start looks for.
func TestServiceUnit(t *testing.T) {
	u := serviceUnit("/opt/hammock/hmkd", "/srv/my state")
	for _, want := range []string{"ExecStart=/opt/hammock/hmkd --dir \"/srv/my state\"\n", "KillMode=process", "Restart=on-failure", "WantedBy=default.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
}

func TestQRText(t *testing.T) {
	q, err := qrText("hammock://pair/abc")
	if err != nil || !strings.Contains(q, "█") || strings.Count(q, "\n") < 10 {
		t.Fatalf("%v\n%s", err, q)
	}
}
