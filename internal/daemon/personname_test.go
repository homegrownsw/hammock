package daemon

import (
	"os/user"
	"testing"
)

func TestPersonName(t *testing.T) {
	for _, c := range []struct{ uid, gecos, want string }{
		{"501", "Sam Smith", "Sam Smith"},
		{"1000", "Sam Smith,,,", "Sam Smith"},
		{"0", "Console and webGui login account", ""},
		{"0", "root", ""},
	} {
		if got := personName(&user.User{Uid: c.uid, Name: c.gecos}); got != c.want {
			t.Errorf("personName(%q, %q) = %q, want %q", c.uid, c.gecos, got, c.want)
		}
	}
}
