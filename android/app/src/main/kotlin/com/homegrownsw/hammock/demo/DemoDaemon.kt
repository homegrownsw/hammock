package com.homegrownsw.hammock.demo

import com.homegrownsw.hammock.client.DaemonStandIn
import hammock.v1.Hammock
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage

/**
 * A made-up Mac for the demo: Groups, agents working, waiting and done,
 * nothing paired or sent. Anything that would change the Mac says it needs
 * one. (Filled out in full in a later commit.)
 */
class DemoDaemon : DaemonStandIn {
    override fun receive(msg: ClientMessage, reply: (ServerMessage) -> Unit) {
        val r = ServerMessage.newBuilder().setReplyTo(msg.id)
        when (msg.msgCase) {
            ClientMessage.MsgCase.HELLO -> r.setHelloReply(Hammock.HelloReply.newBuilder().setHostName("MacBook").setUserId("us_demo"))
            ClientMessage.MsgCase.SUBSCRIBE -> r.setState(Hammock.StateUpdate.newBuilder().setFull(true))
            else -> if (msg.id != 0L) r.setError(Hammock.Error.newBuilder().setMessage(NEEDS_MAC)) else return
        }
        reply(r.build())
    }

    companion object {
        const val NEEDS_MAC = "This needs your Mac: pair one to do it."
    }
}
