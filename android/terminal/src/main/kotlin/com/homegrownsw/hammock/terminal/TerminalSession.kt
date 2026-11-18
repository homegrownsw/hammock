package com.homegrownsw.hammock.terminal

import com.homegrownsw.hammock.client.PaneSink
import java.util.concurrent.CopyOnWriteArrayList
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * A pane's client-side terminal plus the views drawing it (PaneSession in
 * AppModel.swift). The model feeds it as a [PaneSink], on its own thread:
 * a snapshot (reset at the pane's size, then the replay), then output, and
 * the pane's new size when it changes. Views observe it to redraw.
 *
 * Attach it with `model.attach(paneID, session)`; [close] it once nothing
 * shows the pane any more.
 */
class TerminalSession(val paneID: String, cols: Int = 80, rows: Int = 24) : PaneSink, AutoCloseable {
    val engine = TerminalEngine(cols, rows)

    private val _ready = MutableStateFlow(false)

    /** Whether the first snapshot has arrived (until then there's nothing to draw). */
    val ready: StateFlow<Boolean> = _ready.asStateFlow()
    val isReady get() = _ready.value

    private val observers = CopyOnWriteArrayList<() -> Unit>()

    /**
     * Calls [fn] after each change to the screen, on the thread that made
     * it (the model's): it should only ask to redraw. Returns a handle that
     * removes it.
     */
    fun observe(fn: () -> Unit): AutoCloseable {
        observers.add(fn)
        return AutoCloseable { observers.remove(fn) }
    }

    /** Tells the observers the screen changed (after writing to [engine] by hand). */
    fun notifyChanged() {
        for (fn in observers) fn()
    }

    override fun snapshot(cols: Int, rows: Int, data: ByteArray) {
        engine.replace(cols, rows, data)
        _ready.value = true
        notifyChanged()
    }

    override fun output(data: ByteArray) {
        engine.write(data)
        notifyChanged()
    }

    override fun resized(cols: Int, rows: Int) {
        engine.resize(cols, rows)
        notifyChanged()
    }

    override fun close() {
        observers.clear()
        engine.close()
    }

    companion object {
        /** A session fed by hand rather than by a daemon, ready to draw at once (previews, tests). */
        fun standalone(paneID: String, cols: Int, rows: Int) = TerminalSession(paneID, cols, rows).also { it._ready.value = true }
    }
}
