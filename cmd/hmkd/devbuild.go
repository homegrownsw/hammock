package main

import "github.com/homegrownsw/hammock/internal/codesign"

// release is set at link time ("-X main.release=1") by builds made for
// people to install, on platforms with no code signature to tell them by
// (scripts/build-linux.sh, given HMK_VERSION).
var release string

// devBuild says whether this hmkd is a development build, the only kind
// that takes the development flags (--dev-pairing, --remote-listen): on a
// Mac, one not signed by a team; elsewhere, one not marked a release.
func devBuild() bool {
	if release != "" {
		return false
	}
	s, _ := codesign.Self()
	return s.Team == ""
}
