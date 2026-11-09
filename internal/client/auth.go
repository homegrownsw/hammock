package client

import "strings"

// What a remote client signs to prove who it is. Each signature covers a
// tag naming its purpose, the host's certificate fingerprint (the pin the
// client dialled with) and the host's fresh nonce, so a signature made for
// one purpose or one host can't be replayed for another.
const (
	DeviceAuthTag = "hmk-device-auth-v1" // a paired device's Authenticate
	PeerAuthTag   = "hmk-peer-auth-v1"   // a guest daemon's PeerHello
	PeerHostTag   = "hmk-peer-host-v1"   // the host's answer to a guest's nonce
)

// NonceSize is the length of every authentication nonce.
const NonceSize = 32

// AuthMessage is tag | 0 | certSHA256 (lowercase hex) | 0 | nonce.
func AuthMessage(tag, certSHA256 string, nonce []byte) []byte {
	cert := strings.ToLower(certSHA256)
	b := make([]byte, 0, len(tag)+len(cert)+len(nonce)+2)
	b = append(b, tag...)
	b = append(b, 0)
	b = append(b, cert...)
	b = append(b, 0)
	return append(b, nonce...)
}
