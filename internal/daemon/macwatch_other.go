//go:build !darwin && !linux

package daemon

// This Mac triggers watch a Mac: elsewhere there's nothing to see (they're
// refused when saved, cleanTrigger).

func hostVolumes() []macVolume { return nil }

func hostPower() macPower { return macPower{} }
