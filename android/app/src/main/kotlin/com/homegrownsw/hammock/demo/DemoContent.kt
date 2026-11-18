package com.homegrownsw.hammock.demo

import hammock.v1.Hammock.Agent
import hammock.v1.Hammock.AgentBlock
import hammock.v1.Hammock.AgentKind
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.AttentionItem
import hammock.v1.Hammock.Computer
import hammock.v1.Hammock.Computers
import hammock.v1.Hammock.DiffInfo
import hammock.v1.Hammock.EditorInfo
import hammock.v1.Hammock.FloatingPane
import hammock.v1.Hammock.Group
import hammock.v1.Hammock.HammockKeyApprover
import hammock.v1.Hammock.HammockKeyRequest
import hammock.v1.Hammock.HammockKeyStatus
import hammock.v1.Hammock.HostTheme
import hammock.v1.Hammock.Layout
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.PaneKind
import hammock.v1.Hammock.Role
import hammock.v1.Hammock.Schedule
import hammock.v1.Hammock.ScheduleRun
import hammock.v1.Hammock.Split
import hammock.v1.Hammock.StateUpdate
import hammock.v1.Hammock.Tab
import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime

/**
 * The demo's made-up Mac (DemoDaemon): its Groups, panes, schedules, agents'
 * conversations and terminals. As the iPhone's (apple/iOS/Hammock/DemoMac.swift).
 */
internal object DemoContent {
    const val ME = "us_sample_macbook"
    const val STUDIO = "us_sample_studio"
    const val TRIAGE_COMMAND = "gh issue list --label needs-triage --json number,title"

    private const val E = "\u001B["

    fun state(now: Long, macTheme: String? = null, hammockKey: String? = null): StateUpdate {
        fun ago(minutes: Double) = now - (minutes * 60_000).toLong()
        val zone = ZoneId.systemDefault()
        val today = ZonedDateTime.ofInstant(Instant.ofEpochMilli(now), zone)
        /** The day `days` from today at hh:mm. */
        fun at(days: Long, hour: Int, minute: Int) =
            today.plusDays(days).withHour(hour).withMinute(minute).withSecond(0).withNano(0).toInstant().toEpochMilli()

        fun group(id: String, name: String, icon: String, order: Int, tabs: List<String>, host: String = "") =
            Group.newBuilder().setId(id).setName(name).setIcon(icon).setOrder(order).addAllTabIds(tabs).apply {
                if (host.isNotEmpty()) { hostUserId = host; myRole = Role.ROLE_OWNER }
            }.build()
        fun tab(id: String, group: String, name: String, pane: String) =
            Tab.newBuilder().setId(id).setGroupId(group).setName(name)
                .setLayout(Layout.newBuilder().setPaneId(pane)).setFocusedPaneId(pane)
        fun pane(
            id: String, tab: String, agent: AgentState? = null, summary: String = "", since: Double = 5.0, seen: Boolean = false,
            schedule: String = "", run: String = "",
        ) = Pane.newBuilder().setId(id).setTabId(tab).setKind(PaneKind.PANE_KIND_TERMINAL).setCwd("/Users/me/Developer/hammock")
            .setCols(80).setRows(40).setScheduleId(schedule).setScheduleRunId(run).apply {
                if (agent != null) {
                    setAgent(Agent.newBuilder().setHarness("claude").setState(agent).setSummary(summary)
                        .setStateSinceUnixMs(ago(since)).setSeen(seen))
                }
            }

        val u = StateUpdate.newBuilder().setFull(true)
        u.computers = Computers.newBuilder()
            .addList(Computer.newBuilder().setUserId(ME).setName("MacBook").setModel("MacBook Pro").setKind("laptop").setOs("macos")
                .setThisComputer(true).setOnline(true))
            .addList(Computer.newBuilder().setUserId(STUDIO).setName("Studio").setModel("Mac Studio").setKind("studio").setOs("macos")
                .setTint("#7C3AED").setOnline(true))
            .setActiveId(ME).build()
        u.addAllGroups(listOf(
            group("g_notes", "Notes", "🗞️", 0, listOf("t_brief", "t_inbox")),
            group("g_hammock", "Hammock", "🌴", 1, listOf("t_triage", "t_polish", "t_site", "t_shell", "t_backup")),
            group("g_release", "Releases", "📦", 2, listOf("t_notes"), host = STUDIO),
            // Its tabs all closed: what to open next.
            Group.newBuilder().setId("g_units").setName("Units").setIcon("⚖️").setOrder(3).setFolder("/Users/me/Developer/units").build(),
        ))
        u.addAllTabs(listOf(
            tab("t_brief", "g_notes", "Morning briefing", "p_brief").build(),
            tab("t_inbox", "g_notes", "Inbox zero", "p_inbox").build(),
            tab("t_triage", "g_hammock", "Triage new issues", "p_triage").build(),
            tab("t_polish", "g_hammock", "Agent view polish", "p_polish").build(),
            // Several panes, as on a Mac: the agent beside its tests and its
            // diff, and the docs' server floating over them.
            tab("t_site", "g_hammock", "Docs site", "p_site").setLayout(
                Layout.newBuilder().setSplit(Split.newBuilder().setDirection(Split.Direction.DIRECTION_HORIZONTAL)
                    .addAllWeights(listOf(0.55f, 0.45f))
                    .addChildren(Layout.newBuilder().setPaneId("p_site"))
                    .addChildren(Layout.newBuilder().setSplit(Split.newBuilder().setDirection(Split.Direction.DIRECTION_VERTICAL)
                        .addAllWeights(listOf(0.5f, 0.5f))
                        .addChildren(Layout.newBuilder().setPaneId("p_tests"))
                        .addChildren(Layout.newBuilder().setPaneId("p_diff"))))))
                .addFloating(FloatingPane.newBuilder().setPaneId("p_docs"))
                .addFloating(FloatingPane.newBuilder().setPaneId("p_page")).build(),
            tab("t_shell", "g_hammock", "zsh", "p_shell").build(),
            tab("t_backup", "g_hammock", "Back up dotfiles", "p_backup").build(),
            tab("t_notes", "g_release", "Weekly release notes", "p_notes").build(),
        ))
        u.addAllPanes(listOf(
            pane("p_brief", "t_brief", AgentState.AGENT_STATE_IDLE, "Wrote today’s briefing: 3 meetings, 2 threads waiting on you.", since = 95.0,
                schedule = "s_brief", run = "r_brief_today").build(),
            pane("p_inbox", "t_inbox", AgentState.AGENT_STATE_IDLE, "Archived 41 newsletters and drafted two replies.", since = 240.0, seen = true).build(),
            pane("p_triage", "t_triage", AgentState.AGENT_STATE_NEEDS_INPUT, "Bash: $TRIAGE_COMMAND", since = 3.0,
                schedule = "s_triage", run = "r_triage_now").build(),
            pane("p_polish", "t_polish", AgentState.AGENT_STATE_WORKING, "Editing AgentViews.swift", since = 2.0).build(),
            pane("p_site", "t_site", AgentState.AGENT_STATE_IDLE, "The phone’s docs page covers the pane strip now.", since = 12.0, seen = true).build(),
            pane("p_tests", "t_site").addProgram("go").setTitle("go test").setCols(100).setRows(30).build(),
            pane("p_diff", "t_site").setKind(PaneKind.PANE_KIND_DIFF).setDiff(DiffInfo.newBuilder().setRepo("/Users/me/Developer/hammock")
                .setTitle("Uncommitted changes").setBranch("agent-view").setFiles(3).setAdded(142).setRemoved(38).setOpenThreads(1)).build(),
            pane("p_page", "t_site").setKind(PaneKind.PANE_KIND_EDITOR).setEditor(EditorInfo.newBuilder()
                .setPath("/Users/me/Developer/hammock/site/src/content/docs/iphone.md").setName("iphone.md").setDirty(true).setLanguage("markdown")).build(),
            pane("p_docs", "t_site").addAllProgram(listOf("npm", "node")).setTitle("docs").setCols(90).setRows(24).build(),
            pane("p_shell", "t_shell").build(),
            pane("p_backup", "t_backup", schedule = "s_backup", run = "r_backup_y").build(),
            pane("p_notes", "t_notes", AgentState.AGENT_STATE_IDLE, "Release notes for 1.8 are ready to review.", since = 50.0,
                schedule = "s_notes", run = "r_notes_today").build(),
        ))
        u.addAgentKinds(AgentKind.newBuilder().setId("claude").setName("Claude Code").setShortName("Claude").setInstalled(true).setCanResume(true)
            .setAgentView(true).setCanSwitch(true).setSchedulable(true).setUsage(true).setHooks(true).setSessions(true))
        u.addAgentKinds(AgentKind.newBuilder().setId("codex").setName("Codex").setShortName("Codex").setInstalled(true).setCanResume(true)
            .setAgentView(true).setCanSwitch(true).setSchedulable(true).setSessions(true))
        // The Mac's theme (debug: macTheme, or custom), so the phone's Same as Mac has one to follow.
        u.theme = HostTheme.newBuilder().setName(macTheme ?: "Tokyo Night").build()
        u.addAttention(AttentionItem.newBuilder().setId("a_triage").setPaneId("p_triage").setState(AgentState.AGENT_STATE_NEEDS_INPUT)
            .setCreatedUnixMs(ago(3.0)).setSummary("Bash: $TRIAGE_COMMAND"))

        fun run(
            id: String, status: ScheduleRun.Status, started: Long, minutes: Double, note: String = "", tab: String = "", pane: String = "",
            output: String = "", closed: Boolean = false, session: String = "", manual: Boolean = false,
        ) = ScheduleRun.newBuilder().setId(id).setStatus(status).setDueUnixMs(if (manual) 0 else started).setStartedUnixMs(started).apply {
            if (status != ScheduleRun.Status.STATUS_RUNNING && status != ScheduleRun.Status.STATUS_WAITING && status != ScheduleRun.Status.STATUS_MISSED) {
                endedUnixMs = started + (minutes * 60_000).toLong()
            }
            if (status == ScheduleRun.Status.STATUS_MISSED) startedUnixMs = 0
        }.setNote(note).setTabId(tab).setPaneId(pane).setOutput(output).setTabClosed(closed).setSessionId(session).setManual(manual).build()

        u.addSchedules(Schedule.newBuilder().setId("s_brief").setName("Morning briefing").setGroupId("g_notes").setGroupName("Notes").setGroupIcon("🗞️")
            .setKind("claude").setCwd("/Users/me/Notes").addAllWeekdays(listOf(1, 2, 3, 4, 5)).setMinuteOfDay(8 * 60 + 30)
            .setPrompt("Read my calendar, Slack and email since yesterday evening and write today’s briefing to briefing.html, then show it.")
            .setNextRunUnixMs(at(1, 8, 30)).setComputerId(ME).setCreatedBy("You").setCreatedUnixMs(at(-20, 21, 4))
            .addAllAllowedTools(listOf("Bash(gh issue list:*)", "mcp__slack__read"))
            .addRuns(run("r_brief_today", ScheduleRun.Status.STATUS_READY, ago(100.0), 4.0,
                note = "3 meetings today; the 1:1 with Sam moved to 15:00. 2 Slack threads need you.",
                tab = "t_brief", pane = "p_brief", output = "/Users/me/Notes/briefing.html", session = "sess1"))
            .addRuns(run("r_brief_y", ScheduleRun.Status.STATUS_READY, at(-1, 8, 30), 5.0, note = "Quiet day: no meetings before noon.",
                tab = "t_old", closed = true, session = "sess0"))
            .addRuns(run("r_brief_2", ScheduleRun.Status.STATUS_MISSED, at(-2, 8, 30), 0.0, note = "The Mac was asleep"))
            .addRuns(run("r_brief_3", ScheduleRun.Status.STATUS_GAVE_UP, at(-3, 8, 30), 240.0, note = "Waited 4 hours for you to allow Bash",
                tab = "t_old2", closed = true, session = "sess-1")))
        u.addSchedules(Schedule.newBuilder().setId("s_triage").setName("Triage new issues").setGroupId("g_hammock").setGroupName("Hammock").setGroupIcon("🌴")
            .setKind("claude").setCwd("/Users/me/Developer/hammock").setMinuteOfDay(10 * 60)
            .setPrompt("Label and prioritise new GitHub issues.")
            .setNextRunUnixMs(at(1, 10, 0)).setComputerId(ME)
            .addRuns(run("r_triage_now", ScheduleRun.Status.STATUS_WAITING, ago(6.0), 0.0, note = "Asking to run gh", tab = "t_triage", pane = "p_triage")))
        u.addSchedules(Schedule.newBuilder().setId("s_backup").setName("Back up dotfiles").setGroupId("g_hammock").setGroupName("Hammock").setGroupIcon("🌴")
            .setKind("command").setCwd("/Users/me").setMinuteOfDay(18 * 60)
            .setPrompt("rsync -a ~/.config/ /Volumes/Backup/config/")
            .setNextRunUnixMs(now + 3 * 3_600_000L).setSkipUntilUnixMs(now + 3 * 3_600_000L + 60_000L)
            .setComputerId(ME)
            .addRuns(run("r_backup_y", ScheduleRun.Status.STATUS_READY, at(-1, 18, 0), 0.5, tab = "t_backup", pane = "p_backup")))
        u.addSchedules(Schedule.newBuilder().setId("s_notes").setName("Weekly release notes").setGroupId("g_release").setGroupName("Releases").setGroupIcon("📦")
            .setKind("codex").setCwd("/Users/me/Developer/app").addWeekdays(5).setMinuteOfDay(16 * 60)
            .setPrompt("Write this week’s release notes from the merged PRs.")
            .setNextRunUnixMs(at(2, 16, 0)).setComputerId(STUDIO)
            .addRuns(run("r_notes_today", ScheduleRun.Status.STATUS_READY, ago(58.0), 7.0, note = "Release notes for 1.8: 12 fixes, 3 features.",
                tab = "t_notes", pane = "p_notes", output = "https://github.com/homegrownsw/app/releases/new", manual = true)))
        u.addSchedules(Schedule.newBuilder().setId("s_caches").setName("Clear build caches").setGroupId("g_hammock").setGroupName("Hammock").setGroupIcon("🌴")
            .setKind("command").setCwd("/Users/me/Developer/hammock").addWeekdays(0).setMinuteOfDay(3 * 60)
            .setPrompt("just clean").setPaused(true).setComputerId(ME)
            .addRuns(run("r_caches", ScheduleRun.Status.STATUS_FAILED, at(-4, 3, 0), 1.0, note = "Exit code 1: no such recipe", tab = "t_c", closed = true)))

        // Debug: hammockKey "1", the Mac's Hammock Key, this phone allowed to
        // approve it, and a request waiting (its sheet shows); "agent", an
        // agent's ssh login instead.
        if (hammockKey == "1" || hammockKey == "agent") {
            val agent = hammockKey == "agent"
            u.hammockKey = HammockKeyStatus.newBuilder().setExists(true).setAvailable(true)
                .setFingerprint("SHA256:q2Xh0f3m3w2a9cJk1Vt8nZs4yLr6pE5uB7dG0iH1oKc")
                .addApprovers(HammockKeyApprover.newBuilder().setDeviceId("sample").setName("Phone")).build()
            u.addHammockKeyRequests(HammockKeyRequest.newBuilder().setId("fk_sample").setPaneId("p_triage").setPaneLabel("Hammock ▸ Triage").setAgent(agent).apply {
                if (agent) {
                    what = "Log in as me with SSH"; command = "ssh build-box just test"
                    headline = "Claude in Hammock ▸ Triage: Log in as me with SSH (ssh build-box just test)"
                    windowMs = 300_000
                } else {
                    what = "Sign for git (a commit or tag)"; command = "git commit -m Fix the release notes"
                    headline = "A program in Hammock ▸ Triage: Sign for git (a commit or tag) (git commit -m Fix the release notes)"
                    windowMs = 600_000
                }
            }.setDataHash("ab".repeat(32)).setCreatedUnixMs(ago(0.0)).setExpiresUnixMs(now + 120_000))
        }
        return u.build()
    }

    /** When a schedule runs next, from now: its time of day, on one of its weekdays (none: every day). */
    fun nextRun(s: Schedule, now: Long = System.currentTimeMillis()): Long {
        val zone = ZoneId.systemDefault()
        val today = ZonedDateTime.ofInstant(Instant.ofEpochMilli(now), zone).toLocalDate().atStartOfDay(zone)
        for (days in 0L..7L) {
            val day = today.plusDays(days)
            val at = day.plusMinutes(s.minuteOfDay.toLong())
            if (at.toInstant().toEpochMilli() <= now) continue
            val weekday = day.dayOfWeek.value % 7 // 0 Sunday … 6 Saturday
            if (s.weekdaysList.isEmpty() || s.weekdaysList.contains(weekday)) return at.toInstant().toEpochMilli()
        }
        return 0
    }

    /** What the demo's shell answers to a few commands (anything else says the demo doesn't run commands). */
    val shellReplies: Map<String, String> = mapOf(
        "git status" to "On branch main\r\nYour branch is ahead of 'origin/main' by 2 commits.\r\n\r\n" +
            "Changes not staged for commit:\r\n  ${E}31mmodified:   apple/iOS/Settings.swift${E}0m\r\n",
        "git push" to "Enumerating objects: 14, done.\r\nWriting objects: 100% (8/8), 2.31 KiB | 2.31 MiB/s, done.\r\n" +
            "To github.com:acme/widgets.git\r\n   ${E}33m7d3e215..4f2a91c${E}0m  main -> main\r\n",
        "ls" to "${E}1;34mapple${E}0m  ${E}1;34minternal${E}0m  ${E}1;34mrelease-notes${E}0m  justfile  README.md  go.mod\r\n",
        "npm test" to "\r\n> widgets@1.8.0 test\r\n> vitest run\r\n\r\n ${E}32m✓${E}0m src/forecast.test.ts ${E}2m(12 tests)${E}0m\r\n" +
            " ${E}32m✓${E}0m src/theme.test.ts ${E}2m(4 tests)${E}0m\r\n\r\n ${E}1;32mTest Files${E}0m  2 passed\r\n ${E}1;32m     Tests${E}0m  16 passed\r\n",
    )
    const val DEMO_SHELL_NOTE = "\u001B[2m(The demo doesn’t run commands: with your Mac paired, this is its shell.)\u001B[0m\r\n"
    val prompt = "${E}1;32mme@MacBook${E}0m ${E}1;34m~/Developer/hammock${E}0m $ "

    /** The editor pane's file (p_page). */
    val pageText = """
        ---
        title: iPhone
        description: Your Mac's Groups, agents and panes on your phone.
        ---

        ## Groups

        Groups shows each Group with its tabs, as on your Mac.

        A tab with several panes opens on the one you last had open there.
        A strip along the bottom has a pill for each pane: tap one to switch,
        or swipe sideways across a pane.

        The button at the left of the strip opens Exposé: the tab laid out as
        on the Mac, each pane live. Tap one to go to it.

        ## Terminals

        Tap a terminal to bring up the keyboard, with a row above it of the
        keys a phone lacks.
    """.trimIndent()

    /** What a pane's terminal shows. */
    fun terminal(paneID: String): String = when (paneID) {
        "p_shell" -> {
            val ok = "${E}32mok${E}0m"
            prompt + "git log --oneline -4\r\n" +
                "${E}33m4f2a91c${E}0m Agent view: long lines wrap\r\n" +
                "${E}33m9c07e3d${E}0m Settings: a theme for the phone\r\n" +
                "${E}33m1b88f40${E}0m Daemon: quieter logs\r\n" +
                "${E}33m7d3e215${E}0m Groups remember their folder\r\n" +
                prompt + "git status --short\r\n" +
                " ${E}31mM${E}0m apple/iOS/Settings.swift\r\n" +
                " ${E}31mM${E}0m internal/daemon/theme.go\r\n" +
                "${E}32m??${E}0m apple/iOS/PhoneTheme.swift\r\n" +
                prompt + "go test ./internal/...\r\n" +
                "$ok  internal/agents     ${E}2m0.41s${E}0m\r\n" +
                "$ok  internal/daemon     ${E}2m12.4s${E}0m\r\n" +
                "$ok  internal/state      ${E}2m0.18s${E}0m\r\n" +
                "$ok  internal/thread     ${E}2m0.09s${E}0m\r\n" +
                "$ok  internal/tui        ${E}2m0.57s${E}0m\r\n" +
                "$ok  internal/vt         ${E}2m0.02s${E}0m\r\n" +
                prompt + "npm run dev\r\n\r\n" +
                "  ${E}1;32mVITE${E}0m v7.1  ready in ${E}1m312${E}0m ms\r\n\r\n" +
                "  ${E}32m➜${E}0m  Local:   ${E}36mhttp://localhost:5173/${E}0m\r\n" +
                "  ${E}2m➜  Network: use --host to expose${E}0m\r\n\r\n" +
                "${E}2m9:38:02${E}0m ${E}36m[vite]${E}0m page reload ${E}2msrc/App.tsx${E}0m\r\n" +
                "${E}2m9:39:15${E}0m ${E}36m[vite]${E}0m hmr update ${E}2m/src/Forecast.tsx${E}0m\r\n" +
                "${E}2m9:40:47${E}0m ${E}36m[vite]${E}0m hmr update ${E}2m/src/theme.css${E}0m\r\n^C\r\n" + prompt
        }
        "p_tests" -> {
            val ok = "${E}32mok${E}0m"
            prompt + "watchexec -e go -- go test ./internal/agents/...\r\n" +
                "${E}2m[Running: go test ./internal/agents/...]${E}0m\r\n" +
                "$ok  internal/agents          ${E}2m0.44s${E}0m\r\n" +
                "$ok  internal/agents/claude   ${E}2m0.21s${E}0m\r\n" +
                "$ok  internal/agents/codex    ${E}2m0.17s${E}0m\r\n" +
                "${E}2m[Command was successful]${E}0m\r\n" +
                "${E}2m[Running: go test ./internal/agents/...]${E}0m\r\n" +
                "${E}31m--- FAIL${E}0m: TestBlocksWrapLongLines ${E}2m(0.01s)${E}0m\r\n" +
                "    blocks_test.go:88: got 3 rows, want 2\r\n" +
                "${E}31mFAIL${E}0m  internal/agents          ${E}2m0.39s${E}0m\r\n" +
                "${E}2m[Command exited with 1]${E}0m\r\n"
        }
        "p_docs" -> prompt + "npm run docs-dev\r\n\r\n" +
            "  ${E}1;35mastro${E}0m  v5.4 ${E}2mready in 612 ms${E}0m\r\n\r\n" +
            "  ${E}32m┃${E}0m Local    ${E}36mhttp://localhost:4321/hammock/docs${E}0m\r\n\r\n" +
            "${E}2m09:41:12${E}0m ${E}36m[watch]${E}0m src/content/docs/iphone.md\r\n" +
            "${E}2m09:41:12${E}0m ${E}32m[200]${E}0m /hammock/docs/iphone/ ${E}2m18ms${E}0m\r\n"
        "p_backup" -> "${E}1;32mme@MacBook${E}0m ${E}1;34m~${E}0m $ rsync -a ~/.config/ /Volumes/Backup/config/\r\n" +
            "${E}2msent 18.2 MB  received 4.1 kB  12.1 MB/sec${E}0m\r\n" +
            "${E}1;32mme@MacBook${E}0m ${E}1;34m~${E}0m $ "
        else -> "${E}38;5;209m✻${E}0m ${E}1mClaude Code${E}0m\r\n\r\n" +
            "${E}2m  /Users/me/Developer/hammock${E}0m\r\n\r\n" +
            "${E}2m  Easier to read in the agent view: the speech bubble above.${E}0m\r\n\r\n" +
            "╭──────────────────────────────────────────────╮\r\n" +
            "│ >                                            │\r\n" +
            "╰──────────────────────────────────────────────╯\r\n"
    }

    fun block(
        id: String, kind: AgentBlock.Kind, text: String = "", title: String = "", tool: String = "",
        input: String = "", output: String = "", error: Boolean = false, pending: Boolean = false,
        diff: String = "", added: Int = 0, removed: Int = 0, minutesAgo: Double = 0.0,
    ): AgentBlock = AgentBlock.newBuilder().setId(id).setKind(kind).setText(text).setTitle(title)
        .setTool(if (tool.isEmpty() && kind == AgentBlock.Kind.KIND_TOOL) "Bash" else tool)
        .setInput(input).setOutput(output).setError(error).setPending(pending)
        .setDiff(diff).setAdded(added).setRemoved(removed)
        .setTimeUnixMs(System.currentTimeMillis() - (minutesAgo * 60_000).toLong())
        .build()

    /** A turn's end: how long it worked, done `minutesAgo`. */
    fun done(id: String, workedMinutes: Double, minutesAgo: Double): AgentBlock =
        block(id, AgentBlock.Kind.KIND_DONE, minutesAgo = minutesAgo).toBuilder().setDurationMs((workedMinutes * 60_000).toLong()).build()

    private val USER = AgentBlock.Kind.KIND_USER
    private val TEXT = AgentBlock.Kind.KIND_TEXT
    private val TOOL = AgentBlock.Kind.KIND_TOOL
    private val THINKING = AgentBlock.Kind.KIND_THINKING

    /** Each agent's conversation so far. */
    fun transcripts(): MutableMap<String, MutableList<AgentBlock>> = mutableMapOf(
        "p_brief" to mutableListOf(
            block("b1", USER, text = "Read my calendar, Slack and email since yesterday evening and write today’s briefing to briefing.html, then show it.", minutesAgo = 100.0),
            block("b2", TOOL, title = "Read today’s calendar", tool = "mcp__calendar__list_events", input = "today", output = "3 events", minutesAgo = 99.0),
            block("b3", TOOL, title = "Searched Slack", tool = "mcp__slack__search", input = "to:me after:yesterday", output = "2 threads waiting on a reply", minutesAgo = 98.0),
            block("b4", TOOL, title = "Wrote briefing.html", tool = "Write", input = "/Users/me/Notes/briefing.html", added = 86, minutesAgo = 97.0),
            block("b5", TOOL, title = "Showed briefing.html", tool = "mcp__hammock__show_output", input = "briefing.html", output = "Opened in a browser pane", minutesAgo = 96.0),
            block("b6", TEXT, text = "Today’s briefing is in **briefing.html**:\n\n- **3 meetings.** The 1:1 with Sam moved to 15:00.\n- **2 Slack threads** need you: the launch checklist and Priya’s question about the API.\n- Nothing urgent in email.", minutesAgo = 95.0),
            done("b7", workedMinutes = 4.6, minutesAgo = 95.0),
        ),
        "p_inbox" to mutableListOf(
            block("b1", USER, text = "Clear out my inbox: archive the newsletters, and draft replies to anything that needs one.", minutesAgo = 250.0),
            block("b2", TOOL, title = "Searched mail", tool = "mcp__mail__search", input = "in:inbox", output = "58 messages", minutesAgo = 249.0),
            block("b3", TOOL, title = "Archived 41 newsletters", tool = "mcp__mail__archive", input = "41 messages", minutesAgo = 246.0),
            block("b4", TEXT, text = "Archived 41 newsletters and drafted two replies, to Priya about Thursday and to the landlord about the boiler. They’re in Drafts for you to check before they go.", minutesAgo = 240.0),
            done("b5", workedMinutes = 9.8, minutesAgo = 240.0),
        ),
        "p_site" to mutableListOf(
            block("b1", USER, text = "Document the iPhone’s new pane strip on the docs site, and keep the tests passing.", minutesAgo = 20.0),
            block("b2", TOOL, title = "Edited iphone.md", tool = "Edit", input = "/Users/me/Developer/hammock/site/src/content/docs/iphone.md", added = 24, removed = 9, minutesAgo = 16.0),
            block("b3", TOOL, title = "Ran `go test ./internal/agents/...`", input = "go test ./internal/agents/...", output = "FAIL TestBlocksWrapLongLines", error = true, minutesAgo = 14.0),
            block("b4", TEXT, text = "The iPhone page now covers the pane strip and Exposé. One test is failing, **TestBlocksWrapLongLines**, which is from the agent view work in the next tab, not this change.", minutesAgo = 12.0),
            done("b5", workedMinutes = 8.1, minutesAgo = 12.0),
        ),
        "p_triage" to mutableListOf(
            block("b1", USER, text = "Label and prioritise new GitHub issues.", minutesAgo = 6.0),
            block("b2", TEXT, text = "I’ll start by listing the issues that need triage.", minutesAgo = 4.0),
            block("b_triage_gh", TOOL, title = "Run `gh issue list`", input = TRIAGE_COMMAND, pending = true, minutesAgo = 3.0),
        ),
        "p_polish" to mutableListOf(
            block("b1", USER, text = "The agent view’s code blocks scroll sideways on the phone. Make long lines wrap, and keep the copy button in reach.", minutesAgo = 9.0),
            block("b2", TOOL, title = "Read AgentViews.swift", tool = "Read", input = "apple/iOS/Hammock/AgentViews.swift", output = "412 lines", minutesAgo = 8.0),
            block("b3", TOOL, title = "Edited AgentViews.swift", tool = "Edit", input = "apple/iOS/Hammock/AgentViews.swift",
                diff = "@@ -88,9 +88,11 @@\n-        ScrollView(.horizontal) {\n-            Text(code).font(.mono)\n-        }\n+        Text(code)\n+            .font(.mono)\n+            .lineLimit(nil)\n+            .fixedSize(horizontal: false, vertical: true)\n+            .overlay(alignment: .topTrailing) { CopyButton(code) }",
                added = 12, removed = 4, minutesAgo = 3.0),
            block("b4", TOOL, title = "Run `swift build`", input = "swift build", pending = true, minutesAgo = 2.0),
        ),
        "p_notes" to mutableListOf(
            block("b1", USER, text = "Write this week’s release notes from the merged PRs.", minutesAgo = 58.0),
            block("b2", TOOL, title = "Ran `gh pr list`", input = "gh pr list --state merged --limit 50", output = "15 pull requests", minutesAgo = 57.0),
            block("b3", TOOL, title = "Wrote RELEASE_NOTES.md", tool = "Write", input = "RELEASE_NOTES.md", added = 34, minutesAgo = 52.0),
            block("b4", TEXT, text = "Release notes for 1.8 are ready in **RELEASE_NOTES.md**: 12 fixes and 3 features, with the new offline mode first. I’ve opened a draft release for you to review.", minutesAgo = 51.0),
            done("b5", workedMinutes = 6.9, minutesAgo = 51.0),
        ),
    )

    /** A long reply, as Claude's often are: a list, a few paragraphs. */
    fun longReply(n: Int): String = """
        Here's what I found ($n):

        - **Size:** Nerd Fonts are about 2 MB per style, which is fine. Iosevka's collection files are about 43 MB each, so I'd cap the size (say 20 MB per family) and send only the styles the terminal uses: regular, bold, italic and bold italic.
        - **Offline:** once downloaded, a font stays on the phone, so it keeps working when the Mac is away.

        **My suggestion:** do both, in this order:

        1. **Bundle:** Symbols Nerd Font Mono as the fallback for every font, plus Obel Mono, JetBrains Mono, Hack, Fira Code and Cascadia Code. This helps everyone, even without the Mac connected.
        2. **Mac fonts:** add the phone feature as its own change on top.

        Shall I start on the bundle in a worktree, and open an issue for the Mac fonts, or build both?
    """.trimIndent()

    /**
     * Many turns (a page's worth, as a Mac sends: 150 blocks), of rows of very
     * different heights: long messages and replies, code, edits standing on
     * their own, narration, short answers. For the longChat debug hook.
     */
    fun longTranscript(prefix: String = "L"): List<AgentBlock> {
        val out = ArrayList<AgentBlock>()
        val code = "```swift\n" + (0 until 40).joinToString("\n") { "let value$it = compute($it) // a line of code that is long enough to wrap on a phone" } + "\n```"
        var turn = 0
        while (out.size < 150) {
            val ago = (40 - turn) * 3.0 + if (prefix == "L") 0.0 else 150.0
            out += block("${prefix}${turn}_u", USER, text = if (turn % 3 == 0)
                "Have a look at how fonts reach the phone. Could we bundle the popular ones, and could the phone fetch the Mac's own fonts when it's connected? ".repeat(1 + turn % 4)
            else "OK, carry on.", minutesAgo = ago)
            out += block("${prefix}${turn}_t", THINKING, minutesAgo = ago - 0.1)
            for (i in 0 until 1 + turn % 5) {
                out += block("${prefix}${turn}_tool$i", TOOL, title = "Ran `grep -rn font`", input = "grep -rn font apple/", output = "${10 + i} matches", minutesAgo = ago - 0.2)
            }
            if (turn % 4 == 1) {
                out += block("${prefix}${turn}_say", THINKING, text = "Committing now.", minutesAgo = ago - 0.3)
                out += block("${prefix}${turn}_edit", TOOL, title = "Edited Fonts.swift", tool = "Edit", input = "apple/Sources/HammockKit/Fonts.swift",
                    diff = "@@ -1,3 +1,4 @@\n-old\n+new\n+newer", added = 2, removed = 1, minutesAgo = ago - 0.3)
            }
            out += when (turn % 3) {
                0 -> block("${prefix}${turn}_r", TEXT, text = longReply(turn) + "\n\n" + code, minutesAgo = ago - 1)
                1 -> block("${prefix}${turn}_r", TEXT, text = "Done.", minutesAgo = ago - 1)
                else -> block("${prefix}${turn}_r", TEXT, text = longReply(turn), minutesAgo = ago - 1)
            }
            out += done("${prefix}${turn}_d", workedMinutes = 2.0, minutesAgo = ago - 1)
            turn += 1
        }
        return out
    }
}
