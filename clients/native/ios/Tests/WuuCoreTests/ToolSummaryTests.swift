import XCTest
@testable import WuuCore

final class ToolSummaryTests: XCTestCase {
    func testDesktopBundleRunsWithoutBrowserAndTracksTransitions() async throws {
        let native = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent()
        let script = try String(contentsOf: native.appendingPathComponent("shared-ui/NativeUI/process.js"), encoding: .utf8)
        let engine = ToolSummaryEngine(script: script)
        let started: JSONValue = ["id": "read", "type": "tool_call", "name": "read_file", "status": "in_progress",
            "arguments": "{\"path\":\"README.md\"}"]
        let running = try await engine.summarize([started])
        XCTAssertEqual(running.activity, "read")
        XCTAssertFalse(running.failed)
        XCTAssertTrue(running.text.contains("README.md"))
        var fields = ["id": JSONValue.string("read"), "type": "tool_call", "name": "read_file", "status": "failed", "error": "File not found"]
        fields["arguments"] = started["arguments"]
        let failed = try await engine.summarize([.object(fields)])
        XCTAssertTrue(failed.failed)
        let second = try await engine.summarize([started])
        XCTAssertEqual(second, running)
    }

    func testMissingBundleFailsInsteadOfReturningInventedSummary() async {
        do { _ = try await ToolSummaryEngine(script: "").summarize([]); XCTFail("Missing runtime must fail") }
        catch { }
    }
}
