package com.homegrownsw.hammock.client

import kotlinx.coroutines.runBlocking
import org.junit.Test
import kotlin.test.assertFailsWith

/**
 * A connection told to go, twice, then used: nothing throws but the calls,
 * which say it isn't connected (they used to crash the app from OkHttp's
 * threads once its executor had shut down).
 */
class ConnectionLifecycleTest {
    @Test fun disconnectTwiceThenUse(): Unit = runBlocking {
        val c = DaemonConnection.remote(listOf("127.0.0.1:9"), emptyList(), null, "00".repeat(32)) { emptyList() }
        c.connect()
        c.disconnect()
        Thread.sleep(1200) // past the executor's shutdown
        c.disconnect()
        c.send { setFocus(hammock.v1.Hammock.Focus.newBuilder().setPaneId("p").build()) }
        c.post { }
        c.setMediaHandler("s", null)
        assertFailsWith<DaemonException> { c.call { setSubscribe(hammock.v1.Hammock.Subscribe.getDefaultInstance()) } }
    }
}
