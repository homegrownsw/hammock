import SwiftUI
#if canImport(AppKit)
import AppKit
#else
import UIKit
#endif

/// A person's picture, or their initials on their colour. While they type,
/// a ring in their colour glows.
public struct AvatarView: View {
    let person: Person
    var size: CGFloat = 20
    var typing = false
    @State private var glow = false

    public init(person: Person, size: CGFloat = 20, typing: Bool = false) {
        self.person = person
        self.size = size
        self.typing = typing
    }

    private var color: Color { Color(hue: person.hue, saturation: 0.55, brightness: 0.78) }

    public var body: some View {
        ZStack {
            if let image = PlatformImage(data: person.avatar) {
                #if canImport(AppKit)
                Image(nsImage: image).resizable().scaledToFill()
                #else
                Image(uiImage: image).resizable().scaledToFill()
                #endif
            } else {
                color
                Text(person.initials)
                    .font(.system(size: size * 0.42, weight: .semibold, design: .rounded))
                    .foregroundStyle(.white)
            }
        }
        .frame(width: size, height: size)
        .clipShape(Circle())
        .overlay {
            Circle().strokeBorder(.background, lineWidth: max(1, size / 14))
        }
        .background {
            if typing {
                Circle()
                    .fill(color.opacity(glow ? 0.55 : 0.2))
                    .padding(-size * 0.18)
                    .onAppear { withAnimation(.easeInOut(duration: 0.6).repeatForever()) { glow = true } }
            }
        }
        .help(person.deviceName.isEmpty ? person.name : "\(person.name) · \(person.deviceName)")
        .accessibilityLabel(person.name + (typing ? ", typing" : ""))
    }
}

/// A few people, overlapping, as in a shared document's header.
public struct AvatarStack: View {
    let people: [(Member, Person)]
    var size: CGFloat = 18

    public init(_ people: [(Member, Person)], size: CGFloat = 18) {
        self.people = people
        self.size = size
    }

    public var body: some View {
        // Nothing at all with nobody to show: an empty HStack still takes
        // its row's spacing, which moved a collapsed Group's computer badge
        // off the column the other Groups' badges are in.
        if !people.isEmpty {
            HStack(spacing: -size * 0.3) {
                ForEach(people.prefix(4), id: \.0.userID) { m, p in
                    AvatarView(person: p, size: size, typing: m.typing)
                }
            }
        }
    }
}
