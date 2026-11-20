package com.homegrownsw.hammock.demo

import com.google.protobuf.ByteString
import com.homegrownsw.hammock.client.DaemonStandIn
import com.homegrownsw.hammock.client.canSleep
import com.homegrownsw.hammock.demo.DemoContent.block
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentBlock
import hammock.v1.Hammock.AgentPrompt
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ClientMessage.MsgCase
import hammock.v1.Hammock.Schedule
import hammock.v1.Hammock.ScheduleRun
import hammock.v1.Hammock.ServerMessage
import hammock.v1.Hammock.StateUpdate
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch

/**
 * The demo: a made-up Mac, for trying the app with none paired (and for Play
 * review). It stands in for the daemon, so the app runs as it would against a
 * Mac: Groups on it and on a linked computer, agents working, waiting and done,
 * their conversations and terminals, and scheduled tasks with runs. Allowing the
 * waiting agent lets it finish; a message to an agent gets an answer saying this
 * is a demo; what would change the Mac (a new Group, tab or agent) says to pair
 * one. Nothing leaves the phone. As the iPhone's (apple/iOS/Hammock/DemoMac.swift).
 *
 * The parameters are debug hooks (docs/debug-hooks.md, "Android"): [longChat]
 * and [newChat] change the polish agent's conversation, [macTheme] the Mac's
 * theme, [hammockKey] ("1" or "agent") adds the Hammock Key with a request
 * waiting. [pace] scales every delay (tests run it fast).
 */
class DemoDaemon(
    private val longChat: Boolean = false,
    private val newChat: Boolean = false,
    macTheme: String? = null,
    hammockKey: String? = null,
    private val pace: Double = 1.0,
) : DaemonStandIn {
    companion object {
        /** What the app hears when it asks the demo for something only a Mac can do. */
        const val NEEDS_MAC = "That needs your Mac: this is a demo. Pair your Mac to do it for real."
        const val ME = DemoContent.ME
        const val STUDIO = DemoContent.STUDIO
    }

    /** Pretending the computer is away (asleep, off this network): each connect fails. */
    @Volatile var away = false
    override val reachable get() = !away

    /**
     * Debug hook (`--es link`): "down", the computer never answers (the first load, then can't reach
     * it); "drop[:<s>]", a moment after the app has its state, the link drops and the computer stays
     * away (for <s> seconds, if given): the connection banner's states, from the real model.
     */
    fun debugLink(spec: String?, model: com.homegrownsw.hammock.client.AppModel) {
        if (spec == null) return
        if (spec == "down") { away = true; return }
        if (!spec.startsWith("drop")) return
        val back = spec.substringAfter(':', "").toDoubleOrNull()
        model.scope.launch {
            model.link.first { it == com.homegrownsw.hammock.client.Link.Connected }
            kotlinx.coroutines.delay(2_000)
            away = true
            model.dropLink()
            if (back != null) {
                kotlinx.coroutines.delay((back * 1000).toLong())
                away = false
            }
        }
    }

    /** Everything happens on `q`, in order, as on a daemon's connection. */
    private val q = Executors.newSingleThreadScheduledExecutor { r -> Thread(r, "hammock-demo").apply { isDaemon = true } }
    private val state: StateUpdate.Builder = DemoContent.state(System.currentTimeMillis(), macTheme, hammockKey).toBuilder()
    private val transcripts = DemoContent.transcripts()
    private val prompts = HashMap<String, AgentPrompt>()
    private val working = HashMap<String, String>() // pane: what it says it's doing
    private val watched = HashSet<String>()
    private val sizes = HashMap<String, Pair<Int, Int>>()
    private var typed = StringBuilder() // the demo shell's line so far
    private var reply: ((ServerMessage) -> Unit)? = null

    init {
        prompts["p_triage"] = AgentPrompt.newBuilder().setKind(AgentPrompt.Kind.KIND_PERMISSION).setBlockId("b_triage_gh")
            .setTitle("Do you want to proceed?")
            .addAllOptions(listOf("Yes", "Yes, and don’t ask again for gh issue list commands", "No, and tell Claude what to do differently"))
            .setSelected(1).setOther(3).build()
        working["p_polish"] = "Building…"
        if (longChat) { transcripts["p_polish"] = DemoContent.longTranscript().toMutableList(); working.remove("p_polish") }
        if (newChat) { transcripts["p_polish"] = mutableListOf(); working.remove("p_polish") }
    }

    override fun receive(msg: ClientMessage, reply: (ServerMessage) -> Unit) {
        q.execute { this.reply = reply; handle(msg) }
    }

    // Answering

    private fun send(m: ServerMessage) { reply?.invoke(m) }
    private fun send(build: ServerMessage.Builder.() -> Unit) = send(ServerMessage.newBuilder().apply(build).build())

    private fun answer(msg: ClientMessage, build: ServerMessage.Builder.() -> Unit = { ok = Hammock.Ok.getDefaultInstance() }) {
        if (msg.id == 0L) return
        send(ServerMessage.newBuilder().setReplyTo(msg.id).apply(build).build())
    }

    private fun fail(msg: ClientMessage, why: String) = answer(msg) { error = Hammock.Error.newBuilder().setMessage(why).build() }

    private fun later(seconds: Double, work: () -> Unit) {
        q.schedule(work, (seconds * pace * 1000).toLong(), TimeUnit.MILLISECONDS)
    }

    private fun handle(msg: ClientMessage) {
        when (msg.msgCase) {
            MsgCase.HELLO -> answer(msg) {
                helloReply = Hammock.HelloReply.newBuilder().setProtocolVersion(1).setHostName("MacBook").setUserId(ME).setDaemonVersion("demo").build()
            }
            MsgCase.SUBSCRIBE -> answer(msg) { this.state = this@DemoDaemon.state.build() }
            MsgCase.ATTACH_PANE -> {
                val id = msg.attachPane.paneId
                if (id == "p_page") {
                    // The editor pane: its buffer.
                    send { editorUpdate = Hammock.EditorUpdate.newBuilder().setPaneId(id).setVersion(1).setSnapshot(true).setText(DemoContent.pageText).build() }
                } else {
                    val (cols, rows) = sizes[id] ?: (80 to 40)
                    snapshot(id, cols, rows)
                }
            }
            MsgCase.RESIZE -> {
                // As a Mac does: the pane takes the size the phone asks for, and is drawn again at it.
                val r = msg.resize
                sizes[r.paneId] = r.cols to r.rows
                snapshot(r.paneId, r.cols, r.rows)
            }
            MsgCase.INPUT -> if (msg.input.paneId == "p_shell") shellTyped(msg.input.data) else answer(msg)
            MsgCase.WATCH_AGENT -> {
                val w = msg.watchAgent
                if (!w.watch || w.subagentId.isNotEmpty() || w.shellId.isNotEmpty()) return
                if (w.beforeBlockId.isNotEmpty()) {
                    // The long conversation (longChat) has a page of history before it.
                    val older = if (longChat && w.paneId == "p_polish") DemoContent.longTranscript(prefix = "O") else emptyList()
                    send { agentView = Hammock.AgentView.newBuilder().setPaneId(w.paneId).setOlder(true).addAllBlocks(older).build() }
                    return
                }
                watched += w.paneId
                sendView(w.paneId, replace = true)
            }
            MsgCase.AGENT_ANSWER -> { answer(msg); agentAnswered(msg.agentAnswer) }
            MsgCase.AGENT_SEND -> { answer(msg); agentSent(msg.agentSend.paneId, msg.agentSend.text) }
            MsgCase.CLEAR_ATTENTION -> {
                val id = msg.clearAttention.id
                answer(msg)
                update { addClearedAttentionIds(id) }
                removeAttention { it.id == id }
            }
            MsgCase.LIST_FOLDERS -> {
                // Start an Agent: somewhere to start, and the agents it has.
                val path = msg.listFolders.path.ifEmpty { "/Users/me" }
                answer(msg) {
                    folderList = Hammock.FolderList.newBuilder().setPath(path).setParent(parentOf(path)).setHome("/Users/me")
                        .addAllFolders(if (path == "/Users/me") listOf("Developer", "Documents", "Notes") else emptyList())
                        .addSuggestions(Hammock.FolderSuggestion.newBuilder().setPath("/Users/me/Developer/hammock").setWhy("This Group"))
                        .addAllAgents(listOf("claude", "codex")).build()
                }
            }
            MsgCase.SET_SCHEDULE_PAUSED -> {
                val p = msg.setSchedulePaused
                answer(msg)
                schedule(p.scheduleId) { it.setPaused(p.paused).setNextRunUnixMs(if (p.paused) 0 else DemoContent.nextRun(it.build())) }
            }
            MsgCase.SKIP_SCHEDULE_RUN -> {
                val k = msg.skipScheduleRun
                val s = state.schedulesList.firstOrNull { it.id == k.scheduleId }
                if (s == null || (k.skip && s.nextRunUnixMs <= 0)) return fail(msg, "It isn’t due to run.")
                answer(msg)
                schedule(k.scheduleId) { it.setSkipUntilUnixMs(if (k.skip) it.nextRunUnixMs + 1 else 0) }
            }
            MsgCase.MOVE_TAB -> { answer(msg); moveTab(msg.moveTab.tabId, msg.moveTab.groupId, msg.moveTab.index) }
            MsgCase.SET_GROUP_ICON -> {
                answer(msg)
                val g = msg.setGroupIcon
                val i = state.groupsList.indexOfFirst { it.id == g.groupId }
                if (i < 0) return
                val group = state.getGroups(i).toBuilder().setIcon(g.icon).build()
                state.setGroups(i, group)
                update { addGroups(group) }
            }
            MsgCase.FOCUS -> { answer(msg); focused(msg.focus.paneId) }
            MsgCase.SLEEP_PANE -> {
                // As the Mac would: only an agent at rest sleeps.
                val sp = msg.sleepPane
                val i = state.panesList.indexOfFirst { it.id == sp.paneId }
                if (i < 0 || !(sp.wake || state.getPanes(i).canSleep)) return fail(msg, "its agent can't sleep now: it's working")
                answer(msg)
                val p = state.getPanes(i).toBuilder().setAsleep(!sp.wake).build()
                state.setPanes(i, p)
                update { addPanes(p) }
            }
            MsgCase.CREATE_GROUP, MsgCase.CREATE_TAB, MsgCase.CREATE_PANE, MsgCase.START_AGENT, MsgCase.CLOSE_TAB, MsgCase.CLOSE_GROUP,
            MsgCase.CLOSE_PANE, MsgCase.RESUME_SESSION, MsgCase.RUN_SCHEDULE, MsgCase.SAVE_SCHEDULE, MsgCase.DELETE_SCHEDULE,
            MsgCase.UPLOAD_FILE, MsgCase.CREATE_PAIRING, MsgCase.CREATE_INVITE, MsgCase.LINK_COMPUTER, MsgCase.REOPEN_CLOSED,
            MsgCase.RENAME_GROUP, MsgCase.RENAME_TAB, MsgCase.SWITCH_AGENT, MsgCase.SET_GROUP_ICON_IMAGE, MsgCase.SET_GROUP_FOLDER,
            MsgCase.LEAVE_SHARED,
            -> fail(msg, NEEDS_MAC)
            // Everything else (sizes, presence, lists) is fine as it is.
            else -> answer(msg)
        }
    }

    private fun parentOf(path: String): String {
        val trimmed = path.trimEnd('/')
        val i = trimmed.lastIndexOf('/')
        return if (i <= 0) "/" else trimmed.substring(0, i)
    }

    // Terminals

    private fun snapshot(paneID: String, cols: Int, rows: Int) {
        val text = if (paneID == "p_shell") DemoContent.terminal(paneID) + typed else DemoContent.terminal(paneID)
        send { paneSnapshot = Hammock.PaneSnapshot.newBuilder().setPaneId(paneID).setCols(cols).setRows(rows).setData(ByteString.copyFromUtf8(text)).build() }
    }

    // State

    private fun update(build: StateUpdate.Builder.() -> Unit) {
        send { this.state = StateUpdate.newBuilder().apply(build).build() }
    }

    private fun removeAttention(where: (Hammock.AttentionItem) -> Boolean) {
        val kept = state.attentionList.filterNot(where)
        state.clearAttention().addAllAttention(kept)
    }

    private fun setAgent(paneID: String, s: AgentState, summary: String) {
        val i = state.panesList.indexOfFirst { it.id == paneID }
        if (i < 0) return
        val pane = state.getPanes(i)
        val p = pane.toBuilder().setAgent(pane.agent.toBuilder().setState(s).setSummary(summary)
            .setStateSinceUnixMs(System.currentTimeMillis()).setSeen(false)).build()
        state.setPanes(i, p)
        update { addPanes(p) }
    }

    private fun setRun(runID: String, status: ScheduleRun.Status, note: String) {
        val si = state.schedulesList.indexOfFirst { s -> s.runsList.any { it.id == runID } }
        if (si < 0) return
        val sched = state.getSchedules(si)
        val ri = sched.runsList.indexOfFirst { it.id == runID }
        val run = sched.getRuns(ri).toBuilder().setStatus(status).setNote(note).apply {
            if (status == ScheduleRun.Status.STATUS_READY) endedUnixMs = System.currentTimeMillis()
        }.build()
        val s = sched.toBuilder().setRuns(ri, run).build()
        state.setSchedules(si, s)
        update { addSchedules(s) }
    }

    private fun schedule(id: String, change: (Schedule.Builder) -> Unit) {
        val i = state.schedulesList.indexOfFirst { it.id == id }
        if (i < 0) return
        val b = state.getSchedules(i).toBuilder()
        change(b)
        val s = b.build()
        state.setSchedules(i, s)
        update { addSchedules(s) }
    }

    /** As the daemon does: a tab moved within its Group, or to another. */
    private fun moveTab(tabID: String, groupID: String, index: Int) {
        val ti = state.tabsList.indexOfFirst { it.id == tabID }
        if (ti < 0) return
        val from = state.getTabs(ti).groupId
        val dest = groupID.ifEmpty { from }
        val di = state.groupsList.indexOfFirst { it.id == dest }
        if (di < 0) return
        val changed = ArrayList<Hammock.Group>()
        val si = state.groupsList.indexOfFirst { it.id == from }
        if (si >= 0) {
            val g = state.getGroups(si).toBuilder()
            val ids = g.tabIdsList.filter { it != tabID }
            g.clearTabIds().addAllTabIds(ids)
            state.setGroups(si, g.build())
            if (si != di) changed += g.build()
        }
        val d = state.getGroups(di).toBuilder()
        val ids = d.tabIdsList.toMutableList()
        ids.add(index.coerceIn(0, ids.size), tabID)
        d.clearTabIds().addAllTabIds(ids)
        state.setGroups(di, d.build())
        changed += d.build()
        val t = state.getTabs(ti).toBuilder().setGroupId(dest).build()
        state.setTabs(ti, t)
        update { addAllGroups(changed); addTabs(t) }
    }

    /**
     * As the daemon does: looking at a finished agent acknowledges it (its
     * checkmark, and its item in Needs You, go away).
     */
    private fun focused(paneID: String) {
        val i = state.panesList.indexOfFirst { it.id == paneID }
        if (i < 0) return
        val pane = state.getPanes(i)
        if (!pane.hasAgent() || pane.agent.seen || pane.agent.state != AgentState.AGENT_STATE_IDLE) return
        val p = pane.toBuilder().setAgent(pane.agent.toBuilder().setSeen(true)).build()
        state.setPanes(i, p)
        val cleared = state.attentionList.filter { it.paneId == paneID && it.state == AgentState.AGENT_STATE_IDLE }.map { it.id }
        removeAttention { it.id in cleared }
        update { addPanes(p); addAllClearedAttentionIds(cleared) }
    }

    // Agents

    private fun sendView(paneID: String, replace: Boolean = false, blocks: List<AgentBlock>? = null) {
        if (paneID !in watched) return
        val v = Hammock.AgentView.newBuilder().setPaneId(paneID).setReplace(replace)
            .addAllBlocks(blocks ?: transcripts[paneID] ?: emptyList())
            .setSessionId("demo-$paneID").setModel("claude-opus-5-5").setEffort("high")
        prompts[paneID]?.let { v.prompt = it }
        working[paneID]?.let { v.working = true; v.status = it }
        if (replace && longChat && paneID == "p_polish") v.hasMore = true
        send { agentView = v.build() }
    }

    private fun add(paneID: String, b: AgentBlock) {
        val list = transcripts.getOrPut(paneID) { mutableListOf() }
        list.removeAll { it.id == b.id }
        list += b
        sendView(paneID, blocks = listOf(b))
    }

    private fun agentAnswered(a: Hammock.AgentAnswer) {
        if (a.paneId != "p_triage" || prompts["p_triage"] == null) return
        prompts.remove("p_triage")
        removeAttention { it.id == "a_triage" }
        update { addClearedAttentionIds("a_triage") }
        val tool = AgentBlock.Kind.KIND_TOOL
        val denied = a.decision == Hammock.AgentAnswer.Decision.DECISION_DENY || a.option == 3
        if (denied) {
            add("p_triage", block("b_triage_gh", tool, title = "Ran `gh issue list`", input = DemoContent.TRIAGE_COMMAND,
                output = "The user doesn’t want to proceed with this tool use.", error = true))
            add("p_triage", block("b_triage_stop", AgentBlock.Kind.KIND_TEXT, text = "OK, I won’t run that. Tell me what you’d like instead."))
            setAgent("p_triage", AgentState.AGENT_STATE_IDLE, "Stopped: you said no to gh issue list")
            setRun("r_triage_now", ScheduleRun.Status.STATUS_READY, "Stopped when you said no")
            return
        }
        working["p_triage"] = "Labelling issues…"
        add("p_triage", block("b_triage_gh", tool, title = "Ran `gh issue list`", input = DemoContent.TRIAGE_COMMAND,
            output = "[{\"number\":214,\"title\":\"Crash when a pane closes mid-paste\"},\n {\"number\":215,\"title\":\"Dark mode: code blocks too dim\"},\n {\"number\":216,\"title\":\"Add a shortcut to jump to the waiting agent\"}]"))
        setAgent("p_triage", AgentState.AGENT_STATE_WORKING, "Labelling issues…")
        setRun("r_triage_now", ScheduleRun.Status.STATUS_RUNNING, "Labelling issues")
        later(1.5) {
            add("p_triage", block("b_triage_label", tool, title = "Ran `gh issue edit` 3 times",
                input = "gh issue edit 214 --add-label bug,priority:high",
                output = "https://github.com/acme/widgets/issues/214\nhttps://github.com/acme/widgets/issues/215\nhttps://github.com/acme/widgets/issues/216"))
        }
        later(3.0) {
            working.remove("p_triage")
            add("p_triage", block("b_triage_done", AgentBlock.Kind.KIND_TEXT, text = """
                Triaged the 3 new issues:

                - **#214** Crash when a pane closes mid-paste: bug, high priority. It loses work, so it's first.
                - **#215** Dark mode code blocks too dim: bug, medium.
                - **#216** Shortcut to the waiting agent: enhancement, low.
            """.trimIndent()))
            setAgent("p_triage", AgentState.AGENT_STATE_IDLE, "Triaged 3 issues: one high-priority crash.")
            setRun("r_triage_now", ScheduleRun.Status.STATUS_READY, "Triaged 3 issues: one high-priority crash.")
        }
    }

    private fun agentSent(paneID: String, text: String) {
        val n = (transcripts[paneID]?.size ?: 0) + 1
        if (longChat && paneID == "p_polish") return replyWithTools(paneID, text, n)
        // In the transcript a moment after it's sent, as from a Mac: shown pending meanwhile.
        if (newChat && paneID == "p_polish" && n == 1) {
            working[paneID] = "Thinking…"
            setAgent(paneID, AgentState.AGENT_STATE_WORKING, "Thinking…")
            sendView(paneID, blocks = emptyList())
            return later(0.8) { demoReply(paneID, text, n) }
        }
        demoReply(paneID, text, n)
    }

    private fun demoReply(paneID: String, text: String, n: Int) {
        add(paneID, block("b_you_$n", AgentBlock.Kind.KIND_USER, text = text))
        working[paneID] = "Thinking…"
        sendView(paneID, blocks = emptyList())
        setAgent(paneID, AgentState.AGENT_STATE_WORKING, "Thinking…")
        later(2.0) {
            working.remove(paneID)
            add(paneID, block("b_demo_$n", AgentBlock.Kind.KIND_TEXT, text =
                "This is Hammock's demo, so there's no agent here to do that. With your Mac paired, " +
                    "your message goes to the agent running on it, and you see its work here as it happens."))
            setAgent(paneID, AgentState.AGENT_STATE_IDLE, "Answered in the demo")
        }
    }

    /**
     * Debug (longChat): answers a message as Claude does: thinking, then a run
     * of tool calls arriving one after another, then a reply.
     */
    private fun replyWithTools(paneID: String, text: String, n: Int) {
        add(paneID, block("b_you_$n", AgentBlock.Kind.KIND_USER, text = text))
        working[paneID] = "Contemplating…"
        sendView(paneID, blocks = emptyList())
        setAgent(paneID, AgentState.AGENT_STATE_WORKING, "Contemplating…")
        var t = 0.4
        later(t) { add(paneID, block("b_think_$n", AgentBlock.Kind.KIND_THINKING)) }
        for (i in 0 until 12) {
            val id = "b_tool_${n}_$i"
            val file = "apple/Sources/HammockKit/File$i.swift"
            t += 0.35
            later(t) { add(paneID, block(id, AgentBlock.Kind.KIND_TOOL, title = "Read $file", tool = "Read", input = file, pending = true)) }
            t += 0.3
            later(t) { add(paneID, block(id, AgentBlock.Kind.KIND_TOOL, title = "Read $file", tool = "Read", input = file, output = "${120 + i * 17} lines")) }
            if (i == 5) {
                t += 0.3
                later(t) { add(paneID, block("b_say_$n", AgentBlock.Kind.KIND_THINKING, text = "Now the scroll view itself.")) }
            }
        }
        t += 0.6
        later(t) {
            working.remove(paneID)
            // "medium" in the message: a reply a little taller than what shows above the keyboard.
            val reply = if (text.contains("medium")) DemoContent.longReply(n) else DemoContent.longReply(n) + "\n\n" + DemoContent.longReply(n + 1)
            add(paneID, block("b_reply_$n", AgentBlock.Kind.KIND_TEXT, text = reply))
            setAgent(paneID, AgentState.AGENT_STATE_IDLE, "Answered")
        }
    }

    // The shell

    private fun shellTyped(data: ByteString) {
        val out = StringBuilder()
        var lastCR = false
        for (c in data.toStringUtf8()) {
            when (c) {
                '\r', '\n' -> {
                    if (c == '\n' && lastCR) { lastCR = false; continue } // \r\n is one Return
                    lastCR = c == '\r'
                    val line = typed.toString().trim(' ', '\t')
                    typed = StringBuilder()
                    out.append("\r\n")
                    val canned = DemoContent.shellReplies[line]
                    if (canned != null) out.append(canned) else if (line.isNotEmpty()) out.append(DemoContent.DEMO_SHELL_NOTE)
                    out.append(DemoContent.prompt)
                    continue
                }
                '\u007F', '\b' -> if (typed.isNotEmpty()) { typed.setLength(typed.length - 1); out.append("\b \b") }
                else -> if (c.code >= 0x20) { typed.append(c); out.append(c) } // other control keys do nothing here
            }
            lastCR = false
        }
        if (out.isEmpty()) return
        send { paneOutput = Hammock.PaneOutput.newBuilder().setPaneId("p_shell").setData(ByteString.copyFromUtf8(out.toString())).build() }
    }
}
