// The Hammock Key's own approval panel: which tab in which Group is
// asking, what's signed and by what, with Touch ID inside it
// (LAAuthenticationView), instead of the system sheet's one line of text.
//
// Still a real LocalAuthentication evaluation in this helper: the panel only
// draws it. The headline is in the helper's words (what's signed, from the
// data; or what creating, deleting or a new approver does: changePrompt),
// as are its notes and a window's length; where it's from (Group, tab,
// agent, command) is the daemon's, labelled as such, and so is a device's
// name, beside its key's fingerprint. Without Touch ID (the lid's closed, no Magic Keyboard with it), or
// when you'd rather type your password (the default button: Return), the
// system sheet asks instead.
import AppKit
import LocalAuthentication
import LocalAuthenticationEmbeddedUI
import SwiftUI

/// What the panel shows.
struct KeyPrompt {
    var headline: String // the helper's words ("Sign for git (a commit or tag)")
    /// Devices a change lets approve: each one's name (the daemon's) and
    /// its key's fingerprint (the helper's).
    struct Device { var name: String; var fingerprint: String }
    var devices: [Device] = []
    var notes: [String] = [] // the helper's words: what allowing does
    var group = ""
    var groupIcon = "" // an emoji
    var groupPicture: NSImage?
    var tab = ""
    var agent = "" // "Claude", "An agent", or "" for a program of yours
    var command = ""
    var windowMinutes: Int64 = 0

    /// From the daemon's "prompt" (keyPrompt in hammockkey.go).
    init(headline: String, from p: [String: Any]?, window: Bool = false) {
        self.headline = headline
        // As long as this helper lets a token last (sign in main.swift).
        windowMinutes = window ? keyWindowMs / 60000 : 0
        guard let p else { return }
        group = p["group"] as? String ?? ""
        groupIcon = p["group_icon"] as? String ?? ""
        tab = p["tab"] as? String ?? ""
        agent = p["agent"] as? String ?? ""
        command = p["command"] as? String ?? ""
        if let b64 = p["group_picture"] as? String, let d = Data(base64Encoded: b64) {
            groupPicture = NSImage(data: d)
        }
    }

    init(headline: String) { self.headline = headline }
}

enum PanelOutcome { case allowed, declined, password, unavailable }

/// The first letter up: describe's words start a sentence here.
func sentence(_ s: String) -> String { s.prefix(1).uppercased() + s.dropFirst() }

// MARK: - The view

struct KeyPromptView: View {
    let prompt: KeyPrompt
    let touchID: AnyView
    var onDeny: () -> Void = {}
    var onPassword: () -> Void = {}

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            header
            if !prompt.devices.isEmpty { devices }
            if !prompt.tab.isEmpty || !prompt.group.isEmpty { source }
            ForEach(prompt.notes, id: \.self) { note in
                Text(note)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if prompt.windowMinutes > 0 {
                Label("Allowing also lets programs in this pane sign for the next \(prompt.windowMinutes) minutes without asking again.",
                      systemImage: "clock")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            footer
        }
        .padding(20)
        .frame(width: 440)
    }

    private var header: some View {
        HStack(spacing: 14) {
            ZStack(alignment: .bottomTrailing) {
                Image(nsImage: NSApp.applicationIconImage ?? NSImage())
                    .resizable()
                    .frame(width: 52, height: 52)
                Image(systemName: "key.fill")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(.white)
                    .frame(width: 22, height: 22)
                    .background(Circle().fill(Color.orange))
                    .overlay(Circle().stroke(Color(nsColor: .windowBackgroundColor), lineWidth: 2))
                    .offset(x: 4, y: 4)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text("Hammock Key")
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(.secondary)
                Text(sentence(prompt.headline))
                    .font(.title3.weight(.semibold))
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    /// The devices a change lets approve: the name it gave, and its key.
    private var devices: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(Array(prompt.devices.enumerated()), id: \.offset) { _, d in
                HStack(spacing: 12) {
                    Image(systemName: "iphone")
                        .font(.system(size: 17, weight: .medium))
                        .foregroundStyle(.secondary)
                        .frame(width: 40, height: 40)
                        .background(RoundedRectangle(cornerRadius: 9).fill(Color.primary.opacity(0.07)))
                    VStack(alignment: .leading, spacing: 3) {
                        Text("“\(d.name)”")
                            .font(.headline)
                            .lineLimit(1)
                        Text("Key \(d.fingerprint)")
                            .font(.system(.callout, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 0)
                }
            }
        }
        .padding(14)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(0.05)))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.08)))
    }

    /// Where it's from: the Group and tab, as large as the headline.
    private var source: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(prompt.agent.isEmpty ? "Asked from" : "\(prompt.agent) is asking from")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .textCase(.uppercase)
            HStack(spacing: 12) {
                avatar
                VStack(alignment: .leading, spacing: 3) {
                    if !prompt.group.isEmpty {
                        Text(prompt.group)
                            .font(.headline)
                            .lineLimit(1)
                    }
                    if !prompt.tab.isEmpty {
                        Label(prompt.tab, systemImage: "rectangle.topthird.inset.filled")
                            .font(.body.weight(.medium))
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
            }
            if !prompt.command.isEmpty {
                Text(prompt.command)
                    .font(.system(.callout, design: .monospaced))
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .truncationMode(.middle)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 5)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(0.05)))
            }
        }
        .padding(14)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color.primary.opacity(0.05)))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(Color.primary.opacity(0.08)))
    }

    @ViewBuilder private var avatar: some View {
        let shape = RoundedRectangle(cornerRadius: 9)
        Group {
            if !prompt.groupIcon.isEmpty {
                Text(prompt.groupIcon).font(.system(size: 22))
            } else if let pic = prompt.groupPicture {
                Image(nsImage: pic).resizable().scaledToFill()
            } else {
                Image(systemName: prompt.group.isEmpty ? "terminal" : "square.stack.3d.up.fill")
                    .font(.system(size: 17, weight: .medium))
                    .foregroundStyle(.secondary)
            }
        }
        .frame(width: 40, height: 40)
        .background(shape.fill(Color.primary.opacity(0.07)))
        .clipShape(shape)
    }

    private var footer: some View {
        HStack(spacing: 10) {
            Button("Don't Allow", action: onDeny)
                .keyboardShortcut(.cancelAction)
                .controlSize(.large)
            Button("Use Password…", action: onPassword)
                .keyboardShortcut(.defaultAction)
                .controlSize(.large)
            Spacer(minLength: 8)
            Text("Touch ID to allow")
                .font(.callout)
                .foregroundStyle(.secondary)
            touchID.frame(width: 32, height: 32)
        }
    }
}

/// LAAuthenticationView, which shows the evaluation of its context.
struct AuthView: NSViewRepresentable {
    let view: LAAuthenticationView
    func makeNSView(context: Context) -> LAAuthenticationView { view }
    func updateNSView(_ v: LAAuthenticationView, context: Context) {}
}

/// The fingerprint drawn in its place, for snapshots.
struct TouchIDGlyph: View {
    var body: some View {
        Image(systemName: "touchid")
            .font(.system(size: 28, weight: .light))
            .foregroundStyle(Color.pink)
    }
}

// MARK: - Showing it

func makePanel(_ view: some View) -> NSPanel {
    let panel = NSPanel(contentRect: .zero, styleMask: [.titled, .fullSizeContentView], backing: .buffered, defer: false)
    panel.titlebarAppearsTransparent = true
    panel.titleVisibility = .hidden
    panel.title = "Hammock Key"
    for b in [NSWindow.ButtonType.closeButton, .miniaturizeButton, .zoomButton] { panel.standardWindowButton(b)?.isHidden = true }
    panel.isMovableByWindowBackground = true
    panel.level = .modalPanel
    panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
    panel.hidesOnDeactivate = false
    panel.isReleasedWhenClosed = false
    let host = NSHostingView(rootView: view)
    host.safeAreaRegions = [] // drawn under the (hidden) titlebar, no gap above
    panel.contentView = host
    panel.setContentSize(host.fittingSize)
    panel.center()
    return panel
}

/// The screen's locked: nobody's there to see a panel (the system sheet
/// says so at once, and your phone answers).
func screenLocked() -> Bool {
    guard let d = CGSessionCopyCurrentDictionary() as? [String: Any] else { return false }
    return d["CGSSessionScreenIsLocked"] as? Bool ?? false
}

/// Asks with the panel and Touch ID in it. .unavailable when there's no
/// Touch ID to ask with; .password when you'd rather type it (or Touch ID
/// gave up): the system sheet asks then.
func askInPanel(_ prompt: KeyPrompt, reason: String) -> PanelOutcome {
    guard !screenLocked(), LAContext().canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: nil) else {
        return .unavailable
    }
    let app = NSApplication.shared
    app.setActivationPolicy(.accessory)

    var outcome: PanelOutcome?
    func finish(_ o: PanelOutcome) {
        guard outcome == nil else { return }
        outcome = o
        current?.invalidate()
        app.stop(nil)
        // stop takes effect after the next event.
        app.postEvent(NSEvent.otherEvent(with: .applicationDefined, location: .zero, modifierFlags: [], timestamp: 0,
                                         windowNumber: 0, context: nil, subtype: 0, data1: 0, data2: 0)!, atStart: false)
    }

    let panel = makePanel(EmptyView())
    var waiting = false // Touch ID stopped when we lost focus: again once it's back
    // A context per evaluation: LAAuthenticationView shows one context's.
    func arm() {
        waiting = false
        let ctx = LAContext()
        let auth = LAAuthenticationView(context: ctx, controlSize: .small) // 32 pt; .regular is 64
        let host = NSHostingView(rootView: KeyPromptView(prompt: prompt, touchID: AnyView(AuthView(view: auth)),
                                                         onDeny: { finish(.declined) },
                                                         onPassword: { finish(.password) }))
        host.safeAreaRegions = []
        panel.contentView = host
        panel.setContentSize(host.fittingSize)
        current = ctx
        ctx.evaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, localizedReason: reason) { ok, error in
            DispatchQueue.main.async {
                guard current === ctx else { return } // an older evaluation
                if ok { return finish(.allowed) }
                switch (error as? LAError)?.code {
                case .userCancel: finish(.declined)
                case .appCancel: break // ours: a button already said
                case .systemCancel: if app.isActive { arm() } else { waiting = true } // focus went elsewhere
                default: finish(.password) // fallback, lockout, too many tries: the sheet asks
                }
            }
        }
    }
    let back = NotificationCenter.default.addObserver(forName: NSApplication.didBecomeActiveNotification, object: nil, queue: .main) { _ in
        if waiting, outcome == nil { arm() }
    }
    arm()
    panel.center()
    panel.makeKeyAndOrderFront(nil)
    // Take focus so Touch ID (and Return, Esc) work at once: a plain
    // activate() is refused, as nothing yields to a helper the daemon ran.
    // Activating as though from the app in front is allowed.
    if let front = NSWorkspace.shared.frontmostApplication, front != NSRunningApplication.current {
        NSRunningApplication.current.activate(from: front, options: [])
    }
    if !app.isActive { app.activate(ignoringOtherApps: true) }
    app.run()
    NotificationCenter.default.removeObserver(back)
    panel.orderOut(nil)
    current = nil
    return outcome ?? .unavailable
}

/// `hmk-key preview`: the panel for a made-up request (or the one on stdin),
/// signing nothing: {"command": "create"|"delete"|"add-approver", "approvers"}
/// for the helper's own prompts for those, else {"headline", "window",
/// "prompt"} as the daemon sends a signature's. With --snapshot <prefix>,
/// renders it to <prefix>-light.png and -dark.png instead of showing it. A
/// live one only from a development (ad hoc) build: a signed helper showing
/// any words anyone gives it would make the real panel worth less.
func preview(_ args: [String]) {
    var req = isatty(0) == 0 ? stdinJSON() : [:]
    if req.isEmpty {
        req = ["headline": "sign for git (a commit or tag)", "window": true,
               "prompt": ["group": "Hammock", "group_icon": "🌴", "tab": "Key Prompt",
                          "command": "git commit -S -m \"Key prompt: show the tab\""]]
    }
    var prompt = KeyPrompt(headline: req["headline"] as? String ?? "sign with your Hammock Key",
                           from: req["prompt"] as? [String: Any], window: req["window"] as? Bool ?? false)
    if let command = req["command"] as? String, ["create", "delete", "add-approver"].contains(command) {
        let approvers = (req["approvers"] as? [Any] ?? []).compactMap { try? parseApprover($0) }
        prompt = changePrompt(command, approvers: approvers).0
    }
    if let i = args.firstIndex(of: "--snapshot"), i + 1 < args.count {
        let prefix = args[i + 1]
        NSApplication.shared.setActivationPolicy(.prohibited)
        for (name, look) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            let panel = makePanel(KeyPromptView(prompt: prompt, touchID: AnyView(TouchIDGlyph()))
                .background(Color(nsColor: .windowBackgroundColor)))
            panel.appearance = NSAppearance(named: look)
            guard let v = panel.contentView else { continue }
            v.layoutSubtreeIfNeeded()
            let bounds = panel.frame.size
            guard let rep = v.bitmapImageRepForCachingDisplay(in: NSRect(origin: .zero, size: bounds)) else { continue }
            v.cacheDisplay(in: NSRect(origin: .zero, size: bounds), to: rep)
            let out = URL(fileURLWithPath: "\(prefix)-\(name).png")
            try? rep.representation(using: .png, properties: [:])?.write(to: out)
            print(out.path)
        }
        return
    }
    guard adHoc() else {
        FileHandle.standardError.write(Data("hmk-key: a live preview is only for development builds (use --snapshot)\n".utf8))
        exit(1)
    }
    print(askInPanel(prompt, reason: prompt.headline))
}
