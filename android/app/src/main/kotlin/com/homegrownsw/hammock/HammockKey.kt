package com.homegrownsw.hammock

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_STRONG
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import hammock.v1.Hammock.HammockKeyRequest
import kotlinx.coroutines.suspendCancellableCoroutine
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * The Hammock Key: a computer's own SSH key, which signs only once you
 * approve. This phone approves with a P-256 key of its own in the Keystore
 * (StrongBox when the phone has one) that signs only after your fingerprint,
 * face or screen lock, each time. The computer knows its public key
 * (SetApprovalKey) and checks the signature (helpers/key).
 */
object ApprovalKey {
    private const val ALIAS = "hammock-approval-key"
    private const val STORE = "AndroidKeyStore"

    private fun store() = KeyStore.getInstance(STORE).apply { load(null) }

    private fun create() {
        fun spec(strongBox: Boolean) = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setDigests(KeyProperties.DIGEST_SHA256)
            .setUserAuthenticationRequired(true)
            .setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG or KeyProperties.AUTH_DEVICE_CREDENTIAL)
            .setIsStrongBoxBacked(strongBox)
            .build()
        val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, STORE)
        try {
            gen.initialize(spec(true))
            gen.generateKeyPair()
        } catch (_: StrongBoxUnavailableException) {
            gen.initialize(spec(false))
            gen.generateKeyPair()
        }
    }

    /** The public key as x9.63 (0x04 ‖ X ‖ Y), made first if there's none; null if the phone can't (no screen lock). */
    fun publicKey(): ByteArray? = try {
        if (!store().containsAlias(ALIAS)) create()
        val pub = store().getCertificate(ALIAS).publicKey as ECPublicKey
        fun coord(v: java.math.BigInteger) = v.toByteArray().let { b ->
            when {
                b.size == 32 -> b
                b.size > 32 -> b.copyOfRange(b.size - 32, b.size)
                else -> ByteArray(32 - b.size) + b
            }
        }
        byteArrayOf(4) + coord(pub.w.affineX) + coord(pub.w.affineY)
    } catch (_: Exception) {
        null
    }

    /** What the phone signs to approve a request (helpers/key's approvalMessage; hammockkey_test.go has a test vector). */
    fun message(r: HammockKeyRequest, window: Boolean, expires: Long): ByteArray =
        "hmk-key-approval-v2\n${r.keyId}\n${r.id}\n${r.dataHash}\n${if (window) "window" else "once"}\n${r.paneId}\n$expires".toByteArray()

    /** How long an approval lasts: it signs the request as it is now. */
    fun expiry(): Long = System.currentTimeMillis() + 120_000

    /** Signs message (ECDSA, DER) after asking for your fingerprint, face or screen lock. */
    suspend fun sign(activity: FragmentActivity, message: ByteArray, reason: String): ByteArray {
        if (publicKey() == null) throw IllegalStateException("Set a screen lock on this phone to approve with it.")
        val sig = Signature.getInstance("SHA256withECDSA")
        sig.initSign(store().getKey(ALIAS, null) as PrivateKey)
        val unlocked = suspendCancellableCoroutine { cont ->
            val prompt = BiometricPrompt(activity, ContextCompat.getMainExecutor(activity), object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    if (cont.isActive) cont.resume(result.cryptoObject?.signature ?: sig)
                }
                override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                    if (cont.isActive) cont.resumeWithException(IllegalStateException(errString.toString()))
                }
            })
            prompt.authenticate(
                BiometricPrompt.PromptInfo.Builder().setTitle("Approve").setSubtitle(reason)
                    .setAllowedAuthenticators(BIOMETRIC_STRONG or DEVICE_CREDENTIAL).build(),
                BiometricPrompt.CryptoObject(sig),
            )
        }
        unlocked.update(message)
        return unlocked.sign()
    }
}
