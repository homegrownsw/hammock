// How the Hammock app proves to hmkd that a connection to its socket is
// the app's: a Mach message, on rendezvous.h, whose audit token the kernel
// fixes as it's sent. A unix socket's peer can't prove it: macOS works its
// audit token out when asked, from the pid that last used the other end as
// it is then, so a process that connects, keeps a forked child with the
// socket and execs a suspended copy of the signed app in its own place
// passes for the app (rendezvous.h says more).
//
// Shared by both ends, so an app and a daemon of one build agree: the app
// (apple/Sources/HammockAppProof, called from DaemonConnection) and the
// daemon (internal/daemon/appproof_darwin.go, through cgo, whose tests send
// proofs with it too).
//
//   1. On a new connection to hmkd's socket, before anything else, the app
//      sends ProveApp. The daemon answers AppChallenge: the name of a Mach
//      service it holds (checked in under a random name for its life), the
//      connection's number, and a fresh 32-byte nonce for it alone.
//   2. The app sends that number and nonce to the service (hmk_app_prove).
//   3. The daemon takes the proof once, while it's fresh, and only if the
//      message's audit token (the sender's as it sent it, pid version and
//      all) is a live Hammock app, signed as person_darwin.go's
//      isHammockApp says, and the very process at the other end of that
//      connection (the token the socket's listener took, and the socket's
//      peer now). It then answers on the reply right: "ok", or why not.
//      Meanwhile it reads nothing more from that connection, so nothing the
//      app sends after the proof can be handled before it. (The app needn't
//      believe the answer: it's only for its log. What the connection may
//      do is the daemon's to say.)
#pragma once

#include <TargetConditionals.h>

#if TARGET_OS_OSX

#include <stdint.h>
#include "rendezvous.h"

#define HMK_APP_PROOF_HELLO "hmk-app-proof-v1" // 16 bytes, no NUL: the wire format's version
#define HMK_APP_PROOF_NONCE 32

// A proof's body: the connection's number (big-endian), and its nonce.
typedef struct {
	uint8_t conn[8];
	uint8_t nonce[HMK_APP_PROOF_NONCE];
} hmk_app_proof_body_t;

// hmk_app_prove sends the proof for connection conn to the daemon's
// service and waits up to ms for each step. KERN_SUCCESS with the daemon's
// answer ("ok", or why not; at most cap-1 bytes, NUL-terminated) in answer.
static inline kern_return_t hmk_app_prove(const char *service, uint64_t conn, const uint8_t *nonce, size_t nonce_len,
                                          mach_msg_timeout_t ms, char *answer, size_t cap) {
	if (nonce_len != HMK_APP_PROOF_NONCE || cap == 0) return KERN_INVALID_ARGUMENT;
	hmk_app_proof_body_t b;
	for (int i = 0; i < 8; i++) b.conn[i] = (uint8_t)(conn >> (56 - 8 * i));
	memcpy(b.nonce, nonce, HMK_APP_PROOF_NONCE);
	void *data = NULL;
	size_t len = 0;
	audit_token_t token;
	answer[0] = 0;
	kern_return_t kr = hmk_rv_ask(service, HMK_APP_PROOF_HELLO, &b, sizeof b, ms, &data, &len, &token);
	if (kr != KERN_SUCCESS) return kr;
	size_t n = len < cap - 1 ? len : cap - 1;
	memcpy(answer, data, n);
	answer[n] = 0;
	vm_deallocate(mach_task_self(), (vm_address_t)data, len);
	return KERN_SUCCESS;
}

#endif
