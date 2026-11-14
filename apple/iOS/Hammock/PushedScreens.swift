import SwiftUI
import UIKit

extension View {
    /// Hides the tab bar while this screen is pushed (on a compact width;
    /// a wide screen keeps it), bringing it back in step with the swipe
    /// back. SwiftUI's `.toolbar(.hidden, for: .tabBar)` hides it (before
    /// the screen's first layout: hidden any later, the screen laid out
    /// again taller, its message box dropping and its scroll jumping), but
    /// shows it only once a swipe back has finished, so it popped in.
    func hidesTabBarWhenPushed(_ hide: Bool) -> some View {
        modifier(TabBarHider(hide: hide))
    }

    /// Holds this screen still while it's popped: it slides away as it was
    /// when the swipe began. With the keyboard up, the keyboard leaves
    /// sliding sideways with the screen, but SwiftUI gives back the space
    /// it took as the swipe goes, so the conversation scrolled down under
    /// it (Messages stays still).
    func steadyWhilePopped() -> some View {
        background { PopFreezer().frame(width: 0, height: 0) }
    }
}

extension EnvironmentValues {
    /// This screen has been gone back from and is only sliding away (kept
    /// a moment by `CockpitSplit`), so it no longer hides the tab bar.
    @Entry var screenLeaving = false
}

private struct TabBarHider: ViewModifier {
    let hide: Bool
    @Environment(\.screenLeaving) private var leaving

    func body(content: Content) -> some View {
        // Still hidden while it slid away, SwiftUI took the tab bar for
        // hidden, though TabBarShower had shown it: a screen opened before
        // this one went, hiding it too, changed nothing, and the bar stayed.
        content
            .toolbar(hide && !leaving ? .hidden : .automatic, for: .tabBar)
            .background { if hide { TabBarShower().frame(width: 0, height: 0) } }
    }
}

/// Covers its screen with a picture of itself as a pop begins, taken away
/// if the pop is called off (a swipe back let go of): what changes under it
/// meanwhile isn't seen.
private struct PopFreezer: UIViewControllerRepresentable {
    func makeUIViewController(context: Context) -> Freezer { Freezer() }
    func updateUIViewController(_ vc: Freezer, context: Context) {}

    final class Freezer: UIViewController {
        private weak var pushed: UIViewController?

        override func viewWillAppear(_ animated: Bool) {
            super.viewWillAppear(animated)
            pushed = UIViewController.pushed(from: self)
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            // Its own stack's transition (not a cover over it), to the list.
            guard let screen = pushed?.view,
                  let coordinator = pushed?.navigationController?.transitionCoordinator,
                  let to = coordinator.viewController(forKey: .to), UIViewController.pushed(from: to) == nil,
                  let still = screen.snapshotView(afterScreenUpdates: false) else { return }
            still.frame = screen.bounds
            screen.addSubview(still)
            coordinator.animate(alongsideTransition: nil) { _ in still.removeFromSuperview() }
        }
    }
}

/// Fades the tab bar in as its screen leaves for the list, alongside the
/// transition: an interactive swipe scrubs it, and letting go of the swipe
/// puts it away again. SwiftUI then finds it already shown.
private struct TabBarShower: UIViewControllerRepresentable {
    func makeUIViewController(context: Context) -> Shower { Shower() }
    func updateUIViewController(_ vc: Shower, context: Context) {}

    final class Shower: UIViewController {
        /// The controller its navigation controller pushed.
        private weak var pushed: UIViewController?

        override func viewWillAppear(_ animated: Bool) {
            super.viewWillAppear(animated)
            pushed = UIViewController.pushed(from: self)
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            // Back to the list (not on to another screen, which hides it
            // too, or under a cover, which isn't its stack's transition).
            guard let coordinator = pushed?.navigationController?.transitionCoordinator,
                  let to = coordinator.viewController(forKey: .to), UIViewController.pushed(from: to) == nil,
                  let tabs = tabBarController, tabs.isTabBarHidden else { return }
            // Faded: shown outright, it would be there at once over the
            // screen sliding away.
            let bar = tabs.tabBar
            tabs.setTabBarHidden(false, animated: false)
            bar.alpha = 0
            coordinator.animate(alongsideTransition: { _ in
                bar.alpha = 1
            }, completion: { ctx in
                if ctx.isCancelled { tabs.setTabBarHidden(true, animated: false) }
                bar.alpha = 1
            })
        }
    }
}

private extension UIViewController {
    /// The ancestor of vc a navigation controller pushed, nil for the list
    /// (a stack's root). On a phone the split view's detail stack is itself
    /// pushed onto the list's, so its root goes on up.
    static func pushed(from vc: UIViewController) -> UIViewController? {
        if let nav = vc as? UINavigationController, let top = nav.topViewController, top !== vc {
            return pushed(from: top) ?? (nav.parent is UINavigationController && nav.parent?.children.first !== nav ? nav : nil)
        }
        var vc: UIViewController? = vc
        while let v = vc, let p = v.parent {
            if p is UINavigationController, p.children.first !== v { return v }
            vc = p
        }
        return nil
    }
}
