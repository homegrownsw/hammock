package com.homegrownsw.hammock.client

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.security.KeyPairGenerator
import java.security.Signature
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/** A software Ed25519 key, for tests (the phone's lives in the Keystore). */
class SoftwareKey : DeviceKey {
    private val pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair()
    override val publicKey: ByteArray = pair.public.encoded.takeLast(32).toByteArray() // X.509 SPKI: the raw key is its last 32 bytes
    override fun sign(message: ByteArray): ByteArray =
        Signature.getInstance("Ed25519").run { initSign(pair.private); update(message); sign() }
}

/**
 * Pairs with a real daemon and signs in: run with a link from
 * android/scripts/dev-daemon.sh (HAMMOCK_PAIR=hammock://pair/… ./gradlew :client:test).
 * Skipped without one.
 */
class LiveDaemonTest {
    @Test fun pairsSignsInAndFollowsUpdates() = runBlocking {
        val link = System.getenv("HAMMOCK_PAIR")
        assumeTrue("set HAMMOCK_PAIR to a pairing link", !link.isNullOrEmpty())
        val p = assertNotNull(PairingCodes.parse(link!!))
        val key = SoftwareKey()
        var id = RemoteIdentity(
            address = "", addresses = p.addressesList, lanSubnets = p.lanSubnetsList, certSHA256 = p.certSha256,
            deviceID = null, pairingSecret = p.secret.toByteArray(), deviceName = "Test phone",
        )
        val model = AppModel.remote(id, key)
        model.onIdentityChanged = { id = it }
        model.start()
        withTimeout(15_000) { model.link.first { it == Link.Connected } }
        assertNotNull(id.deviceID, "paired")
        assertTrue(model.workspace.value.hostName.isNotEmpty())
        model.stop().join()

        // Again, signing in with the key now.
        val again = AppModel.remote(id, key)
        again.start()
        withTimeout(15_000) { again.link.first { it == Link.Connected } }
        val gid = again.createGroup("From Android")
        assertNotNull(gid)
        withTimeout(5_000) { again.workspace.first { w -> w.groups.any { it.id == gid } } }
        assertEquals("From Android", again.workspace.value.groups.first { it.id == gid }.name)
        again.stop().join()
    }
}
