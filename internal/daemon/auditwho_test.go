package daemon

import "testing"

// Phones in the audit log: by their kind before they had names, by the name
// they paired with after.
func TestAuditWhoPhones(t *testing.T) {
	d := &Daemon{}
	for actor, want := range map[string]string{
		"ios":                "Your iPhone",
		"android":            "Your Android phone",
		"device:Sam's Pixel": "Sam's Pixel",
		"remote:Sam's Pixel": "A device calling itself “Sam's Pixel”",
		"remote:":            "A device",
	} {
		if got, kind := d.auditWho(actor); got != want || kind != "device" {
			t.Errorf("auditWho(%q) = %q, %q; want %q, device", actor, got, kind, want)
		}
	}
}
