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
