package com.homegrownsw.hammock.agentview

import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.DaemonStandIn
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentBlock
import hammock.v1.Hammock.AgentBlock.Kind
import kotlinx.coroutines.runBlocking
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertSame
import kotlin.test.assertTrue

class AgentSessionTest {
    private val model = AppModel.standIn(object : DaemonStandIn {
        override fun receive(msg: Hammock.ClientMessage, reply: (Hammock.ServerMessage) -> Unit) {}
    })

    private fun block(id: String, kind: Kind, text: String = "", build: AgentBlock.Builder.() -> Unit = {}) =
        AgentBlock.newBuilder().setId(id).setKind(kind).setText(text).apply(build).build()

    private fun view(vararg blocks: AgentBlock, build: Hammock.AgentView.Builder.() -> Unit = {}) =
        Hammock.AgentView.newBuilder().setPaneId("p1").addAllBlocks(blocks.toList()).apply(build).build()

    private fun session(subagent: String = "", shell: String = "") = AgentSession(model, "p1", subagent, shell)

    @Test
    fun replaceShowsTheConversation() {
        val s = session()
        assertFalse(s.state.value.heard)
        s.apply(view(block("u1", Kind.KIND_USER, "Hi"), block("t1", Kind.KIND_TEXT, "Hello")) {
            replace = true; hasMore = true; addCommands("/review"); addStartCommands("/clear"); working = true; status = "Thinking"
        })
        val st = s.state.value
        assertTrue(st.heard)
        assertTrue(st.hasMore)
        assertEquals(listOf("u1", "t1"), st.blocks.map { it.id })
        assertEquals(listOf("u1", "t1"), st.items.map { it.id })
        assertEquals(listOf("/review"), st.commands)
        assertEquals(setOf("/clear"), st.startCommands)
        assertTrue(st.working)
        assertEquals("Thinking", st.status)
        // Loaded with the conversation, not arriving: not revealed.
        assertNull(s.arrived["t1"])
    }

    @Test
    fun updatesReplaceBlocksInPlaceByIDAndAppendNewOnes() {
        val s = session()
        s.apply(view(block("u1", Kind.KIND_USER, "Run it"), block("b1", Kind.KIND_TOOL) { tool = "Bash"; pending = true }) { replace = true })
        val before = s.state.value.blocks[0]
        s.apply(view(block("b1", Kind.KIND_TOOL) { tool = "Bash"; output = "ok" }, block("t1", Kind.KIND_TEXT, "Done.")), nowMs = 1234)
        val st = s.state.value
        assertEquals(listOf("u1", "b1", "t1"), st.blocks.map { it.id })
        assertEquals("ok", st.blocks[1].output)
        assertFalse(st.blocks[1].pending)
        // Untouched blocks are the same instances (rows skip recomposing).
        assertSame(before, st.blocks[0])
        // A reply arriving while watched is revealed.
        assertEquals(1234L, s.arrived["t1"])
    }

    @Test
    fun anUpdateWithoutBlocksKeepsThemAsTheyWere() {
        val s = session()
        s.apply(view(block("t1", Kind.KIND_TEXT, "Hi")) { replace = true })
        val blocks = s.state.value.blocks
        val items = s.state.value.items
        s.apply(view { working = true; status = "Reading" })
        assertSame(blocks, s.state.value.blocks)
        assertSame(items, s.state.value.items)
        assertEquals("Reading", s.state.value.status)
    }

    @Test
    fun olderPagesGoInBeforeAndSayIfThereIsMore() {
        val s = session()
        s.apply(view(block("t3", Kind.KIND_TEXT, "Three")) { replace = true; hasMore = true; status = "kept" })
        s.loadEarlier() // not connected: nothing asked, not left loading
        assertFalse(s.state.value.loadingOlder)
        s.apply(view(block("t1", Kind.KIND_TEXT, "One"), block("t2", Kind.KIND_TEXT, "Two")) { older = true; hasMore = false })
        val st = s.state.value
        assertEquals(listOf("t1", "t2", "t3"), st.blocks.map { it.id })
        assertFalse(st.hasMore)
        // An older page doesn't carry the live state.
        assertEquals("kept", st.status)
    }

    @Test
    fun aNewSnapshotKeepsRowsAlreadyShownAboveIt() {
        val s = session()
        s.apply(view(block("a", Kind.KIND_TEXT, "A"), block("b", Kind.KIND_TEXT, "B")) { replace = true; hasMore = true })
        s.apply(view(block("b", Kind.KIND_TEXT, "B"), block("c", Kind.KIND_TEXT, "C")) { replace = true; hasMore = false })
        val st = s.state.value
        assertEquals(listOf("a", "b", "c"), st.blocks.map { it.id })
        // Kept rows above: whether there's more above them is as it was.
        assertTrue(st.hasMore)
    }

    @Test
    fun aPendingMessageGoesWhenTheTranscriptShowsIt() {
        val s = session()
        s.apply(view(block("u1", Kind.KIND_USER, "Earlier")) { replace = true })
        s.addPending(PendingSend(1, "Fix  the\ntests"))
        s.addPending(PendingSend(2, "And the docs"))
        // History paging in isn't new.
        s.apply(view(block("u0", Kind.KIND_USER, "Fix the tests")) { older = true })
        assertEquals(2, s.state.value.pendingSends.size)
        // Reworded spacing still matches, and the second waits.
        s.apply(view(block("u2", Kind.KIND_USER, "Fix the tests")))
        assertEquals(listOf(2L), s.state.value.pendingSends.map { it.id })
        // Not matching at all: the oldest goes (the agent changed it on queueing).
        s.apply(view(block("u3", Kind.KIND_USER, "[Image #1] something else")))
        assertTrue(s.state.value.pendingSends.isEmpty())
    }

    @Test
    fun aBangCommandGoesWhenItsRunIsRecorded() {
        val s = session()
        s.apply(view(block("u1", Kind.KIND_USER, "Earlier")) { replace = true })
        s.addPending(PendingSend(1, "!echo  hi"))
        s.addPending(PendingSend(2, "And then this"))
        // It's shown as a running command, with what it's printed so far.
        val run = s.state.value.pendingSends[0].bangRun("hi")!!
        assertTrue(run.isBang() && run.pending)
        assertEquals("echo  hi", run.input)
        assertEquals("hi", run.output)
        assertEquals(null, s.state.value.pendingSends[1].bangRun())
        // The agent's own Bash run (in the turn of a message) isn't it.
        s.apply(view(block("t1", Kind.KIND_TOOL) { tool = "Bash"; input = "echo hi"; turnId = "u1" }))
        assertEquals(2, s.state.value.pendingSends.size)
        // Claude Code records it as a Bash run that's a turn of its own.
        s.apply(view(block("b1", Kind.KIND_TOOL) { tool = "Bash"; input = "echo hi"; turnId = "b1" }))
        assertEquals(listOf(2L), s.state.value.pendingSends.map { it.id })
    }

    @Test
    fun sendsWaitingOnARunningBangCommandAreKept() {
        val s = session()
        s.apply(view { replace = true; working = true })
        s.addPending(PendingSend(1, "!sleep 100"))
        s.addPending(PendingSend(2, "Hello"))
        // A "!" command isn't a turn: the agent stops working while it runs,
        // with its output coming, and the message queued after it waits.
        s.apply(view { working = false; bangOutput = "Running… (4s)" })
        runBlocking { kotlinx.coroutines.delay(3_300) }
        assertEquals(listOf(1L, 2L), s.state.value.pendingSends.map { it.id })
        // Stopped in the terminal: no output, not working, and they go.
        s.apply(view { working = true; bangOutput = "" })
        s.apply(view { working = false })
        runBlocking { kotlinx.coroutines.delay(3_300) }
        assertTrue(s.state.value.pendingSends.isEmpty())
    }

    @Test
    fun extrasChangeOnlyWhenSent() {
        val s = session()
        val task = Hammock.AgentTask.newBuilder().setId("1").setSubject("Run the tests").build()
        val link = Hammock.AgentLink.newBuilder().setUrl("https://example.com/pr/1").setKind(Hammock.AgentLink.Kind.KIND_PULL_REQUEST).build()
        s.apply(view {
            replace = true
            tasks = Hammock.AgentTaskList.newBuilder().addTasks(task).build()
            links = Hammock.AgentLinks.newBuilder().addLinks(link).build()
            addSubagents(Hammock.AgentSubagent.newBuilder().setId("s1"))
        })
        s.apply(view { status = "x" })
        assertEquals(1, s.state.value.tasks.size)
        assertEquals(1, s.state.value.links.size)
        assertEquals(1, s.state.value.subagents.size)
        // Sent empty: none left.
        s.apply(view { tasks = Hammock.AgentTaskList.getDefaultInstance() })
        assertTrue(s.state.value.tasks.isEmpty())
    }

    @Test
    fun aSubagentsViewDoesntTakeTheTerminalsDraft() {
        val main = session()
        main.apply(view { replace = true; draft = "typed" })
        assertEquals("typed", main.state.value.draft)
        val sub = session(subagent = "s1")
        sub.apply(view { replace = true; draft = "typed"; subagentId = "s1" })
        assertEquals("", sub.state.value.draft)
    }

    @Test
    fun theSharedDraftComesWithReplacesAndChanges() {
        val s = session()
        s.apply(view { replace = true; composerDraft = "fix the"; composerRev = 3; composerWriter = "mac" })
        assertEquals(SharedDraft("fix the", 3, "mac"), s.state.value.sharedDraft)
        // An update about something else: unchanged.
        s.apply(view { working = true })
        assertEquals(3L, s.state.value.sharedDraft.rev)
        s.apply(view { composerDraft = "fix the test"; composerRev = 4; composerWriter = "phone" })
        assertEquals(SharedDraft("fix the test", 4, "phone"), s.state.value.sharedDraft)
        // The daemon restarted: it has none.
        s.apply(view { replace = true })
        assertEquals(SharedDraft(), s.state.value.sharedDraft)
        // A subagent's view has none.
        val sub = session(subagent = "s1")
        sub.apply(view { replace = true; subagentId = "s1"; composerDraft = "hi"; composerRev = 1 })
        assertEquals(SharedDraft(), sub.state.value.sharedDraft)
    }

    @Test
    fun aShellsOutputIsAppendedAndRedrawnLinesKeepTheirLast() {
        val s = session(shell = "sh1")
        s.apply(view { replace = true; shellOutput = "one\n" })
        s.apply(view { shellOutput = "10%\r50%\r" })
        s.apply(view { shellOutput = "100%\ntwo\n" })
        assertEquals("one\n100%\ntwo\n", s.state.value.shellOutput)
    }

    @Test
    fun rowIDsAreUniqueEvenIfABlockComesTwice() {
        val items = AgentItem.group(listOf(block("t", Kind.KIND_TEXT, "A"), block("t", Kind.KIND_TEXT, "B")))
        assertEquals(listOf("t", "t#1"), items.map { it.id })
    }

    @Test
    fun theRowsGroupAsTheIPhonesDo() {
        val blocks = listOf(
            block("u", Kind.KIND_USER, "Go"),
            block("th", Kind.KIND_THINKING) { timeUnixMs = 1_000 },
            block("r1", Kind.KIND_TOOL) { tool = "Read"; timeUnixMs = 6_000 },
            block("th2", Kind.KIND_THINKING), // silent, between tools: folded
            block("r2", Kind.KIND_TOOL) { tool = "Grep" },
            block("e1", Kind.KIND_TOOL) { tool = "Edit"; diff = "@@\n-a\n+b\n" },
            block("r3", Kind.KIND_TOOL) { tool = "Bash" },
            block("n", Kind.KIND_THINKING, "Committing now."),
            block("d1", Kind.KIND_DONE) { durationMs = 5_000 },
            block("t", Kind.KIND_TEXT, "Done"),
            block("d2", Kind.KIND_DONE) { durationMs = 60_000 },
        )
        val items = AgentItem.group(blocks)
        assertEquals(listOf("u", "th", "g:r1", "g:e1", "g:r3", "n", "t", "d2"), items.map { it.id })
        assertEquals(5, items[1].thoughtSeconds)
        assertEquals(listOf("r1", "r2"), items[2].tools.map { it.id })
        assertNotNull(items[3].block)
        assertEquals("group", items[2].kind)
        assertEquals("tool", items[4].kind)
        assertEquals("text", items[5].kind)
    }

    @Test
    fun aFailedSendIsTheComposersErrorNotThePromptCards(): Unit = runBlocking {
        val s = session()
        assertFalse(s.send("hello"))
        assertEquals("Not connected. Your message wasn't sent.", s.state.value.sendError)
        assertNull(s.state.value.answerError)
        assertTrue(s.state.value.pendingSends.isEmpty())
        s.clearSendError()
        assertNull(s.state.value.sendError)
    }

    @Test
    fun aNewPromptClearsTheLastOnesAnswerError(): Unit = runBlocking {
        val s = session()
        val p = { id: String -> Hammock.AgentPrompt.newBuilder().setPromptId(id).build() }
        s.apply(view { prompt = p("a") })
        s.answer(1) // not connected: it fails
        assertNotNull(s.state.value.answerError)
        s.apply(view { prompt = p("a") })
        assertNotNull(s.state.value.answerError, "the same prompt keeps it")
        s.apply(view { prompt = p("b") })
        assertNull(s.state.value.answerError)
        assertNull(s.state.value.sendError)
    }
}
