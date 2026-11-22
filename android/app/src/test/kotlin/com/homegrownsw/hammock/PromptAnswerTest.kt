package com.homegrownsw.hammock

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** An agent's permission prompt's notification offers Allow and Deny, which answer that prompt; nothing else's does. */
class PromptAnswerTest {
    @Test fun onlyAnAgentsPromptOffersThem() {
        assertTrue(Notifications.offersAnswers(Notifications.CATEGORY_AGENT_PROMPT, "pn_1", "3f9a1c"))
        // Without the pane or the prompt it answers (a note cut short), none.
        assertFalse(Notifications.offersAnswers(Notifications.CATEGORY_AGENT_PROMPT, "", "3f9a1c"))
        assertFalse(Notifications.offersAnswers(Notifications.CATEGORY_AGENT_PROMPT, "pn_1", ""))
        // A question, a finished agent (no category), a grant: not these.
        assertFalse(Notifications.offersAnswers("", "pn_1", "3f9a1c"))
        assertFalse(Notifications.offersAnswers("grant", "pn_1", "3f9a1c"))
    }

    @Test fun allowAndDenyAreTheDecision() {
        fun answer(action: String) = NotificationAnswer(action, "host", "pn_1", "at_pn_1", "3f9a1c")
        assertEquals(true, answer(Notifications.ACTION_PROMPT_ALLOW).allowsPrompt)
        assertEquals(false, answer(Notifications.ACTION_PROMPT_DENY).allowsPrompt)
        assertNull(answer(Notifications.ACTION_GRANT_ALLOW).allowsPrompt)
        assertNull(answer(Notifications.ACTION_JOIN_VIEW).allowsPrompt)
    }
}
