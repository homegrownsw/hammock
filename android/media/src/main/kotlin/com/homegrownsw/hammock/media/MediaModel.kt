package com.homegrownsw.hammock.media

import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import org.json.JSONArray
import org.json.JSONObject

// The daemon calls the video panes make, on the app's model (as the iPhone's
// AppModel has them): simulator ops, browser input and navigation, screen
// input and the screens' pictures. Args go as JSON, as the daemon's ops take them.

internal fun argsJSON(args: Map<String, Any?>): String {
    val o = JSONObject()
    for ((k, v) in args) {
        when (v) {
            null -> {}
            is Collection<*> -> o.put(k, JSONArray(v))
            is Float -> o.put(k, v.toDouble())
            else -> o.put(k, v)
        }
    }
    return o.toString()
}

/** A simulator op's reply: its JSON, and the picture for a screenshot. */
class SimulatorReply(val json: JSONObject, val data: ByteArray)

/**
 * Drives a simulator pane (the daemon's ops: screenshot, tap, swipe, pinch,
 * text, button, rotate, fold…; coordinates in the device's points), waiting
 * for its reply. Null if it failed (model.lastError says why).
 */
suspend fun AppModel.simulatorCall(paneID: String, op: String, args: Map<String, Any?> = emptyMap()): SimulatorReply? {
    val r = request { setSimulatorCall(Hammock.SimulatorCall.newBuilder().setPaneId(paneID).setOp(op).setArgsJson(argsJSON(args))) } ?: return null
    val json = try { JSONObject(r.simulatorResult.json.ifEmpty { "{}" }) } catch (_: Exception) { JSONObject() }
    return SimulatorReply(json, r.simulatorResult.png.toByteArray())
}

/** Sends a simulator op without waiting (presses, the remote), in the order given. */
fun AppModel.simulatorInput(paneID: String, op: String, args: Map<String, Any?> = emptyMap()) {
    send { setSimulatorCall(Hammock.SimulatorCall.newBuilder().setPaneId(paneID).setOp(op).setArgsJson(argsJSON(args))) }
}

/** An Apple TV ("tvOS") or an Android TV emulator: no touch screen, driven by its remote. */
val Hammock.Pane.isTV: Boolean get() = devicePlatform == "tvOS" || devicePlatform == "Android TV"
val Hammock.Pane.isAppleTV: Boolean get() = devicePlatform == "tvOS"
val Hammock.Pane.isAndroid: Boolean get() = devicePlatform == "Android" || devicePlatform == "Android TV"
/** An Android phone or tablet: Back, Home and Recents. */
val Hammock.Pane.isAndroidPhone: Boolean get() = devicePlatform == "Android"

/**
 * A TV remote's button (sim_remote's names: up, down, left, right, select,
 * menu, home, play_pause, volume_up, volume_down, mute, siri).
 */
fun AppModel.pressRemote(paneID: String, name: String) = simulatorInput(paneID, "remote", mapOf("name" to name))

/**
 * A simulator's button: home, lock, side, siri or keyboard (the software
 * keyboard); an Android phone's back, home, recents, power, volume_up or
 * volume_down (lock is its power). A TV's Home is its remote's.
 */
fun AppModel.pressSimulatorButton(pane: Hammock.Pane, name: String) {
    if (pane.isTV) {
        if (name == "home" || (name == "siri" && pane.isAppleTV)) pressRemote(pane.id, name)
        return
    }
    if (pane.isAndroidPhone) {
        val android = if (name == "lock") "power" else name
        if (android in setOf("back", "home", "recents", "power", "volume_up", "volume_down")) simulatorInput(pane.id, "button", mapOf("name" to android))
        return
    }
    simulatorInput(pane.id, "button", mapOf("name" to name))
}

/** Input to a browser pane watched as video: mouse, wheel, key or text, positions as fractions of the page. */
fun AppModel.browserInput(paneID: String, op: String, args: Map<String, Any?> = emptyMap()) {
    send { setBrowserInput(Hammock.BrowserInput.newBuilder().setPaneId(paneID).setOp(op).setArgsJson(argsJSON(args))) }
}

/** Drives a browser pane: "load" (with url), "back", "forward", "reload" or "stop". */
fun AppModel.navigate(paneID: String, action: String, url: String = "") {
    send { setBrowserNavigate(Hammock.BrowserNavigate.newBuilder().setPaneId(paneID).setAction(action).setUrl(url)) }
}

/** Answers a page's menu: the item picked, or -1 for none. */
fun AppModel.answerBrowserMenu(menu: Hammock.BrowserMenu, index: Int) {
    send { setBrowserMenuChoice(Hammock.BrowserMenuChoice.newBuilder().setPaneId(menu.paneId).setSeq(menu.seq).setKind(menu.kind).setIndex(index)) }
}

/** The computer id the daemon wants for a screen: empty for the one connected to. */
internal fun AppModel.screenComputer(computerID: String): String = if (computerID == workspace.value.myUserID) "" else computerID

/** Your input to a display you watch (ScreenInput's ops: mouse, wheel, key, text). */
fun AppModel.screenInput(computerID: String, display: Int, op: String, args: Map<String, Any?>) {
    val cid = screenComputer(computerID)
    send { setScreenInput(Hammock.ScreenInput.newBuilder().setComputerId(cid).setDisplayId(display).setOp(op).setArgsJson(argsJSON(args))) }
}

/** A computer's displays, and whether its screen can be shared now. Null if it didn't answer. */
suspend fun AppModel.listDisplays(computerID: String): Hammock.Displays? {
    val cid = screenComputer(computerID)
    return request { setListDisplays(Hammock.ListDisplays.newBuilder().setComputerId(cid)) }?.displays
}

/** A display as a small picture: what's on it, or what it last showed if it's asleep. */
suspend fun AppModel.screenSnapshot(computerID: String, display: Int = 0, maxWidth: Int = 960): Hammock.ScreenSnapshotReply? {
    val cid = screenComputer(computerID)
    return request {
        setScreenSnapshot(Hammock.ScreenSnapshot.newBuilder().setComputerId(cid).setDisplayId(display).setMaxWidth(maxWidth.coerceAtMost(2048)))
    }?.screenSnapshotReply
}
