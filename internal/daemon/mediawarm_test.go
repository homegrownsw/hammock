package daemon

import "testing"

// A viewer's screen opened again soon after starts where the last one got
// to; another viewer, the other layer, or a session too short to have
// measured anything, start cold.
func TestScreenVideoStartsWarmForTheSameViewer(t *testing.T) {
	h := &mediaHub{d: &Daemon{}, byID: map[string]*mediaSession{}}
	phone, other := &conn{deviceID: "phone"}, &conn{deviceID: "tablet"}
	session := func(c *conn, background bool) *mediaSession {
		s := &mediaSession{hub: h, paneID: screenPaneID(0), rate: mediaStartRate, floor: mediaScreenMinRate,
			ceiling: mediaScreenMaxRate, canHEVC: true, background: background}
		s.warmUp(c)
		return s
	}

	first := session(phone, false)
	if first.rate != mediaStartRate {
		t.Fatalf("first session's rate = %v, want the start rate", first.rate)
	}
	first.keepWarm(30_000_000, 60_000_000, true, 1)
	if s := session(phone, false); s.rate != mediaStartRate {
		t.Fatalf("after a 1 s session, rate = %v, want the start rate", s.rate)
	}

	first.keepWarm(30_000_000, 60_000_000, true, 20)
	s := session(phone, false)
	if s.rate != 30_000_000 || s.linkBW != 60_000_000 || !s.hevc {
		t.Fatalf("warm session: rate %v, link %v, hevc %v; want 30M, 60M, true", s.rate, s.linkBW, s.hevc)
	}
	if s := session(other, false); s.rate != mediaStartRate {
		t.Fatalf("another viewer's rate = %v, want the start rate", s.rate)
	}
	if s := session(phone, true); s.rate != mediaStartRate {
		t.Fatalf("the base layer's rate = %v, want the start rate", s.rate)
	}
	if s := session(&conn{}, false); s.rate != mediaStartRate {
		t.Fatalf("a viewer with no device id: rate = %v, want the start rate", s.rate)
	}
}
