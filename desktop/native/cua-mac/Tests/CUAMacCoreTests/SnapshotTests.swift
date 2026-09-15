import XCTest
@testable import CUAMacCore

final class SnapshotTests: XCTestCase {
    func testReferencesRejectReplacedConsumedOrReincarnatedState() throws {
        var ticket = SnapshotTicket(processID: 42, launchTime: 10, controlEpoch: "lease-a")
        try ticket.validate(reference: ticket.id, processID: 42, launchTime: 10, epoch: "lease-a", requiresReference: true)
        XCTAssertThrowsError(try ticket.validate(reference: "old", processID: 42, launchTime: 10, epoch: "lease-a", requiresReference: true))
        XCTAssertThrowsError(try ticket.validate(reference: ticket.id, processID: 42, launchTime: 11, epoch: "lease-a", requiresReference: true))
        XCTAssertThrowsError(try ticket.validate(reference: ticket.id, processID: 42, launchTime: 10, epoch: "lease-b", requiresReference: true))
        ticket.invalidate()
        XCTAssertThrowsError(try ticket.validate(reference: ticket.id, processID: 42, launchTime: 10, epoch: "lease-a", requiresReference: true))
        // Fresh selector and keyboard actions need no stale element references.
        try ticket.validate(reference: nil, processID: 42, launchTime: 10, epoch: "lease-a", requiresReference: false)
    }

    func testPagesPreserveElementIDsDuplicatesAndHierarchy() {
        let text = "[0] AXWindow\n  [1] AXButton Save\n  [2] AXButton Save\n  [3] AXTextField Name"
        let first = SnapshotPage(text: text, query: nil, offset: 0, limit: 2)
        let second = SnapshotPage(text: text, query: nil, offset: first.nextOffset!, limit: 2)
        XCTAssertEqual(first.text + "\n" + second.text, text)
        XCTAssertNil(second.nextOffset)
        let matches = SnapshotPage(text: text, query: "save", offset: 0, limit: 10)
        XCTAssertEqual(matches.total, 2)
        XCTAssertTrue(matches.text.contains("  [2] AXButton Save"))
        XCTAssertEqual(SnapshotPage(text: text, query: nil, offset: 99, limit: 10).text, "")
    }

    func testPostconditionDoesNotTreatAmbiguityOrTruncationAsSuccess() throws {
        let saved = AXElementDescriptor(role: "AXTextField", title: "Name", description: "", frame: nil, identifier: "name", value: "Ada")
        let expectation = try AXExpectation(["title": "Name", "value": "Ada"])
        XCTAssertEqual(expectation.evaluate([saved], truncated: false), "matched")
        XCTAssertEqual(expectation.evaluate([saved, saved], truncated: false), "ambiguous")
        XCTAssertEqual(expectation.evaluate([saved], truncated: true), "unavailable")
        let absent = try AXExpectation(["title": "Name", "exists": false])
        XCTAssertEqual(absent.evaluate([], truncated: true), "unavailable")
        XCTAssertEqual(absent.evaluate([], truncated: false), "matched")
    }

    func testObservationOptionsValidateAtProtocolBoundary() throws {
        let command = try ComputerCommand(arguments: ["action": "observe", "mode": "ax", "limit": 999, "root_element_id": 12, "snapshot_id": "s"])
        XCTAssertEqual(command.observationMode, .ax)
        XCTAssertEqual(command.limit, 300)
        XCTAssertTrue(command.referencesSnapshot)
        XCTAssertThrowsError(try ComputerCommand(arguments: ["action": "observe", "mode": "unknown"]))
        XCTAssertThrowsError(try ComputerCommand(arguments: ["action": "click", "after": "unknown"]))
    }
}
