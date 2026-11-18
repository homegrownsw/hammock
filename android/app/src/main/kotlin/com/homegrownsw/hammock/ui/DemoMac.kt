package com.homegrownsw.hammock.ui

import com.homegrownsw.hammock.Session

/** The demo: a made-up Mac standing in for a daemon (the iPhone's DemoMac.swift). */
object DemoMac {
    fun start(session: Session) = session.startDemo(com.homegrownsw.hammock.demo.DemoDaemon())
}
