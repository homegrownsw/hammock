package com.homegrownsw.hammock.ui

import com.homegrownsw.hammock.ui.PromptAnswer.ALLOW
import com.homegrownsw.hammock.ui.PromptAnswer.ALLOW_SCHEDULE
import com.homegrownsw.hammock.ui.PromptAnswer.DENY
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.AttentionItem
import org.junit.Assert.assertEquals
import org.junit.Test

/** A session's touch-and-hold menu answers a permission prompt as its card does, and as the iPhone's menu. */
class PromptAnswersTest {
    private fun item(detail: String = "Bash command\n  ls\nDo you want to proceed?", question: Boolean = false,
                     state: AgentState = AgentState.AGENT_STATE_NEEDS_INPUT) =
        AttentionItem.newBuilder().setState(state).setDetail(detail).setQuestion(question).build()

    @Test fun aPermissionPromptOffersAllowAndDeny() {
        assertEquals(listOf(ALLOW, DENY), promptAnswers(item(), scheduledClaude = false))
        assertEquals(listOf(ALLOW, ALLOW_SCHEDULE, DENY), promptAnswers(item(), scheduledClaude = true))
    }

    @Test fun onlyDenyWhenThereIsMoreThanTheCardShows() {
        assertEquals(listOf(DENY), promptAnswers(item("…\nrm -rf build\nDo you want to proceed?"), scheduledClaude = true))
    }

    @Test fun nothingForAQuestionOrAnAgentNotAsking() {
        assertEquals(emptyList<PromptAnswer>(), promptAnswers(null, scheduledClaude = false))
        assertEquals(emptyList<PromptAnswer>(), promptAnswers(item(question = true), scheduledClaude = false))
        assertEquals(emptyList<PromptAnswer>(), promptAnswers(item(state = AgentState.AGENT_STATE_WORKING), scheduledClaude = false))
    }
}
