package daemon

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/pwr_mgt/IOPMLib.h>

static IOPMAssertionID hmk_awake = 0;

// hmk_hold_awake takes or lets go of an assertion keeping the system (not
// the display) from idle sleep; it returns whether one is held.
static int hmk_hold_awake(int on) {
	if (on && hmk_awake == 0) {
		IOPMAssertionID a = 0;
		if (IOPMAssertionCreateWithName(kIOPMAssertionTypePreventUserIdleSystemSleep, kIOPMAssertionLevelOn,
		                                CFSTR("Hammock: an agent is working (Settings > Agents > Keep this Mac awake)"), &a) == kIOReturnSuccess) {
			hmk_awake = a;
		}
	} else if (!on && hmk_awake != 0) {
		IOPMAssertionRelease(hmk_awake);
		hmk_awake = 0;
	}
	return hmk_awake != 0;
}
*/
import "C"

// holdAwake takes or lets go of the Mac's power assertion.
func holdAwake(on bool) bool {
	v := 0
	if on {
		v = 1
	}
	return C.hmk_hold_awake(C.int(v)) != 0
}
