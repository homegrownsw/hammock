package daemon

import (
	"testing"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// On a computer without a screen, "you're on your phone" means you used it
// since you last used any of your linked computers, not in the last 1000
// hours: at your Mac after using the phone, the server doesn't push.
func TestUsingPhoneHeadless(t *testing.T) {
	now := time.Now()
	d := &Daemon{}
	d.comp.links = map[string]*pb.StoredLink{
		"us_mac": {Record: &pb.Computer{LastInputUnixMs: now.Add(-30 * time.Second).UnixMilli()}},
	}
	if d.usingPhone(headlessIdle) {
		t.Fatal("never used the phone")
	}
	d.phoneAt = now.Add(-10 * time.Minute) // then you went to the Mac
	if d.usingPhone(headlessIdle) {
		t.Fatal("used the phone before the Mac: you're at the Mac")
	}
	d.phoneAt = now.Add(-5 * time.Second) // and back to the phone
	if !d.usingPhone(headlessIdle) {
		t.Fatal("used the phone after the Mac: you're on the phone")
	}
	// A Mac's own idle time still decides there.
	d.phoneAt = now.Add(-10 * time.Minute)
	if !d.usingPhone(time.Hour) {
		t.Fatal("on a Mac untouched for an hour, the phone 10 minutes ago counts")
	}
}
