package daemon

/*
#cgo LDFLAGS: -framework Foundation -framework DiskArbitration -framework IOKit -framework CoreFoundation
#include <stdlib.h>
char *hmk_volumes(void);
void hmk_power(int *known, int *ac, int *battery, int *percent);
*/
import "C"

import (
	"encoding/json"
	"unsafe"
)

// hostVolumes is the Mac's mounted volumes (macwatch_darwin.m).
func hostVolumes() []macVolume {
	p := C.hmk_volumes()
	if p == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(p))
	var raw []struct {
		Path      string `json:"path"`
		Name      string `json:"name"`
		Total     int64  `json:"total"`
		Avail     int64  `json:"avail"`
		Internal  bool   `json:"internal"`
		Removable bool   `json:"removable"`
		Ejectable bool   `json:"ejectable"`
		Local     bool   `json:"local"`
		Browsable bool   `json:"browsable"`
		Root      bool   `json:"root"`
		Model     string `json:"model"`
		Protocol  string `json:"protocol"`
	}
	if json.Unmarshal([]byte(C.GoString(p)), &raw) != nil {
		return nil
	}
	out := make([]macVolume, 0, len(raw))
	for _, r := range raw {
		v := macVolume{Path: r.Path, Name: r.Name, Total: r.Total, Avail: r.Avail}
		v.External = externalDrive(r.Path, r.Name, r.Model, r.Protocol, r.Internal, r.Removable, r.Ejectable, r.Local, r.Browsable, r.Root)
		out = append(out, v)
	}
	return out
}

// hostPower is where the Mac's power comes from (macwatch_darwin.m).
func hostPower() macPower {
	var known, ac, battery, percent C.int
	C.hmk_power(&known, &ac, &battery, &percent)
	return macPower{Known: known != 0, OnAC: ac != 0, HasBattery: battery != 0, Percent: int(percent)}
}
