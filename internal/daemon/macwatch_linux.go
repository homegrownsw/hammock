package daemon

import "syscall"

// On Linux only the root file system is looked at, for Only If's disk
// condition (This Mac triggers are refused when saved, cleanTrigger).
func hostVolumes() []macVolume {
	var st syscall.Statfs_t
	if syscall.Statfs("/", &st) != nil {
		return nil
	}
	bs := int64(st.Bsize)
	return []macVolume{{Path: "/", Name: "/", Total: int64(st.Blocks) * bs, Avail: int64(st.Bavail) * bs}}
}

func hostPower() macPower { return macPower{} }
