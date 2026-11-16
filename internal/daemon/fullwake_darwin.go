package daemon

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/IOKitLib.h>
#include <IOKit/pwr_mgt/IOPM.h>
#include <CoreFoundation/CoreFoundation.h>

// hmk_full_wake: 0 in a dark wake (macOS woke without the display, for
// Power Nap, mail or a network keep-alive, and sleeps again in seconds),
// 1 fully awake, or when it can't tell. IOPMrootDomain's "System
// Capabilities" has the graphics bit only in a full wake (the display
// sleeping while the Mac is awake doesn't clear it).
static int hmk_full_wake(void) {
	io_service_t root = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceNameMatching("IOPMrootDomain"));
	if (root == IO_OBJECT_NULL) return 1;
	int full = 1;
	CFTypeRef v = IORegistryEntryCreateCFProperty(root, CFSTR("System Capabilities"), kCFAllocatorDefault, 0);
	if (v != NULL) {
		long long caps = 0;
		if (CFGetTypeID(v) == CFNumberGetTypeID() && CFNumberGetValue((CFNumberRef)v, kCFNumberLongLongType, &caps)) {
			full = (caps & kIOPMSystemCapabilityGraphics) != 0;
		}
		CFRelease(v);
	}
	IOObjectRelease(root);
	return full;
}
*/
import "C"

// fullyAwake: the Mac is fully awake, not in a dark wake.
func fullyAwake() bool { return C.hmk_full_wake() != 0 }
