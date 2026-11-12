import XCTest
@testable import HammockKit

#if os(macOS)
final class AppLocationTests: XCTestCase {
    private let home = "/Users/me"

    func testApplicationsFoldersCount() {
        XCTAssertTrue(AppLocation.isInApplications("/Applications/Hammock.app", home: home))
        XCTAssertTrue(AppLocation.isInApplications("/Applications/Utilities/Hammock.app", home: home))
        XCTAssertTrue(AppLocation.isInApplications("/Users/me/Applications/Hammock.app", home: home))
    }

    func testElsewhereDoesNot() {
        XCTAssertFalse(AppLocation.isInApplications("/Volumes/Hammock/Hammock.app", home: home))
        XCTAssertFalse(AppLocation.isInApplications("/Users/me/Downloads/Hammock.app", home: home))
        XCTAssertFalse(AppLocation.isInApplications("/Users/me/Developer/app/dist/Hammock.app", home: home))
        // Not fooled by a name that starts the same.
        XCTAssertFalse(AppLocation.isInApplications("/Applications Old/Hammock.app", home: home))
        XCTAssertFalse(AppLocation.isInApplications("/Users/me/Applications2/Hammock.app", home: home))
        // Or a path that climbs back out.
        XCTAssertFalse(AppLocation.isInApplications("/Applications/../tmp/Hammock.app", home: home))
    }

    func testTranslocation() {
        XCTAssertTrue(AppLocation.isTranslocated("/private/var/folders/xy/abc/T/AppTranslocation/1234-ABCD/d/Hammock.app"))
        XCTAssertFalse(AppLocation.isTranslocated("/Users/me/Downloads/Hammock.app"))
    }

    func testDownloads() {
        XCTAssertTrue(AppLocation.isInDownloads("/Users/me/Downloads/Hammock.app", home: home))
        XCTAssertTrue(AppLocation.isInDownloads("/Users/me/Downloads/Hammock 2/Hammock.app", home: home))
        XCTAssertFalse(AppLocation.isInDownloads("/Users/me/Desktop/Hammock.app", home: home))
    }

    func testDiskImageVolume() {
        XCTAssertEqual(AppLocation.volume(of: "/Volumes/Hammock/Hammock.app"), "/Volumes/Hammock")
        XCTAssertEqual(AppLocation.volume(of: "/Volumes/Hammock 1/Hammock.app"), "/Volumes/Hammock 1")
        XCTAssertNil(AppLocation.volume(of: "/Users/me/Downloads/Hammock.app"))
        XCTAssertNil(AppLocation.volume(of: "/Volumes"))
    }

    func testExistingCopy() {
        XCTAssertEqual(AppLocation.existing(installed: nil, running: "900"), .none)
        XCTAssertEqual(AppLocation.existing(installed: "900", running: "900"), .useIt)
        XCTAssertEqual(AppLocation.existing(installed: "950", running: "900"), .useIt)
        XCTAssertEqual(AppLocation.existing(installed: "850", running: "900"), .older)
        // Unreadable build numbers: use what's installed rather than replace it.
        XCTAssertEqual(AppLocation.existing(installed: "", running: "900"), .useIt)
    }
}
#endif
