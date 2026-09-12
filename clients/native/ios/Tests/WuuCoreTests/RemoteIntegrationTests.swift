import Foundation
import XCTest
@testable import WuuCore

final class RemoteIntegrationTests: XCTestCase {
    func testRealGoHostSendAndFreshReconnect() async throws {
        guard let executable = ProcessInfo.processInfo.environment["WUU_NATIVE_TESTHOST"] else {
            throw XCTSkip("Set WUU_NATIVE_TESTHOST to the built clients/native/testhost binary")
        }
        let process = Process()
        let input = Pipe(), output = Pipe()
        process.executableURL = URL(fileURLWithPath: executable)
        process.standardInput = input; process.standardOutput = output
        try process.run()
        defer {
            try? input.fileHandleForWriting.write(contentsOf: Data("quit\n".utf8))
            process.waitUntilExit()
        }
        var data = Data()
        while let byte = try output.fileHandleForReading.read(upToCount: 1), !byte.isEmpty {
            if byte == Data([10]) { break }; data.append(byte)
        }
        let fixture = try JSONDecoder().decode(JSONValue.self, from: data)
        let account = try JSONDecoder().decode(AccountSession.self, from: data)
        let host = try XCTUnwrap(fixture["host"].string)
        let remote = try RemoteConnection(account: account, host: host)
        try await remote.connect()
        _ = try await remote.call("initialize")
        let started = try await remote.call("thread/start", params: ["cwd": fixture["workspace"]])
        let id = try XCTUnwrap(started["thread"]["id"].string)
        let completed = expectation(description: "real Go execution completed")
        let notes = Task<ChatThread?, Never> {
            var projected: ChatThread?
            for await event in remote.events {
                if case .snapshot("selected", let result) = event {
                    projected = ChatThread(result["thread"], pending: result["pending_user_messages"].array)
                }
                if case .notification(let method, let params) = event {
                    projected?.apply(method, params)
                    if method == "turn/completed", params["thread_id"].string == id {
                        completed.fulfill(); return projected
                    }
                }
            }
            return nil
        }
        defer { notes.cancel() }
        _ = try await remote.call("turn/start", params: ["thread_id": .string(id), "prompt": "native phone message"])
        let queued = try await remote.call("turn/queue", params: ["thread_id": .string(id), "prompt": "follow up"])
        let pendingSnapshot = try await remote.call("thread/resume", params: ["session_id": .string(id), "response_only": true], snapshotTag: "selected")
        let pending = ChatThread(pendingSnapshot["thread"], pending: pendingSnapshot["pending_user_messages"].array)
        XCTAssertTrue(pending.messages.contains { $0.role == "pending" && $0.text == "follow up" })
        _ = try await remote.call("turn/dequeue", params: ["thread_id": .string(id), "queue_id": queued["queued"]["id"]])
        try input.fileHandleForWriting.write(contentsOf: Data("release\n".utf8))
        await fulfillment(of: [completed], timeout: 20)
        let snapshot = try await remote.call("thread/resume", params: ["session_id": .string(id), "response_only": true])
        let messages = ChatThread(snapshot["thread"]).messages
        let projected = await notes.value
        XCTAssertEqual(projected?.messages, messages)
        XCTAssertEqual(projected?.running, false)
        XCTAssertTrue(messages.contains { $0.role == "user" && $0.text == "native phone message" })
        XCTAssertTrue(messages.contains { $0.role == "assistant" && $0.text == "native transport verified" })
        await remote.disconnect()

        let restored = try RemoteConnection(account: account, host: host)
        try await restored.connect()
        _ = try await restored.call("initialize")
        let resumed = try await restored.call("thread/resume", params: ["session_id": .string(id), "response_only": true])
        XCTAssertEqual(ChatThread(resumed["thread"]).messages, messages)
        await restored.disconnect()
    }
}
