import Foundation
import HammockKit

/// The demo: a made-up Mac, for trying the app with none paired (and for App
/// Review). It stands in for the daemon (DaemonStandIn), so the app runs as it
/// would against a Mac: Groups on it and on a linked computer, agents working,
/// waiting and done, their conversations and terminals, and scheduled tasks
/// with runs. Allowing the waiting agent lets it finish; a message to an agent
/// gets an answer saying this is a demo; what would change the Mac (a new
/// Group, tab or agent) says to pair one. Nothing leaves the phone.
///
/// Dev aid: HMK_DEBUG_SAMPLE=1 (in the simulator, SIMCTL_CHILD_HMK_DEBUG_SAMPLE=1
/// with `simctl launch`) opens straight into it, for screenshots.
final class DemoMac: DaemonStandIn, @unchecked Sendable {
    static let me = "us_sample_macbook"
    static let studio = "us_sample_studio"
    /// What the app hears when it asks the demo for something only a Mac can do.
    static let needsMac = "That needs your Mac: this is a demo. Pair your Mac to do it for real."

    #if DEBUG
    static var launchedInto: Bool { ProcessInfo.processInfo.environment["HMK_DEBUG_SAMPLE"] == "1" }
    #endif

    /// Dev aid for the App Store screenshots (marketing/scripts/appstore-screenshots): HMK_DEBUG_SCREENSHOT=1 hides
    /// the demo's bar, and once the app has its state and has settled, writes "HammockScreenshot: done" to stderr,
    /// which is when the picture is taken. Debug builds only.
    static var screenshotMode: Bool {
        #if DEBUG
        ProcessInfo.processInfo.environment["HMK_DEBUG_SCREENSHOT"] == "1"
        #else
        false
        #endif
    }

    /// Says the screen has settled (screenshotMode), a moment after the state arrives: long enough for a pushed
    /// screen, its conversation and its terminal to draw.
    @MainActor static func reportSettled() {
        guard screenshotMode else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 2.5) {
            FileHandle.standardError.write(Data("HammockScreenshot: done\n".utf8))
        }
    }

    /// Everything happens on `q`, in order, as on a daemon's connection.
    private let q = DispatchQueue(label: "hammock.demo")
    private var state: Hammock_V1_StateUpdate
    private var transcripts: [String: [AgentBlock]]
    private var prompts: [String: AgentPrompt] = [:]
    private var working: [String: String] = [:]  // pane: what it says it's doing
    private var watched: Set<String> = []
    private var typed = ""  // the demo shell's line so far
    private var reply: (@Sendable (ServerMessage) -> Void)?

    init() {
        let now = Date()
        state = Self.state(now: now)
        transcripts = Self.transcripts(now: now)
        prompts["p_triage"] = .with {
            $0.kind = .permission; $0.blockID = "b_triage_gh"; $0.title = "Do you want to proceed?"
            $0.options = ["Yes", "Yes, and don’t ask again for gh issue list commands", "No, and tell Claude what to do differently"]
            $0.selected = 1; $0.other = 3
        }
        working["p_polish"] = "Building…"
        #if DEBUG
        if Self.longChat { transcripts["p_polish"] = Self.longTranscript(); working["p_polish"] = nil }
        if let first = Self.replay?.first { transcripts["p_polish"] = first.view.blocks; working["p_polish"] = nil }
        #endif
    }

    func receive(_ msg: ClientMessage, reply: @escaping @Sendable (ServerMessage) -> Void) {
        q.async { self.reply = reply; self.handle(msg) }
    }

    // MARK: Answering

    private func send(_ m: ServerMessage) { reply?(m) }

    private func answer(_ msg: ClientMessage, _ build: (inout ServerMessage) -> Void = { _ in }) {
        guard msg.id != 0 else { return }
        var m = ServerMessage()
        m.replyTo = msg.id
        build(&m)
        send(m)
    }

    private func later(_ seconds: Double, _ work: @escaping () -> Void) {
        q.asyncAfter(deadline: .now() + seconds, execute: work)
    }

    private func handle(_ msg: ClientMessage) {
        switch msg.msg {
        case .hello:
            answer(msg) { $0.helloReply = .with { $0.protocolVersion = 1; $0.hostName = "MacBook"; $0.userID = Self.me; $0.daemonVersion = "demo" } }
        case .subscribe:
            answer(msg) { $0.state = state }
        case .attachPane(let a):
            let (cols, rows) = sizes[a.paneID] ?? (80, 40)
            snapshot(a.paneID, cols: cols, rows: rows)
        case .resize(let r):
            // As a Mac does: the pane takes the size the phone asks for, and is drawn again at it.
            sizes[r.paneID] = (r.cols, r.rows)
            snapshot(r.paneID, cols: r.cols, rows: r.rows)
        case .input(let i) where i.paneID == "p_shell":
            shellTyped(i.data)
        case .watchAgent(let w):
            guard w.watch, w.subagentID.isEmpty, w.shellID.isEmpty else { return }
            if !w.beforeBlockID.isEmpty {
                var older: [AgentBlock] = []
                #if DEBUG
                // The long conversation (HMK_DEBUG_LONG_CHAT) has a page of history before it.
                if Self.longChat, w.paneID == "p_polish" { older = Self.longTranscript(prefix: "O") }
                #endif
                send(.with { $0.agentView = .with { $0.paneID = w.paneID; $0.older = true; $0.blocks = older } })
                return
            }
            watched.insert(w.paneID)
            sendView(w.paneID, replace: true)
        case .agentAnswer(let a):
            answer(msg)
            agentAnswered(a)
        case .agentSend(let s):
            answer(msg)
            agentSent(s.paneID, s.text)
        case .clearAttention_p(let c):
            update { $0.clearedAttentionIds = [c.id] }
            state.attention.removeAll { $0.id == c.id }
        case .listFolders(let l):
            // Start an Agent: somewhere to start, and the agents it has.
            let path = l.path.isEmpty ? "/Users/me" : l.path
            answer(msg) {
                $0.folderList = .with {
                    $0.path = path; $0.parent = (path as NSString).deletingLastPathComponent; $0.home = "/Users/me"
                    $0.folders = path == "/Users/me" ? ["Developer", "Documents", "Notes"] : []
                    $0.suggestions = [.with { $0.path = "/Users/me/Developer/hammock"; $0.why = "This Group" }]
                    $0.agents = ["claude", "codex"]
                }
            }
        case .setSchedulePaused(let p):
            answer(msg)
            schedule(p.scheduleID) {
                $0.paused = p.paused
                $0.nextRunUnixMs = p.paused ? 0 : Self.nextRun($0)
            }
        case .skipScheduleRun(let k):
            guard let s = state.schedules.first(where: { $0.id == k.scheduleID }), !k.skip || s.nextRunUnixMs > 0 else {
                answer(msg) { $0.error = .with { $0.message = "It isn’t due to run." } }
                return
            }
            answer(msg)
            schedule(k.scheduleID) { $0.skipUntilUnixMs = k.skip ? $0.nextRunUnixMs + 1 : 0 }
        case .moveTab(let m):
            answer(msg)
            moveTab(m.tabID, to: m.groupID, index: Int(m.index))
        case .setGroupIcon(let g):
            answer(msg)
            guard let i = state.groups.firstIndex(where: { $0.id == g.groupID }) else { return }
            state.groups[i].icon = g.icon
            let group = state.groups[i]
            update { $0.groups = [group] }
        case .focus(let f):
            answer(msg)
            focused(f.paneID)
        case .sleepPane(let sp):
            // As the Mac would: only an agent at rest sleeps.
            guard let i = state.panes.firstIndex(where: { $0.id == sp.paneID }), sp.wake || state.panes[i].canSleep else {
                answer(msg) { $0.error = .with { $0.message = "its agent can't sleep now: it's working" } }
                return
            }
            answer(msg)
            state.panes[i].asleep = !sp.wake
            let p = state.panes[i]
            update { $0.panes = [p] }
        case .createGroup, .createTab, .createPane, .startAgent, .closeTab, .closeGroup, .closePane, .resumeSession,
             .runSchedule, .saveSchedule, .deleteSchedule, .uploadFile, .createPairing, .createInvite, .linkComputer,
             .reopenClosed, .renameGroup, .renameTab, .switchAgent, .setGroupIconImage, .setGroupFolder, .leaveShared:
            answer(msg) { $0.error = .with { $0.message = Self.needsMac } }
        default:
            // Everything else (sizes, focus, presence, lists) is fine as it is.
            answer(msg)
        }
    }

    // MARK: Terminals

    private var sizes: [String: (UInt32, UInt32)] = [:]

    private func snapshot(_ paneID: String, cols: UInt32, rows: UInt32) {
        let text = paneID == "p_shell" ? Self.terminal(paneID) + typed : Self.terminal(paneID)
        send(.with { $0.paneSnapshot = .with { $0.paneID = paneID; $0.cols = cols; $0.rows = rows; $0.data = Data(text.utf8) } })
    }

    // MARK: State

    private func update(_ build: (inout Hammock_V1_StateUpdate) -> Void) {
        var u = Hammock_V1_StateUpdate()
        build(&u)
        send(.with { $0.state = u })
    }

    private func setAgent(_ paneID: String, _ s: AgentState, summary: String) {
        guard let i = state.panes.firstIndex(where: { $0.id == paneID }) else { return }
        state.panes[i].agent.state = s
        state.panes[i].agent.summary = summary
        state.panes[i].agent.stateSinceUnixMs = Int64(Date().timeIntervalSince1970 * 1000)
        state.panes[i].agent.seen = false
        let p = state.panes[i]
        update { $0.panes = [p] }
    }

    private func setRun(_ runID: String, _ status: ScheduleRun.Status, note: String) {
        guard let si = state.schedules.firstIndex(where: { $0.runs.contains { $0.id == runID } }),
              let ri = state.schedules[si].runs.firstIndex(where: { $0.id == runID }) else { return }
        state.schedules[si].runs[ri].status = status
        state.schedules[si].runs[ri].note = note
        if status == .ready { state.schedules[si].runs[ri].endedUnixMs = Int64(Date().timeIntervalSince1970 * 1000) }
        let s = state.schedules[si]
        update { $0.schedules = [s] }
    }

    private func schedule(_ id: String, _ change: (inout Schedule) -> Void) {
        guard let i = state.schedules.firstIndex(where: { $0.id == id }) else { return }
        change(&state.schedules[i])
        let s = state.schedules[i]
        update { $0.schedules = [s] }
    }

    /// When a schedule runs next, from now: its time of day, on one of its weekdays (none: every day).
    private static func nextRun(_ s: Schedule, now: Date = Date()) -> Int64 {
        let cal = Calendar.current
        let today = cal.startOfDay(for: now)
        for days in 0...7 {
            guard let day = cal.date(byAdding: .day, value: days, to: today),
                  let at = cal.date(byAdding: .minute, value: Int(s.minuteOfDay), to: day), at > now else { continue }
            if s.weekdays.isEmpty || s.weekdays.contains(Int32(cal.component(.weekday, from: day) - 1)) {
                return Int64(at.timeIntervalSince1970 * 1000)
            }
        }
        return 0
    }

    /// As the daemon does: a tab moved within its Group, or to another.
    private func moveTab(_ tabID: String, to groupID: String, index: Int) {
        guard let ti = state.tabs.firstIndex(where: { $0.id == tabID }) else { return }
        let dest = groupID.isEmpty ? state.tabs[ti].groupID : groupID
        guard let di = state.groups.firstIndex(where: { $0.id == dest }) else { return }
        var changed: [WorkspaceGroup] = []
        if let si = state.groups.firstIndex(where: { $0.id == state.tabs[ti].groupID }) {
            state.groups[si].tabIds.removeAll { $0 == tabID }
            if si != di { changed.append(state.groups[si]) }
        }
        state.groups[di].tabIds.insert(tabID, at: max(0, min(index, state.groups[di].tabIds.count)))
        state.tabs[ti].groupID = dest
        changed.append(state.groups[di])
        let t = state.tabs[ti]
        update { $0.groups = changed; $0.tabs = [t] }
    }

    /// As the daemon does: looking at a finished agent acknowledges it (its
    /// checkmark, and its item in Needs You, go away).
    private func focused(_ paneID: String) {
        guard let i = state.panes.firstIndex(where: { $0.id == paneID }), state.panes[i].hasAgent,
              !state.panes[i].agent.seen, state.panes[i].agent.state == .idle else { return }
        state.panes[i].agent.seen = true
        let p = state.panes[i]
        let cleared = state.attention.filter { $0.paneID == paneID && $0.state == .idle }.map(\.id)
        state.attention.removeAll { cleared.contains($0.id) }
        update { $0.panes = [p]; $0.clearedAttentionIds = cleared }
    }

    // MARK: Agents

    private func sendView(_ paneID: String, replace: Bool = false, blocks: [AgentBlock]? = nil) {
        guard watched.contains(paneID) else { return }
        send(.with {
            $0.agentView = .with {
                $0.paneID = paneID
                $0.replace = replace
                $0.blocks = blocks ?? transcripts[paneID] ?? []
                $0.sessionID = "demo-" + paneID
                $0.model = "claude-opus-5-5"
                $0.effort = "high"
                if let p = prompts[paneID] { $0.prompt = p }
                if let w = working[paneID] { $0.working = true; $0.status = w }
                #if DEBUG
                if replace, Self.longChat, paneID == "p_polish" { $0.hasMore_p = true }
                #endif
            }
        })
    }

    private func add(_ paneID: String, _ b: AgentBlock) {
        transcripts[paneID, default: []].removeAll { $0.id == b.id }
        transcripts[paneID, default: []].append(b)
        sendView(paneID, blocks: [b])
    }

    private func agentAnswered(_ a: Hammock_V1_AgentAnswer) {
        guard a.paneID == "p_triage", prompts["p_triage"] != nil else { return }
        prompts["p_triage"] = nil
        state.attention.removeAll { $0.id == "a_triage" }
        update { $0.clearedAttentionIds = ["a_triage"] }
        let denied = a.decision == .deny || a.option == 3
        guard !denied else {
            add("p_triage", Self.block("b_triage_gh", .tool, title: "Ran `gh issue list`", input: Self.triageCommand,
                                       output: "The user doesn’t want to proceed with this tool use.", error: true))
            add("p_triage", Self.block("b_triage_stop", .text, text: "OK, I won’t run that. Tell me what you’d like instead."))
            setAgent("p_triage", .idle, summary: "Stopped: you said no to gh issue list")
            setRun("r_triage_now", .ready, note: "Stopped when you said no")
            return
        }
        working["p_triage"] = "Labelling issues…"
        add("p_triage", Self.block("b_triage_gh", .tool, title: "Ran `gh issue list`", input: Self.triageCommand,
                                   output: "[{\"number\":214,\"title\":\"Crash when a pane closes mid-paste\"},\n {\"number\":215,\"title\":\"Dark mode: code blocks too dim\"},\n {\"number\":216,\"title\":\"Add a shortcut to jump to the waiting agent\"}]"))
        setAgent("p_triage", .working, summary: "Labelling issues…")
        setRun("r_triage_now", .running, note: "Labelling issues")
        later(1.5) {
            self.add("p_triage", Self.block("b_triage_label", .tool, title: "Ran `gh issue edit` 3 times",
                                            input: "gh issue edit 214 --add-label bug,priority:high",
                                            output: "https://github.com/acme/widgets/issues/214\nhttps://github.com/acme/widgets/issues/215\nhttps://github.com/acme/widgets/issues/216"))
        }
        later(3) {
            self.working["p_triage"] = nil
            self.add("p_triage", Self.block("b_triage_done", .text, text: """
                Triaged the 3 new issues:

                - **#214** Crash when a pane closes mid-paste: bug, high priority. It loses work, so it's first.
                - **#215** Dark mode code blocks too dim: bug, medium.
                - **#216** Shortcut to the waiting agent: enhancement, low.
                """))
            self.setAgent("p_triage", .idle, summary: "Triaged 3 issues: one high-priority crash.")
            self.setRun("r_triage_now", .ready, note: "Triaged 3 issues: one high-priority crash.")
        }
    }

    private func agentSent(_ paneID: String, _ text: String) {
        let n = (transcripts[paneID]?.count ?? 0) + 1
        #if DEBUG
        if paneID == "p_polish", let replay = Self.replay { return play(paneID, replay.dropFirst()) }
        if Self.longChat, paneID == "p_polish" { return replyWithTools(paneID, text, n) }
        #endif
        add(paneID, Self.block("b_you_\(n)", .user, text: text))
        working[paneID] = "Thinking…"
        sendView(paneID, blocks: [])
        setAgent(paneID, .working, summary: "Thinking…")
        later(2) {
            self.working[paneID] = nil
            self.add(paneID, Self.block("b_demo_\(n)", .text, text: """
                This is Hammock's demo, so there's no agent here to do that. With your Mac paired, \
                your message goes to the agent running on it, and you see its work here as it happens.
                """))
            self.setAgent(paneID, .idle, summary: "Answered in the demo")
        }
    }

    #if DEBUG
    /// Dev aid: HMK_DEBUG_LONG_CHAT=1 gives the polish agent (p_polish) a long conversation, and answers a message
    /// to it as Claude does: thinking, then a run of tool calls arriving one after another, then a reply. For
    /// checking the agent view following the end through a long conversation (docs/debug-hooks.md).
    static var longChat: Bool { ProcessInfo.processInfo.environment["HMK_DEBUG_LONG_CHAT"] == "1" }

    /// Dev aid: HMK_DEBUG_REPLAY=<file> (made by scripts/agentblocks from a real Claude session) gives the polish
    /// agent that session's history, and a message to it plays the rest at the session's own pace, as a Mac
    /// watching it would send them.
    static let replay: [(ms: Int, view: Hammock_V1_AgentView)]? = {
        guard let path = ProcessInfo.processInfo.environment["HMK_DEBUG_REPLAY"],
              let text = try? String(contentsOfFile: path, encoding: .utf8) else { return nil }
        return text.split(separator: "\n").compactMap { line in
            let parts = line.split(separator: "\t", maxSplits: 1)
            guard parts.count == 2, let ms = Int(parts[0]),
                  let v = try? Hammock_V1_AgentView(jsonString: String(parts[1])) else { return nil }
            return (ms, v)
        }
    }()

    private func play(_ paneID: String, _ events: ArraySlice<(ms: Int, view: Hammock_V1_AgentView)>) {
        working[paneID] = "Contemplating…"
        setAgent(paneID, .working, summary: "Contemplating…")
        var t = 0.0
        for (i, e) in events.enumerated() {
            t += Double(e.ms) / 1000
            later(t) { for b in e.view.blocks { self.add(paneID, b) } }
        }
        later(t + 0.5) {
            self.working[paneID] = nil
            self.sendView(paneID, blocks: [])
            self.setAgent(paneID, .idle, summary: "Done")
        }
    }

    private func replyWithTools(_ paneID: String, _ text: String, _ n: Int) {
        add(paneID, Self.block("b_you_\(n)", .user, text: text))
        working[paneID] = "Contemplating…"
        sendView(paneID, blocks: [])
        setAgent(paneID, .working, summary: "Contemplating…")
        var t = 0.4
        let thinking = "b_think_\(n)"
        later(t) { self.add(paneID, Self.block(thinking, .thinking)) }
        for i in 0..<12 {
            let id = "b_tool_\(n)_\(i)", file = "apple/Sources/HammockKit/File\(i).swift"
            t += 0.35
            later(t) { self.add(paneID, Self.block(id, .tool, title: "Read \(file)", tool: "Read", input: file, pending: true)) }
            t += 0.3
            later(t) { self.add(paneID, Self.block(id, .tool, title: "Read \(file)", tool: "Read", input: file, output: "\(120 + i * 17) lines")) }
            if i == 5 {
                t += 0.3
                later(t) { self.add(paneID, Self.block("b_say_\(n)", .thinking, text: "Now the scroll view itself.")) }
            }
        }
        t += 0.6
        later(t) {
            self.working[paneID] = nil
            // "medium" in the message: a reply a little taller than what shows above the keyboard.
            let reply = text.contains("medium") ? Self.longReply(n) : Self.longReply(n) + "\n\n" + Self.longReply(n + 1)
            self.add(paneID, Self.block("b_reply_\(n)", .text, text: reply))
            self.setAgent(paneID, .idle, summary: "Answered")
        }
    }

    /// A long reply, as Claude's often are: a list, a few paragraphs.
    static func longReply(_ n: Int) -> String {
        """
        Here's what I found (\(n)):

        - **Size:** Nerd Fonts are about 2 MB per style, which is fine. Iosevka's collection files are about \
        43 MB each, so I'd cap the size (say 20 MB per family) and send only the styles the terminal uses: \
        regular, bold, italic and bold italic.
        - **Offline:** once downloaded, a font stays on the phone, so it keeps working when the Mac is away.

        **My suggestion:** do both, in this order:

        1. **Bundle:** Symbols Nerd Font Mono as the fallback for every font, plus Obel Mono, JetBrains Mono, \
        Hack, Fira Code and Cascadia Code. This helps everyone, even without the Mac connected.
        2. **Mac fonts:** add the phone feature as its own change on top.

        Shall I start on the bundle in a worktree, and open an issue for the Mac fonts, or build both?
        """
    }

    /// Many turns (a page's worth, as a Mac sends: 150 blocks), of rows of very different heights: long
    /// messages and replies, code, edits standing on their own, narration, short answers.
    static func longTranscript(prefix: String = "L") -> [AgentBlock] {
        var out: [AgentBlock] = []
        let code = "```swift\n" + (0..<40).map { "let value\($0) = compute(\($0)) // a line of code that is long enough to wrap on a phone" }.joined(separator: "\n") + "\n```"
        var turn = 0
        while out.count < 150 {
            let ago = Double(40 - turn) * 3 + (prefix == "L" ? 0 : 150)
            out.append(block("\(prefix)\(turn)_u", .user, text: turn % 3 == 0
                ? String(repeating: "Have a look at how fonts reach the phone. Could we bundle the popular ones, and could the phone fetch the Mac's own fonts when it's connected? ", count: 1 + turn % 4)
                : "OK, carry on.", minutesAgo: ago))
            out.append(block("\(prefix)\(turn)_t", .thinking, minutesAgo: ago - 0.1))
            for i in 0..<(1 + turn % 5) {
                out.append(block("\(prefix)\(turn)_tool\(i)", .tool, title: "Ran `grep -rn font`", input: "grep -rn font apple/", output: "\(10 + i) matches", minutesAgo: ago - 0.2))
            }
            if turn % 4 == 1 {
                out.append(block("\(prefix)\(turn)_say", .thinking, text: "Committing now.", minutesAgo: ago - 0.3))
                out.append(block("\(prefix)\(turn)_edit", .tool, title: "Edited Fonts.swift", tool: "Edit", input: "apple/Sources/HammockKit/Fonts.swift",
                                 diff: "@@ -1,3 +1,4 @@\n-old\n+new\n+newer", added: 2, removed: 1, minutesAgo: ago - 0.3))
            }
            switch turn % 3 {
            case 0: out.append(block("\(prefix)\(turn)_r", .text, text: longReply(turn) + "\n\n" + code, minutesAgo: ago - 1))
            case 1: out.append(block("\(prefix)\(turn)_r", .text, text: "Done.", minutesAgo: ago - 1))
            default: out.append(block("\(prefix)\(turn)_r", .text, text: longReply(turn), minutesAgo: ago - 1))
            }
            out.append(done("\(prefix)\(turn)_d", workedMinutes: 2, minutesAgo: ago - 1))
            turn += 1
        }
        return out
    }
    #endif

    // MARK: The shell

    private func shellTyped(_ data: Data) {
        var out = ""
        for c in String(decoding: data, as: UTF8.self) {
            switch c {
            case "\r", "\n":
                let line = typed.trimmingCharacters(in: .whitespaces)
                typed = ""
                out += "\r\n"
                if let canned = Self.shellReplies[line] {
                    out += canned
                } else if !line.isEmpty {
                    out += "\u{1B}[2m(The demo doesn’t run commands: with your Mac paired, this is its shell.)\u{1B}[0m\r\n"
                }
                out += Self.prompt
            case "\u{7F}", "\u{08}":
                if !typed.isEmpty { typed.removeLast(); out += "\u{08} \u{08}" }
            default:
                if let v = c.asciiValue, v < 0x20 { continue } // other control keys do nothing here
                typed.append(c)
                out.append(c)
            }
        }
        guard !out.isEmpty else { return }
        send(.with { $0.paneOutput = .with { $0.paneID = "p_shell"; $0.data = Data(out.utf8) } })
    }

    // MARK: Made up

    static func state(now: Date) -> Hammock_V1_StateUpdate {
        let ms = { (d: Date) in Int64(d.timeIntervalSince1970 * 1000) }
        let ago = { (minutes: Double) in ms(now.addingTimeInterval(-minutes * 60)) }
        let cal = Calendar.current
        /// The day `days` from today at hh:mm.
        let at = { (days: Int, hour: Int, minute: Int) -> Int64 in
            let day = cal.date(byAdding: .day, value: days, to: now) ?? now
            return ms(cal.date(bySettingHour: hour, minute: minute, second: 0, of: day) ?? day)
        }

        func group(_ id: String, _ name: String, _ icon: String, order: Int32, tabs: [String], host: String = "") -> WorkspaceGroup {
            .with {
                $0.id = id; $0.name = name; $0.icon = icon; $0.order = order; $0.tabIds = tabs
                if !host.isEmpty { $0.hostUserID = host; $0.myRole = .owner }
            }
        }
        func tab(_ id: String, _ group: String, _ name: String, pane: String) -> WorkspaceTab {
            .with { $0.id = id; $0.groupID = group; $0.name = name; $0.layout = .with { $0.paneID = pane }; $0.focusedPaneID = pane }
        }
        func pane(_ id: String, _ tab: String, agent: AgentState? = nil, summary: String = "", since: Double = 5, seen: Bool = false,
                  schedule: String = "", run: String = "") -> Pane {
            .with {
                $0.id = id; $0.tabID = tab; $0.kind = .terminal; $0.cwd = "/Users/me/Developer/hammock"
                $0.cols = 80; $0.rows = 40
                $0.scheduleID = schedule; $0.scheduleRunID = run
                if let agent {
                    $0.agent = .with { $0.harness = "claude"; $0.state = agent; $0.summary = summary; $0.stateSinceUnixMs = ago(since); $0.seen = seen }
                }
            }
        }

        var u = Hammock_V1_StateUpdate()
        u.full = true
        u.computers = .with {
            $0.list = [
                .with { $0.userID = me; $0.name = "MacBook"; $0.model = "MacBook Pro"; $0.kind = "laptop"; $0.os = "macos"; $0.thisComputer = true; $0.online = true },
                .with { $0.userID = studio; $0.name = "Studio"; $0.model = "Mac Studio"; $0.kind = "studio"; $0.os = "macos"; $0.tint = "#7C3AED"; $0.online = true },
            ]
            $0.activeID = me
        }
        u.groups = [
            group("g_notes", "Notes", "🗞️", order: 0, tabs: ["t_brief", "t_inbox"]),
            group("g_hammock", "Hammock", "🌴", order: 1, tabs: ["t_triage", "t_polish", "t_shell", "t_backup"]),
            group("g_release", "Releases", "📦", order: 2, tabs: ["t_notes"], host: studio),
            // Its tabs all closed: what to open next.
            .with { $0.id = "g_units"; $0.name = "Units"; $0.icon = "⚖️"; $0.order = 3; $0.folder = "/Users/me/Developer/units" },
        ]
        u.tabs = [
            tab("t_brief", "g_notes", "Morning briefing", pane: "p_brief"),
            tab("t_inbox", "g_notes", "Inbox zero", pane: "p_inbox"),
            tab("t_triage", "g_hammock", "Triage new issues", pane: "p_triage"),
            tab("t_polish", "g_hammock", "Agent view polish", pane: "p_polish"),
            tab("t_shell", "g_hammock", "zsh", pane: "p_shell"),
            tab("t_backup", "g_hammock", "Back up dotfiles", pane: "p_backup"),
            tab("t_notes", "g_release", "Weekly release notes", pane: "p_notes"),
        ]
        u.panes = [
            pane("p_brief", "t_brief", agent: .idle, summary: "Wrote today’s briefing: 3 meetings, 2 threads waiting on you.", since: 95,
                 schedule: "s_brief", run: "r_brief_today"),
            pane("p_inbox", "t_inbox", agent: .idle, summary: "Archived 41 newsletters and drafted two replies.", since: 240, seen: true),
            pane("p_triage", "t_triage", agent: .needsInput, summary: "Bash: gh issue list --label needs-triage --json number,title", since: 3,
                 schedule: "s_triage", run: "r_triage_now"),
            pane("p_polish", "t_polish", agent: .working, summary: "Editing AgentViews.swift", since: 2),
            pane("p_shell", "t_shell"),
            pane("p_backup", "t_backup", schedule: "s_backup", run: "r_backup_y"),
            pane("p_notes", "t_notes", agent: .idle, summary: "Release notes for 1.8 are ready to review.", since: 50,
                 schedule: "s_notes", run: "r_notes_today"),
        ]
        u.agentKinds = [
            .with { $0.id = "claude"; $0.name = "Claude Code"; $0.shortName = "Claude"; $0.installed = true; $0.canResume = true
                $0.agentView = true; $0.canSwitch = true; $0.schedulable = true; $0.usage = true; $0.hooks = true; $0.sessions = true },
            .with { $0.id = "codex"; $0.name = "Codex"; $0.shortName = "Codex"; $0.installed = true; $0.canResume = true
                $0.agentView = true; $0.canSwitch = true; $0.schedulable = true; $0.sessions = true },
        ]
        u.theme = .with { $0.name = "Tokyo Night" }
        #if DEBUG
        // The Mac's theme (HMK_DEBUG_MAC_THEME=<name>, or custom), so the
        // phone's Same as Mac has one to follow.
        u.theme = .with { $0.name = ProcessInfo.processInfo.environment["HMK_DEBUG_MAC_THEME"] ?? "Tokyo Night" }
        #endif
        u.attention = [
            .with { $0.id = "a_triage"; $0.paneID = "p_triage"; $0.state = .needsInput; $0.createdUnixMs = ago(3)
                $0.summary = "Bash: gh issue list --label needs-triage --json number,title" },
        ]

        func run(_ id: String, _ status: ScheduleRun.Status, started: Int64, minutes: Double, note: String = "", tab: String = "", pane: String = "",
                 output: String = "", closed: Bool = false, session: String = "", manual: Bool = false) -> ScheduleRun {
            .with {
                $0.id = id; $0.status = status; $0.dueUnixMs = manual ? 0 : started; $0.startedUnixMs = started
                if status != .running, status != .waiting, status != .missed { $0.endedUnixMs = started + Int64(minutes * 60_000) }
                if status == .missed { $0.startedUnixMs = 0 }
                $0.note = note; $0.tabID = tab; $0.paneID = pane; $0.output = output; $0.tabClosed = closed
                $0.sessionID = session; $0.manual = manual
            }
        }
        u.schedules = [
            .with {
                $0.id = "s_brief"; $0.name = "Morning briefing"; $0.groupID = "g_notes"; $0.groupName = "Notes"; $0.groupIcon = "🗞️"
                $0.kind = "claude"; $0.cwd = "/Users/me/Notes"; $0.weekdays = [1, 2, 3, 4, 5]; $0.minuteOfDay = 8 * 60 + 30
                $0.prompt = "Read my calendar, Slack and email since yesterday evening and write today’s briefing to briefing.html, then show it."
                $0.nextRunUnixMs = at(1, 8, 30); $0.computerID = me; $0.createdBy = "You"; $0.createdUnixMs = at(-20, 21, 4)
                $0.allowedTools = ["Bash(gh issue list:*)", "mcp__slack__read"]
                $0.runs = [
                    run("r_brief_today", .ready, started: ago(100), minutes: 4, note: "3 meetings today; the 1:1 with Sam moved to 15:00. 2 Slack threads need you.",
                        tab: "t_brief", pane: "p_brief", output: "/Users/me/Notes/briefing.html", session: "sess1"),
                    run("r_brief_y", .ready, started: at(-1, 8, 30), minutes: 5, note: "Quiet day: no meetings before noon.", tab: "t_old", closed: true, session: "sess0"),
                    run("r_brief_2", .missed, started: at(-2, 8, 30), minutes: 0, note: "The Mac was asleep"),
                    run("r_brief_3", .gaveUp, started: at(-3, 8, 30), minutes: 240, note: "Waited 4 hours for you to allow Bash", tab: "t_old2", closed: true, session: "sess-1"),
                ]
            },
            .with {
                $0.id = "s_triage"; $0.name = "Triage new issues"; $0.groupID = "g_hammock"; $0.groupName = "Hammock"; $0.groupIcon = "🌴"
                $0.kind = "claude"; $0.cwd = "/Users/me/Developer/hammock"; $0.minuteOfDay = 10 * 60
                $0.prompt = "Label and prioritise new GitHub issues."
                $0.nextRunUnixMs = at(1, 10, 0); $0.computerID = me
                $0.runs = [run("r_triage_now", .waiting, started: ago(6), minutes: 0, note: "Asking to run gh", tab: "t_triage", pane: "p_triage")]
            },
            .with {
                $0.id = "s_backup"; $0.name = "Back up dotfiles"; $0.groupID = "g_hammock"; $0.groupName = "Hammock"; $0.groupIcon = "🌴"
                $0.kind = "command"; $0.cwd = "/Users/me"; $0.minuteOfDay = 18 * 60
                $0.prompt = "rsync -a ~/.config/ /Volumes/Backup/config/"
                $0.nextRunUnixMs = ms(now.addingTimeInterval(3 * 3600)); $0.skipUntilUnixMs = ms(now.addingTimeInterval(3 * 3600 + 60))
                $0.computerID = me
                $0.runs = [run("r_backup_y", .ready, started: at(-1, 18, 0), minutes: 0.5, tab: "t_backup", pane: "p_backup")]
            },
            .with {
                $0.id = "s_notes"; $0.name = "Weekly release notes"; $0.groupID = "g_release"; $0.groupName = "Releases"; $0.groupIcon = "📦"
                $0.kind = "codex"; $0.cwd = "/Users/me/Developer/app"; $0.weekdays = [5]; $0.minuteOfDay = 16 * 60
                $0.prompt = "Write this week’s release notes from the merged PRs."
                $0.nextRunUnixMs = at(2, 16, 0); $0.computerID = studio
                $0.runs = [run("r_notes_today", .ready, started: ago(58), minutes: 7, note: "Release notes for 1.8: 12 fixes, 3 features.",
                               tab: "t_notes", pane: "p_notes", output: "https://github.com/homegrownsw/app/releases/new", manual: true)]
            },
            .with {
                $0.id = "s_caches"; $0.name = "Clear build caches"; $0.groupID = "g_hammock"; $0.groupName = "Hammock"; $0.groupIcon = "🌴"
                $0.kind = "command"; $0.cwd = "/Users/me/Developer/hammock"; $0.weekdays = [0]; $0.minuteOfDay = 3 * 60
                $0.prompt = "just clean"; $0.paused = true; $0.computerID = me
                $0.runs = [run("r_caches", .failed, started: at(-4, 3, 0), minutes: 1, note: "Exit code 1: no such recipe", tab: "t_c", closed: true)]
            },
        ]
        #if DEBUG
        // HMK_DEBUG_HMK_KEY=1: the Mac's Hammock Key, this phone allowed to
        // approve it, and a request waiting (its sheet shows); =agent, an
        // agent's ssh login instead.
        let keyDebug = ProcessInfo.processInfo.environment["HMK_DEBUG_HMK_KEY"]
        if keyDebug == "1" || keyDebug == "agent" {
            let agent = keyDebug == "agent"
            u.hammockKey = .with {
                $0.exists = true; $0.available = true; $0.fingerprint = "SHA256:q2Xh0f3m3w2a9cJk1Vt8nZs4yLr6pE5uB7dG0iH1oKc"
                $0.approvers = [.with { $0.deviceID = "sample"; $0.name = "iPhone" }]
            }
            u.hammockKeyRequests = [.with {
                $0.id = "fk_sample"; $0.paneID = "p_triage"; $0.paneLabel = "Hammock ▸ Triage"; $0.agent = agent
                if agent {
                    $0.what = "Log in as me with SSH"; $0.command = "ssh build-box just test"
                    $0.headline = "Claude in Hammock ▸ Triage: Log in as me with SSH (ssh build-box just test)"
                    $0.windowMs = 300_000
                } else {
                    $0.what = "Sign for git (a commit or tag)"; $0.command = "git commit -m Fix the release notes"
                    $0.headline = "A program in Hammock ▸ Triage: Sign for git (a commit or tag) (git commit -m Fix the release notes)"
                    $0.windowMs = 600_000
                }
                $0.dataHash = String(repeating: "ab", count: 32); $0.createdUnixMs = ago(0); $0.expiresUnixMs = ms(now.addingTimeInterval(120))
            }]
        }
        #endif
        return u
    }

    static let triageCommand = "gh issue list --label needs-triage --json number,title"

    /// What the demo's shell answers to a few commands (anything else says the demo doesn't run commands).
    static let shellReplies: [String: String] = [
        "git status": "On branch main\r\nYour branch is ahead of 'origin/main' by 2 commits.\r\n\r\n"
            + "Changes not staged for commit:\r\n  \(e)31mmodified:   apple/iOS/Settings.swift\(e)0m\r\n",
        "git push": "Enumerating objects: 14, done.\r\nWriting objects: 100% (8/8), 2.31 KiB | 2.31 MiB/s, done.\r\n"
            + "To github.com:acme/widgets.git\r\n   \(e)33m7d3e215..4f2a91c\(e)0m  main -> main\r\n",
        "ls": "\(e)1;34mapple\(e)0m  \(e)1;34minternal\(e)0m  \(e)1;34mrelease-notes\(e)0m  justfile  README.md  go.mod\r\n",
        "npm test": "\r\n> widgets@1.8.0 test\r\n> vitest run\r\n\r\n \(e)32m✓\(e)0m src/forecast.test.ts \(e)2m(12 tests)\(e)0m\r\n"
            + " \(e)32m✓\(e)0m src/theme.test.ts \(e)2m(4 tests)\(e)0m\r\n\r\n \(e)1;32mTest Files\(e)0m  2 passed\r\n \(e)1;32m     Tests\(e)0m  16 passed\r\n",
    ]
    private static let e = "\u{1B}["
    static let prompt = "\(e)1;32mme@MacBook\(e)0m \(e)1;34m~/Developer/hammock\(e)0m $ "

    /// What a pane's terminal shows.
    static func terminal(_ paneID: String) -> String {
        switch paneID {
        case "p_shell":
            let ok = "\(e)32mok\(e)0m"
            return prompt + "git log --oneline -4\r\n"
                + "\(e)33m4f2a91c\(e)0m Agent view: long lines wrap\r\n"
                + "\(e)33m9c07e3d\(e)0m Settings: a theme for the phone\r\n"
                + "\(e)33m1b88f40\(e)0m Daemon: quieter logs\r\n"
                + "\(e)33m7d3e215\(e)0m Groups remember their folder\r\n"
                + prompt + "git status --short\r\n"
                + " \(e)31mM\(e)0m apple/iOS/Settings.swift\r\n"
                + " \(e)31mM\(e)0m internal/daemon/theme.go\r\n"
                + "\(e)32m??\(e)0m apple/iOS/PhoneTheme.swift\r\n"
                + prompt + "go test ./internal/...\r\n"
                + "\(ok)  internal/agents     \(e)2m0.41s\(e)0m\r\n"
                + "\(ok)  internal/daemon     \(e)2m12.4s\(e)0m\r\n"
                + "\(ok)  internal/state      \(e)2m0.18s\(e)0m\r\n"
                + "\(ok)  internal/thread     \(e)2m0.09s\(e)0m\r\n"
                + "\(ok)  internal/tui        \(e)2m0.57s\(e)0m\r\n"
                + "\(ok)  internal/vt         \(e)2m0.02s\(e)0m\r\n"
                + prompt + "npm run dev\r\n\r\n"
                + "  \(e)1;32mVITE\(e)0m v7.1  ready in \(e)1m312\(e)0m ms\r\n\r\n"
                + "  \(e)32m➜\(e)0m  Local:   \(e)36mhttp://localhost:5173/\(e)0m\r\n"
                + "  \(e)2m➜  Network: use --host to expose\(e)0m\r\n\r\n"
                + "\(e)2m9:38:02\(e)0m \(e)36m[vite]\(e)0m page reload \(e)2msrc/App.tsx\(e)0m\r\n"
                + "\(e)2m9:39:15\(e)0m \(e)36m[vite]\(e)0m hmr update \(e)2m/src/Forecast.tsx\(e)0m\r\n"
                + "\(e)2m9:40:47\(e)0m \(e)36m[vite]\(e)0m hmr update \(e)2m/src/theme.css\(e)0m\r\n^C\r\n" + prompt
        case "p_backup":
            return "\(e)1;32mme@MacBook\(e)0m \(e)1;34m~\(e)0m $ rsync -a ~/.config/ /Volumes/Backup/config/\r\n"
                + "\(e)2msent 18.2 MB  received 4.1 kB  12.1 MB/sec\(e)0m\r\n"
                + "\(e)1;32mme@MacBook\(e)0m \(e)1;34m~\(e)0m $ "
        default:
            return "\(e)38;5;209m✻\(e)0m \(e)1mClaude Code\(e)0m\r\n\r\n"
                + "\(e)2m  /Users/me/Developer/hammock\(e)0m\r\n\r\n"
                + "\(e)2m  Easier to read in the agent view: the speech bubble above.\(e)0m\r\n\r\n"
                + "╭──────────────────────────────────────────────╮\r\n"
                + "│ >                                            │\r\n"
                + "╰──────────────────────────────────────────────╯\r\n"
        }
    }

    static func block(_ id: String, _ kind: AgentBlock.Kind, text: String = "", title: String = "", tool: String = "",
                      input: String = "", output: String = "", error: Bool = false, pending: Bool = false,
                      diff: String = "", added: Int32 = 0, removed: Int32 = 0, minutesAgo: Double = 0) -> AgentBlock {
        .with {
            $0.id = id; $0.kind = kind; $0.text = text; $0.title = title; $0.tool = tool.isEmpty && kind == .tool ? "Bash" : tool
            $0.input = input; $0.output = output; $0.error = error; $0.pending = pending
            $0.diff = diff; $0.added = added; $0.removed = removed
            $0.timeUnixMs = Int64(Date().addingTimeInterval(-minutesAgo * 60).timeIntervalSince1970 * 1000)
        }
    }

    /// A turn's end: how long it worked, done `minutesAgo`.
    static func done(_ id: String, workedMinutes: Double, minutesAgo: Double) -> AgentBlock {
        var b = block(id, .done, minutesAgo: minutesAgo)
        b.durationMs = Int64(workedMinutes * 60_000)
        return b
    }

    /// Each agent's conversation so far.
    static func transcripts(now: Date) -> [String: [AgentBlock]] {
        [
            "p_brief": [
                block("b1", .user, text: "Read my calendar, Slack and email since yesterday evening and write today’s briefing to briefing.html, then show it.", minutesAgo: 100),
                block("b2", .tool, title: "Read today’s calendar", tool: "mcp__calendar__list_events", input: "today", output: "3 events", minutesAgo: 99),
                block("b3", .tool, title: "Searched Slack", tool: "mcp__slack__search", input: "to:me after:yesterday", output: "2 threads waiting on a reply", minutesAgo: 98),
                block("b4", .tool, title: "Wrote briefing.html", tool: "Write", input: "/Users/me/Notes/briefing.html", added: 86, minutesAgo: 97),
                block("b5", .tool, title: "Showed briefing.html", tool: "mcp__hammock__show_output", input: "briefing.html", output: "Opened in a browser pane", minutesAgo: 96),
                block("b6", .text, text: "Today’s briefing is in **briefing.html**:\n\n- **3 meetings.** The 1:1 with Sam moved to 15:00.\n- **2 Slack threads** need you: the launch checklist and Priya’s question about the API.\n- Nothing urgent in email.", minutesAgo: 95),
                done("b7", workedMinutes: 4.6, minutesAgo: 95),
            ],
            "p_inbox": [
                block("b1", .user, text: "Clear out my inbox: archive the newsletters, and draft replies to anything that needs one.", minutesAgo: 250),
                block("b2", .tool, title: "Searched mail", tool: "mcp__mail__search", input: "in:inbox", output: "58 messages", minutesAgo: 249),
                block("b3", .tool, title: "Archived 41 newsletters", tool: "mcp__mail__archive", input: "41 messages", minutesAgo: 246),
                block("b4", .text, text: "Archived 41 newsletters and drafted two replies, to Priya about Thursday and to the landlord about the boiler. They’re in Drafts for you to check before they go.", minutesAgo: 240),
                done("b5", workedMinutes: 9.8, minutesAgo: 240),
            ],
            "p_triage": [
                block("b1", .user, text: "Label and prioritise new GitHub issues.", minutesAgo: 6),
                block("b2", .text, text: "I’ll start by listing the issues that need triage.", minutesAgo: 4),
                block("b_triage_gh", .tool, title: "Run `gh issue list`", input: triageCommand, pending: true, minutesAgo: 3),
            ],
            "p_polish": [
                block("b1", .user, text: "The agent view’s code blocks scroll sideways on the phone. Make long lines wrap, and keep the copy button in reach.", minutesAgo: 9),
                block("b2", .tool, title: "Read AgentViews.swift", tool: "Read", input: "apple/iOS/Hammock/AgentViews.swift", output: "412 lines", minutesAgo: 8),
                block("b3", .tool, title: "Edited AgentViews.swift", tool: "Edit", input: "apple/iOS/Hammock/AgentViews.swift",
                      diff: "@@ -88,9 +88,11 @@\n-        ScrollView(.horizontal) {\n-            Text(code).font(.mono)\n-        }\n+        Text(code)\n+            .font(.mono)\n+            .lineLimit(nil)\n+            .fixedSize(horizontal: false, vertical: true)\n+            .overlay(alignment: .topTrailing) { CopyButton(code) }",
                      added: 12, removed: 4, minutesAgo: 3),
                block("b4", .tool, title: "Run `swift build`", input: "swift build", pending: true, minutesAgo: 2),
            ],
            "p_notes": [
                block("b1", .user, text: "Write this week’s release notes from the merged PRs.", minutesAgo: 58),
                block("b2", .tool, title: "Ran `gh pr list`", input: "gh pr list --state merged --limit 50", output: "15 pull requests", minutesAgo: 57),
                block("b3", .tool, title: "Wrote RELEASE_NOTES.md", tool: "Write", input: "RELEASE_NOTES.md", added: 34, minutesAgo: 52),
                block("b4", .text, text: "Release notes for 1.8 are ready in **RELEASE_NOTES.md**: 12 fixes and 3 features, with the new offline mode first. I’ve opened a draft release for you to review.", minutesAgo: 51),
                done("b5", workedMinutes: 6.9, minutesAgo: 51),
            ],
        ]
    }
}
