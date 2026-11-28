package com.homegrownsw.hammock.client

import hammock.v1.Hammock.Agent
import hammock.v1.Hammock.AgentKind
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.GrantRequest
import hammock.v1.Hammock.Group
import hammock.v1.Hammock.Layout
import hammock.v1.Hammock.LimitResume
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.PaneKind
import hammock.v1.Hammock.Role
import hammock.v1.Hammock.Tab
import java.time.Instant
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.time.temporal.ChronoUnit
import java.util.Locale

// Helpers on the protocol's messages, as the iPhone's Model+UI.swift,
// Sharing.swift and AgentCatalog.swift have them.

/**
 * The state to show: a command run with ! after the agent went idle shows as work; one waiting out its usage
 * limit, once you've seen it stop, as at rest ([waitingOnLimit]).
 */
val Agent.shownState: AgentState get() = when {
    waitingOnLimit -> AgentState.AGENT_STATE_IDLE
    runningCommand && state == AgentState.AGENT_STATE_IDLE -> AgentState.AGENT_STATE_WORKING
    else -> state
}

/**
 * It stopped at its usage limit, you've seen that, and it carries on when the limit resets: a calm wait (a
 * clock, "Continues at 18:37"), not an error, out of Needs You. Unseen, it's still the error.
 */
val Agent.waitingOnLimit: Boolean get() = state == AgentState.AGENT_STATE_ERRORED && hasLimitResume() && seen

/** At rest with work still going on (background shells, subagents). */
val Agent.restingWithWork: Boolean get() = state == AgentState.AGENT_STATE_IDLE && !runningCommand && (shells > 0 || subagents > 0)

/** At rest with subagents still running: its dot goes round in the working indigo. */
val Agent.restingWithSubagents: Boolean get() = state == AgentState.AGENT_STATE_IDLE && !runningCommand && subagents > 0

val AgentState.label: String get() = when (this) {
    AgentState.AGENT_STATE_NEEDS_INPUT -> "Waiting for you"
    AgentState.AGENT_STATE_WORKING -> "Working"
    AgentState.AGENT_STATE_IDLE -> "Finished"
    AgentState.AGENT_STATE_ERRORED -> "Errored"
    AgentState.AGENT_STATE_STARTING -> "Starting"
    AgentState.AGENT_STATE_EXITED -> "Exited"
    else -> ""
}

val Pane.isReportingProgram: Boolean get() = hasAgent() && agent.reported && agent.harness.isEmpty()
val Pane.isLog: Boolean get() = hasLog()
val Pane.isDiff: Boolean get() = kind == PaneKind.PANE_KIND_DIFF
val Pane.isFiles: Boolean get() = kind == PaneKind.PANE_KIND_FILES
val Pane.isTerminal: Boolean get() = kind == PaneKind.PANE_KIND_TERMINAL || kind == PaneKind.PANE_KIND_UNSPECIFIED

/** The pane's process for people: the agent's harness if known, else its title. */
val Pane.displayName: String get() {
    if (kind == PaneKind.PANE_KIND_EDITOR) return editor.name.ifEmpty { "Untitled" }
    if (kind == PaneKind.PANE_KIND_FILES) return title.ifEmpty { "Files" }
    if (hasAgent() && agent.harness.isNotEmpty()) return agent.harness
    if (isReportingProgram) listOf(agent.title, agent.app, programList.firstOrNull() ?: "").firstOrNull { it.isNotEmpty() }?.let { return it }
    return title.ifEmpty { commandList.firstOrNull()?.substringAfterLast('/') ?: "shell" }
}

/** 0–100 while working or waiting, when the agent says (OSC 7501). */
val Pane.agentProgress: Int? get() =
    if (hasAgent() && agent.hasProgress() && (agent.state == AgentState.AGENT_STATE_WORKING || agent.state == AgentState.AGENT_STATE_NEEDS_INPUT)) agent.progress else null

val Pane.sessionURL: String? get() = if (hasAgent() && agent.sessionUrl.startsWith("https://")) agent.sessionUrl else null

val Pane.canSleep: Boolean get() = isTerminal && hasAgent() && !exited && !asleep &&
    (agent.state == AgentState.AGENT_STATE_IDLE || agent.state == AgentState.AGENT_STATE_ERRORED) && !agent.runningCommand

/**
 * Its program ended (a failed command, an agent that quit) and can start again in the same pane
 * (RestartPane): a terminal's, not a log's, nor a scheduled run's (its task runs again instead).
 */
val Pane.canRunAgain: Boolean get() = isTerminal && exited && !asleep && !isLog && scheduleRunId.isEmpty()

/** Running it again resumes its agent's session (the daemon falls back to its own command when there's nothing to resume). */
fun Pane.resumesWhenRunAgain(kinds: List<AgentKind>): Boolean =
    hasAgent() && agent.sessionId.isNotEmpty() && kinds.firstOrNull { it.id == agent.harness }?.canResume == true

/** How its program ended, for the bar over an ended pane: "Exited 1", or "Couldn't start" when it never ran. */
val Pane.exitedStatusLabel: String get() = if (startError.isEmpty()) "Exited $exitStatus" else "Couldn't start"

/** The button that starts it again: Resume for an agent's session. */
fun Pane.runAgainTitle(kinds: List<AgentKind>): String = if (resumesWhenRunAgain(kinds)) "Resume" else "Run Again"

val Pane.agentStatusLabel: String get() = if (asleep) "Asleep" else agent.state.label

/**
 * Its agent stopped at its usage limit and is sent "continue" when the limit resets (Agent.limit_resume):
 * when, in words (LimitResume.label). Null when it isn't.
 */
fun Pane.limitResumeLabel(now: ZonedDateTime = ZonedDateTime.now(), locale: Locale = Locale.getDefault()): String? =
    if (hasAgent() && agent.hasLimitResume() && agent.state == AgentState.AGENT_STATE_ERRORED) agent.limitResume.label(now, locale) else null

/**
 * When the agent is sent "continue", in this phone's time: "Continues at 15:01", "Continues tomorrow at
 * 09:00", "Continues Thursday at 10:00", "Continues 3 Dec at 10:00"; "Tries again at …" when the limit's
 * reset isn't known. As the iPhone says it.
 */
fun LimitResume.label(now: ZonedDateTime = ZonedDateTime.now(), locale: Locale = Locale.getDefault()): String {
    val at = Instant.ofEpochMilli(atUnixMs).atZone(now.zone)
    val verb = if (resetKnown) "Continues" else "Tries again"
    val time = at.format(DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale))
    val days = ChronoUnit.DAYS.between(now.toLocalDate(), at.toLocalDate())
    return when {
        days <= 0 -> "$verb at $time"
        days == 1L -> "$verb tomorrow at $time"
        days <= 6 -> "$verb ${at.format(DateTimeFormatter.ofPattern("EEEE", locale))} at $time"
        else -> "$verb ${at.format(DateTimeFormatter.ofPattern("d MMM", locale))} at $time"
    }
}

/** Its agent is at work (working, starting, waiting for you, or with subagents still running): closing the pane stops it. */
val Pane.agentAtWork: Boolean get() = hasAgent() && !exited && (agent.subagents > 0 || agent.shownState in setOf(
    AgentState.AGENT_STATE_WORKING, AgentState.AGENT_STATE_STARTING, AgentState.AGENT_STATE_NEEDS_INPUT))

/** What closing these panes (one, or a tab's) would stop, worded as the iPhone's; null when no agent among them is at work. */
fun closeWarning(panes: List<Pane>): String? {
    val busy = panes.filter { it.agentAtWork }
    if (busy.size > 1) return "${busy.size} agents are still at work. Closing stops them."
    val p = busy.firstOrNull() ?: return null
    val a = p.agent
    val doing = when {
        a.shownState == AgentState.AGENT_STATE_NEEDS_INPUT -> "is waiting for you"
        a.runningCommand -> "is running a command"
        a.shownState == AgentState.AGENT_STATE_IDLE -> "has ${if (a.subagents == 1) "a subagent" else "${a.subagents} subagents"} still running"
        else -> "is still working"
    }
    return "${p.displayName.replaceFirstChar { it.uppercase() }} $doing. Closing stops it."
}

/** Shared with you from another computer. */
val Group.isRemote: Boolean get() = hostUserId.isNotEmpty()
val Group.canDrive: Boolean get() = !isRemote || myRole == Role.ROLE_DRIVER || myRole == Role.ROLE_OWNER
val Group.canAddTabs: Boolean get() = canDrive && !offline
val Group.isOnLinkedComputer: Boolean get() = isRemote && myRole == Role.ROLE_OWNER

val GrantRequest.headlineText: String get() = headline.ifEmpty { "An agent wants to $description" }
/** Whether its parts come apart (an automation's): [titleText] short, `detailsList` beneath. */
val GrantRequest.hasDetails: Boolean get() = title.isNotEmpty() && detailsCount > 0
/** What's asked: short when its parts are shown beneath it ([hasDetails]), else all of the headline. */
val GrantRequest.titleText: String get() = if (hasDetails) title else headlineText
val GrantRequest.agentNote: String? get() = if (headline.isEmpty() || description.isEmpty()) null else description
val GrantRequest.originText: String? get() = if (person && origin.isNotEmpty()) origin else null
val GrantRequest.canAllowAlways: Boolean get() = !person

fun Layout.paneIDs(): List<String> = when (nodeCase) {
    Layout.NodeCase.PANE_ID -> listOf(paneId)
    Layout.NodeCase.SPLIT -> split.childrenList.flatMap { it.paneIDs() }
    else -> emptyList()
}

/** A tab's panes: its splits', then its floating ones. */
fun Tab.allPaneIDs(): List<String> = layout.paneIDs() + floatingList.map { it.paneId }

/** "3m", "1h 4m". */
fun elapsedString(sinceUnixMs: Long, nowMs: Long = System.currentTimeMillis()): String {
    if (sinceUnixMs <= 0) return ""
    val s = maxOf(0L, (nowMs - sinceUnixMs) / 1000)
    return when {
        s < 60 -> "${s}s"
        s < 3600 -> "${s / 60}m"
        else -> "${s / 3600}h ${s % 3600 / 60}m"
    }
}

/** A user key's short fingerprint, as a join card shows it: "3f2a 91c0 7be4 1d05". */
fun keyFingerprint(userID: String): String = userID.removePrefix("us_").chunked(4).joinToString(" ")

/**
 * The coding agents the daemon knows (ADR-0015). A daemon too old to send them
 * knows Claude Code and Codex, with everything.
 */
object AgentCatalog {
    val builtIn = listOf("claude", "codex")

    private val known = mapOf(
        "claude" to ("Claude Code" to "Claude"), "codex" to ("Codex" to "Codex"), "gemini" to ("Gemini CLI" to "Gemini"),
        "opencode" to ("OpenCode" to "OpenCode"), "cursor" to ("Cursor Agent" to "Cursor"), "copilot" to ("GitHub Copilot CLI" to "Copilot"),
        "droid" to ("Factory Droid" to "Droid"), "amp" to ("Amp" to "Amp"), "pi" to ("Pi" to "Pi"), "qwen" to ("Qwen Code" to "Qwen"),
        "crush" to ("Crush" to "Crush"), "grok" to ("Grok CLI" to "Grok"), "kiro" to ("Kiro CLI" to "Kiro"), "goose" to ("Goose" to "Goose"),
        "aider" to ("Aider" to "Aider"), "kilo" to ("Kilo CLI" to "Kilo"), "cline" to ("Cline" to "Cline"), "kimi" to ("Kimi Code" to "Kimi"),
        "antigravity" to ("Antigravity CLI" to "Antigravity"),
    )

    fun resolve(received: List<AgentKind>): List<AgentKind> {
        if (received.isNotEmpty()) return received
        return builtIn.map { id ->
            AgentKind.newBuilder().setId(id).setName(known[id]?.first ?: id).setShortName(known[id]?.second ?: id)
                .setInstalled(true).setCanResume(true).setAgentView(true).setCanSwitch(true).setSchedulable(true)
                .setUsage(true).setLiveUsage(id == "claude").setHooks(true).setSessions(true).setShellMode(true).build()
        }
    }

    fun name(id: String, kinds: List<AgentKind>): String {
        kinds.firstOrNull { it.id == id }?.name?.takeIf { it.isNotEmpty() }?.let { return it }
        if (id.isEmpty()) return "Agent"
        return known[id]?.first ?: id
    }

    fun shortName(id: String, kinds: List<AgentKind>): String {
        kinds.firstOrNull { it.id == id }?.let { k ->
            if (k.shortName.isNotEmpty()) return k.shortName
            if (k.name.isNotEmpty()) return known[id]?.second ?: k.name
        }
        if (id.isEmpty()) return "Agent"
        return known[id]?.second ?: id
    }

    /** The agent a session continues in by default: Claude's in Codex, others' in Claude. */
    fun other(id: String, kinds: List<AgentKind>): String? {
        val k = kinds.firstOrNull { it.id == id } ?: return null
        if (!k.canSwitch) return null
        val target = if (id == "claude") "codex" else "claude"
        return if (kinds.any { it.id == target && it.canSwitch }) target else null
    }

    fun hasAgentView(id: String, kinds: List<AgentKind>) = kinds.firstOrNull { it.id == id }?.agentView ?: false
}
