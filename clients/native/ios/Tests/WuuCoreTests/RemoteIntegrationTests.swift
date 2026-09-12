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
        let exited = DispatchSemaphore(value: 0)
        process.terminationHandler = { _ in exited.signal() }
        try process.run()
        try output.fileHandleForWriting.close()
        try input.fileHandleForReading.close()
        defer {
            try? input.fileHandleForWriting.write(contentsOf: Data("quit\n".utf8))
            closeFixture(process, exited: exited)
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
        let choices = try await remote.call("config/read")["providers"].array.map(RemoteProvider.init)
        XCTAssertTrue(choices.contains { $0.id == "native-test" && $0.models.contains { $0.id == "alternate" && $0.variants.contains("careful") } })
        let settingsThread = try await remote.call("thread/start")["thread"]
        let settingsID = try XCTUnwrap(settingsThread["id"].string)
        var selection = ThreadSettings(settingsThread)
        XCTAssertThrowsError(try selection.updateParams(threadID: " "))
        selection.model = "alternate"; selection.variant = "careful"; selection.permission = "read_only"
        _ = try await remote.call("config/model/update", params: selection.updateParams(threadID: settingsID))
        let settingsSnapshot = try await remote.call("thread/resume", params: ["session_id": .string(settingsID), "response_only": true])
        XCTAssertEqual(ThreadSettings(settingsSnapshot["thread"]), selection)
        var metadataProjection = ChatThread(settingsThread)
        metadataProjection.apply("thread/updated", ["thread": settingsSnapshot["thread"]])
        XCTAssertEqual(metadataProjection.settings, selection)
        let defaults = try await remote.call("config/read")
        XCTAssertEqual(defaults["model"].string, "native-test")
        let unaffected = try await remote.call("thread/start")["thread"]
        XCTAssertEqual(ThreadSettings(unaffected).model, "native-test")
        XCTAssertEqual(ThreadSettings(unaffected).permission, "standard")
        let pagedID = try XCTUnwrap(fixture["paged_thread"].string)
        let firstPage = try await remote.call("thread/resume", params: ["session_id": .string(pagedID), "response_only": true, "history_page": true])
        var history = ChatThread(firstPage["thread"])
        XCTAssertFalse(history.historyCursor.isEmpty)
        for _ in 0..<10 where !history.historyCursor.isEmpty {
            let page = try await remote.call("thread/history/read", params: ["thread_id": .string(pagedID), "cursor": .string(history.historyCursor)])
            history.prependHistory(page)
        }
        XCTAssertEqual(history.historyCursor, "")
        XCTAssertEqual(history.messages.count, 86)
        XCTAssertEqual(history.messages.first?.text, "message 0")
        let large = try XCTUnwrap(history.messages.last)
        XCTAssertFalse(large.contentRef.isEmpty)
        let item = try await remote.readContent(large.contentRef, threadID: pagedID)
        history.expandContent(large.contentRef, item: item)
        XCTAssertEqual(history.messages.last?.text, String(repeating: "长消息🌱\n", count: 20_000))
        XCTAssertEqual(history.messages.last?.contentRef, "")
        let toolID = try XCTUnwrap(fixture["tool_thread"].string)
        let toolSnapshot = try await remote.call("thread/resume", params: ["session_id": .string(toolID), "response_only": true, "history_page": true])
        var toolHistory = ChatThread(toolSnapshot["thread"])
        let activity = try XCTUnwrap(toolHistory.messages.first { $0.tool != nil })
        XCTAssertEqual(activity.tool?.status, "completed")
        XCTAssertLessThan(activity.tool?.result.utf8.count ?? 0, 64 * 1024)
        XCTAssertFalse(activity.contentRef.isEmpty)
        let fullTool = try await remote.readContent(activity.contentRef, threadID: toolID)
        toolHistory.expandContent(activity.contentRef, item: fullTool)
        let expanded = try XCTUnwrap(toolHistory.messages.first { $0.tool != nil })
        XCTAssertEqual(expanded.tool?.result, String(repeating: "工具结果🌱\n", count: 20_000))
        XCTAssertEqual(expanded.tool?.arguments, #"{"path":"fixture.txt"}"#)
        XCTAssertEqual(expanded.contentRef, "")
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
        let image = try InputAttachment(filename: "photo.png", mediaType: "image/png", data: XCTUnwrap(Data(base64Encoded: fixture["image"].string ?? "")))
        let pdf = try InputAttachment(filename: "document.pdf", mediaType: "application/pdf", data: XCTUnwrap(Data(base64Encoded: fixture["pdf"].string ?? "")))
        _ = try await remote.call("turn/start", params: ChatInput(text: "native phone message", attachments: [image, pdf]).params(threadID: id, queued: false))
        let queued = try await remote.call("turn/queue", params: ChatInput(text: "follow up").params(threadID: id, queued: true))
        let pendingSnapshot = try await remote.call("thread/resume", params: ["session_id": .string(id), "response_only": true], snapshotTag: "selected")
        let pending = ChatThread(pendingSnapshot["thread"], pending: pendingSnapshot["pending_user_messages"].array)
        XCTAssertTrue(pending.pending.contains { !$0.held && $0.text == "follow up" })
        _ = try await remote.call("turn/dequeue", params: ["thread_id": .string(id), "queue_id": queued["queued"]["id"]])
        try input.fileHandleForWriting.write(contentsOf: Data("release\n".utf8))
        await fulfillment(of: [completed], timeout: 20)
        notes.cancel()
        let snapshot = try await remote.call("thread/resume", params: ["session_id": .string(id), "response_only": true])
        let messages = ChatThread(snapshot["thread"]).messages
        let projected = await notes.value
        XCTAssertEqual(projected?.messages, messages)
        XCTAssertEqual(projected?.running, false)
        XCTAssertTrue(messages.contains { $0.role == "user" && $0.text == "native phone message" })
        XCTAssertEqual(messages.first(where: { $0.role == "user" })?.attachments.count, 2)
        XCTAssertEqual(messages.first(where: { $0.role == "user" })?.attachments.last?["filename"].string, "document.pdf")
        XCTAssertTrue(messages.contains { $0.role == "assistant" && $0.text == "native transport verified" })
        let pagedAttachments = try await remote.call("thread/resume", params: ["session_id": .string(id), "response_only": true, "history_page": true])
        let user = try XCTUnwrap(ChatThread(pagedAttachments["thread"]).messages.first { $0.role == "user" })
        XCTAssertFalse(user.attachments[0]["remote_ref"].string?.isEmpty ?? true)
        let downloaded = try await remote.readAttachment(user.attachments[0], threadID: id, messageID: user.id)
        XCTAssertEqual(downloaded.data, image.data)
        let document = try await remote.readAttachment(user.attachments[1], threadID: id, messageID: user.id)
        XCTAssertEqual(document.data, pdf.data)
        do {
            _ = try await remote.readAttachment(user.attachments[0], threadID: pagedID, messageID: user.id)
            XCTFail("a reference must not cross conversation boundaries")
        } catch NativeError.invalid {}
        do {
            _ = try await remote.readAttachment(user.attachments[0], threadID: id, messageID: "other:item")
            XCTFail("a reference must not cross message boundaries")
        } catch NativeError.invalid {}
        await remote.disconnect()

        let restored = try RemoteConnection(account: account, host: host)
        try await restored.connect()
        _ = try await restored.call("initialize")
        let resumed = try await restored.call("thread/resume", params: ["session_id": .string(id), "response_only": true])
        XCTAssertEqual(ChatThread(resumed["thread"]).messages, messages)
        let renamed = try await restored.call("thread/rename", params: ["thread_id": .string(id), "title": "Searchable native conversation"])
        XCTAssertEqual(renamed["thread"]["title"].string, "Searchable native conversation")
        let found = try await restored.call("thread/search", params: ["query": "native phone message", "limit": 100])
        XCTAssertTrue(found["results"].array.contains { $0["thread"]["id"].string == id })
        _ = try await restored.call("thread/archive", params: ["thread_id": .string(id), "archived": true])
        let active = try await restored.call("thread/listAll", params: ["summary_only": true])
        XCTAssertFalse(active["threads"].array.contains { $0["id"].string == id })
        let archive = try await restored.call("thread/listArchived", params: ["summary_only": true])
        XCTAssertTrue(archive["threads"].array.contains { ChatThread($0).id == id && ChatThread($0).archived }, String(describing: archive))
        _ = try await restored.call("thread/archive", params: ["thread_id": .string(id), "archived": false])
        let unarchived = try await restored.call("thread/listAll", params: ["summary_only": true])
        XCTAssertTrue(unarchived["threads"].array.contains { $0["id"].string == id }, String(describing: unarchived))

        let interrupted = expectation(description: "interruption settles and holds queued work")
        let holdProjection = Task<ChatThread?, Never> {
            var projected = ChatThread(resumed["thread"])
            for await event in restored.events {
                if case .notification(let method, let params) = event {
                    projected.apply(method, params)
                    if !projected.running, projected.pending.contains(where: { $0.held && $0.id == "held-native" }) {
                        interrupted.fulfill(); return projected
                    }
                }
            }
            return nil
        }
        _ = try await restored.call("turn/start", params: ["thread_id": .string(id), "prompt": "interrupt this"])
        _ = try await restored.call("turn/queue", params: ["thread_id": .string(id), "prompt": "continue once", "client_id": "held-native"])
        _ = try await restored.call("turn/interrupt", params: ["thread_id": .string(id)])
        await fulfillment(of: [interrupted], timeout: 20)
        holdProjection.cancel()
        let heldSnapshot = try await restored.call("thread/resume", params: ["session_id": .string(id), "response_only": true])
        let held = ChatThread(heldSnapshot["thread"], held: heldSnapshot["held_user_messages"].array)
        let projectedHold = await holdProjection.value
        XCTAssertTrue(projectedHold?.pending.contains { $0.held && $0.id == "held-native" } == true)
        let message = try XCTUnwrap(held.pending.first { $0.id == "held-native" })
        _ = try await restored.call("turn/steer", params: message.resumeParams)
        let consumed = try await restored.call("thread/resume", params: ["session_id": .string(id), "response_only": true])
        XCTAssertTrue(consumed["held_user_messages"].array.isEmpty)
        XCTAssertEqual(ChatThread(consumed["thread"]).messages.filter { $0.text == "continue once" }.count, 1)
        _ = try await restored.call("turn/interrupt", params: ["thread_id": .string(id)])
        await restored.disconnect()
    }
    private func closeFixture(_ process: Process, exited: DispatchSemaphore) {
        if exited.wait(timeout: .now() + 10) != .success {
            if process.isRunning { process.terminate() }
            XCTFail("Execution fixture did not exit")
        }
    }
}
