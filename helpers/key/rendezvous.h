// How the daemon hands the Hammock Key helper its request: the rendezvous
// in helpers/common/rendezvous.h (which says why a Mach message, not the
// helper's parent or a socket's peer), with the Key's own hello. The
// helper's bridging header (build.sh); the daemon's end is
// internal/daemon/keyhelper_darwin.go.
//
// The daemon runs the helper with --daemon <name> and answers only the
// helper it just started (the hello's audit-trailer pid: its own child)
// with the request. The answer goes back on the helper's stdout and exit
// code, as before: a pipe the daemon made for the child it started, which
// nothing else has.
#pragma once

#include "../common/rendezvous.h"

#define HMK_KEY_HELLO "hmk-key-hello-v1" // 16 bytes, no NUL: the wire format's version

// hmk_key_fetch asks the daemon's service for the request (hmk_rv_fetch).
static inline kern_return_t hmk_key_fetch(const char *name, mach_msg_timeout_t ms, void **data, size_t *len,
                                          audit_token_t *token) {
	return hmk_rv_fetch(name, HMK_KEY_HELLO, ms, data, len, token);
}
