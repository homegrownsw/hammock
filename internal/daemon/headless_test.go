package daemon

import (
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
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
	// On a Mac too, untouched for an hour, the linked Mac decides: you used
	// it since the phone.
	d.phoneAt = now.Add(-10 * time.Minute)
	if d.usingPhone(time.Hour) {
		t.Fatal("at the linked Mac since the phone: not on the phone")
	}
	d.comp.links["us_mac"].Record.LastInputUnixMs = now.Add(-time.Hour).UnixMilli()
	if !d.usingPhone(time.Hour) {
		t.Fatal("both Macs untouched for an hour, the phone 10 minutes ago counts")
	}
}

// At another linked Mac all day, with the phone connected to this one: the
// phone opening Hammock this morning doesn't send this Mac's pushes to it
// while you work at the other (its own notifications tell you there).
func TestUsingPhoneAtOtherMac(t *testing.T) {
	now := time.Now()
	d := &Daemon{}
	d.comp.links = map[string]*pb.StoredLink{
		"us_work": {Record: &pb.Computer{LastInputUnixMs: now.Add(-20 * time.Second).UnixMilli()}},
	}
	d.phoneAt = now.Add(-3 * time.Hour) // opened on the phone this morning
	if d.usingPhone(4 * time.Hour) {
		t.Fatal("typing at the work Mac: not on the phone")
	}
	d.phoneAt = now.Add(-5 * time.Second) // picked up the phone just now
	if !d.usingPhone(4 * time.Hour) {
		t.Fatal("used the phone after the work Mac: on the phone")
	}
	// A time ahead of this clock (it stepped back since) says nothing either.
	d.comp.links["us_work"].Record.LastInputUnixMs = now.Add(time.Hour).UnixMilli()
	if !d.usingPhone(4 * time.Hour) {
		t.Fatal("an input time in the future hid the phone's use")
	}
	d.comp.links["us_work"].Record.LastInputUnixMs = now.Add(-20 * time.Second).UnixMilli()
	// A computer it no longer links with says nothing of where you are.
	d.phoneAt = now.Add(-3 * time.Hour)
	d.comp.links["us_work"].Refused = true
	if !d.usingPhone(4 * time.Hour) {
		t.Fatal("refused link: only this Mac's idle time counts")
	}
}
