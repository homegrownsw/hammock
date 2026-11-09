// Package codesign asks macOS who signed a running process: how hmkd tells
// the Hammock app from anything else of yours that connects to it, and how a
// pane holder tells hmkd from a program that read its secret. The same
// checks as helpers/common/peer.h, in C over CoreFoundation and Security
// (no Objective-C runtime: every pane holder links this).
package codesign

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>

static SecCodeRef hmk_guest(int pid) {
	CFNumberRef n = CFNumberCreate(NULL, kCFNumberIntType, &pid);
	const void *k[] = {kSecGuestAttributePid};
	const void *v[] = {n};
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, k, v, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	SecCodeRef code = NULL;
	OSStatus st = SecCodeCopyGuestWithAttributes(NULL, attrs, kSecCSDefaultFlags, &code);
	CFRelease(attrs);
	CFRelease(n);
	return st == errSecSuccess ? code : NULL;
}

static void hmk_copy(CFStringRef s, char *buf, int len) {
	buf[0] = 0;
	if (s && CFGetTypeID(s) == CFStringGetTypeID()) CFStringGetCString(s, buf, len, kCFStringEncodingUTF8);
}

// hmk_info fills in code's signing identifier and team ("" ad hoc) and
// whether it runs with the hardened runtime. 0 on success.
static int hmk_info(SecCodeRef code, char *ident, int ilen, char *team, int tlen, int *runtime) {
	SecStaticCodeRef st = NULL;
	CFDictionaryRef info = NULL;
	ident[0] = 0; team[0] = 0; *runtime = 0;
	if (SecCodeCopyStaticCode(code, kSecCSDefaultFlags, &st) != errSecSuccess) return -1;
	if (SecCodeCopySigningInformation(st, kSecCSSigningInformation | kSecCSDynamicInformation, &info) != errSecSuccess) {
		CFRelease(st);
		return -1;
	}
	hmk_copy(CFDictionaryGetValue(info, kSecCodeInfoIdentifier), ident, ilen);
	hmk_copy(CFDictionaryGetValue(info, kSecCodeInfoTeamIdentifier), team, tlen);
	CFNumberRef f = CFDictionaryGetValue(info, kSecCodeInfoFlags);
	uint32_t flags = 0;
	if (f && CFGetTypeID(f) == CFNumberGetTypeID()) CFNumberGetValue(f, kCFNumberSInt32Type, &flags);
	*runtime = (flags & kSecCodeSignatureRuntime) != 0;
	CFRelease(info);
	CFRelease(st);
	return 0;
}

static int hmk_self(char *ident, int ilen, char *team, int tlen, int *runtime) {
	SecCodeRef me = NULL;
	if (SecCodeCopySelf(kSecCSDefaultFlags, &me) != errSecSuccess) return -1;
	int r = hmk_info(me, ident, ilen, team, tlen, runtime);
	CFRelease(me);
	return r;
}

// hmk_check: 0 when pid's running code is valid and satisfies req (a code
// requirement in Apple's language), filling in what hmk_info does.
static int hmk_check(int pid, const char *req, char *ident, int ilen, char *team, int tlen, int *runtime) {
	SecCodeRef code = hmk_guest(pid);
	if (!code) return -1;
	SecRequirementRef r = NULL;
	CFStringRef text = CFStringCreateWithCString(NULL, req, kCFStringEncodingUTF8);
	int ok = text && SecRequirementCreateWithString(text, kSecCSDefaultFlags, &r) == errSecSuccess &&
	         SecCodeCheckValidity(code, kSecCSDefaultFlags, r) == errSecSuccess &&
	         hmk_info(code, ident, ilen, team, tlen, runtime) == 0;
	if (r) CFRelease(r);
	if (text) CFRelease(text);
	CFRelease(code);
	return ok ? 0 : -1;
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

// Signature is what a process is signed as.
type Signature struct {
	Identifier string // its signing identifier (a bundle id, or "hmkd")
	Team       string // its team, "" for an ad-hoc signature
	Runtime    bool   // the hardened runtime: nothing injected, no debugger
}

func info(f func(ident *C.char, team *C.char, runtime *C.int) C.int) (Signature, bool) {
	var ident, team [256]C.char
	var runtime C.int
	if f(&ident[0], &team[0], &runtime) != 0 {
		return Signature{}, false
	}
	return Signature{
		Identifier: C.GoString(&ident[0]), Team: C.GoString(&team[0]), Runtime: runtime != 0,
	}, true
}

// Self is how this process is signed.
var Self = sync.OnceValues(func() (Signature, bool) {
	return info(func(ident, team *C.char, runtime *C.int) C.int {
		return C.hmk_self(ident, 256, team, 256, runtime)
	})
})

// Check reports whether pid's running code is validly signed and satisfies
// requirement, and how it's signed.
func Check(pid int, requirement string) (Signature, bool) {
	if pid <= 0 {
		return Signature{}, false
	}
	req := C.CString(requirement)
	defer C.free(unsafe.Pointer(req))
	return info(func(ident, team *C.char, runtime *C.int) C.int {
		return C.hmk_check(C.int(pid), req, ident, 256, team, 256, runtime)
	})
}
