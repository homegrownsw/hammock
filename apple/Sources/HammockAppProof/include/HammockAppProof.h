// The Mac app's end of proving to hmkd that a connection is its own
// (helpers/common/appproof.h, which the daemon builds from too): a C shim,
// so Swift (DaemonConnection) can call that header's inline functions.
#pragma once

#include <stddef.h>
#include <stdint.h>

// hmk_app_proof_send sends the proof for connection conn, with its nonce,
// to the daemon's Mach service, waiting up to ms for each step. 0 with the
// daemon's answer ("ok", or why not) in answer, else a Mach error.
int hmk_app_proof_send(const char *service, uint64_t conn, const uint8_t *nonce, size_t nonce_len, unsigned ms,
                       char *answer, size_t cap);
