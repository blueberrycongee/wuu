import XCTest
@testable import WuuCore

final class PersonalDraftTests: XCTestCase {
    func testColdRecoveryPreservesExactIdentityAndBytesWithoutAutomaticSend() async throws {
        let folder = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        let initialized: JSONValue = ["user_space_id": "local", "product_domain": "personal", "extension_inventory": [["id": "plugin:bundled:personal", "fingerprint": "one", "runtime_state": "active"]]]
        let first = PersonalClient { _, input in ["result": ["agent": ["agent_id": input["input"]["agent_id"], "user_space_id": "local"]]] }
        try await first.configure(initialized); _ = try await first.selectAgent("main")
        let file = try InputAttachment(filename: "photo.pdf", mediaType: "application/pdf", data: Data("%PDF-exact".utf8))
        let draft = try await first.prepareMessage("  exact\ntext  ", attachments: [file])
        let store = PersonalDraftStore(directory: folder, identity: ["account", "login", "host"])
        try await store.save(draft); try await store.invalidate(remove: false)
        let other = PersonalDraftStore(directory: folder, identity: ["other", "login", "host"])
        let absent = try await other.load(); XCTAssertNil(absent)
        let reopened = PersonalDraftStore(directory: folder, identity: ["account", "login", "host"])
        let loaded = try await reopened.load(); let recovered = try XCTUnwrap(loaded)
        XCTAssertEqual(recovered.requestID, draft.requestID); XCTAssertEqual(recovered.prompt, draft.prompt); XCTAssertEqual(recovered.attachments[0].data, file.data)
        let second = PersonalClient { _, input in
            if input["method"].string == "agents.send" {
                XCTAssertEqual(input["input"]["request_id"].string, draft.requestID); XCTAssertEqual(input["user_input"]["prompt"].string, draft.prompt)
                return ["result": ["state": "accepted"]]
            }
            return ["result": ["agent": ["agent_id": input["input"]["agent_id"], "user_space_id": "local"]]]
        }
        try await second.configure(initialized); _ = try await second.selectAgent("other")
        do { _ = try await second.restoreDraft(recovered); XCTFail("Recovered draft crossed agents") } catch {}
        _ = try await second.selectAgent("main"); let retry = try await second.restoreDraft(recovered); _ = try await second.send(retry)
        try await reopened.invalidate(remove: true)
        do { try await reopened.save(draft); XCTFail("Closed draft store resurrected after logout") } catch {}
        let cleared = try await PersonalDraftStore(directory: folder, identity: ["account", "login", "host"]).load(); XCTAssertNil(cleared)
    }
    func testPersonalDraftRejectsOversizedUTF8FilenameBeforeTransport() async throws {
        let client = PersonalClient { _, input in ["result": ["agent": ["agent_id": input["input"]["agent_id"], "user_space_id": "local"]]] }
        try await client.configure(["user_space_id": "local", "product_domain": "personal", "extension_inventory": [["id": "plugin:bundled:personal", "fingerprint": "one", "runtime_state": "active"]]])
        _ = try await client.selectAgent("main")
        for name in [String(repeating: "文", count: 100) + ".pdf", "bad\u{0085}.pdf"] {
            let file = try InputAttachment(filename: name, mediaType: "application/pdf", data: Data("%PDF-data".utf8))
            do { _ = try await client.prepareMessage("", attachments: [file]); XCTFail("Invalid Personal filename admitted") } catch {}
        }
    }
}
