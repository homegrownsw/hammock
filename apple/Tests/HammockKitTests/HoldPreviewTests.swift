import CoreGraphics
import XCTest
@testable import HammockKit

/// A session or tab row's touch and hold: its preview's size, and the
/// answers its menu offers a permission prompt.
final class HoldPreviewTests: XCTestCase {
    private let room = CGSize(width: 370, height: 800)

    func testAPaneWithoutAGridTakesMostOfTheScreen() {
        XCTAssertEqual(HoldPreview.size(room: room, cols: 0, rows: 0), CGSize(width: 370, height: 480))
    }

    func testATerminalTakesItsShapeWithinLimits() {
        // Wide and short: no shorter than a third of the screen.
        XCTAssertEqual(HoldPreview.size(room: room, cols: 200, rows: 20).height, 267)
        // Tall: no taller than 60% of it.
        XCTAssertEqual(HoldPreview.size(room: room, cols: 40, rows: 100).height, 480)
        // In between: its own shape, inside the padding, and the name above it.
        let s = HoldPreview.size(room: room, cols: 100, rows: 50)
        XCTAssertEqual(s.width, 370)
        XCTAssertEqual(s.height, ((370 - 44) * 50 / 60 + 44 + 24).rounded())
    }

    private func prompt(_ edit: (inout AttentionItem) -> Void = { _ in }) -> AttentionItem {
        var i = AttentionItem.with {
            $0.paneID = "p"
            $0.state = .needsInput
            $0.detail = "Bash command\n  ls\nDo you want to proceed?"
        }
        edit(&i)
        return i
    }

    func testAPermissionPromptOffersAllowAndDeny() {
        XCTAssertEqual(prompt().promptAnswers(scheduledClaude: false), [.allow, .deny])
        XCTAssertEqual(prompt().promptAnswers(scheduledClaude: true), [.allow, .allowForSchedule, .deny])
    }

    func testOnlyDenyWhenThereIsMoreThanTheCardShows() {
        let long = prompt { $0.detail = "…\nrm -rf build\nDo you want to proceed?" }
        XCTAssertEqual(long.promptAnswers(scheduledClaude: true), [.deny])
    }

    func testNothingForAQuestionOrAnAgentNotAsking() {
        XCTAssertEqual(prompt { $0.question = true }.promptAnswers(scheduledClaude: false), [])
        XCTAssertEqual(prompt { $0.state = .working }.promptAnswers(scheduledClaude: false), [])
        XCTAssertEqual(prompt { $0.state = .errored }.promptAnswers(scheduledClaude: false), [])
    }

    func testAnswersMapToTheDaemonsDecisions() {
        XCTAssertEqual(PromptAnswer.allow.decision, .allow)
        XCTAssertEqual(PromptAnswer.allowForSchedule.decision, .allowSchedule)
        XCTAssertEqual(PromptAnswer.deny.decision, .deny)
    }
}
