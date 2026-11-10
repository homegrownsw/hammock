// How the daemon hands the Hammock Key helper its request: over Mach, so
// the helper knows the request was sent by Hammock's daemon itself.
//
// Shared by both ends, so a daemon and a helper from one build always
// agree: the helper (main.swift, as its bridging header) and the daemon
// (internal/daemon/keyhelper_darwin.go, through cgo).
//
//   1. The daemon checks in a service under a fresh random name
//      (bootstrap_check_in: launchd refuses a second check-in while it's
//      held, and forgets the name once the daemon lets the port go), and
//      runs the helper with --daemon <name>.
//   2. The helper looks the name up and sends a hello, with a reply right
//      to a port of its own.
//   3. The daemon answers only a hello sent by the helper it just started
//      (the sender's pid, from the message's audit trailer), with the
//      request: JSON, out of line.
//   4. The helper takes the request only if its sender, by the audit token
//      the kernel put in the message as it was sent, is a running hmkd
//      signed by the helper's own team with the hardened runtime.
//
// Why a message and not the helper's parent, or a socket's peer: both are
// a pid, and a process can make itself hmkd after the fact (exec a
// suspended hmkd in its own place, POSIX_SPAWN_SETEXEC, which never runs).
// macOS then reports the real hmkd for that pid, and a socket's peer token
// is worked out from the pid when asked. A message's audit token is the
// sender's when it was sent, pid version and all: once that process has
// exec'd, the token names nothing (SecCodeCopyGuestWithAttributes fails),
// so a request can only have come from code that was hmkd when it sent it.
//
// The answer goes back on the helper's stdout and exit code, as before:
// a pipe the daemon made for the child it started, which nothing else has.
#pragma once

#include <bootstrap.h>
#include <mach/mach.h>
#include <string.h>
#include <unistd.h>

#define HMK_KEY_HELLO_ID 0x686d6b01   // "hmk" 1
#define HMK_KEY_REQUEST_ID 0x686d6b02 // "hmk" 2
#define HMK_KEY_HELLO "hmk-key-hello-v1" // 16 bytes, no NUL: the wire format's version
#define HMK_KEY_MAX_REQUEST (16 << 20)

#pragma pack(push, 4)
typedef struct {
	mach_msg_header_t header;
	char magic[16];
} hmk_key_hello_t;

typedef struct {
	mach_msg_header_t header;
	mach_msg_body_t body;
	mach_msg_ool_descriptor_t data;
} hmk_key_request_t;
#pragma pack(pop)

// The audit trailer after a received message.
static inline mach_msg_audit_trailer_t *hmk_key_trailer(mach_msg_header_t *h) {
	return (mach_msg_audit_trailer_t *)((uint8_t *)h + round_msg(h->msgh_size));
}

#define HMK_KEY_RCV_OPTIONS \
	(MACH_RCV_MSG | MACH_RCV_TIMEOUT | MACH_RCV_TRAILER_TYPE(MACH_MSG_TRAILER_FORMAT_0) | \
	 MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_AUDIT))

// ---------- the daemon's end ----------

// hmk_key_checkin makes the service the helper will look up.
static inline kern_return_t hmk_key_checkin(const char *name, mach_port_t *port) {
	return bootstrap_check_in(bootstrap_port, name, port);
}

// hmk_key_close lets the service go (launchd forgets its name).
static inline void hmk_key_close(mach_port_t port) {
	mach_port_mod_refs(mach_task_self(), port, MACH_PORT_RIGHT_RECEIVE, -1);
}

// hmk_key_hello waits up to ms for a hello. KERN_SUCCESS with its sender's
// pid in *pid and its reply right in *reply (the caller sends on it, or
// hands it to hmk_key_drop); anything else that arrives is dropped
// (KERN_INVALID_ARGUMENT), and a timeout is MACH_RCV_TIMED_OUT.
static inline kern_return_t hmk_key_hello(mach_port_t port, mach_msg_timeout_t ms, pid_t *pid, mach_port_t *reply) {
	struct {
		hmk_key_hello_t m;
		mach_msg_max_trailer_t trailer;
	} r;
	memset(&r, 0, sizeof r);
	kern_return_t kr = mach_msg(&r.m.header, HMK_KEY_RCV_OPTIONS, 0, sizeof r, port, ms, MACH_PORT_NULL);
	if (kr != KERN_SUCCESS) return kr;
	if ((r.m.header.msgh_bits & MACH_MSGH_BITS_COMPLEX) || r.m.header.msgh_id != HMK_KEY_HELLO_ID ||
	    r.m.header.msgh_size != sizeof r.m || memcmp(r.m.magic, HMK_KEY_HELLO, 16) != 0 ||
	    MACH_MSGH_BITS_REMOTE(r.m.header.msgh_bits) != MACH_MSG_TYPE_PORT_SEND_ONCE) {
		mach_msg_destroy(&r.m.header);
		return KERN_INVALID_ARGUMENT;
	}
	*pid = (pid_t)hmk_key_trailer(&r.m.header)->msgh_audit.val[5];
	*reply = r.m.header.msgh_remote_port;
	return KERN_SUCCESS;
}

// hmk_key_drop lets a hello's reply right go unanswered.
static inline void hmk_key_drop(mach_port_t reply) {
	mach_port_deallocate(mach_task_self(), reply);
}

// hmk_key_send answers a hello with the request (n bytes at data).
static inline kern_return_t hmk_key_send(mach_port_t reply, const void *data, size_t n) {
	hmk_key_request_t m;
	memset(&m, 0, sizeof m);
	m.header.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_MOVE_SEND_ONCE, 0) | MACH_MSGH_BITS_COMPLEX;
	m.header.msgh_size = sizeof m;
	m.header.msgh_remote_port = reply;
	m.header.msgh_id = HMK_KEY_REQUEST_ID;
	m.body.msgh_descriptor_count = 1;
	m.data.address = (void *)data;
	m.data.size = (mach_msg_size_t)n;
	m.data.deallocate = 0;
	m.data.copy = MACH_MSG_VIRTUAL_COPY;
	m.data.type = MACH_MSG_OOL_DESCRIPTOR;
	kern_return_t kr = mach_msg(&m.header, MACH_SEND_MSG | MACH_SEND_TIMEOUT, sizeof m, 0, MACH_PORT_NULL, 5000, MACH_PORT_NULL);
	if (kr == MACH_SEND_TIMED_OUT || kr == MACH_SEND_INTERRUPTED) mach_port_deallocate(mach_task_self(), reply);
	return kr;
}

// ---------- the helper's end ----------

// hmk_key_fetch asks the daemon's service for the request, waiting up to
// ms for each step. KERN_SUCCESS with the request in *data and *len
// (vm_deallocate it) and its sender's audit token, as it was sent, in
// *token: the caller checks who that is before believing a byte of it.
static inline kern_return_t hmk_key_fetch(const char *name, mach_msg_timeout_t ms, void **data, size_t *len,
                                          audit_token_t *token) {
	mach_port_t daemon = MACH_PORT_NULL, rx = MACH_PORT_NULL;
	kern_return_t kr = bootstrap_look_up(bootstrap_port, name, &daemon);
	if (kr != KERN_SUCCESS) return kr;
	kr = mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &rx);
	if (kr != KERN_SUCCESS) {
		mach_port_deallocate(mach_task_self(), daemon);
		return kr;
	}
	hmk_key_hello_t h;
	memset(&h, 0, sizeof h);
	h.header.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND, MACH_MSG_TYPE_MAKE_SEND_ONCE);
	h.header.msgh_size = sizeof h;
	h.header.msgh_remote_port = daemon;
	h.header.msgh_local_port = rx;
	h.header.msgh_id = HMK_KEY_HELLO_ID;
	memcpy(h.magic, HMK_KEY_HELLO, 16);
	kr = mach_msg(&h.header, MACH_SEND_MSG | MACH_SEND_TIMEOUT, sizeof h, 0, MACH_PORT_NULL, ms, MACH_PORT_NULL);
	mach_port_deallocate(mach_task_self(), daemon);
	if (kr == KERN_SUCCESS) {
		struct {
			hmk_key_request_t m;
			mach_msg_max_trailer_t trailer;
		} r;
		memset(&r, 0, sizeof r);
		kr = mach_msg(&r.m.header, HMK_KEY_RCV_OPTIONS, 0, sizeof r, rx, ms, MACH_PORT_NULL);
		if (kr == KERN_SUCCESS) {
			if (!(r.m.header.msgh_bits & MACH_MSGH_BITS_COMPLEX) || r.m.header.msgh_id != HMK_KEY_REQUEST_ID ||
			    r.m.header.msgh_size != sizeof r.m || r.m.body.msgh_descriptor_count != 1 ||
			    r.m.data.type != MACH_MSG_OOL_DESCRIPTOR || r.m.data.size > HMK_KEY_MAX_REQUEST) {
				mach_msg_destroy(&r.m.header);
				kr = KERN_INVALID_ARGUMENT;
			} else {
				*token = hmk_key_trailer(&r.m.header)->msgh_audit;
				*data = r.m.data.address;
				*len = r.m.data.size;
			}
		}
	}
	mach_port_mod_refs(mach_task_self(), rx, MACH_PORT_RIGHT_RECEIVE, -1);
	return kr;
}
