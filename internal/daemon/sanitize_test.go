package daemon

import (
	"strings"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestCleanText(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		breaks   bool
	}{
		{"plain name", "plain name", false},
		{"evil\x1b]52;c;aGk=\x07name", "evil]52;c;aGk=name", false},
		{"a‮gnp.exe", "agnp.exe", false},
		{"c1\u009bx\u007f", "c1x", false},
		{"two\nlines", "two lines", false},
		{"two\nlines\x1b[2J", "two\nlines[2J", true},
		{"café 🎉", "café 🎉", false},
	} {
		if got := cleanText(tc.in, 100, tc.breaks); got != tc.want {
			t.Errorf("cleanText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := cleanLine(strings.Repeat("é", 300), 10); got != strings.Repeat("é", 10) {
		t.Errorf("not cut: %q", got)
	}
}

// What a host sends of its Groups arrives without terminal escapes or
// bidi overrides.
func TestPeerLinkCleansHostState(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	defer d.mu.Unlock()
	a := newLink(d, &pb.InviteToken{CertSha256: "aa", HostUserId: "us_a"})
	d.links["aa"] = a
	a.applyLocked(&pb.StateUpdate{
		Groups:    []*pb.Group{{Id: "gr_a", Name: "G\x1b[31m"}},
		Tabs:      []*pb.Tab{{Id: "tb_a", GroupId: "gr_a", Name: "T‮"}},
		Panes:     []*pb.Pane{{Id: "pn_a", TabId: "tb_a", Title: "\x1b]0;x\x07P", Url: "https://x\x00y"}},
		Attention: []*pb.AttentionItem{{Id: "at_a", PaneId: "pn_a", Summary: "ok\n\x1b[2Jdone"}},
		People:    []*pb.Person{{UserId: "us_a", Name: "Host\x9b", DeviceName: "Mac⁦"}},
	}, false)
	if g := a.groups["gr_a"].Name; g != "G[31m" {
		t.Errorf("group %q", g)
	}
	if n := a.tabs["tb_a"].Name; n != "T" {
		t.Errorf("tab %q", n)
	}
	if p := a.panes["pn_a"]; p.Title != "]0;xP" || p.Url != "https://xy" {
		t.Errorf("pane %q %q", p.Title, p.Url)
	}
	if s := a.attn["at_a"].Summary; s != "ok\n[2Jdone" {
		t.Errorf("summary %q", s)
	}
	if p := a.people["us_a"]; p.Name != "Host" || p.DeviceName != "Mac" {
		t.Errorf("person %q %q", p.Name, p.DeviceName)
	}
}
