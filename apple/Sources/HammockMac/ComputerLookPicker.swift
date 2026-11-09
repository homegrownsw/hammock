import AppKit
import HammockKit
import SwiftUI

// A linked computer's icon and colour (Icon…, from the footer's
// switcher, the palette, the View menu and Settings ▸ Devices): its colour,
// then an SF Symbol (those that make sense for a computer) or any emoji,
// suggested ones first, with a search. Each pick applies at once, on all
// your computers (its record is its own: it's set there).

struct ComputerLookPicker: View {
    let model: AppModel
    let userID: String
    /// In a sheet: Done closes it. In a popover: nil (a click outside does).
    var done: (() -> Void)?

    enum Kind: String, CaseIterable { case symbols = "Symbols", emoji = "Emoji" }
    @State private var kind: Kind
    @State private var query: String
    @State private var error: String?
    @State private var custom = Color.gray
    @State private var customTask: Task<Void, Never>?
    @FocusState private var searchFocused: Bool

    init(model: AppModel, userID: String, done: (() -> Void)? = nil, kind: Kind? = nil, query: String = "") {
        self.model = model
        self.userID = userID
        self.done = done
        _kind = State(initialValue: kind ?? (model.computer(userID)?.emoji != nil ? .emoji : .symbols))
        _query = State(initialValue: query)
        _custom = State(initialValue: model.computer(userID).flatMap { Color(hex: $0.tint) } ?? .gray)
    }

    private var computer: Computer? { model.computer(userID) }

    var body: some View {
        VStack(spacing: 0) {
            if let c = computer {
                header(c)
                    .padding([.horizontal, .top], 16)
                    .padding(.bottom, 10)
                colours(c)
                    .padding(.horizontal, 16)
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .font(.callout)
                        .foregroundStyle(.red)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 16)
                        .padding(.top, 6)
                }
                Picker("Icon", selection: $kind) {
                    ForEach(Kind.allCases, id: \.self) { Text($0.rawValue) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .padding(.horizontal, 16)
                .padding(.top, 14)
                search
                    .padding(.horizontal, 16)
                    .padding(.vertical, 10)
                Divider()
                grid(c)
            } else {
                ContentUnavailableView("Not Linked", systemImage: "desktopcomputer", description: Text("That computer isn’t linked with this Mac any more."))
            }
            if let done {
                Divider()
                HStack {
                    Spacer()
                    Button("Done", action: done).keyboardShortcut(.defaultAction)
                }
                .padding(12)
            }
        }
        .frame(width: 400, height: done == nil ? 520 : 580)
        .onAppear {
            searchFocused = true
        }
    }

    private func header(_ c: Computer) -> some View {
        HStack(spacing: 12) {
            ComputerIcon(c, size: 26)
                .frame(width: 48, height: 48)
                .background(c.color.opacity(0.14), in: RoundedRectangle(cornerRadius: 11, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text(c.thisComputer ? "\(c.name) (this Mac)" : c.name).font(.headline)
                Text("Its icon, in its colour, on all your computers")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if !c.icon.isEmpty {
                Button("Default Icon") { pick(icon: "") }
                    .help("Its kind’s symbol, \(Computer.symbol(kind: c.kind, os: c.os))")
            }
        }
    }

    /// The colours: Hammock's, then any (each computer has its own).
    private func colours(_ c: Computer) -> some View {
        HStack(spacing: 8) {
            ForEach(computerTints, id: \.self) { t in
                let owner = model.tintOwner(t, except: c.userID)
                let on = t.caseInsensitiveCompare(c.tint) == .orderedSame || (t == neutralTint && c.tint.isEmpty)
                Button { pick(tint: t) } label: {
                    Circle()
                        .fill(Color(hex: t) ?? .primary)
                        .frame(width: 22, height: 22)
                        .overlay {
                            if on {
                                Image(systemName: "checkmark").font(.system(size: 10, weight: .bold))
                                    .foregroundStyle(t == neutralTint ? AnyShapeStyle(.background) : AnyShapeStyle(.white))
                            } else if let owner {
                                // Another computer's: shown with its icon, not offered.
                                ComputerIcon(owner, size: 10, color: .white).opacity(0.9)
                            }
                        }
                        .overlay { Circle().strokeBorder(on ? Color.primary.opacity(0.55) : .clear, lineWidth: 2).padding(-3) }
                        .opacity(owner == nil ? 1 : 0.35)
                }
                .buttonStyle(.plain)
                .disabled(owner != nil)
                .help(owner.map { "\(tintName(t)): \($0.thisComputer ? "this Mac" : $0.name)’s colour" } ?? tintName(t))
                .accessibilityLabel(tintName(t))
                .accessibilityAddTraits(on ? .isSelected : [])
            }
            Spacer(minLength: 4)
            ColorPicker("Other Colour", selection: $custom, supportsOpacity: false)
                .labelsHidden()
                .help("Any colour")
                .onChange(of: custom) { _, new in
                    guard let hex = new.hexString, hex.caseInsensitiveCompare(c.tint) != .orderedSame else { return }
                    // The panel sends every step of a drag: the last one counts.
                    customTask?.cancel()
                    customTask = Task {
                        try? await Task.sleep(for: .milliseconds(350))
                        if !Task.isCancelled { pick(tint: hex) }
                    }
                }
        }
    }

    private var search: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField(kind == .symbols ? "Search symbols" : "Search emoji", text: $query)
                .textFieldStyle(.plain)
                .focused($searchFocused)
            if !query.isEmpty {
                Button { query = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary) }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Clear search")
            }
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 7))
    }

    private func grid(_ c: Computer) -> some View {
        let sections = ComputerIcons.sections(emoji: kind == .emoji, query: query)
        return ScrollView {
            if sections.isEmpty {
                Text(kind == .symbols ? "No symbols match. Try Emoji." : "No emoji match.")
                    .foregroundStyle(.secondary)
                    .padding(.top, 40)
            }
            LazyVStack(alignment: .leading, spacing: 4, pinnedViews: [.sectionHeaders]) {
                ForEach(sections) { section in
                    Section {
                        LazyVGrid(columns: Array(repeating: GridItem(.fixed(40), spacing: 4), count: 8), spacing: 4) {
                            ForEach(section.icons, id: \.icon) { choice in
                                cell(choice, c)
                            }
                        }
                        .padding(.bottom, 8)
                    } header: {
                        Text(section.title)
                            .font(.subheadline.weight(.semibold))
                            .foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.vertical, 5)
                            .background(.background)
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 6)
        }
        .scrollDismissesKeyboard(.never)
    }

    private func cell(_ choice: IconChoice, _ c: Computer) -> some View {
        let on = choice.icon == c.icon || (c.icon.isEmpty && choice.icon == c.symbol && kind == .symbols)
        return Button { pick(icon: choice.icon) } label: {
            Group {
                if kind == .emoji {
                    Text(choice.icon).font(.system(size: 24))
                } else {
                    Image(systemName: choice.icon)
                        .font(.system(size: 19, weight: .medium))
                        .foregroundStyle(on ? (c.isNeutral ? AnyShapeStyle(.background) : AnyShapeStyle(.white)) : AnyShapeStyle(c.color))
                }
            }
            .frame(width: 40, height: 40)
            .background(on ? (kind == .emoji ? c.color.opacity(0.25) : c.color) : Color.clear,
                        in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(choice.name.capitalizedFirst)
        .accessibilityLabel(choice.name)
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func pick(tint: String = "", icon: String? = nil) {
        Task {
            error = await model.setComputerLook(userID, tint: tint, icon: icon)
        }
    }
}

private extension String {
    var capitalizedFirst: String { prefix(1).uppercased() + dropFirst() }
}

extension Color {
    /// "#RRGGBB", in sRGB.
    var hexString: String? {
        guard let c = NSColor(self).usingColorSpace(.sRGB) else { return nil }
        let v = [c.redComponent, c.greenComponent, c.blueComponent].map { max(0, min(255, Int(($0 * 255).rounded()))) }
        return String(format: "#%02X%02X%02X", v[0], v[1], v[2])
    }
}

/// A colour swatch for a menu: drawn in its colour (a menu draws symbols as
/// templates, in the text's colour).
func swatchImage(_ hex: String, size: CGFloat = 12) -> NSImage {
    let image = NSImage(size: NSSize(width: size, height: size), flipped: false) { r in
        // (Drawn when shown: the text's colour follows light and dark.)
        (Color(hex: hex).map { NSColor($0) } ?? .labelColor).setFill()
        NSBezierPath(ovalIn: r.insetBy(dx: 0.5, dy: 0.5)).fill()
        return true
    }
    image.isTemplate = false
    return image
}
