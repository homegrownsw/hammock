package daemon

import (
	"strings"
	"time"
)

// A screen's video opened again by the same viewer soon after its last
// one ended (the phone left the app a moment, and its link broke) starts
// where that one had got to: its rate, the link's bandwidth and the sharp
// tier, not mediaStartRate's guess, which took several seconds of soft
// pictures to climb back from.
const mediaWarmFor = time.Minute

type mediaWarm struct {
	rate, linkBW float64
	hevc         bool
	at           time.Time
}

// warmKeyFor is who's watching what: "" when there's no telling (a viewer
// with no device id), or it isn't a screen. Set once, as the session opens.
func (s *mediaSession) warmKeyFor(c *conn) string {
	if !strings.HasPrefix(s.paneID, "screen:") {
		return ""
	}
	who := s.viewer
	if who == "" {
		s.hub.d.mu.Lock()
		who = c.deviceID
		s.hub.d.mu.Unlock()
	}
	if who == "" {
		return ""
	}
	layer := "detail"
	if s.background {
		layer = "base"
	}
	return who + "\x00" + s.paneID + "\x00" + layer
}

// keepWarm remembers how far an ending session got (after a few seconds'
// measuring: before that its rate is still the guess).
func (s *mediaSession) keepWarm(rate, linkBW float64, hevc bool, secs float64) {
	key := s.warmKey
	if key == "" || secs < 3 {
		return
	}
	h := s.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for k, w := range h.warm {
		if now.Sub(w.at) > mediaWarmFor {
			delete(h.warm, k)
		}
	}
	if h.warm == nil {
		h.warm = map[string]mediaWarm{}
	}
	h.warm[key] = mediaWarm{rate: rate, linkBW: linkBW, hevc: hevc, at: now}
}

// warmUp starts a new session where its viewer's last one left off, if
// that was lately. Before begin.
func (s *mediaSession) warmUp(c *conn) {
	s.warmKey = s.warmKeyFor(c)
	key := s.warmKey
	if key == "" {
		return
	}
	h := s.hub
	h.mu.Lock()
	w, ok := h.warm[key]
	h.mu.Unlock()
	if !ok || time.Since(w.at) > mediaWarmFor {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rate = min(max(w.rate, s.floor), s.ceiling)
	s.linkBW = w.linkBW
	s.hevc = s.canHEVC && w.hevc
	// The size follows on the first tick, down at once if the rate wants
	// it smaller than the viewer's own (where the encoder starts).
}
