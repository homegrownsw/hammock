package com.homegrownsw.hammock

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import com.homegrownsw.hammock.client.DeviceKey
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.spec.ECGenParameterSpec

/**
 * This phone's Ed25519 key for one computer, made and kept in the Android
 * Keystore (Ed25519 there needs Android 13, the app's minimum): it never
 * leaves it, and isn't in backups.
 */
class KeystoreKey private constructor(private val alias: String) : DeviceKey {
    companion object {
        private const val STORE = "AndroidKeyStore"
        private fun keyStore() = KeyStore.getInstance(STORE).apply { load(null) }

        /** The key under `alias`, made first if there's none. */
        fun get(alias: String): KeystoreKey {
            if (!keyStore().containsAlias(alias)) {
                val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, STORE)
                gen.initialize(
                    KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
                        .setAlgorithmParameterSpec(ECGenParameterSpec("ed25519"))
                        .setDigests(KeyProperties.DIGEST_NONE)
                        .build(),
                )
                gen.generateKeyPair()
            }
            return KeystoreKey(alias)
        }

        fun delete(alias: String) = runCatching { keyStore().deleteEntry(alias) }
    }

    override val publicKey: ByteArray by lazy {
        // X.509 SubjectPublicKeyInfo for Ed25519: a 12-byte header, then the raw 32 bytes.
        keyStore().getCertificate(alias).publicKey.encoded.takeLast(32).toByteArray()
    }

    override fun sign(message: ByteArray): ByteArray {
        val key = keyStore().getKey(alias, null) as PrivateKey
        return Signature.getInstance("Ed25519").run {
            initSign(key)
            update(message)
            sign()
        }
    }
}

/** A paired computer, as this phone keeps it. Its id is its certificate's fingerprint. */
data class StoredHost(
    val hostName: String,
    val address: String,
    val addresses: List<String>,
    val lanSubnets: List<String>,
    val certSHA256: String,
    val deviceID: String?,
    /** Its key's alias in the Keystore. */
    val keyAlias: String,
    /** "mac", "linux", "server"…: for its icon. */
    val kind: String = "",
) {
    val id: String get() = certSHA256
    val shortName: String get() = hostName.removeSuffix(".local")

    fun toJSON(): JSONObject = JSONObject()
        .put("hostName", hostName).put("address", address)
        .put("addresses", JSONArray(addresses)).put("lanSubnets", JSONArray(lanSubnets))
        .put("certSHA256", certSHA256).put("deviceID", deviceID ?: JSONObject.NULL)
        .put("keyAlias", keyAlias).put("kind", kind)

    companion object {
        fun fromJSON(o: JSONObject) = StoredHost(
            hostName = o.optString("hostName"), address = o.optString("address"),
            addresses = o.optJSONArray("addresses").strings(), lanSubnets = o.optJSONArray("lanSubnets").strings(),
            certSHA256 = o.getString("certSHA256"), deviceID = if (o.isNull("deviceID")) null else o.optString("deviceID"),
            keyAlias = o.getString("keyAlias"), kind = o.optString("kind"),
        )

        private fun JSONArray?.strings(): List<String> = if (this == null) emptyList() else (0 until length()).map { getString(it) }
    }
}

/**
 * The paired computers and which is shown, in the app's no-backup files (a
 * restored phone pairs again: its keys didn't come with it).
 */
class IdentityStore(context: Context) {
    private val file = File(context.noBackupFilesDir, "hosts.json")

    data class Stored(val hosts: List<StoredHost>, val active: String?)

    fun load(): Stored = try {
        val o = JSONObject(file.readText())
        val a = o.getJSONArray("hosts")
        Stored((0 until a.length()).map { StoredHost.fromJSON(a.getJSONObject(it)) }, o.optString("active").ifEmpty { null })
    } catch (_: Exception) {
        Stored(emptyList(), null)
    }

    fun save(s: Stored) {
        val o = JSONObject().put("hosts", JSONArray(s.hosts.map { it.toJSON() })).put("active", s.active ?: "")
        val tmp = File(file.parentFile, "hosts.json.tmp")
        tmp.writeText(o.toString())
        tmp.renameTo(file)
    }
}
