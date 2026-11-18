package com.homegrownsw.hammock.client

import com.google.protobuf.ByteString
import hammock.v1.Hammock.Pairing
import java.util.Base64

/**
 * A device's signing key for one computer: Ed25519, as the daemon expects
 * (Pair.public_key, Authenticate.signature). On the phone it lives in the
 * Android Keystore and never leaves it; tests use a software key.
 */
interface DeviceKey {
    /** The raw 32-byte Ed25519 public key. */
    val publicKey: ByteArray
    fun sign(message: ByteArray): ByteArray
}

/**
 * How this device proves who it is to one paired computer: its device id and
 * key, or (the first time) a pairing secret to register that key.
 */
data class RemoteIdentity(
    /** The address that worked last, tried first. */
    val address: String,
    /** Every address the computer gave ("host:port"), and its local networks' subnets. */
    val addresses: List<String>,
    val lanSubnets: List<String>,
    /** The computer's certificate, the hex SHA-256 of its DER: the only thing trusted. */
    val certSHA256: String,
    val deviceID: String?,
    val pairingSecret: ByteArray?,
    val deviceName: String,
    val platform: String = "android",
) {
    override fun equals(other: Any?): Boolean = other is RemoteIdentity && address == other.address &&
        addresses == other.addresses && lanSubnets == other.lanSubnets && certSHA256 == other.certSHA256 &&
        deviceID == other.deviceID && pairingSecret.contentEqualsNullable(other.pairingSecret) &&
        deviceName == other.deviceName && platform == other.platform

    override fun hashCode(): Int = certSHA256.hashCode() * 31 + (deviceID?.hashCode() ?: 0)

    private fun ByteArray?.contentEqualsNullable(o: ByteArray?) = if (this == null) o == null else o != null && contentEquals(o)
}

object DeviceAuth {
    /**
     * What a device signs to sign in: bound to its purpose and to the
     * computer's certificate, so the signature can't be replayed anywhere else
     * (internal/client/auth.go AuthMessage).
     */
    fun message(certSHA256: String, nonce: ByteArray): ByteArray {
        val out = java.io.ByteArrayOutputStream()
        out.write("hmk-device-auth-v1".toByteArray())
        out.write(0)
        out.write(certSHA256.lowercase().toByteArray())
        out.write(0)
        out.write(nonce)
        return out.toByteArray()
    }
}

object PairingCodes {
    /** Parses a `hammock://pair/…` link or a bare code. */
    fun parse(code: String): Pairing? {
        var s = code.trim()
        val i = s.indexOf("pair/")
        if (i >= 0) s = s.substring(i + 5)
        s = s.substringBefore('?').substringBefore('#').trim()
        if (s.isEmpty()) return null
        return try {
            val bytes = Base64.getUrlDecoder().decode(s.trimEnd('=').replace('+', '-').replace('/', '_'))
            Pairing.parseFrom(bytes).takeIf { it.certSha256.isNotEmpty() && !it.secret.isEmpty }
        } catch (_: Exception) {
            null
        }
    }

    /** The `hammock://pair/…` link for a pairing. */
    fun url(p: Pairing): String = "hammock://pair/" + Base64.getUrlEncoder().withoutPadding().encodeToString(p.toByteArray())

    /** The start of the computer's certificate fingerprint, to check by eye: "AB12 CD34 EF56 7890". */
    fun shortFingerprint(certSHA256: String): String =
        certSHA256.uppercase().take(16).chunked(4).joinToString(" ")

    fun bytes(b: ByteArray): ByteString = ByteString.copyFrom(b)
}

/** Lowercase hex. */
fun ByteArray.hex(): String = joinToString("") { "%02x".format(it) }
