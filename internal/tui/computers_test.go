package tui

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The TUI asks about another computer's link request, or one it asked for
// itself; not one hmk link asked for (that prompts in its own terminal).
func TestUnansweredLink(t *testing.T) {
	a := &App{linkReqs: []*pb.LinkRequest{{Id: "out", Code: "111 222"}}}
	if len(a.waitingLinks()) != 0 {
		t.Fatal("asked about a link hmk link is prompting for")
	}
	a.linking = true
	if w := a.waitingLinks(); len(w) == 0 || w[0].Id != "out" {
		t.Fatal("didn't ask about the link it asked for")
	}
	a.linking = false
	a.linkReqs = append(a.linkReqs, &pb.LinkRequest{Id: "in", Code: "333 444", Incoming: true})
	if w := a.waitingLinks(); len(w) == 0 || w[0].Id != "in" {
		t.Fatal("didn't ask about another computer's request")
	}
}

// Another computer asking to link doesn't open a dialog over the terminal
// (where Enter, meant for a pane, would answer it): it's said, and waits.
func TestIncomingLinkDoesntTakeKeys(t *testing.T) {
	a := &App{anim: newSprings()}
	a.applyLinkRequests(&pb.StateUpdate{LinkRequests: []*pb.LinkRequest{{Id: "in", Name: "Studio", Code: "333 444", Incoming: true}}})
	if a.overlay != nil {
		t.Fatal("an incoming link request opened a dialog by itself")
	}
	if len(a.waitingLinks()) == 0 {
		t.Fatal("the request isn't waiting")
	}
}

// A link this TUI asks for opens its code dialog even when another
// computer's request came first.
func TestOwnLinkAfterIncoming(t *testing.T) {
	a := &App{anim: newSprings()}
	a.applyLinkRequests(&pb.StateUpdate{LinkRequests: []*pb.LinkRequest{{Id: "in", Name: "Studio", Code: "333 444", Incoming: true}}})
	a.linking = true
	a.applyLinkRequests(&pb.StateUpdate{LinkRequests: []*pb.LinkRequest{{Id: "out", Name: "devbox", Code: "555 666"}}})
	if a.overlay == nil {
		t.Fatal("the link this TUI asked for didn't show its code")
	}
}

// Two computers asking at once: the palette answers either.
func TestPaletteOffersEachLinkRequest(t *testing.T) {
	a := &App{anim: newSprings(), computers: &pb.Computers{}}
	a.applyLinkRequests(&pb.StateUpdate{LinkRequests: []*pb.LinkRequest{
		{Id: "s", Name: "Studio", Code: "111 222", Incoming: true},
		{Id: "d", Name: "devbox", Code: "333 444", Incoming: true},
	}})
	var titles []string
	a.computerItems(func(id, title, hint, kw string, run func()) { titles = append(titles, title) })
	for _, want := range []string{"Check the Code from Studio…", "Check the Code from devbox…"} {
		found := false
		for _, got := range titles {
			found = found || got == want
		}
		if !found {
			t.Errorf("no %q in the palette: %v", want, titles)
		}
	}
}

// A computer that doesn't link with this one (the only other one, say)
// gets the same actions as on the Mac: Link Again, Remove (here only), and
// Unlink from all; and the footer marks it.
func TestPaletteOffersRefusedComputer(t *testing.T) {
	for _, st := range []*pb.Computer{
		{UserId: "us_b", Name: "Studio", Refused: true, Addresses: []string{"100.64.0.2:7433"}},
		{UserId: "us_b", Name: "Studio", Suspect: true, Addresses: []string{"100.64.0.2:7433"}},
	} {
		a := &App{anim: newSprings(), computers: &pb.Computers{List: []*pb.Computer{{UserId: "us_a", Name: "Laptop", ThisComputer: true}, st}}}
		a.setComputers(a.computers)
		if a.linked() {
			t.Fatal("counted as linked")
		}
		ids := map[string]bool{}
		a.computerItems(func(id, title, hint, kw string, run func()) { ids[id] = true })
		for _, want := range []string{"link-again:us_b", "remove:us_b", "unlink:us_b"} {
			if !ids[want] {
				t.Fatalf("%v: no %s in %v", st, want, ids)
			}
		}
		if computerState(st) == "" {
			t.Fatal("no state shown")
		}
	}
	// Its certificate changed: linked still, with Link Again.
	a := &App{anim: newSprings(), computers: &pb.Computers{List: []*pb.Computer{{UserId: "us_a", ThisComputer: true},
		{UserId: "us_b", Name: "Studio", CertChanged: true, Addresses: []string{"100.64.0.2:7433"}}}}}
	ids := map[string]bool{}
	a.computerItems(func(id, title, hint, kw string, run func()) { ids[id] = true })
	if !ids["link-again:us_b"] || ids["remove:us_b"] {
		t.Fatalf("certificate changed: %v", ids)
	}
}
