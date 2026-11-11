import HammockKit
import SwiftUI
import UIKit

struct TerminalScreen: View {
    let model: AppModel
    let paneID: String
    /// The pane on screen (not a neighbour the pager keeps loaded): it sizes
    /// the PTY to fit.
    var active = true
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        TerminalRepresentable(model: model, paneID: paneID, active: active)
            .ignoresSafeArea(.container, edges: .bottom)
            .background(theme.terminalBackground)
    }
}

struct TerminalRepresentable: UIViewRepresentable {
    let model: AppModel
    let paneID: String
    let active: Bool

    func makeUIView(context: Context) -> TerminalUIView {
        let v = TerminalUIView()
        let s = model.acquireSession(paneID)
        v.session = s
        v.onInput = { [model, paneID] data in model.send(paneID, data) }
        v.onFocus = { [model, paneID] in model.focus(paneID) }
        v.onResize = { [model, paneID] cols, rows in model.resize(paneID, cols: cols, rows: rows) }
        v.release = { [model] id in model.releaseSession(id) }
        v.active = active
        return v
    }

    func updateUIView(_ v: TerminalUIView, context: Context) { v.active = active }

    static func dismantleUIView(_ v: TerminalUIView, coordinator: ()) {
        if let s = v.session {
            v.session = nil
            MainActor.assumeIsolated { v.release?(s.paneID) }
        }
    }
}

/// The terminal's font, from Settings.
enum TerminalPrefs {
    static let fontName = "terminalFontName"
    static let fontSize = "terminalFontSize"
    /// Extra row height, a percentage of the font's line height (negative: tighter).
    static let lineSpacing = "terminalLineSpacing"
    static let defaultSize = 11.0

    /// Installed font families with a monospaced regular face.
    static var monospacedFamilies: [String] {
        UIFont.familyNames.sorted().filter { family in
            UIFont(descriptor: UIFontDescriptor(fontAttributes: [.family: family]), size: 12)
                .fontDescriptor.symbolicTraits.contains(.traitMonoSpace)
        }
    }

    static func font(size: CGFloat, bold: Bool) -> UIFont {
        let name = UserDefaults.standard.string(forKey: fontName) ?? ""
        guard !name.isEmpty else { return .monospacedSystemFont(ofSize: size, weight: bold ? .bold : .regular) }
        var d = UIFontDescriptor(fontAttributes: [.family: name])
        if bold, let b = d.withSymbolicTraits(.traitBold) { d = b }
        return UIFont(descriptor: d, size: size)
    }
}

/// Draws a pane from its TerminalEngine and takes software-keyboard input.
/// Drag to scroll (or to send the wheel to programs that track the mouse),
/// tap to click in those programs, long-press to paste.
final class TerminalUIView: UIView, UITextInput, UIEditMenuInteractionDelegate {
    var session: PaneSession? {
        didSet {
            oldValue?.unobserve(self)
            session?.observe(self) { [weak self] in self?.setNeedsDisplay() }
            applyTheme()
            setNeedsDisplay()
        }
    }
    var onInput: ((Data) -> Void)?
    var onResize: ((Int, Int) -> Void)?
    var onFocus: (() -> Void)?
    var release: ((String) -> Void)?
    /// Showing on screen: fit the PTY to the view, now and whenever the
    /// view changes size (the keyboard opening or closing).
    var active = false {
        didSet {
            if active, !oldValue { reportGrid(force: true) }
            // Hidden behind the agent view: let go of the keyboard.
            if !active, isFirstResponder { _ = resignFirstResponder() }
        }
    }

    private let padding: CGFloat = 4
    private var ctrlLatched = false
    /// Hardware keys held down, repeating (arrows, backspace).
    fileprivate var hardwareRepeats: [UIKeyboardHIDUsage: KeyRepeater] = [:]
    /// Where the cursor was last drawn (the keyboard asks, as a text view's caret).
    fileprivate var cursorRect = CGRect.zero
    /// What's been typed on this line (for the keyboard's document): dictation
    /// revises what it typed by reading it back, and replacing parts.
    fileprivate var typed: [Character] = []
    private var lastGrid = (0, 0)
    /// Where the last frame was drawn, for turning touches into cells.
    private var drawn = (origin: CGPoint.zero, cw: CGFloat(1), ch: CGFloat(1))
    private var scrollAccumulator: CGFloat = 0
    private var momentum: CGFloat = 0
    private var momentumLink: CADisplayLink?
    private lazy var editMenu = UIEditMenuInteraction(delegate: self)

    override init(frame: CGRect) {
        super.init(frame: frame)
        isOpaque = true
        contentMode = .redraw
        addGestureRecognizer(UITapGestureRecognizer(target: self, action: #selector(tapped)))
        let pan = UIPanGestureRecognizer(target: self, action: #selector(panned))
        addGestureRecognizer(pan)
        addGestureRecognizer(UILongPressGestureRecognizer(target: self, action: #selector(longPressed)))
        addInteraction(editMenu)
        NotificationCenter.default.addObserver(self, selector: #selector(prefsChanged), name: UserDefaults.didChangeNotification, object: nil)
        NotificationCenter.default.addObserver(self, selector: #selector(themeChanged), name: PhoneTheme.changed, object: nil)
    }

    required init?(coder: NSCoder) { fatalError() }

    override var canBecomeFirstResponder: Bool { true }

    /// The font settings last drawn with: any default changing posts the
    /// notification (the agent view's draft, saved as you type there), and
    /// redrawing the grid for each one lagged the keyboard.
    private var drawnPrefs = TerminalUIView.fontPrefs

    private static var fontPrefs: String {
        let d = UserDefaults.standard
        return "\(d.string(forKey: TerminalPrefs.fontName) ?? "")|\(d.double(forKey: TerminalPrefs.fontSize))|\(d.double(forKey: TerminalPrefs.lineSpacing))"
    }

    @objc private func prefsChanged() {
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            let now = Self.fontPrefs
            guard now != drawnPrefs else { return }
            drawnPrefs = now
            reportGrid(force: false)
            setNeedsDisplay()
        }
    }

    @objc private func tapped(_ g: UITapGestureRecognizer) {
        stopMomentum()
        // Programs that track the mouse get the tap as a click.
        if let engine = session?.engine, engine.mouseTracking {
            sendMouse(.press, button: .left, at: g.location(in: self))
            sendMouse(.release, button: .left, at: g.location(in: self))
            if !isFirstResponder { becomeFirstResponder() }
            return
        }
        if isFirstResponder { resignFirstResponder() } else { becomeFirstResponder() }
    }

    // MARK: Touch

    private func sendMouse(_ action: MouseAction, button: MouseButton, at p: CGPoint) {
        guard let engine = session?.engine else { return }
        let o = CGPoint(x: drawn.origin.x - padding, y: drawn.origin.y - padding)
        let g = MouseGeometry(width: Float(bounds.width - o.x), height: Float(bounds.height - o.y),
                              cellWidth: Float(drawn.cw), cellHeight: Float(drawn.ch), padding: Float(padding),
                              scale: Float(window?.screen.scale ?? 3))
        if let d = engine.encodeMouse(action, button: button, x: Float(p.x - o.x), y: Float(p.y - o.y), mods: [], geometry: g) {
            onInput?(d)
        }
    }

    /// Only vertical drags: sideways ones page between the tab's panes.
    /// Asked of every gesture starting on this view, those of views around
    /// it too: only its own scrolling is held to that, or a sideways drag
    /// couldn't swipe back either.
    override func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
        guard let pan = g as? UIPanGestureRecognizer, pan.view === self else { return super.gestureRecognizerShouldBegin(g) }
        let v = pan.velocity(in: self)
        return abs(v.y) > abs(v.x)
    }

    @objc private func panned(_ g: UIPanGestureRecognizer) {
        switch g.state {
        case .began:
            stopMomentum()
            scrollAccumulator = 0
        case .changed:
            scroll(by: g.translation(in: self).y)
            g.setTranslation(.zero, in: self)
        case .ended:
            momentum = g.velocity(in: self).y
            if abs(momentum) > 200 {
                let link = CADisplayLink(target: self, selector: #selector(glide))
                link.add(to: .main, forMode: .common)
                momentumLink = link
            }
        default:
            break
        }
    }

    @objc private func glide(_ link: CADisplayLink) {
        let dt = CGFloat(link.targetTimestamp - link.timestamp)
        scroll(by: momentum * dt)
        momentum *= pow(0.12, dt) // decays like a scroll view's fling
        if abs(momentum) < 40 { stopMomentum() }
    }

    private func stopMomentum() {
        momentumLink?.invalidate()
        momentumLink = nil
    }

    /// Scrolls like the Mac's wheel: a finger moving down shows earlier lines.
    private func scroll(by dy: CGFloat) {
        guard let engine = session?.engine else { return }
        scrollAccumulator += dy
        let lines = Int(scrollAccumulator / drawn.ch)
        guard lines != 0 else { return }
        scrollAccumulator -= CGFloat(lines) * drawn.ch
        if engine.mouseTracking {
            // The program wants wheel events.
            let at = CGPoint(x: bounds.midX, y: bounds.midY)
            for _ in 0..<abs(lines) { sendMouse(.press, button: lines > 0 ? .wheelUp : .wheelDown, at: at) }
        } else if engine.isAlternateScreen {
            // Full-screen apps without mouse support: scroll with arrow keys.
            let key: UInt16 = lines > 0 ? 0x7E : 0x7D
            for _ in 0..<abs(lines) {
                if let d = engine.encodeKey(macKeyCode: key, mods: [], text: nil, unshifted: 0) { onInput?(d) }
            }
        } else {
            engine.scroll(by: -lines)
            setNeedsDisplay()
        }
    }

    // MARK: Paste

    @objc private func longPressed(_ g: UILongPressGestureRecognizer) {
        guard g.state == .began else { return }
        stopMomentum()
        editMenu.presentEditMenu(with: UIEditMenuConfiguration(identifier: nil, sourcePoint: g.location(in: self)))
    }

    func editMenuInteraction(_ interaction: UIEditMenuInteraction, menuFor configuration: UIEditMenuConfiguration,
                             suggestedActions: [UIMenuElement]) -> UIMenu? {
        guard UIPasteboard.general.hasStrings else { return nil }
        return UIMenu(children: [UIAction(title: "Paste") { [weak self] _ in self?.paste(nil) }])
    }

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        action == #selector(paste(_:)) ? UIPasteboard.general.hasStrings : super.canPerformAction(action, withSender: sender)
    }

    override func paste(_ sender: Any?) {
        guard let text = UIPasteboard.general.string, !text.isEmpty, let engine = session?.engine else { return }
        engine.scrollToBottom()
        onInput?(engine.encodePaste(text))
    }

    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        onFocus?()
        // Typing from the phone makes it the pane's driver: size the PTY to fit.
        reportGrid(force: true)
        return ok
    }

    override func traitCollectionDidChange(_ previous: UITraitCollection?) {
        super.traitCollectionDidChange(previous)
        applyTheme()
    }

    @objc private func themeChanged() { applyTheme() }

    /// Settings ▸ Theme's terminal colours (by default, the Mac's), light or dark.
    private func applyTheme() {
        session?.engine.theme = PhoneTheme.current.terminal(dark: traitCollection.userInterfaceStyle == .dark)
        setNeedsDisplay()
    }

    // MARK: Geometry

    private var baseSize: CGFloat {
        let v = UserDefaults.standard.double(forKey: TerminalPrefs.fontSize)
        return v > 0 ? v : TerminalPrefs.defaultSize
    }

    /// `inset` is how far text sits below its row's top: line spacing is
    /// shared above and below it.
    private func metrics(size: CGFloat) -> (font: UIFont, bold: UIFont, cw: CGFloat, ch: CGFloat, inset: CGFloat) {
        let font = TerminalPrefs.font(size: size, bold: false)
        let bold = TerminalPrefs.font(size: size, bold: true)
        let cw = ("M" as NSString).size(withAttributes: [.font: font]).width
        let natural = ceil(font.lineHeight) + 1
        let pct = UserDefaults.standard.double(forKey: TerminalPrefs.lineSpacing)
        let ch = max((natural * (1 + pct / 100)).rounded(), ceil(size * 0.5))
        return (font, bold, cw, ch, ((ch - natural) / 2).rounded(.down))
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        reportGrid(force: false)
        setNeedsDisplay()
    }

    override func safeAreaInsetsDidChange() {
        super.safeAreaInsetsDidChange()
        reportGrid(force: false)
        setNeedsDisplay()
    }

    override func didMoveToWindow() {
        super.didMoveToWindow()
        if window != nil, active { reportGrid(force: true) }
        #if DEBUG
        // Dev aid (the App Store screenshots): HMK_DEBUG_KEYBOARD=1 types into the terminal as it opens, keyboard up.
        if window != nil, active, ProcessInfo.processInfo.environment["HMK_DEBUG_KEYBOARD"] == "1" {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { [weak self] in _ = self?.becomeFirstResponder() }
        }
        #endif
    }

    /// The grid this view could show at the base font size.
    private func reportGrid(force: Bool) {
        guard (active || isFirstResponder || force), window != nil, bounds.width > 0, bounds.height > 0 else { return }
        let m = metrics(size: baseSize)
        let usable = bounds.inset(by: safeAreaInsets)
        let cols = max(Int((usable.width - padding * 2) / m.cw), 10)
        let rows = max(Int((usable.height - padding * 2) / m.ch), 4)
        if (cols, rows) != lastGrid || force {
            lastGrid = (cols, rows)
            onResize?(cols, rows)
        }
    }

    // MARK: Drawing

    private func color(_ c: RGB, alpha: CGFloat = 1) -> UIColor {
        UIColor(red: CGFloat(c.r) / 255, green: CGFloat(c.g) / 255, blue: CGFloat(c.b) / 255, alpha: alpha)
    }

    override func draw(_ rect: CGRect) {
        guard let session else { return }
        let f = session.engine.frame()
        let theme = session.engine.theme
        color(f.background).setFill()
        UIRectFill(bounds)
        guard f.cols > 0 else { return }
        // Shrink the font so a wider pane (sized by the Mac) still fits.
        let usable = bounds.inset(by: safeAreaInsets)
        var size = baseSize
        let base = metrics(size: size)
        // Fit both dimensions so the prompt (usually the bottom row) stays visible.
        let needW = CGFloat(f.cols) * base.cw + padding * 2
        let needH = CGFloat(f.rows) * base.ch + padding * 2
        let scale = min(1, usable.width / needW, usable.height / needH)
        if scale < 1 { size = max(5, size * scale) }
        let m = metrics(size: size)
        let origin = CGPoint(x: usable.minX + padding, y: usable.minY + padding)
        drawn = (origin, m.cw, m.ch)

        for (y, row) in f.cells.enumerated() {
            let top = origin.y + CGFloat(y) * m.ch
            if top > bounds.maxY { break }
            for (x, c) in row.enumerated() {
                let bg = c.selected ? theme.selection : (c.inverse ? (c.fg ?? f.foreground) : c.bg)
                if let bg {
                    color(bg).setFill()
                    UIRectFill(CGRect(x: origin.x + CGFloat(x) * m.cw, y: top, width: m.cw * (c.wide ? 2 : 1), height: m.ch))
                }
            }
            for (x, c) in row.enumerated() where !c.text.isEmpty && !c.spacer && !c.invisible {
                let fg = c.inverse ? (c.bg ?? f.background) : (c.fg ?? f.foreground)
                var attrs: [NSAttributedString.Key: Any] = [.font: c.bold ? m.bold : m.font, .foregroundColor: color(fg, alpha: c.faint ? 0.6 : 1)]
                if c.underline { attrs[.underlineStyle] = NSUnderlineStyle.single.rawValue }
                (c.text as NSString).draw(at: CGPoint(x: origin.x + CGFloat(x) * m.cw, y: top + m.inset), withAttributes: attrs)
            }
        }
        if f.cursorVisible {
            let r = CGRect(x: origin.x + CGFloat(f.cursorX) * m.cw, y: origin.y + CGFloat(f.cursorY) * m.ch, width: m.cw, height: m.ch)
            cursorRect = r
            color(f.cursorColor ?? theme.cursor, alpha: isFirstResponder ? 0.7 : 0.35).setFill()
            UIRectFill(isFirstResponder ? r : CGRect(x: r.minX, y: r.maxY - 2, width: r.width, height: 2))
        }
    }

    // MARK: Keyboard

    var hasText: Bool { true }
    var autocorrectionType: UITextAutocorrectionType { get { .no } set {} }
    var autocapitalizationType: UITextAutocapitalizationType { get { .none } set {} }
    var smartQuotesType: UITextSmartQuotesType { get { .no } set {} }
    var smartDashesType: UITextSmartDashesType { get { .no } set {} }
    var spellCheckingType: UITextSpellCheckingType { get { .no } set {} }
    var keyboardType: UIKeyboardType { get { .asciiCapable } set {} }
    var smartInsertDeleteType: UITextSmartInsertDeleteType { get { .no } set {} }
    // For UITextInput (below): the composition in progress (IME, dictation).
    fileprivate var marked = ""
    var markedTextStyle: [NSAttributedString.Key: Any]?
    weak var inputDelegate: UITextInputDelegate?
    lazy var tokenizer: UITextInputTokenizer = UITextInputStringTokenizer(textInput: self)

    func insertText(_ text: String) {
        var t = text.replacingOccurrences(of: "\n", with: "\r")
        if ctrlLatched, let s = t.lowercased().unicodeScalars.first, t.count == 1, ("a"..."z").contains(Character(s)) || "[\\]^_ ".contains(Character(s)) {
            let code = s == " " ? 0 : (s.value & 0x1F)
            t = String(UnicodeScalar(UInt8(code)))
            ctrlLatched = false
            accessory.ctrlButton.isSelected = false
        }
        session?.engine.scrollToBottom()
        onInput?(Data(t.utf8))
        if t.contains("\r") || t.unicodeScalars.contains(where: { $0.value < 0x20 }) {
            forgetTyped() // a new line, or a control key: the line's someone else's now
        } else {
            typed.append(contentsOf: t)
            if typed.count > 400 { typed.removeFirst(typed.count - 400) }
        }
    }

    func deleteBackward() {
        if !typed.isEmpty { typed.removeLast() }
        onInput?(Data([0x7F]))
    }

    /// Forget what was typed (the cursor may have moved), and tell the
    /// keyboard its document changed.
    fileprivate func forgetTyped() {
        guard !typed.isEmpty || !marked.isEmpty else { return }
        inputDelegate?.textWillChange(self)
        typed = []
        marked = ""
        inputDelegate?.textDidChange(self)
    }

    private lazy var accessory: KeyAccessoryView = {
        let v = KeyAccessoryView()
        v.onKey = { [weak self] key in self?.accessoryKey(key) }
        return v
    }()

    override var inputAccessoryView: UIView? { accessory }

    private func accessoryKey(_ key: KeyAccessoryView.Key) {
        guard let engine = session?.engine else { return }
        if key != .backspace, key != .ctrl, key != .hide { forgetTyped() }
        let mac: UInt16
        switch key {
        case .esc: mac = 0x35
        case .tab: mac = 0x30
        case .up: mac = 0x7E
        case .down: mac = 0x7D
        case .left: mac = 0x7B
        case .right: mac = 0x7C
        case .backspace:
            onInput?(Data([0x7F]))
            return
        case .hide:
            resignFirstResponder()
            return
        case .ctrl:
            ctrlLatched.toggle()
            accessory.ctrlButton.isSelected = ctrlLatched
            return
        }
        if let d = engine.encodeKey(macKeyCode: mac, mods: [], text: nil, unshifted: 0) { onInput?(d) }
    }
}

/// Esc / Tab / Ctrl / arrows above the software keyboard, and a button to put it away.
final class KeyAccessoryView: UIInputView {
    enum Key { case esc, tab, ctrl, up, down, left, right, backspace, hide }
    /// Held, these go again and again, as on a keyboard.
    static let repeating: Set<Key> = [.up, .down, .left, .right, .backspace]
    private var repeater: KeyRepeater?
    private let haptic = UIImpactFeedbackGenerator(style: .light)
    var onKey: ((Key) -> Void)?
    let ctrlButton = UIButton(configuration: .gray())

    init() {
        super.init(frame: CGRect(x: 0, y: 0, width: 320, height: 48), inputViewStyle: .keyboard)
        allowsSelfSizing = true
        let items: [(String, Key)] = [("esc", .esc), ("tab", .tab), ("ctrl", .ctrl), ("arrow.left", .left), ("arrow.down", .down), ("arrow.up", .up), ("arrow.right", .right), ("delete.left", .backspace), ("keyboard.chevron.compact.down", .hide)]
        let stack = UIStackView()
        stack.axis = .horizontal
        stack.distribution = .fillEqually
        stack.spacing = 5
        stack.translatesAutoresizingMaskIntoConstraints = false
        for (title, key) in items {
            let b = key == .ctrl ? ctrlButton : UIButton(configuration: .gray())
            // Narrow insets and no wrapping, so "esc" stays on one line.
            b.configuration?.contentInsets = NSDirectionalEdgeInsets(top: 6, leading: 0, bottom: 6, trailing: 0)
            b.configuration?.titleLineBreakMode = .byClipping
            if key == .hide {
                b.setImage(UIImage(systemName: title), for: .normal)
                b.accessibilityLabel = "Hide keyboard"
            } else if title.hasPrefix("arrow") {
                b.setImage(UIImage(systemName: title), for: .normal)
                b.accessibilityLabel = title.replacingOccurrences(of: "arrow.", with: "") + " arrow"
            } else if key == .backspace {
                b.setImage(UIImage(systemName: title), for: .normal)
                b.accessibilityLabel = "Delete"
            } else {
                b.configuration?.attributedTitle = AttributedString(title, attributes: AttributeContainer([.font: UIFont.systemFont(ofSize: 15)]))
            }
            b.configurationUpdateHandler = { btn in
                var c = btn.configuration
                c?.baseBackgroundColor = btn.isSelected ? .tintColor : nil
                c?.baseForegroundColor = btn.isSelected ? .white : .label
                btn.configuration = c
            }
            if Self.repeating.contains(key) {
                // Down at once, and again while held.
                b.addAction(UIAction { [weak self] _ in
                    guard let self else { return }
                    self.haptic.prepare()
                    self.repeater = KeyRepeater { [weak self] in
                        self?.onKey?(key)
                        // A light tick each time, as the keyboard's own keys give.
                        self?.haptic.impactOccurred(intensity: 0.55)
                    }
                }, for: .touchDown)
                b.addAction(UIAction { [weak self] _ in self?.repeater = nil }, for: [.touchUpInside, .touchUpOutside, .touchCancel, .touchDragExit])
            } else {
                b.addAction(UIAction { [weak self] _ in self?.onKey?(key) }, for: .touchUpInside)
            }
            stack.addArrangedSubview(b)
        }
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: layoutMarginsGuide.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: layoutMarginsGuide.trailingAnchor),
            stack.topAnchor.constraint(equalTo: topAnchor, constant: 6),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -6),
        ])
    }

    required init?(coder: NSCoder) { fatalError() }
}

// MARK: Hardware keyboard

extension TerminalUIView {
    override func pressesBegan(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        var handled = false
        for press in presses {
            guard let key = press.key, let engine = session?.engine else { continue }
            let mac: UInt16? = switch key.keyCode {
            case .keyboardUpArrow: 0x7E
            case .keyboardDownArrow: 0x7D
            case .keyboardLeftArrow: 0x7B
            case .keyboardRightArrow: 0x7C
            case .keyboardEscape: 0x35
            case .keyboardTab: 0x30
            case .keyboardReturnOrEnter: 0x24
            case .keyboardDeleteOrBackspace: 0x33
            default: nil
            }
            var mods: KeyMods = []
            if key.modifierFlags.contains(.shift) { mods.insert(.shift) }
            if key.modifierFlags.contains(.control) { mods.insert(.ctrl) }
            if key.modifierFlags.contains(.alternate) { mods.insert(.alt) }
            if let mac {
                if mac != 0x33 { forgetTyped() } else if !typed.isEmpty { typed.removeLast() }
                if let d = engine.encodeKey(macKeyCode: mac, mods: mods, text: nil, unshifted: 0) {
                    if [0x7E, 0x7D, 0x7B, 0x7C, 0x33].contains(mac) {
                        // UIKit doesn't repeat keys an app takes itself: held, these go
                        // again until let go.
                        hardwareRepeats[key.keyCode] = KeyRepeater { [weak self] in self?.onInput?(d) }
                    } else {
                        onInput?(d)
                    }
                }
                handled = true
            } else if key.modifierFlags.contains(.control), let s = key.charactersIgnoringModifiers.lowercased().unicodeScalars.first,
                      ("a"..."z").contains(Character(s)) {
                onInput?(Data([UInt8(s.value & 0x1F)]))
                handled = true
            }
        }
        if !handled { super.pressesBegan(presses, with: event) }
    }

    override func pressesEnded(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        for p in presses { if let k = p.key { hardwareRepeats[k.keyCode] = nil } }
        super.pressesEnded(presses, with: event)
    }

    override func pressesCancelled(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        for p in presses { if let k = p.key { hardwareRepeats[k.keyCode] = nil } }
        super.pressesCancelled(presses, with: event)
    }
}

/// A key held down: fire once now, then after a pause again and again, as a
/// keyboard repeats, until released (dropped).
final class KeyRepeater {
    private var timer: Timer?

    init(_ fire: @escaping () -> Void) {
        fire()
        timer = Timer.scheduledTimer(withTimeInterval: 0.4, repeats: false) { [weak self] _ in
            self?.timer = Timer.scheduledTimer(withTimeInterval: 0.06, repeats: true) { _ in fire() }
        }
    }

    deinit { timer?.invalidate() }
}

// MARK: UITextInput

// The software keyboard repeats its delete key (and offers dictation and
// IME) only for views that are UITextInput, not just UIKeyInput. So the
// terminal is one, over a stand-in document that never changes: a run of
// underscores with the caret at its end (never spaces, or two spaces
// would trigger the full-stop shortcut). The keyboard deletes from it as
// from any text, and each delete goes to the terminal as a backspace.

final class StandInPosition: UITextPosition {
    let i: Int
    init(_ i: Int) { self.i = i }
}

final class StandInRange: UITextRange {
    let a: Int, b: Int
    init(_ a: Int, _ b: Int) { (self.a, self.b) = (min(a, b), max(a, b)) }
    override var start: UITextPosition { StandInPosition(a) }
    override var end: UITextPosition { StandInPosition(b) }
    override var isEmpty: Bool { a == b }
}

extension TerminalUIView {
    private static let standIn = String(repeating: "_", count: 64)
    private var docLength: Int { Self.standIn.count + typed.count + marked.count }
    private var doc: [Character] { Array(Self.standIn) + typed + Array(marked) }
    private func idx(_ p: UITextPosition) -> Int { (p as? StandInPosition)?.i ?? docLength }

    var selectedTextRange: UITextRange? {
        get { StandInRange(docLength, docLength) }
        set {}
    }

    var markedTextRange: UITextRange? {
        marked.isEmpty ? nil : StandInRange(Self.standIn.count + typed.count, docLength)
    }

    var beginningOfDocument: UITextPosition { StandInPosition(0) }
    var endOfDocument: UITextPosition { StandInPosition(docLength) }

    func text(in range: UITextRange) -> String? {
        guard let r = range as? StandInRange else { return nil }
        let d = doc
        let a = max(0, min(r.a, d.count)), b = max(a, min(r.b, d.count))
        return String(d[a..<b])
    }

    /// The keyboard replacing text (dictation revising what it typed, or a
    /// word deleted at once while delete repeats): back to where the range
    /// starts, then the new text and what came after the range, typed again.
    func replace(_ range: UITextRange, withText text: String) {
        guard let r = range as? StandInRange else { return }
        let base = Self.standIn.count
        let end = base + typed.count
        let a = min(max(r.a, 0), end), b = min(max(r.b, a), end)
        let tail = b > base ? Array(typed[(b - base)...]) : typed
        let back = end - a
        if back > 0 { onInput?(Data(repeating: 0x7F, count: back)) }
        typed = a > base ? Array(typed[..<(a - base)]) : []
        let again = text + String(tail)
        if !again.isEmpty { insertText(again) }
    }

    func setMarkedText(_ markedText: String?, selectedRange: NSRange) {
        marked = markedText ?? ""
    }

    func unmarkText() {
        let t = marked
        marked = ""
        if !t.isEmpty { insertText(t) }
    }

    func textRange(from fromPosition: UITextPosition, to toPosition: UITextPosition) -> UITextRange? {
        StandInRange(idx(fromPosition), idx(toPosition))
    }

    func position(from position: UITextPosition, offset: Int) -> UITextPosition? {
        let i = idx(position) + offset
        return (0...docLength).contains(i) ? StandInPosition(i) : nil
    }

    func position(from position: UITextPosition, in direction: UITextLayoutDirection, offset: Int) -> UITextPosition? {
        switch direction {
        case .left, .up: self.position(from: position, offset: -offset)
        default: self.position(from: position, offset: offset)
        }
    }

    func compare(_ position: UITextPosition, to other: UITextPosition) -> ComparisonResult {
        let a = idx(position), b = idx(other)
        return a < b ? .orderedAscending : a > b ? .orderedDescending : .orderedSame
    }

    func offset(from: UITextPosition, to toPosition: UITextPosition) -> Int { idx(toPosition) - idx(from) }

    func position(within range: UITextRange, farthestIn direction: UITextLayoutDirection) -> UITextPosition? {
        guard let r = range as? StandInRange else { return nil }
        return StandInPosition(direction == .left || direction == .up ? r.a : r.b)
    }

    func characterRange(byExtending position: UITextPosition, in direction: UITextLayoutDirection) -> UITextRange? {
        let i = idx(position)
        return direction == .left || direction == .up ? StandInRange(max(0, i - 1), i) : StandInRange(i, min(docLength, i + 1))
    }

    func baseWritingDirection(for position: UITextPosition, in direction: UITextStorageDirection) -> NSWritingDirection { .leftToRight }
    func setBaseWritingDirection(_ writingDirection: NSWritingDirection, for range: UITextRange) {}

    /// Where the keyboard puts things near the caret: the terminal's cursor.
    func caretRect(for position: UITextPosition) -> CGRect { cursorRect }
    func firstRect(for range: UITextRange) -> CGRect { cursorRect }
    func selectionRects(for range: UITextRange) -> [UITextSelectionRect] { [] }
    func closestPosition(to point: CGPoint) -> UITextPosition? { endOfDocument }
    func closestPosition(to point: CGPoint, within range: UITextRange) -> UITextPosition? { range.end }
    func characterRange(at point: CGPoint) -> UITextRange? { nil }
}
