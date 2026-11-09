package thread

import "sync"

var (
	regMu  sync.RWMutex
	codecs = map[string]Codec{}
)

// Register makes a codec available for its harness.
func Register(c Codec) {
	regMu.Lock()
	defer regMu.Unlock()
	codecs[c.Harness()] = c
}

// CodecFor returns the codec for a harness, if any.
func CodecFor(harness string) (Codec, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	c, ok := codecs[harness]
	return c, ok
}
