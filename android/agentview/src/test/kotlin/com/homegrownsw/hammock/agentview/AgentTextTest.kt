package com.homegrownsw.hammock.agentview

import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class AgentTextTest {
    @Test
    fun slashTokens() {
        val text = "please /rev"
        val t = SlashToken.at(text.length, text)!!
        assertEquals(7..10, t)
        val commands = listOf("/review", "/review-pr", "/clear", "/resume")
        // Built-ins only run at the start of a message.
        assertEquals(listOf("/review", "/review-pr"), SlashToken.suggestions(t, text, commands, startOnly = setOf("/clear")))
        assertEquals(listOf("/clear"), SlashToken.suggestions(SlashToken.at(2, "/c")!!, "/c", commands, setOf("/clear")))
        assertEquals(emptyList(), SlashToken.suggestions(SlashToken.at(10, "please /cl")!!, "please /cl", commands, setOf("/clear")))
        assertNull(SlashToken.at(4, "a/bc"))
        assertNull(SlashToken.at(2, "/ab"))
        assertEquals("please /review " to 15, SlashToken.complete(t, "/review", text))
        assertEquals("/review now" to 8, SlashToken.complete(0..1, "/review", "/r now"))
        assertEquals(listOf(0..6), SlashToken.highlights("/review it /clear", setOf("/review", "/clear"), setOf("/clear")))
    }

    @Test
    fun spokenSlashCommands() {
        val commands = listOf("/review-pr", "/plugin:deploy-site", "/clear")
        assertEquals("/review-pr 42", DictationText.mapSlashCommands("slash review PR 42", commands))
        assertEquals("then /clear", DictationText.mapSlashCommands("then/clear", commands))
        assertEquals("/plugin:deploy-site now", DictationText.mapSlashCommands("slash deploy site now", commands))
        assertEquals("a slash in it", DictationText.mapSlashCommands("a slash in it", commands))
        assertEquals("and/or", DictationText.mapSlashCommands("and/or", commands))
        assertEquals(listOf("review pr", "deploy site", "clear"), DictationText.vocabulary(commands))
        assertEquals("one two", DictationText.join("one", "two"))
        assertEquals("one,", DictationText.join("one", ","))
        assertEquals("two", DictationText.join("", "  two"))
    }

    @Test
    fun modelNames() {
        assertEquals("Opus 5.5", ModelNames.displayName("claude-opus-5-5"))
        assertEquals("Sonnet 4.5", ModelNames.displayName("claude-sonnet-4-5-20250929"))
        assertEquals("Opus 4", ModelNames.displayName("claude-opus-4[1m]"))
        assertEquals("GPT-6 Luna", ModelNames.displayName("gpt-6-luna"))
        assertEquals("Opus 4.1 (1M)", ModelNames.displayName("Opus 4.1 (1M)"))
        assertEquals("Extra high", ModelNames.effortName("xhigh"))
        assertEquals("Turbo", ModelNames.effortName("turbo"))
        assertEquals("120k", ModelNames.tokens(120_400))
        assertEquals("1M", ModelNames.tokens(1_000_000))
        assertEquals("1.5M", ModelNames.tokens(1_500_000))
    }

    @Test
    fun durations() {
        assertEquals("45s", TurnWords.duration(45_000))
        assertEquals("30m 31s", TurnWords.duration(1_831_000))
        assertEquals("2h 5m", TurnWords.duration(7_500_000))
        assertEquals("1m 05s", TurnWords.running(65))
        assertEquals("1h 02m", TurnWords.running(3720))
    }

    @Test
    fun links() {
        assertEquals(LinkPolicy.Opening.Web, LinkPolicy.opening("https://example.com"))
        assertEquals(LinkPolicy.Opening.Direct, LinkPolicy.opening("mailto:you@example.com"))
        assertEquals(LinkPolicy.Opening.File("/Users/me/a b.txt"), LinkPolicy.opening("file:///Users/me/a%20b.txt"))
        assertEquals(LinkPolicy.Opening.Ask, LinkPolicy.opening("file://server/share/x"))
        assertEquals(LinkPolicy.Opening.Ask, LinkPolicy.opening("tel:123"))
    }

    @Test
    fun cards() {
        assertEquals("Yes" to true, CardText.recommended("Yes (Recommended)"))
        assertEquals("No" to false, CardText.recommended("No"))
        assertEquals("Merged", CardText.prState("merged"))
    }

    @Test
    fun rankedCompletion() {
        val commands = listOf("/compact", "/review", "/review-pr", "/plugin:pr-tidy", "/approve", "/prompts:draft")
        val text = "/pr"
        assertEquals(
            listOf("/prompts:draft", "/review-pr", "/plugin:pr-tidy", "/approve"),
            SlashToken.suggestions(SlashToken.at(3, text)!!, text, commands, emptySet()),
        )
        assertEquals(3, SlashToken.rank("/rvpr", "/review-pr"))
        assertNull(SlashToken.rank("/zz", "/review"))
        assertEquals(0..6, SlashToken.at(7, "@src/ma", '@'))
        assertNull(SlashToken.at(11, "you@example", '@'))
        assertEquals("b.go", SlashToken.mentionPath("a/b.go", "/Users/me/app", "/Users/me/app/a"))
        assertEquals("/Users/me/app/x.go", SlashToken.mentionPath("x.go", "/Users/me/app", "/Users/me/other"))
        assertEquals("@\"a b.txt\"", SlashToken.mention("a b.txt"))
    }

    @Test
    fun composerHistory() {
        val h = ComposerHistory<String>()
        val all = listOf("one", "two")
        assertEquals("two", h.up("draft") { all })
        assertEquals("one", h.up("") { all })
        assertNull(h.up("") { all })
        assertEquals("two" to null, h.down())
        assertEquals(null to "draft", h.down())
        assertNull(h.down())
        assertEquals(true, ComposerHistory.steps(true, "", 0, false))
        assertEquals(false, ComposerHistory.steps(true, "hello", 5, false))
        assertEquals(true, ComposerHistory.steps(true, "hello", 5, true))
        assertEquals(false, ComposerHistory.steps(true, "a\nb", 3, true))
        assertEquals(true, ComposerHistory.steps(false, "a\nb", 3, true))
        assertEquals(false, ComposerHistory.steps(false, "", 0, false))
        assertEquals(
            listOf("old one", "typed in the terminal", "fix it", "not there yet"),
            ComposerHistory.merged(listOf("old one", "fix it", "not there yet"), listOf("typed in the terminal", "fix it")),
        )
        assertEquals(listOf("b", "a"), ComposerHistory.recorded(listOf("a", "b"), " a "))
    }

    @Test
    fun promptKeys() {
        val other = PromptKeys.Key.OTHER
        assertEquals(PromptKeys.Action.Pick(2), PromptKeys.action(2, other, false, options = 3, multipleQuestions = false))
        assertNull(PromptKeys.action(4, other, false, options = 3, multipleQuestions = false))
        assertNull(PromptKeys.action(1, other, true, options = 3, multipleQuestions = false))
        assertNull(PromptKeys.action(0, other, false, options = 3, multipleQuestions = false))
        assertNull(PromptKeys.action(0, PromptKeys.Key.LEFT, false, options = 3, multipleQuestions = false))
        assertEquals(PromptKeys.Action.Previous, PromptKeys.action(0, PromptKeys.Key.LEFT, false, options = 3, multipleQuestions = true))
        assertEquals(PromptKeys.Action.Next, PromptKeys.action(0, PromptKeys.Key.RIGHT, false, options = 3, multipleQuestions = true))
        assertEquals(PromptKeys.Action.Escape, PromptKeys.action(0, PromptKeys.Key.ESCAPE, false, options = 3, multipleQuestions = false))
    }
}

class ToolSummaryTest {
    @Test
    fun words() {
        val tools = listOf("Read" to "a.go", "Read" to "b.go", "Bash" to "go test", "Read" to "a.go", "Edit" to "a.go", "Bash" to "go vet")
        assertEquals("Read 2 files · ran 2 commands · edited 1 file", ToolSummary.words(tools))
        assertEquals(
            "Read 1 file · ran 1 command · edited 1 file · 3 more",
            ToolSummary.words(listOf("Read" to "a", "Bash" to "", "Edit" to "a", "Grep" to "", "Grep" to "", "WebFetch" to "")),
        )
        assertEquals("Used 2 tools", ToolSummary.words(listOf("Foo" to "", "Bar" to "")))
        assertEquals("1 failed", ToolSummary.failed(1))
        assertNull(ToolSummary.failed(0))
    }
}

class DiffLinesTest {
    @Test
    fun numbers() {
        val l = DiffLines.numbered("@@ -12 +14 @@\n keep\n-old\n+new\n tail\n")
        assertEquals(listOf(null, 12, 13, null, 14), l.map { it.old })
        assertEquals(listOf(null, 14, null, 15, 16), l.map { it.new })
        assertEquals(3 to 5, DiffLines.hunk("@@ -3,2 +5,4 @@ func x()"))
        assertNull(DiffLines.hunk("@@ a.go"))
        assertEquals(listOf("a.go", "b.go"), DiffLines.files(DiffLines.numbered("@@ a.go\n+x\n@@ b.go\n-y\n@@ and 2 more files\n")))
        assertEquals(true, DiffLines.numbered("-a\n+b").all { it.old == null && it.new == null })
    }
}

class ContextActionsTest {
    @Test
    fun actions() {
        assertEquals(listOf("/compact", "/clear"), ModelNames.contextActions("claude", emptyList()).map { it.second })
        assertEquals(listOf("/compact", "/new"), ModelNames.contextActions("codex", listOf("/clear")).map { it.second })
        assertEquals(emptyList(), ModelNames.contextActions("droid", emptyList()))
    }
}

class ToolWordsTest {
    @Test
    fun words() {
        assertEquals("Running, 12 seconds", ToolWords.running(12))
        assertEquals("Running, 1 minute, 5 seconds", ToolWords.running(65))
        assertEquals("Running", ToolWords.running(null))
        assertEquals("Bash: npm test, failed", ToolWords.row("Bash", "Run the tests", "npm test\nx", false, false, true, 0, 0))
        assertEquals("Edit: a.go, 3 added, 1 removed, succeeded", ToolWords.row("Edit", "Edited a.go", "a.go", false, false, false, 3, 1))
        assertEquals("Shell command: ls, running", ToolWords.row("Bash", "", "ls", true, true, false, 0, 0))
    }
}

class ReviewFixesTest {
    @Test
    fun editingABroughtBackMessageEndsStepping() {
        val h = ComposerHistory<String>()
        h.up("draft") { listOf("one", "two") }
        h.edited("two")
        assertEquals(true, h.browsing)
        h.edited("two, edited")
        assertEquals(false, h.browsing)
    }

    @Test
    fun keepsTheLastFortyPanes() {
        val order = (1..40).map { "p$it" }
        assertEquals(order.drop(1) + "new" to listOf("p1"), ComposerHistory.panesKept(order, "new"))
        assertEquals((order - "p5") + "p5" to emptyList(), ComposerHistory.panesKept(order, "p5"))
    }

    @Test
    fun returnTakesOnlyOnceMoved() {
        assertEquals(false, SlashToken.returnTakes(suggesting = true, moved = false))
        assertEquals(true, SlashToken.returnTakes(suggesting = true, moved = true))
    }

    @Test
    fun promptCardSettles() {
        assertEquals(false, PromptKeys.acceptsPick(1000, 1200))
        assertEquals(true, PromptKeys.acceptsPick(1000, 1600))
    }
}

class TokensLeftTest {
    @Test
    fun words() {
        assertEquals("~120k tokens left", ModelNames.tokensLeft(120_000))
        assertEquals("640 tokens left", ModelNames.tokensLeft(640))
        assertEquals("0 tokens left", ModelNames.tokensLeft(-3))
    }
}

class DiffLinesEdgeTest {
    @Test
    fun notesAndLineBreaks() {
        val l = DiffLines.numbered("@@ -5 +5 @@\r\n-old\r\n\\ No newline at end of file\r\n+new\r\n")
        assertEquals(listOf(DiffLines.Kind.HUNK, DiffLines.Kind.REMOVED, DiffLines.Kind.NOTE, DiffLines.Kind.ADDED), l.map { it.kind })
        assertEquals(5, l[3].new)
        assertNull(l[2].old)
        assertEquals(emptyList(), DiffLines.numbered(""))
    }
}

class OtherAgentsToolsTest {
    @Test
    fun names() {
        assertEquals(
            "Ran 2 commands · searched once · edited 1 file",
            ToolSummary.words(listOf("exec_command" to "", "exec" to "", "web_search" to "", "apply_patch" to "a.go")),
        )
        assertEquals("exec_command: ls, succeeded", ToolWords.row("exec_command", "Ran `ls`", "ls", false, false, false, 0, 0))
        assertEquals("a.go", ToolSummary.diffPath("edit", "a.go", "-x\n+y\n"))
        assertEquals("", ToolSummary.diffPath("apply_patch", "a.go\nb.go", "@@ a.go\n+x\n"))
    }
}

class RoundTwoTest {
    @Test
    fun editingKeepsTheDraftForDown() {
        val h = ComposerHistory<String>()
        h.up("my draft") { listOf("one", "two") }
        h.edited("two, edited")
        assertEquals(false, h.browsing)
        assertEquals(true, h.keepsDraft)
        assertNull(h.savedDraft)
        assertEquals(null to "my draft", h.down())
        assertEquals(false, h.keepsDraft)
        // Stepping again after an edit keeps the first draft.
        val g = ComposerHistory<String>()
        g.up("first") { listOf("a") }
        g.edited("a!")
        g.up("a!") { listOf("a") }
        assertEquals("first", g.savedDraft)
    }

    @Test
    fun aLateLoadKeepsWhatWasSent() {
        assertEquals(listOf("old", "new"), ComposerHistory.withSent(listOf("old"), listOf("new")))
        assertEquals(listOf("b", "a"), ComposerHistory.withSent(listOf("a", "b"), listOf("a")))
    }

    @Test
    fun staleFilesOnlyForTheSameWord() {
        assertEquals(true, SlashToken.mentionShows("p1", "src", "p1", "src/a"))
        assertEquals(false, SlashToken.mentionShows("p1", "src", "p1", "doc"))
        assertEquals(false, SlashToken.mentionShows("p1", "src", "p2", "src"))
        assertEquals(false, SlashToken.mentionShows("p1", null, "p1", ""))
    }

    @Test
    fun cardWaitsOutTyping() {
        assertEquals(0L, PromptKeys.keyDelayMs(0, 10_000))
        assertEquals(700L, PromptKeys.keyDelayMs(10_000, 10_300))
        assertEquals(0L, PromptKeys.keyDelayMs(10_000, 11_200))
        assertEquals(true, PromptKeys.composerRetakes(true, 10_000, 10_400, active = true, hardKeyboard = true))
        assertEquals(false, PromptKeys.composerRetakes(true, 10_000, 99_000, active = true, hardKeyboard = true))
        assertEquals(false, PromptKeys.composerRetakes(true, 10_000, 10_400, active = false, hardKeyboard = true))
        assertEquals(false, PromptKeys.composerRetakes(true, 10_000, 10_400, active = true, hardKeyboard = false))
        assertEquals(false, PromptKeys.composerRetakes(false, 10_000, 10_400, active = true, hardKeyboard = true))
    }
}
