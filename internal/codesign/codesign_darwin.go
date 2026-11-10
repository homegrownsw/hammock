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
#include <libproc.h>
#include <sys/socket.h>
#include <sys/un.h>
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

// hmk_guest_token is hmk_guest by audit token: the process as it was when
// the token was taken. One that has since exec'd (or exited, its pid
// reused) has a new pid version, and isn't found.
static SecCodeRef hmk_guest_token(const audit_token_t *tok) {
	CFDataRef d = CFDataCreate(NULL, (const UInt8 *)tok, sizeof(*tok));
	const void *k[] = {kSecGuestAttributeAudit};
	const void *v[] = {d};
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, k, v, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	SecCodeRef code = NULL;
	OSStatus st = SecCodeCopyGuestWithAttributes(NULL, attrs, kSecCSDefaultFlags, &code);
	CFRelease(attrs);
	CFRelease(d);
	return st == errSecSuccess ? code : NULL;
}

static int hmk_peer_token(int fd, audit_token_t *tok) {
	socklen_t n = sizeof(*tok);
	return getsockopt(fd, SOL_LOCAL, LOCAL_PEERTOKEN, tok, &n) == 0 && n == sizeof(*tok) ? 0 : -1;
}

static int hmk_token_path(audit_token_t *tok, char *buf, int len) {
	return proc_pidpath_audittoken(tok, buf, (uint32_t)len) > 0 ? 0 : -1;
}

// (audit_token_to_pid, without linking libbsm: the pid is its sixth word.)
static int hmk_token_pid(const audit_token_t *tok) { return (int)tok->val[5]; }

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
static int hmk_check_code(SecCodeRef code, const char *req, char *ident, int ilen, char *team, int tlen, int *runtime);

static int hmk_check(int pid, const char *req, char *ident, int ilen, char *team, int tlen, int *runtime) {
	return hmk_check_code(hmk_guest(pid), req, ident, ilen, team, tlen, runtime);
}

static int hmk_check_token(const audit_token_t *tok, const char *req, char *ident, int ilen, char *team, int tlen, int *runtime) {
	return hmk_check_code(hmk_guest_token(tok), req, ident, ilen, team, tlen, runtime);
}

// hmk_check_code checks (and releases) code against req ("" for none:
// only that it's validly signed).
static int hmk_check_code(SecCodeRef code, const char *req, char *ident, int ilen, char *team, int tlen, int *runtime) {
	if (!code) return -1;
	if (!req[0]) {
		int ok = SecCodeCheckValidity(code, kSecCSDefaultFlags, NULL) == errSecSuccess &&
		         hmk_info(code, ident, ilen, team, tlen, runtime) == 0;
		CFRelease(code);
		return ok ? 0 : -1;
	}
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
	"errors"
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

// Token is a process's audit token: its pid and pid version, which changes
// when it execs, so a check by token is of the program that was running
// when the token was taken (a socket's peer as it connected), never one
// exec'd in its place since or a process that took its pid.
type Token struct {
	t  C.audit_token_t
	ok bool
}

// PeerToken is the audit token of the process at the other end of the Unix
// socket fd, as it was when it connected.
func PeerToken(fd int) Token {
	var t Token
	t.ok = C.hmk_peer_token(C.int(fd), &t.t) == 0
	return t
}

// Valid reports whether the token was taken.
func (t Token) Valid() bool { return t.ok }

// PID is the token's process id (0 if none).
func (t Token) PID() int {
	if !t.ok {
		return 0
	}
	return int(C.hmk_token_pid(&t.t))
}

// Path is the executable of the token's process, if it's still the one
// the token was taken from.
func (t Token) Path() (string, error) {
	if !t.ok {
		return "", errors.New("no audit token")
	}
	var buf [4 * 1024]C.char
	if C.hmk_token_path(&t.t, &buf[0], C.int(len(buf))) != 0 {
		return "", errors.New("process gone, or not the one that connected")
	}
	return C.GoString(&buf[0]), nil
}

// Live reports whether the token's process still runs the program it did
// when the token was taken.
func (t Token) Live() bool {
	_, err := t.Path()
	return err == nil
}

// CheckToken is Check for the process the token was taken from, as it was
// then: "" checks only that it's validly signed.
func CheckToken(t Token, requirement string) (Signature, bool) {
	if !t.ok {
		return Signature{}, false
	}
	req := C.CString(requirement)
	defer C.free(unsafe.Pointer(req))
	return info(func(ident, team *C.char, runtime *C.int) C.int {
		return C.hmk_check_token(&t.t, req, ident, 256, team, 256, runtime)
	})
}
