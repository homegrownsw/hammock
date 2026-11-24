import XCTest
@testable import HammockKit

/// The bar over a pane whose agent stopped at its usage limit (the Mac's
/// and the iPhone's LimitResumeBar): when it shows, and what it says.
final class LimitResumeTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_795_000_000) // midday-ish
    private var calendar: Calendar { Calendar.current }

    private func waiting(in seconds: TimeInterval, known: Bool = true, state: AgentState = .errored) -> Pane {
        Pane.with {
            $0.agent = .with {
                $0.state = state
                $0.limitResume = .with {
                    $0.atUnixMs = Int64((now.timeIntervalSince1970 + seconds) * 1000)
                    $0.resetKnown = known
                }
            }
        }
    }

    private func time(_ seconds: TimeInterval) -> String {
        now.addingTimeInterval(seconds).formatted(date: .omitted, time: .shortened)
    }

    func testOnlyWhileErrored() {
        XCTAssertNotNil(waiting(in: 60).limitResumeLabel(now: now))
        XCTAssertNil(waiting(in: 60, state: .working).limitResumeLabel(now: now))
        XCTAssertNil(Pane().limitResumeLabel(now: now))
    }

    func testSeenIsACalmWait() {
        var p = waiting(in: 60)
        XCTAssertFalse(p.agent.waitingOnLimit, "unseen: still its error")
        XCTAssertEqual(p.agent.shownState, .errored)
        p.agent.seen = true
        XCTAssertTrue(p.agent.waitingOnLimit)
        XCTAssertEqual(p.agent.shownState, .idle, "seen: ranked at rest, out of Needs You")
        XCTAssertNotNil(p.limitResumeLabel(now: now))
    }

    func testSaysWhen() {
        let startOfDay = calendar.startOfDay(for: now)
        let today = startOfDay.addingTimeInterval(23 * 3600).timeIntervalSince(now)
        let tomorrow = calendar.date(byAdding: .day, value: 1, to: startOfDay)!.addingTimeInterval(9 * 3600).timeIntervalSince(now)
        let later = calendar.date(byAdding: .day, value: 3, to: startOfDay)!.addingTimeInterval(10 * 3600).timeIntervalSince(now)
        let far = calendar.date(byAdding: .day, value: 9, to: startOfDay)!.addingTimeInterval(10 * 3600).timeIntervalSince(now)
        XCTAssertEqual(waiting(in: today).limitResumeLabel(now: now), "Continues at \(time(today))")
        XCTAssertEqual(waiting(in: today, known: false).limitResumeLabel(now: now), "Tries again at \(time(today))")
        XCTAssertEqual(waiting(in: tomorrow).limitResumeLabel(now: now), "Continues tomorrow at \(time(tomorrow))")
        let weekday = now.addingTimeInterval(later).formatted(.dateTime.weekday(.wide))
        XCTAssertEqual(waiting(in: later).limitResumeLabel(now: now), "Continues \(weekday) at \(time(later))")
        let date = now.addingTimeInterval(far).formatted(.dateTime.day().month(.abbreviated))
        XCTAssertEqual(waiting(in: far).limitResumeLabel(now: now), "Continues \(date) at \(time(far))")
    }
}
