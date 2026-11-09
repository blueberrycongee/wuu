import XCTest
@testable import CUAMacCore

final class WindowRestorationTests: XCTestCase {
    func testRecoverySkipsLiveOwnerAndRetainsFailedRestoration() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let record = WindowRestorationRecord(processID: 42, launchTime: 10, windowID: 7, identifier: "window", original: CGRect(x: 10, y: 20, width: 100, height: 100), parked: CGRect(x: 5000, y: 5000, width: 100, height: 100))
        var owner: WindowRestorationJournal? = try WindowRestorationJournal(directory: directory)
        try owner!.record(record)
        let successor = try WindowRestorationJournal(directory: directory)
        var attempts: [WindowRestorationRecord] = []
        successor.recoverAbandoned { attempts.append($0); return true }
        XCTAssertTrue(attempts.isEmpty)
        owner = nil
        successor.recoverAbandoned { attempts.append($0); return false }
        XCTAssertEqual(attempts, [record])
        successor.recoverAbandoned { attempts.append($0); return true }
        XCTAssertEqual(attempts, [record, record])
        successor.recoverAbandoned { _ in XCTFail("successful restore should be retired"); return true }
    }

    func testRepeatedConcealPreservesOriginalPosition() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let journal = try WindowRestorationJournal(directory: directory)
        let original = WindowRestorationRecord(processID: 42, launchTime: 10, windowID: 7, identifier: "", original: CGRect(x: 10, y: 20, width: 100, height: 100), parked: CGRect(x: 5000, y: 5000, width: 100, height: 100))
        try journal.record(original)
        try journal.record(WindowRestorationRecord(processID: 42, launchTime: 10, windowID: 7, identifier: "", original: original.parked, parked: original.parked))
        XCTAssertEqual(journal.records, [original])
        _ = try journal.restore { _ in false }
        XCTAssertEqual(journal.records.count, 1)
        _ = try journal.restore { _ in true }
        XCTAssertTrue(journal.records.isEmpty)
    }
}
