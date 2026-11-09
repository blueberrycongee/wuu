import XCTest
@testable import WuuCore

final class PersonalLocationTests: XCTestCase {
    private let host = String(repeating: "A", count: 43)
    func testNotificationLocatorRejectsWrongRecipientAndSessionRouting() throws {
        let payload: [String: String] = ["version": "1", "product_domain": "personal", "user_space_id": "local", "host": host, "agent_id": "main", "task_id": "task-one", "message_seq": "17", "recipient": "phone"]
        let locator = try XCTUnwrap(PersonalLocation.notification(payload, recipient: "phone"))
        XCTAssertEqual(locator.agentID, "main"); XCTAssertEqual(locator.taskID, "task-one"); XCTAssertEqual(locator.messageSequence, 17)
        XCTAssertNil(PersonalLocation.notification(payload, recipient: "other"))
        var invalid = payload; invalid["session_id"] = "new-thread"
        XCTAssertNil(PersonalLocation.notification(invalid, recipient: "phone"))
        invalid = payload; invalid["message_seq"] = "0"
        XCTAssertNil(PersonalLocation.notification(invalid, recipient: "phone"))
        invalid = payload; invalid["version"] = "2"
        XCTAssertNil(PersonalLocation.notification(invalid, recipient: "phone"))
    }
    func testURLLocatorRejectsAmbiguityAndKeepsStableBusinessLocation() throws {
        let url = try XCTUnwrap(URL(string: "wuu://personal?version=1&product_domain=personal&user_space_id=local&host=\(host)&agent_id=main&message_seq=17"))
        let locator = try XCTUnwrap(PersonalLocation(url: url))
        XCTAssertEqual(locator.agentID, "main"); XCTAssertEqual(locator.messageSequence, 17)
        XCTAssertNil(PersonalLocation(url: URL(string: url.absoluteString + "&agent_id=other")!))
        XCTAssertNil(PersonalLocation(url: URL(string: url.absoluteString + "&session_id=arbitrary")!))
        XCTAssertNil(PersonalLocation(url: URL(string: url.absoluteString.replacingOccurrences(of: "wuu:", with: "https:"))!))
    }
}
