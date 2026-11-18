package com.homegrownsw.hammock.client

import hammock.v1.Hammock.HammockKeyRequest
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/** What a phone signs to approve a Hammock Key request (helpers/key checks the same bytes). */
object HammockKeyApproval {
    fun message(r: HammockKeyRequest, window: Boolean, expires: Long): ByteArray =
        message(r.keyId, r.id, r.dataHash, r.paneId, window, expires)

    fun message(keyID: String, id: String, dataHash: String, paneID: String, window: Boolean, expires: Long): ByteArray =
        "hmk-key-approval-v2\n$keyID\n$id\n$dataHash\n${if (window) "window" else "once"}\n$paneID\n$expires".toByteArray()

    /** How long an approval lasts: it signs the request as it is now. */
    fun expiry(now: Long = System.currentTimeMillis()): Long = now + 120_000
}

/**
 * A computer's sealed notification (internal/daemon/push.go seal): AES-256-GCM,
 * nonce (12) ‖ ciphertext ‖ tag (16).
 */
object PushCrypto {
    /** The note's plaintext, or null if this key didn't seal it. */
    fun open(key: ByteArray, sealed: ByteArray): ByteArray? {
        if (sealed.size < 12 + 16) return null
        return try {
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, sealed, 0, 12))
            c.doFinal(sealed, 12, sealed.size - 12)
        } catch (_: Exception) {
            null
        }
    }
}
