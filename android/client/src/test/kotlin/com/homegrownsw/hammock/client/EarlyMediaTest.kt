package com.homegrownsw.hammock.client

import hammock.v1.Hammock
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The daemon sends a stream's first frames straight after its ticket, before
 * the stream has set its handler: they're held and handed over in order, not
 * dropped (a still page's only keyframe was lost, and the video never began).
 */
class EarlyMediaTest {
    private fun frame(seq: Int) = ServerMessage.newBuilder()
        .setMediaFrame(Hammock.MediaFrame.newBuilder().setSessionId("ms_1").setSeq(seq).setKeyframe(seq == 1)).build()

    @Test fun framesBeforeTheHandlerAreKept(): Unit = runBlocking {
        val daemon = object : DaemonStandIn {
            override fun receive(msg: ClientMessage, reply: (ServerMessage) -> Unit) {
                if (msg.msgCase != ClientMessage.MsgCase.OPEN_MEDIA) return
                reply(ServerMessage.newBuilder().setReplyTo(msg.id)
                    .setMediaTicket(Hammock.MediaTicket.newBuilder().setSessionId("ms_1")).build())
                for (s in 1..3) reply(frame(s))
            }
        }
        val c = DaemonConnection.standIn(daemon)
        c.connect()
        val ticket = c.call { setOpenMedia(Hammock.OpenMedia.newBuilder().setPaneId("p").setInBand(true)) }.mediaTicket
        Thread.sleep(200) // the frames are in before the handler
        val got = CopyOnWriteArrayList<Int>()
        val all = CountDownLatch(3)
        c.setMediaHandler(ticket.sessionId) { m -> got.add(m.mediaFrame.seq); all.countDown() }
        assertTrue(all.await(2, TimeUnit.SECONDS), "frames held for the handler: $got")
        assertEquals(listOf(1, 2, 3), got.toList())
        c.disconnect()
    }
}
