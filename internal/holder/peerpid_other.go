//go:build !darwin && !linux

package holder

import (
	"errors"
	"net"

	"github.com/alexjsp/hammock/internal/codesign"
)

// sockPeer is unknown here.
func sockPeer(*net.UnixConn) PeerProc { return PeerProc{} }

func peerExe(PeerProc) (string, error) { return "", errors.New("unknown peer") }

func peerSignature(PeerProc, string) (codesign.Signature, bool) { return codesign.Signature{}, false }

func peerStill(PeerProc) bool { return false }

func sameProc(PeerProc, PeerProc) bool { return false }
