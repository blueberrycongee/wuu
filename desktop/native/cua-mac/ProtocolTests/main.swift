import CUAMacCore
import AppKit
import Foundation

private enum TestFailure: Error, CustomStringConvertible {
    case assertion(String)
    var description: String {
        switch self {
        case let .assertion(message): message
        }
    }
}

private func expect(_ condition: @autoclosure () -> Bool, _ message: String) throws {
    if !condition() {
        throw TestFailure.assertion(message)
    }
}

private final class FakeBackend: ComputerBackend {
    var commands: [ComputerCommand] = []
    var next = ComputerResult(
        text: "AXApplication id=0 title=TextEdit",
        screenshot: Data("png".utf8),
        screenshotMIMEType: "image/png",
        structured: ["app": "com.apple.TextEdit", "ax_preferred": true]
    )

    func perform(_ command: ComputerCommand) throws -> ComputerResult {
        commands.append(command)
        return next
    }
}

private func testInitializesAndAdvertisesFullComputerTool() throws {
    let server = MCPServer(backend: FakeBackend())
    let initialized = try server.handle([
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": ["protocolVersion": "2025-11-25"],
    ])
    try expect(initialized?["id"] as? Int == 1, "initialize id")
    let initResult = initialized?["result"] as? [String: Any]
    try expect(initResult?["protocolVersion"] as? String == "2025-11-25", "protocol version")

    let listed = try server.handle(["jsonrpc": "2.0", "id": 2, "method": "tools/list"])
    let result = listed?["result"] as? [String: Any]
    let tools = result?["tools"] as? [[String: Any]]
    try expect(tools?.count == 1, "one tool")
    try expect(tools?.first?["name"] as? String == "computer", "computer tool name")
    let schema = tools?.first?["inputSchema"] as? [String: Any]
    let properties = schema?["properties"] as? [String: Any]
    let variants = schema?["oneOf"] as? [[String: Any]]
    let action = properties?["action"] as? [String: Any]
    let actions = action?["enum"] as? [String]
    let app = properties?["app"] as? [String: Any]
    try expect(actions == [
        "permission_status", "request_permissions", "list_apps", "observe", "query_snapshot",
        "click", "drag", "press_key", "press_keys", "scroll", "set_value", "type_text",
        "select_text", "perform_action", "wait_for_change", "wait_for",
        "sequence",
        "activate_control",
        "conceal_app", "reveal_app",
    ], "full computer actions")
    try expect((app?["description"] as? String)?.contains("Required for every action") == true, "app requirement is explicit")
    func requiredFields(for action: String) -> [String] {
        variants?.first(where: { variant in
            let properties = variant["properties"] as? [String: Any]
            let actionProperty = properties?["action"] as? [String: Any]
            return (actionProperty?["enum"] as? [String]) == [action]
        })?["required"] as? [String] ?? []
    }
    try expect(requiredFields(for: "observe").contains("app"), "observe requires app")
    try expect(requiredFields(for: "press_key").contains("key"), "press_key requires key")
    try expect(requiredFields(for: "press_keys").contains("keys"), "press_keys requires keys")
    try expect(requiredFields(for: "type_text").contains("text"), "type_text requires text")
    try expect(requiredFields(for: "perform_action").contains("action_name"), "perform_action requires action_name")
    try expect(requiredFields(for: "conceal_app") == ["action", "app"], "conceal_app requires only app beyond action")
    try expect(requiredFields(for: "reveal_app") == ["action", "app"], "reveal_app requires only app beyond action")
    let clickVariant = variants?.first(where: { variant in
        let properties = variant["properties"] as? [String: Any]
        let actionProperty = properties?["action"] as? [String: Any]
        return (actionProperty?["enum"] as? [String]) == ["click"]
    })
    let clickAlternatives = clickVariant?["anyOf"] as? [[String: Any]]
    try expect(clickAlternatives?.contains(where: { ($0["required"] as? [String]) == ["element_id"] }) == true, "click accepts element id")
    try expect(clickAlternatives?.contains(where: { ($0["required"] as? [String]) == ["x", "y", "coordinate_space"] }) == true, "click accepts explicit coordinates")
    let coordinateProperty = properties?["coordinate_space"] as? [String: Any]
    try expect((coordinateProperty?["enum"] as? [String])?.contains("normalized") == true, "coordinate space supports resize-independent normalized points")
    let foregroundPolicy = properties?["foreground_policy"] as? [String: Any]
    try expect(foregroundPolicy?["default"] as? String == "avoid", "foreground policy defaults to avoid")
    let scope = properties?["scope"] as? [String: Any]
    try expect((scope?["enum"] as? [String]) == ["window", "app", "screen"], "scope enumerates window, app, and screen")
    try expect(scope?["default"] as? String == "window", "scope defaults to window")
    try expect((scope?["description"] as? String)?.contains("z-order") == true, "scope documents composite z-ordering")
    let disableDiff = properties?["disable_diff"] as? [String: Any]
    try expect(disableDiff?["type"] as? String == "boolean", "disable_diff is a boolean")
    try expect(disableDiff?["default"] as? Bool == false, "disable_diff defaults to false")
    let timeout = properties?["timeout"] as? [String: Any]
    try expect((timeout?["description"] as? String)?.contains("changed=false") == true, "wait timeout is documented as a normal result")
    let x = properties?["x"] as? [String: Any]
    try expect((x?["description"] as? String)?.contains("scrollable region") == true, "scroll coordinates document their target region")
    let pages = properties?["pages"] as? [String: Any]
    try expect((pages?["description"] as? String)?.contains("line-scroll events") == true, "scroll distance documents natural event sequencing")
    try expect((tools?.first?["description"] as? String)?.contains("normal timeout is not an error") == true, "tool description teaches normal wait timeout semantics")
}

private func testPreservesAccessibilityAndScreenshotContent() throws {
    let backend = FakeBackend()
    let server = MCPServer(backend: backend)
    let response = try server.handle([
        "jsonrpc": "2.0", "id": 3, "method": "tools/call",
        "params": ["name": "computer", "arguments": ["action": "observe", "app": "com.apple.TextEdit"]],
    ])
    try expect(backend.commands.count == 1, "backend call count")
    try expect(backend.commands.first?.action == .observe, "observe action")
    try expect(backend.commands.first?.app == "com.apple.TextEdit", "observe app")
    let result = response?["result"] as? [String: Any]
    try expect(result?["isError"] as? Bool == false, "successful result")
    let content = result?["content"] as? [[String: Any]]
    try expect(content?.count == 2, "text and screenshot content")
    try expect(content?.first?["type"] as? String == "text", "text content")
    try expect(content?.last?["type"] as? String == "image", "image content")
    try expect(content?.last?["mimeType"] as? String == "image/png", "image mime")
    try expect(content?.last?["data"] as? String == Data("png".utf8).base64EncodedString(), "image data")
    let structured = result?["structuredContent"] as? [String: Any]
    try expect(structured?["ax_preferred"] as? Bool == true, "structured content")
}

private func testCoordinateAndKeyboardFallbackReachBackend() throws {
    let backend = FakeBackend()
    let server = MCPServer(backend: backend)
    _ = try server.handle([
        "jsonrpc": "2.0", "id": 4, "method": "tools/call",
        "params": ["name": "computer", "arguments": ["action": "click", "app": "com.apple.TextEdit", "x": 100, "y": 200]],
    ])
    _ = try server.handle([
        "jsonrpc": "2.0", "id": 5, "method": "tools/call",
        "params": ["name": "computer", "arguments": ["action": "press_key", "app": "com.apple.TextEdit", "key": "command+a"]],
    ])
    try expect(backend.commands.count == 2, "fallback call count")
    try expect(backend.commands[0].action == .click, "click action")
    try expect(backend.commands[0].x == 100 && backend.commands[0].y == 200, "click coordinates")
    try expect(backend.commands[1].action == .pressKey, "press key action")
    try expect(backend.commands[1].key == "command+a", "key value")
    try expect(backend.commands[0].foregroundPolicy == .avoid, "omitted foreground policy is avoid")
}

private func testScopeAndDisableDiffParseAndReachBackend() throws {
    let backend = FakeBackend()
    let server = MCPServer(backend: backend)
    _ = try server.handle([
        "jsonrpc": "2.0", "id": 20, "method": "tools/call",
        "params": [
            "name": "computer",
            "arguments": [
                "action": "observe",
                "app": "com.apple.TextEdit",
                "scope": "app",
                "disable_diff": true,
            ],
        ],
    ])
    try expect(backend.commands.count == 1, "observe reached backend once")
    try expect(backend.commands.first?.scope == .app, "scope=app reaches the backend command")
    try expect(backend.commands.first?.disableDiff == true, "disable_diff reaches the backend command")

    // Omitting both keeps the legacy defaults, so scope=window observe stays identical.
    let defaults = try ComputerCommand(arguments: ["action": "observe", "app": "com.apple.TextEdit"])
    try expect(defaults.scope == .window, "omitted scope defaults to window")
    try expect(defaults.disableDiff == false, "omitted disable_diff defaults to false")

    let screen = try ComputerCommand(arguments: ["action": "observe", "app": "com.apple.TextEdit", "scope": "screen"])
    try expect(screen.scope == .screen, "scope=screen parses")

    do {
        _ = try ComputerCommand(arguments: ["action": "observe", "app": "com.apple.TextEdit", "scope": "monitor"])
        throw TestFailure.assertion("invalid scope should fail")
    } catch let error as ComputerError {
        try expect(error.errorDescription?.hasPrefix("invalid_arguments:") == true, "invalid scope is an invalid_arguments error")
    }
}

private func testForegroundPolicyParsingAndErrors() throws {
    let allowed = try ComputerCommand(arguments: [
        "action": "press_key", "app": "com.apple.TextEdit", "key": "return", "foreground_policy": "allow",
    ])
    try expect(allowed.foregroundPolicy == .allow, "allow foreground policy")

    do {
        _ = try ComputerCommand(arguments: ["action": "press_key", "app": "com.apple.TextEdit", "key": "return", "foreground_policy": "sometimes"])
        throw TestFailure.assertion("invalid foreground policy should fail")
    } catch let error as ComputerError {
        try expect(error.errorDescription?.hasPrefix("invalid_arguments:") == true, "invalid foreground policy error")
    }

    final class ForegroundFailingBackend: ComputerBackend {
        func perform(_ command: ComputerCommand) throws -> ComputerResult {
            throw ComputerError.requiresForeground("native input required")
        }
    }
    let server = MCPServer(backend: ForegroundFailingBackend())
    let response = try server.handle([
        "jsonrpc": "2.0", "id": 8, "method": "tools/call",
        "params": ["name": "computer", "arguments": ["action": "press_key", "app": "com.apple.TextEdit", "key": "return"]],
    ])
    let result = response?["result"] as? [String: Any]
    let structured = result?["structuredContent"] as? [String: Any]
    try expect(result?["isError"] as? Bool == true, "foreground rejection is a tool error")
    try expect(structured?["error_code"] as? String == "requires_foreground", "foreground rejection has a stable code")
}

private func testKeyChordParserSupportsSkyStyleAliases() throws {
    let chord = try KeyChord.parse("super+shift+a")
    try expect(chord.keyCode == 0, "A key code")
    try expect(chord.modifiers.contains(.command), "super maps to command")
    try expect(chord.modifiers.contains(.shift), "shift modifier")
    let enter = try KeyChord.parse("Return")
    try expect(enter.keyCode == 36, "Return key code")
    let multiply = try KeyChord.parse("*")
    try expect(multiply.keyCode == 28 && multiply.modifiers.contains(.shift), "asterisk maps to shift-8")
    let plus = try KeyChord.parse("+")
    try expect(plus.keyCode == 24 && plus.modifiers.contains(.shift), "plus maps to shift-equals")
    let commandPlus = try KeyChord.parse("cmd++")
    try expect(commandPlus.keyCode == 24 && commandPlus.modifiers.contains(.shift) && commandPlus.modifiers.contains(.command), "command-plus keeps both modifiers")
}

private func testNativePermissionAndAppDiscoveryAreInspectable() throws {
    let backend = MacComputerBackend()
    let permission = try backend.perform(ComputerCommand(arguments: ["action": "permission_status"]))
    try expect(permission.structured["accessibility"] is Bool, "accessibility permission status")
    try expect(permission.structured["screen_recording"] is Bool, "screen recording permission status")
    let apps = try backend.perform(ComputerCommand(arguments: ["action": "list_apps"]))
    try expect(apps.structured["apps"] is [[String: Any]], "running apps list")
}

// A wait_for_change timeout is a normal result, never a tool error: the
// contract travels through the server unchanged, and the timeout argument
// reaches the backend command.
private func testWaitForChangePassesThroughAsANormalResult() throws {
    let backend = FakeBackend()
    backend.next = ComputerResult(
        text: "No accessibility change was observed within 0.1 seconds.",
        structured: ["changed": false, "timed_out": true, "timeout_seconds": 0.1]
    )
    let server = MCPServer(backend: backend)
    let response = try server.handle([
        "jsonrpc": "2.0", "id": 9, "method": "tools/call",
        "params": [
            "name": "computer",
            "arguments": ["action": "wait_for_change", "app": "com.apple.TextEdit", "timeout": 0.1],
        ],
    ])
    try expect(backend.commands.count == 1, "wait_for_change reaches the backend once")
    try expect(backend.commands.first?.action == .waitForChange, "wait_for_change action")
    try expect(backend.commands.first?.timeout == 0.1, "timeout reaches the backend command")
    let result = response?["result"] as? [String: Any]
    let structured = result?["structuredContent"] as? [String: Any]
    try expect(result?["isError"] as? Bool == false, "a wait_for_change timeout is a normal result, not a tool error")
    try expect(structured?["changed"] as? Bool == false, "changed passes through to structured content")
    try expect(structured?["timed_out"] as? Bool == true, "timed_out passes through to structured content")
}

// Integration smoke against the real AX stack. Opt-in (CUA_MAC_INTEGRATION=1)
// because it launches a real GUI process on the developer's machine; whatever
// the test launches is terminated again on the way out.
@MainActor
private func testWaitForChangeAgainstARealApp() throws {
    guard ProcessInfo.processInfo.environment["CUA_MAC_INTEGRATION"] == "1" else {
        print("skipping real-app wait_for_change integration test (set CUA_MAC_INTEGRATION=1 to enable)")
        return
    }
    _ = NSApplication.shared
    let backend = MacComputerBackend()
    let permission = try backend.perform(ComputerCommand(arguments: ["action": "permission_status"]))
    guard permission.structured["accessibility"] as? Bool == true else {
        print("skipping real-app wait_for_change integration test (accessibility permission not granted to the test process)")
        return
    }

    let calculator = "/System/Applications/Calculator.app"
    let calculatorWasRunning = NSWorkspace.shared.runningApplications.contains {
        !$0.isTerminated && $0.bundleURL?.path == calculator
    }
    defer {
        // Only tear down an instance this test launched; one the user already
        // had open stays untouched.
        if !calculatorWasRunning {
            terminateRunningApp(at: calculator)
        }
    }

    _ = try backend.perform(ComputerCommand(arguments: [
        "action": "observe", "app": calculator,
    ]))
    RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.25))
    _ = try backend.perform(ComputerCommand(arguments: [
        "action": "observe", "app": calculator,
    ]))
    let server = MCPServer(backend: backend)
    let response = try server.handle([
        "jsonrpc": "2.0", "id": 9, "method": "tools/call",
        "params": [
            "name": "computer",
            "arguments": [
                "action": "wait_for_change",
                "app": calculator,
                "timeout": 0.1,
            ],
        ],
    ])
    let result = response?["result"] as? [String: Any]
    let structured = result?["structuredContent"] as? [String: Any]
    let changed = structured?["changed"] as? Bool
    let timedOut = structured?["timed_out"] as? Bool
    // The Calculator process can still emit delayed AX startup notifications
    // after the baseline observes above. The test must not assume that a real
    // app stays quiet; it verifies the normal result contract in either case.
    try expect(result?["isError"] as? Bool == false, "wait_for_change returns a normal result")
    try expect(changed != nil, "wait_for_change reports whether accessibility changed")
    try expect(timedOut == !(changed ?? true), "timed_out is the inverse of changed")
}

private func terminateRunningApp(at path: String) {
    for app in NSWorkspace.shared.runningApplications where !app.isTerminated && app.bundleURL?.path == path {
        app.terminate()
        let deadline = Date(timeIntervalSinceNow: 2.5)
        while !app.isTerminated && Date() < deadline {
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.1))
        }
        if !app.isTerminated {
            app.forceTerminate()
        }
    }
}

private func testScreenshotCoordinatesMapToGlobalWindowCoordinates() throws {
    let geometry = CaptureGeometry(
        windowFrame: CGRect(x: 100, y: 50, width: 800, height: 600),
        imageWidth: 1600,
        imageHeight: 1200
    )
    let point = try geometry.screenPoint(x: 800, y: 600)
    try expect(point.x == 500 && point.y == 350, "screenshot midpoint maps to window midpoint")
    let normalizedPoint = try geometry.screenPoint(normalizedX: 500, normalizedY: 500)
    try expect(normalizedPoint.x == 500 && normalizedPoint.y == 350, "normalized midpoint maps independently of screenshot resolution")
    let padded = CaptureGeometry(
        windowFrame: CGRect(x: 100, y: 50, width: 800, height: 600),
        imageWidth: 1800,
        imageHeight: 1400,
        visibleImageFrame: CGRect(x: 100, y: 100, width: 1600, height: 1200)
    )
    let paddedPoint = try padded.screenPoint(x: 900, y: 700)
    try expect(paddedPoint.x == 500 && paddedPoint.y == 350, "transparent screenshot padding preserves full-image mapping")
}

private func testCompositeUnionGeometryMapsClicksAcrossWindows() throws {
    // A scope=app composite of two windows: union covers global (100,50)..(1400,650),
    // rendered at scale 2 → 2600×1200 px. CaptureGeometry uses the union as its
    // window frame, so a click anywhere in the composite maps to the right global
    // point without any composite-specific math.
    let geometry = CaptureGeometry(
        windowFrame: CGRect(x: 100, y: 50, width: 1300, height: 600),
        imageWidth: 2600,
        imageHeight: 1200
    )
    let center = try geometry.screenPoint(x: 1300, y: 600)
    try expect(center.x == 750 && center.y == 350, "composite midpoint maps to the union midpoint")
    // A pixel over the second window (its global x starts at 1000) resolves inside it.
    let secondWindow = try geometry.screenPoint(x: 2200, y: 0)
    try expect(secondWindow.x == 1200 && secondWindow.y == 50, "composite pixel over the second window maps to its global position")
    let normalized = try geometry.screenPoint(normalizedX: 500, normalizedY: 500)
    try expect(normalized.x == 750 && normalized.y == 350, "normalized center maps to the union center regardless of composite resolution")
}

do {
    try testInitializesAndAdvertisesFullComputerTool()
    try testPreservesAccessibilityAndScreenshotContent()
    try testCoordinateAndKeyboardFallbackReachBackend()
    try testScopeAndDisableDiffParseAndReachBackend()
    try testForegroundPolicyParsingAndErrors()
    try testKeyChordParserSupportsSkyStyleAliases()
    try testNativePermissionAndAppDiscoveryAreInspectable()
    try testWaitForChangePassesThroughAsANormalResult()
    try testWaitForChangeAgainstARealApp()
    try testScreenshotCoordinatesMapToGlobalWindowCoordinates()
    try testCompositeUnionGeometryMapsClicksAcrossWindows()
    print("cua-mac protocol tests passed")
} catch {
    FileHandle.standardError.write(Data("cua-mac protocol tests failed: \(error)\n".utf8))
    exit(1)
}
