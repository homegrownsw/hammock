// How the daemon hands a helper it starts something only it may have (the
// Hammock Key helper its request, the Screen helper its secret): over
// Mach, so each end knows who the other is by the kernel's word for who
// sent a message, not by a pid.
//
// Shared by both ends, so a daemon and a helper from one build always
// agree: the helpers (helpers/key/main.swift through helpers/key/rendezvous.h,
// helpers/screen/host.m) and the daemon (internal/rendezvous, through cgo).
// Each helper says hello with its own magic (16 bytes, no NUL), which is
// also its wire format's version, and may add a few bytes of its own
// (hmk_rv_ask, hmk_rv_hear). The Mac app proves to the daemon that a socket
// connection is its own the same way (appproof.h), with the daemon checking
// the hello's sender, and so does the daemon to a pane holder
// (internal/holder/proof.go, through internal/rendezvous at both ends).
//
//   1. The daemon checks in a service under a fresh random name
//      (bootstrap_check_in: launchd refuses a second check-in while it's
//      held, and forgets the name once the daemon lets the port go), and
//      starts the helper with that name in its arguments.
//   2. The helper looks the name up and sends a hello, with a reply right
//      to a port of its own.
//   3. The daemon answers only a hello from the helper it means (by the
//      sender's audit token, from the message's audit trailer: the Key
//      helper's pid, its own child; the Screen helper's signature and
//      program, as Launch Services starts it), with what that helper
//      needs: JSON, out of line.
//   4. The helper believes the answer only if its sender, by the audit
//      token the kernel put in the message as it was sent, is a running
//      hmkd signed by the helper's own team with the hardened runtime.
//
// Why a message and not a pid (the helper's parent, or a socket's peer): a
// process can make itself hmkd after the fact, by exec'ing a suspended hmkd
// in its own place (POSIX_SPAWN_SETEXEC|POSIX_SPAWN_START_SUSPENDED, which
// never runs). macOS then reports the real hmkd for that pid, and a unix
// socket's peer token (LOCAL_PEERTOKEN) is worked out when asked, from
// the pid that last used the socket, so it reports that hmkd too while a
// forked child of the impostor holds the socket and goes on talking. A
// message's audit token is the sender's when it was sent, pid version and
// all: once that process has exec'd, the token names nothing
// (SecCodeCopyGuestWithAttributes and proc_pidpath_audittoken fail), so an
// answer can only have come from code that was hmkd when it sent it, and a
// hello only from code that was the helper.
#pragma once

#include <bootstrap.h>
#include <mach/mach.h>
#include <string.h>
#include <unistd.h>

#define HMK_RV_HELLO_ID 0x686d6b01   // "hmk" 1
#define HMK_RV_ANSWER_ID 0x686d6b02  // "hmk" 2
#define HMK_RV_MAX_ANSWER (16 << 20)
#define HMK_RV_MAX_BODY 64 // a hello's own bytes after its magic, a multiple of 4

#pragma pack(push, 4)
typedef struct {
	mach_msg_header_t header;
	char magic[16];
	uint8_t body[HMK_RV_MAX_BODY]; // (sent only as long as its body is: none for a plain hello)
} hmk_rv_hello_t;

typedef struct {
	mach_msg_header_t header;
	mach_msg_body_t body;
	mach_msg_ool_descriptor_t data;
} hmk_rv_answer_t;
#pragma pack(pop)

// The audit trailer after a received message.
static inline mach_msg_audit_trailer_t *hmk_rv_trailer(mach_msg_header_t *h) {
	return (mach_msg_audit_trailer_t *)((uint8_t *)h + round_msg(h->msgh_size));
}

#define HMK_RV_RCV_OPTIONS \
	(MACH_RCV_MSG | MACH_RCV_TIMEOUT | MACH_RCV_TRAILER_TYPE(MACH_MSG_TRAILER_FORMAT_0) | \
	 MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_AUDIT))

// ---------- the daemon's end ----------

// hmk_rv_checkin makes the service the helper will look up.
static inline kern_return_t hmk_rv_checkin(const char *name, mach_port_t *port) {
	return bootstrap_check_in(bootstrap_port, name, port);
}

// hmk_rv_close lets the service go (launchd forgets its name).
static inline void hmk_rv_close(mach_port_t port) {
	mach_port_mod_refs(mach_task_self(), port, MACH_PORT_RIGHT_RECEIVE, -1);
}

// hmk_rv_hear waits up to ms for a hello with magic and exactly n bytes of
// body (0, or a multiple of 4 up to HMK_RV_MAX_BODY), copied to body.
// KERN_SUCCESS with its sender's audit token, as it was sent, in *token and
// its reply right in *reply (the caller sends on it, or hands it to
// hmk_rv_drop); anything else that arrives is dropped
// (KERN_INVALID_ARGUMENT), and a timeout is MACH_RCV_TIMED_OUT.
static inline kern_return_t hmk_rv_hear(mach_port_t port, mach_msg_timeout_t ms, const char *magic, void *body, size_t n,
                                        audit_token_t *token, mach_port_t *reply) {
	struct {
		hmk_rv_hello_t m;
		mach_msg_max_trailer_t trailer;
	} r;
	memset(&r, 0, sizeof r);
	if (n > HMK_RV_MAX_BODY || n % 4) return KERN_INVALID_VALUE;
	kern_return_t kr = mach_msg(&r.m.header, HMK_RV_RCV_OPTIONS, 0, sizeof r, port, ms, MACH_PORT_NULL);
	if (kr != KERN_SUCCESS) return kr;
	if ((r.m.header.msgh_bits & MACH_MSGH_BITS_COMPLEX) || r.m.header.msgh_id != HMK_RV_HELLO_ID ||
	    r.m.header.msgh_size != sizeof r.m.header + sizeof r.m.magic + n || memcmp(r.m.magic, magic, 16) != 0 ||
	    MACH_MSGH_BITS_REMOTE(r.m.header.msgh_bits) != MACH_MSG_TYPE_PORT_SEND_ONCE) {
		mach_msg_destroy(&r.m.header);
		return KERN_INVALID_ARGUMENT;
	}
	if (n) memcpy(body, r.m.body, n);
	*token = hmk_rv_trailer(&r.m.header)->msgh_audit;
	*reply = r.m.header.msgh_remote_port;
	return KERN_SUCCESS;
}

// hmk_rv_hello is hmk_rv_hear for a hello that's only its magic.
static inline kern_return_t hmk_rv_hello(mach_port_t port, mach_msg_timeout_t ms, const char *magic,
                                         audit_token_t *token, mach_port_t *reply) {
	return hmk_rv_hear(port, ms, magic, NULL, 0, token, reply);
}

// hmk_rv_drop lets a hello's reply right go unanswered.
static inline void hmk_rv_drop(mach_port_t reply) {
	mach_port_deallocate(mach_task_self(), reply);
}

// hmk_rv_send answers a hello with n bytes at data.
static inline kern_return_t hmk_rv_send(mach_port_t reply, const void *data, size_t n) {
	hmk_rv_answer_t m;
	memset(&m, 0, sizeof m);
	m.header.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_MOVE_SEND_ONCE, 0) | MACH_MSGH_BITS_COMPLEX;
	m.header.msgh_size = sizeof m;
	m.header.msgh_remote_port = reply;
	m.header.msgh_id = HMK_RV_ANSWER_ID;
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

// hmk_rv_ask says hello (with magic, and n bytes of body: see hmk_rv_hear)
// to the daemon's service and waits up to ms for each step. KERN_SUCCESS
// with the answer in *data and *len (vm_deallocate it) and its sender's
// audit token, as it was sent, in *token: the caller checks who that is
// before believing a byte of it.
static inline kern_return_t hmk_rv_ask(const char *name, const char *magic, const void *body, size_t n,
                                       mach_msg_timeout_t ms, void **data, size_t *len, audit_token_t *token) {
	if (n > HMK_RV_MAX_BODY || n % 4) return KERN_INVALID_VALUE;
	mach_port_t daemon = MACH_PORT_NULL, rx = MACH_PORT_NULL;
	kern_return_t kr = bootstrap_look_up(bootstrap_port, name, &daemon);
	if (kr != KERN_SUCCESS) return kr;
	kr = mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &rx);
	if (kr != KERN_SUCCESS) {
		mach_port_deallocate(mach_task_self(), daemon);
		return kr;
	}
	hmk_rv_hello_t h;
	memset(&h, 0, sizeof h);
	h.header.msgh_bits = MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND, MACH_MSG_TYPE_MAKE_SEND_ONCE);
	h.header.msgh_size = (mach_msg_size_t)(sizeof h.header + sizeof h.magic + n);
	h.header.msgh_remote_port = daemon;
	h.header.msgh_local_port = rx;
	h.header.msgh_id = HMK_RV_HELLO_ID;
	memcpy(h.magic, magic, 16);
	if (n) memcpy(h.body, body, n);
	kr = mach_msg(&h.header, MACH_SEND_MSG | MACH_SEND_TIMEOUT, h.header.msgh_size, 0, MACH_PORT_NULL, ms, MACH_PORT_NULL);
	mach_port_deallocate(mach_task_self(), daemon);
	if (kr == KERN_SUCCESS) {
		struct {
			hmk_rv_answer_t m;
			mach_msg_max_trailer_t trailer;
		} r;
		memset(&r, 0, sizeof r);
		kr = mach_msg(&r.m.header, HMK_RV_RCV_OPTIONS, 0, sizeof r, rx, ms, MACH_PORT_NULL);
		if (kr == KERN_SUCCESS) {
			if (!(r.m.header.msgh_bits & MACH_MSGH_BITS_COMPLEX) || r.m.header.msgh_id != HMK_RV_ANSWER_ID ||
			    r.m.header.msgh_size != sizeof r.m || r.m.body.msgh_descriptor_count != 1 ||
			    r.m.data.type != MACH_MSG_OOL_DESCRIPTOR || r.m.data.size > HMK_RV_MAX_ANSWER) {
				mach_msg_destroy(&r.m.header);
				kr = KERN_INVALID_ARGUMENT;
			} else {
				*token = hmk_rv_trailer(&r.m.header)->msgh_audit;
				*data = r.m.data.address;
				*len = r.m.data.size;
			}
		}
	}
	mach_port_mod_refs(mach_task_self(), rx, MACH_PORT_RIGHT_RECEIVE, -1);
	return kr;
}

// hmk_rv_fetch is hmk_rv_ask with a hello that's only its magic.
static inline kern_return_t hmk_rv_fetch(const char *name, const char *magic, mach_msg_timeout_t ms, void **data,
                                         size_t *len, audit_token_t *token) {
	return hmk_rv_ask(name, magic, NULL, 0, ms, data, len, token);
}
