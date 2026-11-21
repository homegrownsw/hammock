package tui

import "testing"

func TestReadableFlash(t *testing.T) {
	for _, c := range []struct {
		in, want string
		isErr    bool
	}{
		{"✓ Copied", "✓ Copied", false},
		{"⚠ no such pane", "⚠ no such pane", true},
		{"⚠ rpc error: code = NotFound desc = no such pane", "⚠ No such pane", true},
		{"rpc error: code = Unavailable desc = connection refused", "Connection refused", true},
		{"Couldn't rename it: rpc error: code = Unknown desc = file exists", "Couldn't rename it: file exists", true},
		{"⚠ rpc error: code = Unknown desc = rpc error: code = Internal desc = élan lost", "⚠ Élan lost", true},
	} {
		got, isErr := readableFlash(c.in)
		if got != c.want || isErr != c.isErr {
			t.Errorf("readableFlash(%q) = %q, %v; want %q, %v", c.in, got, isErr, c.want, c.isErr)
		}
	}
}

func TestErrorFlashesStayLonger(t *testing.T) {
	a := &App{anim: newSprings()}
	a.flash("✓ Copied")
	if a.toastFor != flashFor {
		t.Errorf("a flash shows for %v, want %v", a.toastFor, flashFor)
	}
	a.flash("⚠ rpc error: code = NotFound desc = no such pane")
	if a.toastFor != errorFlashFor || a.toast != "⚠ No such pane" {
		t.Errorf("an error shows %q for %v, want %q for %v", a.toast, a.toastFor, "⚠ No such pane", errorFlashFor)
	}
}
