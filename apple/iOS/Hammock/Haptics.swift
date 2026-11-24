import UIKit

/// The taps you feel when an answer goes: on Allow, Deny, letting someone
/// join, a Hammock Key approval and a pairing code read. Imperative rather
/// than `.sensoryFeedback`, since the card answered goes away with the answer.
@MainActor
enum Haptics {
    /// Yes: Allow, Let View or Drive, a key approved, a code recognised.
    static func success() {
        UINotificationFeedbackGenerator().notificationOccurred(.success)
    }

    /// No: Deny, Decline, Don't Allow. A firm tap, not the error buzz:
    /// nothing went wrong.
    static func refuse() {
        UIImpactFeedbackGenerator(style: .rigid).impactOccurred()
    }

    /// A design note's pin dropped.
    static func pin() {
        UIImpactFeedbackGenerator(style: .medium).impactOccurred()
    }
}
