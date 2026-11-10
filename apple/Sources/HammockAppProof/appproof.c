#include "HammockAppProof.h"

#include <TargetConditionals.h>

#if TARGET_OS_OSX
// (Outside the package: shared with the daemon, so both ends agree.)
#include "../../../helpers/common/appproof.h"

int hmk_app_proof_send(const char *service, uint64_t conn, const uint8_t *nonce, size_t nonce_len, unsigned ms,
                       char *answer, size_t cap) {
	return (int)hmk_app_prove(service, conn, nonce, nonce_len, (mach_msg_timeout_t)ms, answer, cap);
}
#else
int hmk_app_proof_send(const char *service, uint64_t conn, const uint8_t *nonce, size_t nonce_len, unsigned ms,
                       char *answer, size_t cap) {
	(void)service; (void)conn; (void)nonce; (void)nonce_len; (void)ms;
	if (cap) answer[0] = 0;
	return -1; // only the Mac app proves itself
}
#endif
