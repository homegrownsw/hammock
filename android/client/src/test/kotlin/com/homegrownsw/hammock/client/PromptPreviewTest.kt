package com.homegrownsw.hammock.client

import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.AttentionItem
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PromptPreviewTest {
    private fun item(detail: String, question: Boolean = false) = AttentionItem.newBuilder()
        .setState(AgentState.AGENT_STATE_NEEDS_INPUT).setDetail(detail).setQuestion(question).build()

    @Test fun aShortPromptIsShownWholeAndAllowed() {
        val i = item("Bash command\n\n  cd /work\n  rm -rf build\n\nDo you want to proceed?")
        val p = i.promptPreview!!
        assertTrue(p.complete)
        assertEquals(0, p.hiddenLines)
        assertNull(p.moreLabel)
        assertTrue(p.text.contains("rm -rf build"))
        assertTrue(i.allowsFromCard)
    }

    @Test fun aLongCommandSaysWhatItLeavesOutAndIsReviewedFirst() {
        val lines = (1..14).joinToString("\n") { "echo $it" }
        val i = item(lines)
        val p = i.promptPreview!!
        assertEquals(4, p.hiddenLines)
        assertEquals("+4 more lines", p.moreLabel)
        assertFalse(p.text.contains("echo 11"))
        assertFalse(i.allowsFromCard)
    }

    @Test fun oneVeryLongLineIsCutAndReviewedFirst() {
        val i = item("x".repeat(PromptPreview.MAX_CHARACTERS + 10))
        assertFalse(i.promptPreview!!.complete)
        assertEquals("More not shown", i.promptPreview!!.moreLabel)
        assertFalse(i.allowsFromCard)
    }

    @Test fun aPromptThatRanOffItsScreenIsReviewedFirst() {
        assertFalse(item("…\nrm -rf build\nDo you want to proceed?").allowsFromCard)
    }

    @Test fun withoutAPromptAllowStaysOnTheCard() {
        assertNull(item("").promptPreview)
        assertTrue(item("").allowsFromCard)
        assertNull(item("Pick one", question = true).promptPreview)
    }
}
