import SwiftUI

/// AppModel.toast: a short note that slides in and goes by itself.
public struct ToastOverlay: View {
    let model: AppModel
    /// The theme's colour, laid nearly solid over the material (the Mac's
    /// toolbar tint, so it looks like the window it's in); nil: the material.
    let tint: Color?

    public init(model: AppModel, tint: Color? = nil) {
        self.model = model
        self.tint = tint
    }

    public var body: some View {
        ZStack {
            if let text = model.toast {
                Text(text)
                    .font(.callout.weight(.medium))
                    .padding(.horizontal, 14)
                    .padding(.vertical, 8)
                    .background {
                        Capsule().fill(.regularMaterial)
                            .overlay { tint.map { Capsule().fill($0.opacity(0.9)) } }
                    }
                    .overlay(Capsule().strokeBorder(.separator.opacity(0.5)))
                    .shadow(color: .black.opacity(0.12), radius: 6, y: 2)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .animation(.snappy(duration: 0.25), value: model.toast)
        .allowsHitTesting(false)
    }
}
