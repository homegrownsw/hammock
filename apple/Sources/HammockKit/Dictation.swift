import AVFoundation
import Foundation
import Observation
import Speech
#if os(macOS)
import AppKit
#else
import UIKit
#endif

extension AgentViewPrefs {
    /// The composer's dictation button (and the menus' Dictate), on this device.
    public static let dictation = "agent.dictation"
}

extension Pref {
    public static let agentDictation = Pref("agent-dictation", defaults: AgentViewPrefs.dictation, true)
}

// MARK: - What it hears, as text

/// The rules for turning what the recogniser hears into the composer's
/// text: phrases joined, and "slash review PR" made the command it means.
public enum DictationText {
    /// One phrase after another: a space between them, unless one is
    /// already there or the next starts with punctuation.
    public static func join(_ a: String, _ b: String) -> String {
        if a.isEmpty { return String(b.drop(while: \.isWhitespace)) }
        if b.isEmpty { return a }
        if a.last!.isWhitespace || b.first!.isWhitespace || b.first!.isPunctuation { return a + b }
        return a + " " + b
    }

    /// Whether text going in after `before` needs a space first (it doesn't
    /// at the start, after a space or line break, or when it starts with
    /// punctuation).
    public static func needsSpace(after before: Character?, for text: String) -> Bool {
        guard let before, let first = text.first else { return false }
        return !before.isWhitespace && !first.isWhitespace && !first.isPunctuation
    }

    /// The words for the recogniser to listen for: the commands as you'd
    /// say them ("/review-pr" is "review pr"), a skill's own name without
    /// its plugin's.
    public static func vocabulary(commands: [String]) -> [String] {
        var seen = Set<String>(), out: [String] = []
        for c in commands {
            var name = c.hasPrefix("/") ? String(c.dropFirst()) : c
            if let colon = name.lastIndex(of: ":") { name = String(name[name.index(after: colon)...]) }
            let said = name.replacingOccurrences(of: "-", with: " ").replacingOccurrences(of: "_", with: " ")
            if !said.isEmpty, seen.insert(said.lowercased()).inserted { out.append(said) }
            if out.count == 100 { break }
        }
        return out
    }

    /// A spoken slash command made the command: "slash review PR" or
    /// "/review PR" (the recogniser writes "slash" as a slash, often joined
    /// to the word before) becomes "/review-pr" when that's one of
    /// `commands`, matched ignoring case, spaces, hyphens and underscores.
    /// Anything else is left as heard.
    public static func mapSlashCommands(_ text: String, commands: [String]) -> String {
        guard !commands.isEmpty, text.contains("/") || text.range(of: "slash", options: .caseInsensitive) != nil else { return text }
        // Each command by its key; a plugin's skill also by its own name.
        var byKey: [String: String] = [:]
        for c in commands where c.hasPrefix("/") {
            let name = String(c.dropFirst())
            byKey[key(name)] = byKey[key(name)] ?? c
            if let colon = name.lastIndex(of: ":") {
                let own = key(String(name[name.index(after: colon)...]))
                byKey[own] = byKey[own] ?? c
            }
        }
        let ns = text as NSString
        var out = ""
        var at = 0          // copied into out up to here
        var searchFrom = 0  // where to look for the next slash
        var changed = false
        while searchFrom < ns.length,
              let m = slashPattern.firstMatch(in: text, range: NSRange(location: searchFrom, length: ns.length - searchFrom)) {
            let marker = m.range(at: 1), wordsRange = m.range(at: 2)
            // The words after it, up to another "slash" (that's the next command).
            var words: [NSRange] = []
            for w in wordPattern.matches(in: text, range: wordsRange).map(\.range) {
                if ns.substring(with: w).lowercased() == "slash" { break }
                words.append(w)
            }
            // The longest run of words (up to four) that names a command.
            var hit: (command: String, end: Int)?
            for n in stride(from: min(4, words.count), through: 1, by: -1) {
                let spoken = words[0..<n].map { ns.substring(with: $0) }.joined()
                if let c = byKey[key(spoken)] {
                    hit = (c, NSMaxRange(words[n - 1]))
                    break
                }
            }
            guard let hit else {
                searchFrom = NSMaxRange(marker)
                continue
            }
            out += ns.substring(with: NSRange(location: at, length: marker.location - at))
            // Its own word: a space before it unless at the start or after one.
            if let last = out.last, !last.isWhitespace { out += " " }
            out += hit.command
            at = hit.end
            searchFrom = hit.end
            changed = true
        }
        guard changed else { return text }
        return out + ns.substring(from: at)
    }

    /// "slash" as a word, or a slash (at the start, after a space or joined
    /// to a word), then the words after it.
    private static let slashPattern = try! NSRegularExpression(
        pattern: "(\\bslash\\s+|(?<![/\\w])/|(?<=\\w)/)([A-Za-z0-9][\\w:-]*(?:\\s+[A-Za-z0-9][\\w:-]*){0,3})",
        options: [.caseInsensitive])
    private static let wordPattern = try! NSRegularExpression(pattern: "[A-Za-z0-9][\\w:-]*")

    private static func key(_ s: String) -> String {
        s.lowercased().filter { $0.isLetter || $0.isNumber }
    }
}

// MARK: - The engine

/// What a dictation engine reports while it listens.
enum DictationEvent: Sendable {
    /// Everything heard so far: what's settled, and what may still change.
    case text(final: String, volatile: String)
    /// How loud the microphone is, 0 to 1.
    case level(Double)
    /// The speech model for the language is downloading (0 to 1).
    case installing(Double)
    /// Listening has started (after any download).
    case listening
}

enum DictationError: LocalizedError {
    case microphoneDenied
    case unsupportedLanguage
    case noMicrophone

    var errorDescription: String? {
        switch self {
        case .microphoneDenied: "Microphone access is off for Hammock."
        case .unsupportedLanguage: "Dictation isn't available in your language yet."
        case .noMicrophone: "There's no microphone to listen with."
        }
    }
}

/// Something that turns the microphone into text: Apple's speech models
/// for now (another engine, WhisperKit say, would be one of these too).
@MainActor
protocol DictationEngine: AnyObject {
    /// Asks for the microphone, gets the language's model, and listens,
    /// reporting as it goes until finish or cancel.
    func start(vocabulary: [String], events: @escaping @MainActor (DictationEvent) -> Void) async throws
    /// Stops listening and waits for the last words to settle; returns all
    /// that was heard.
    func finish() async -> String
    /// Stops at once, dropping anything not yet reported.
    func cancel()
}

/// Apple's on-device dictation model (SpeechAnalyzer's DictationTranscriber).
@MainActor
final class AppleDictation: DictationEngine {
    private var analyzer: SpeechAnalyzer?
    private var mic: MicrophoneTap?
    private var results: Task<Void, Never>?
    private var input: AsyncStream<AnalyzerInput>.Continuation?
    private var finalText = ""
    private var volatileText = ""
    private var stopped = false

    func start(vocabulary: [String], events: @escaping @MainActor (DictationEvent) -> Void) async throws {
        guard await Self.microphoneAllowed() else { throw DictationError.microphoneDenied }
        // The system's language (Locale.current follows the app's own
        // localisations, English only), else English.
        let system = Locale(identifier: Locale.preferredLanguages.first ?? "en-US")
        var found = await DictationTranscriber.supportedLocale(equivalentTo: system)
        if found == nil { found = await DictationTranscriber.supportedLocale(equivalentTo: Locale(identifier: "en-US")) }
        guard let locale = found else { throw DictationError.unsupportedLanguage }
        let transcriber = DictationTranscriber(locale: locale, contentHints: [], transcriptionOptions: [.punctuation],
                                               reportingOptions: [.volatileResults, .frequentFinalization], attributeOptions: [])
        if let request = try await AssetInventory.assetInstallationRequest(supporting: [transcriber]) {
            events(.installing(0))
            let progress = request.progress
            let watch = Task { @MainActor in
                while !Task.isCancelled {
                    events(.installing(progress.fractionCompleted))
                    try? await Task.sleep(for: .milliseconds(250))
                }
            }
            defer { watch.cancel() }
            try await request.downloadAndInstall()
        }
        if stopped { return }
        guard let format = await SpeechAnalyzer.bestAvailableAudioFormat(compatibleWith: [transcriber]) else {
            throw DictationError.unsupportedLanguage
        }
        if stopped { return }
        let analyzer = SpeechAnalyzer(modules: [transcriber])
        self.analyzer = analyzer
        try await analyzer.prepareToAnalyze(in: format)
        if !vocabulary.isEmpty {
            let context = AnalysisContext()
            context.contextualStrings[.general] = vocabulary
            try? await analyzer.setContext(context)
        }
        // Stopped while it was getting ready: nothing to hear.
        if stopped {
            await analyzer.cancelAndFinishNow()
            return
        }

        results = Task { @MainActor [weak self] in
            do {
                for try await r in transcriber.results {
                    guard let self, !self.stopped || r.isFinal else { continue }
                    let heard = String(r.text.characters)
                    if r.isFinal {
                        self.finalText = DictationText.join(self.finalText, heard)
                        self.volatileText = ""
                    } else {
                        self.volatileText = heard
                    }
                    if !self.stopped { events(.text(final: self.finalText, volatile: self.volatileText)) }
                }
            } catch {}
        }

        let (stream, continuation) = AsyncStream<AnalyzerInput>.makeStream()
        input = continuation
        #if os(iOS)
        // Recording only while dictating; the app's audio as it was after
        // (a video attachment plays through the same session).
        let session = AVAudioSession.sharedInstance()
        previousAudio = (session.category, session.mode, session.categoryOptions)
        try session.setCategory(.record, mode: .default)
        try session.setActive(true)
        #endif
        do {
            let mic = try MicrophoneTap(format: format, input: continuation) { level in
                Task { @MainActor in events(.level(level)) }
            }
            self.mic = mic
            try await analyzer.start(inputSequence: stream)
        } catch {
            cancel()
            throw error
        }
        if !stopped { events(.listening) }
    }

    #if os(iOS)
    private var previousAudio: (AVAudioSession.Category, AVAudioSession.Mode, AVAudioSession.CategoryOptions)?
    #endif

    /// How long to wait for the last words to settle before keeping them as heard.
    private static let settleTimeout: Duration = .seconds(2)

    func finish() async -> String {
        guard !stopped else { return DictationText.join(finalText, volatileText) }
        stopped = true
        stopAudio()
        if let analyzer {
            // The last words, but not for long: they're kept as heard if the
            // model doesn't settle them in time (or hadn't started).
            let results = self.results
            let settled = await withCheckedContinuation { (done: CheckedContinuation<Bool, Never>) in
                var resumed = false
                func resume(_ v: Bool) {
                    guard !resumed else { return }
                    resumed = true
                    done.resume(returning: v)
                }
                Task { @MainActor in
                    try? await analyzer.finalizeAndFinishThroughEndOfInput()
                    await results?.value
                    resume(true)
                }
                Task { @MainActor in
                    try? await Task.sleep(for: Self.settleTimeout)
                    resume(false)
                }
            }
            if !settled {
                results?.cancel()
                Task { await analyzer.cancelAndFinishNow() }
            }
        }
        // Anything still unsettled is kept as heard.
        let all = DictationText.join(finalText, volatileText)
        finalText = all
        volatileText = ""
        return all
    }

    func cancel() {
        guard !stopped else { return }
        stopped = true
        stopAudio()
        results?.cancel()
        if let analyzer { Task { await analyzer.cancelAndFinishNow() } }
    }

    private func stopAudio() {
        mic?.stop()
        mic = nil
        input?.finish()
        input = nil
        #if os(iOS)
        if let (category, mode, options) = previousAudio {
            let session = AVAudioSession.sharedInstance()
            try? session.setActive(false, options: .notifyOthersOnDeactivation)
            try? session.setCategory(category, mode: mode, options: options)
            previousAudio = nil
        }
        #endif
    }

    static func microphoneAllowed() async -> Bool {
        #if os(macOS)
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized: return true
        case .notDetermined: return await AVCaptureDevice.requestAccess(for: .audio)
        default: return false
        }
        #else
        switch AVAudioApplication.shared.recordPermission {
        case .granted: return true
        case .undetermined: return await AVAudioApplication.requestRecordPermission()
        default: return false
        }
        #endif
    }
}

/// Hears nothing (debug snapshots of a dictation in progress).
@MainActor
final class SilentEngine: DictationEngine {
    func start(vocabulary: [String], events: @escaping @MainActor (DictationEvent) -> Void) async throws {}
    func finish() async -> String { "" }
    func cancel() {}
}

/// The microphone, converted to the model's format and fed to it, with its
/// level. Its tap runs on the audio thread, so it isn't main-actor code.
private final class MicrophoneTap: @unchecked Sendable {
    private let engine = AVAudioEngine()

    init(format: AVAudioFormat, input: AsyncStream<AnalyzerInput>.Continuation, level: @escaping @Sendable (Double) -> Void) throws {
        let node = engine.inputNode
        let natural = node.outputFormat(forBus: 0)
        guard natural.sampleRate > 0, natural.channelCount > 0 else { throw DictationError.noMicrophone }
        let converter = natural == format ? nil : AVAudioConverter(from: natural, to: format)
        node.installTap(onBus: 0, bufferSize: 4096, format: natural) { buffer, _ in
            level(Self.level(of: buffer))
            guard let converter else {
                input.yield(AnalyzerInput(buffer: buffer))
                return
            }
            let frames = AVAudioFrameCount(Double(buffer.frameLength) * format.sampleRate / natural.sampleRate) + 64
            guard let out = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames) else { return }
            var fed = false
            var error: NSError?
            converter.convert(to: out, error: &error) { _, status in
                if fed {
                    status.pointee = .noDataNow
                    return nil
                }
                fed = true
                status.pointee = .haveData
                return buffer
            }
            if error == nil, out.frameLength > 0 { input.yield(AnalyzerInput(buffer: out)) }
        }
        engine.prepare()
        do {
            try engine.start()
        } catch {
            node.removeTap(onBus: 0)
            throw error
        }
    }

    func stop() {
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
    }

    /// Loudness, 0 (−50 dB or quieter) to 1 (full scale).
    private static func level(of buffer: AVAudioPCMBuffer) -> Double {
        guard let data = buffer.floatChannelData?[0], buffer.frameLength > 0 else { return 0 }
        var sum: Float = 0
        for i in 0..<Int(buffer.frameLength) { sum += data[i] * data[i] }
        let rms = sqrt(sum / Float(buffer.frameLength))
        let db = 20 * log10(max(rms, 0.000_001))
        return Double(min(max((db + 50) / 50, 0), 1))
    }
}

// MARK: - Dictating into the composer

/// A composer's dictation: listening, the words going into its text view as
/// they're heard, and stopping (by the button, typing, sending, leaving, or
/// a while with nothing heard).
@MainActor
@Observable
final class ComposerDictation {
    enum State: Equatable { case idle, starting, listening, finishing }

    private(set) var state: State = .idle
    /// The microphone's level while listening, 0 to 1.
    private(set) var level: Double = 0
    /// The language's speech model downloading, 0 to 1.
    private(set) var installing: Double?
    /// Why it couldn't listen, to show above the composer.
    var problem: String?
    /// The problem is the microphone being refused: the notice offers Settings.
    private(set) var problemIsPermission = false

    var active: Bool { state != .idle }

    /// How long without hearing anything before it stops by itself.
    static let quietTimeout: Duration = .seconds(10)

    @ObservationIgnored private var engine: (any DictationEngine)?
    @ObservationIgnored private weak var text: ComposerTextController?
    @ObservationIgnored private var commands: [String] = []
    @ObservationIgnored private var lastHeard = ContinuousClock.now
    @ObservationIgnored private var watchdog: Task<Void, Never>?
    @ObservationIgnored private var finishing: Task<Void, Never>?
    @ObservationIgnored private var heard = ""
    @ObservationIgnored private var observers: [NSObjectProtocol] = []
    @ObservationIgnored var makeEngine: () -> any DictationEngine = { AppleDictation() }

    /// Starts listening into the text view at its cursor, or stops.
    func toggle(into text: ComposerTextController, commands: [String]) {
        if active { Task { await finish() } } else { start(into: text, commands: commands) }
    }

    func start(into text: ComposerTextController, commands: [String]) {
        guard state == .idle else { return }
        problem = nil
        problemIsPermission = false
        self.text = text
        self.commands = commands
        heard = ""
        state = .starting
        text.beginDictation()
        text.onDictationInterrupted = { [weak self] in self?.abandon() }
        let engine = makeEngine()
        self.engine = engine
        lastHeard = .now
        watch()
        Task {
            do {
                try await engine.start(vocabulary: DictationText.vocabulary(commands: commands)) { [weak self] e in
                    self?.handle(e, from: engine)
                }
            } catch {
                guard self.engine === engine else { return }
                problemIsPermission = (error as? DictationError).map { if case .microphoneDenied = $0 { true } else { false } } ?? false
                problem = (error as? LocalizedError)?.errorDescription ?? "Couldn't start dictation: \(error.localizedDescription)"
                engine.cancel()
                // Nothing was heard: the text as it was, selection and all.
                text.cancelDictation()
                end()
            }
        }
    }

    /// Stops listening, waits for the last words, and leaves them in the text.
    /// (Called again meanwhile, it waits for the same finish.)
    func finish() async {
        if let finishing {
            await finishing.value
            return
        }
        guard active, let engine else { return }
        state = .finishing
        let task = Task { @MainActor in
            let all = await engine.finish()
            guard self.engine === engine else { return }
            if !all.isEmpty { self.show(final: all, volatile: "") }
            self.end()
        }
        finishing = task
        await task.value
        if finishing == task { finishing = nil }
    }

    /// Debug (HMK_DEBUG_DICTATION): listening, with these words heard, and
    /// nothing more coming.
    func debugShow(into text: ComposerTextController, final: String, volatile: String) {
        guard state == .idle else { return }
        self.text = text
        engine = SilentEngine()
        state = .listening
        level = 0.6
        // Once the text view's made.
        Task {
            try? await Task.sleep(for: .milliseconds(300))
            text.beginDictation()
            text.dictate(final: final, volatile: volatile)
        }
    }

    /// Stops at once, keeping what's shown (before the composer moves to
    /// another pane, or goes): nothing more comes.
    func stopNow() {
        guard active else { return }
        text?.endDictation()
        abandon()
    }

    /// Typing or clicking in the text took over: what's shown stays as it
    /// is, and nothing more comes.
    private func abandon() {
        guard active else { return }
        engine?.cancel()
        engine = nil
        state = .idle
        cleanUp()
    }

    private func handle(_ e: DictationEvent, from source: any DictationEngine) {
        guard engine === source else { return }
        switch e {
        case .installing(let p):
            installing = p
        case .listening:
            installing = nil
            lastHeard = .now
            if state == .starting { state = .listening }
        case .level(let l):
            level = l
        case .text(let final, let volatile):
            let now = DictationText.join(final, volatile)
            if now != heard {
                heard = now
                lastHeard = .now
            }
            show(final: final, volatile: volatile)
        }
    }

    private func show(final: String, volatile: String) {
        let mapped = DictationText.mapSlashCommands(final, commands: commands)
        text?.dictate(final: mapped, volatile: volatile)
    }

    private func end() {
        text?.endDictation()
        engine = nil
        state = .idle
        cleanUp()
    }

    private func cleanUp() {
        // A finish still waiting on an engine that's gone isn't this one's.
        finishing = nil
        watchdog?.cancel()
        watchdog = nil
        level = 0
        installing = nil
        text?.onDictationInterrupted = {}
        for o in observers { NotificationCenter.default.removeObserver(o) }
        observers = []
    }

    /// Stops after a while with nothing heard, and when something else
    /// takes the audio (a call, Siri) or the app goes to the background.
    private func watch() {
        watchdog = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard let self else { return }
                guard self.state == .listening else { continue }
                if ContinuousClock.now - self.lastHeard > Self.quietTimeout { await self.finish() }
            }
        }
        let nc = NotificationCenter.default
        #if os(iOS)
        let names: [Notification.Name] = [AVAudioSession.interruptionNotification, UIApplication.willResignActiveNotification]
        #else
        let names: [Notification.Name] = [NSApplication.didResignActiveNotification]
        #endif
        observers = names.map { name in
            nc.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in
                // Only once listening: asking for the microphone puts up the
                // system's prompt, and the app resigns active for it.
                MainActor.assumeIsolated {
                    guard let self, self.state == .listening else { return }
                    Task { await self.finish() }
                }
            }
        }
    }
}

/// Asks the composer showing a pane to start or stop dictating (the menu
/// bar's and the command palette's Dictate).
@MainActor
@Observable
public final class DictationRequests {
    public static let shared = DictationRequests()
    /// The pane, and a new id for each request.
    public private(set) var toggle: (pane: String, id: UUID)?
    /// Panes whose composer is dictating now, for the menu's title.
    public private(set) var listening: Set<String> = []

    public func toggle(_ pane: String) { toggle = (pane, UUID()) }

    func set(_ pane: String, listening on: Bool) {
        if on { listening.insert(pane) } else { listening.remove(pane) }
    }

    /// Dictation is on in Settings (and so in the menus and palette).
    public static var enabled: Bool { Prefs.shared.bool(.agentDictation) }
}
