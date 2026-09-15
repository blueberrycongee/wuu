import Foundation

public enum ComputerAction: String, CaseIterable, Sendable {
    case permissionStatus = "permission_status"
    case requestPermissions = "request_permissions"
    case listApps = "list_apps"
    case observe
    case click
    case drag
    case pressKey = "press_key"
    case pressKeys = "press_keys"
    case scroll
    case setValue = "set_value"
    case typeText = "type_text"
    case selectText = "select_text"
    case performAction = "perform_action"
    case waitForChange = "wait_for_change"
    case sequence
    case activateControl = "activate_control"
    case concealApp = "conceal_app"
    case revealApp = "reveal_app"
}

public enum ForegroundPolicy: String, Sendable {
    case avoid
    case allow
    case require
}

// Capture scope for observe. window keeps the legacy single-window behaviour and
// coordinate space; app and screen composite multiple on-screen windows back-to-front
// into one image whose union frame defines the coordinate space for later clicks.
public enum CaptureScope: String, Sendable {
    case window
    case app
    case screen
}

public struct ComputerCommand: @unchecked Sendable {
    public let action: ComputerAction
    public let app: String?
    public let elementID: Int?
    public let x: Double?
    public let y: Double?
    public let coordinateSpace: String?
    public let toX: Double?
    public let toY: Double?
    public let mouseButton: String?
    public let clickCount: Int?
    public let key: String?
    public let keys: [String]?
    public let text: String?
    public let value: String?
    public let direction: String?
    public let pages: Int?
    public let actionName: String?
    public let prefix: String?
    public let suffix: String?
    public let timeout: Double?
    public let role: String?
    public let title: String?
    public let description: String?
    public let foregroundPolicy: ForegroundPolicy
    public let scope: CaptureScope
    public let disableDiff: Bool

    public init(arguments: [String: Any]) throws {
        guard let rawAction = arguments["action"] as? String,
              let action = ComputerAction(rawValue: rawAction) else {
            throw ComputerError.invalidArguments("action is required and must be supported")
        }
        self.action = action
        app = arguments["app"] as? String
        elementID = Self.int(arguments["element_id"])
        x = Self.double(arguments["x"] ?? arguments["from_x"])
        y = Self.double(arguments["y"] ?? arguments["from_y"])
        coordinateSpace = arguments["coordinate_space"] as? String
        toX = Self.double(arguments["to_x"])
        toY = Self.double(arguments["to_y"])
        mouseButton = arguments["mouse_button"] as? String
        clickCount = Self.int(arguments["click_count"])
        key = arguments["key"] as? String
        keys = arguments["keys"] as? [String]
        text = arguments["text"] as? String
        value = arguments["value"] as? String
        direction = arguments["direction"] as? String
        pages = Self.int(arguments["pages"])
        actionName = arguments["action_name"] as? String
        prefix = arguments["prefix"] as? String
        suffix = arguments["suffix"] as? String
        timeout = Self.double(arguments["timeout"])
        role = arguments["role"] as? String
        title = arguments["title"] as? String
        description = arguments["description"] as? String
        if let rawPolicy = arguments["foreground_policy"] as? String {
            guard let policy = ForegroundPolicy(rawValue: rawPolicy) else {
                throw ComputerError.invalidArguments("foreground_policy must be avoid, allow, or require")
            }
            foregroundPolicy = policy
        } else {
            foregroundPolicy = .avoid
        }
        // Same validation shape as foreground_policy: an unknown enum value is an
        // invalid_arguments error, an omitted value defaults to the legacy behaviour.
        if let rawScope = arguments["scope"] as? String {
            guard let parsedScope = CaptureScope(rawValue: rawScope) else {
                throw ComputerError.invalidArguments("scope must be window, app, or screen")
            }
            scope = parsedScope
        } else {
            scope = .window
        }
        disableDiff = arguments["disable_diff"] as? Bool ?? false
    }

    private static func int(_ value: Any?) -> Int? {
        if let value = value as? Int { return value }
        if let value = value as? NSNumber { return value.intValue }
        return nil
    }

    private static func double(_ value: Any?) -> Double? {
        if let value = value as? Double { return value }
        if let value = value as? Int { return Double(value) }
        if let value = value as? NSNumber { return value.doubleValue }
        return nil
    }
}

public struct ComputerResult: @unchecked Sendable {
    public let text: String
    public let screenshot: Data?
    public let screenshotMIMEType: String?
    public let structured: [String: Any]

    public init(text: String, screenshot: Data? = nil, screenshotMIMEType: String? = nil, structured: [String: Any] = [:]) {
        self.text = text
        self.screenshot = screenshot
        self.screenshotMIMEType = screenshotMIMEType
        self.structured = structured
    }
}

public protocol ComputerBackend: AnyObject {
    func perform(_ command: ComputerCommand) throws -> ComputerResult
    func shutdown()
}

public extension ComputerBackend { func shutdown() {} }

public enum ComputerError: LocalizedError, Equatable {
    case invalidArguments(String)
    case permissionDenied(String)
    case appNotFound(String)
    case elementNotFound(Int)
    case unsupported(String)
    case requiresForeground(String)
    case operationFailed(String)
    case cancelled(String)
    case staleSnapshot(String)

    public var errorDescription: String? {
        switch self {
        case let .invalidArguments(message): "invalid_arguments: \(message)"
        case let .permissionDenied(message): "permission_denied: \(message)"
        case let .appNotFound(app): "app_not_found: \(app)"
        case let .elementNotFound(id): "element_not_found: \(id); observe again for fresh element ids"
        case let .unsupported(message): "unsupported_action: \(message)"
        case let .requiresForeground(message): "requires_foreground: \(message)"
        case let .cancelled(message): "cancelled: \(message)"
        case let .staleSnapshot(message): "stale_snapshot: \(message); observe again"
        case let .operationFailed(message): "operation_failed: \(message)"
        }
    }
}

public final class MCPServer {
    private let backend: ComputerBackend
    private let acceptedProtocols = Set(["2026-06-30", "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"])

    public init(backend: ComputerBackend) {
        self.backend = backend
    }

    public func shutdown() { backend.shutdown() }

    public func handle(_ request: [String: Any], execution: ComputerExecution = ComputerExecution()) throws -> [String: Any]? {
        let method = request["method"] as? String ?? ""
        let id = request["id"]
        if id == nil && method.hasPrefix("notifications/") {
            return nil
        }
        switch method {
        case "initialize":
            let params = request["params"] as? [String: Any]
            let proposed = params?["protocolVersion"] as? String ?? "2025-11-25"
            let selected = acceptedProtocols.contains(proposed) ? proposed : "2025-11-25"
            return response(id: id, result: [
                "protocolVersion": selected,
                "capabilities": ["tools": ["listChanged": false]],
                "serverInfo": ["name": "wuu-cua-mac", "version": "0.1.0"],
            ])
        case "ping":
            return response(id: id, result: [:])
        case "tools/list":
            return response(id: id, result: ["tools": [toolDefinition()]])
        case "tools/call":
            return callTool(id: id, params: request["params"] as? [String: Any], execution: execution)
        default:
            return rpcError(id: id, code: -32601, message: "method not found: \(method)")
        }
    }

    private func callTool(id: Any?, params: [String: Any]?, execution: ComputerExecution) -> [String: Any] {
        guard params?["name"] as? String == "computer" else {
            return rpcError(id: id, code: -32602, message: "unknown tool")
        }
        do {
            let arguments = params?["arguments"] as? [String: Any] ?? [:]
            let command = try ComputerCommand(arguments: arguments)
            let result = try execution.run {
                try execution.check()
                return try backend.perform(command)
            }
            var content: [[String: Any]] = [["type": "text", "text": result.text]]
            if let screenshot = result.screenshot,
               let mimeType = result.screenshotMIMEType,
               !screenshot.isEmpty {
                content.append([
                    "type": "image",
                    "data": screenshot.base64EncodedString(),
                    "mimeType": mimeType,
                ])
            }
            return response(id: id, result: [
                "content": content,
                "structuredContent": result.structured,
                "isError": false,
            ])
        } catch {
            let message = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            let code = errorCode(error)
            return response(id: id, result: [
                "content": [["type": "text", "text": message]],
                "structuredContent": execution.evidence.merging(["error": message, "error_code": code]) { _, new in new },
                "isError": true,
            ])
        }
    }

    private func errorCode(_ error: Error) -> String {
        guard let error = error as? ComputerError else { return "operation_failed" }
        return switch error {
        case .invalidArguments: "invalid_arguments"
        case .permissionDenied: "permission_denied"
        case .appNotFound: "app_not_found"
        case .elementNotFound: "element_not_found"
        case .unsupported: "unsupported_action"
        case .requiresForeground: "requires_foreground"
        case .operationFailed: "operation_failed"
        case .cancelled: "cancelled"
        case .staleSnapshot: "stale_snapshot"
        }
    }

    private func toolDefinition() -> [String: Any] {
        let stringProperty: [String: Any] = ["type": "string"]
        func variant(_ action: ComputerAction, required: [String], anyOf: [[String: Any]]? = nil) -> [String: Any] {
            var value: [String: Any] = [
                "properties": ["action": ["enum": [action.rawValue]]],
                "required": ["action"] + required,
            ]
            if let anyOf { value["anyOf"] = anyOf }
            return value
        }
        let variants: [[String: Any]] = [
            variant(.permissionStatus, required: []),
            variant(.requestPermissions, required: []),
            variant(.listApps, required: []),
            variant(.observe, required: ["app"]),
            variant(.click, required: ["app"], anyOf: [
                ["required": ["element_id"]],
                ["required": ["x", "y", "coordinate_space"]],
            ]),
            variant(.drag, required: ["app", "from_x", "from_y", "to_x", "to_y", "coordinate_space"]),
            variant(.pressKey, required: ["app", "key"]),
            variant(.pressKeys, required: ["app", "keys"]),
            variant(.scroll, required: ["app", "direction"]),
            variant(.setValue, required: ["app", "element_id"], anyOf: [
                ["required": ["value"]],
                ["required": ["text"]],
            ]),
            variant(.typeText, required: ["app", "text"]),
            variant(.selectText, required: ["app", "element_id", "text"]),
            variant(.performAction, required: ["app", "element_id", "action_name"]),
            variant(.waitForChange, required: ["app"]),
            variant(.sequence, required: ["app", "steps"]),
            variant(.activateControl, required: ["app"], anyOf: [
                ["required": ["description"]],
                ["required": ["title"]],
            ]),
            variant(.concealApp, required: ["app"]),
            variant(.revealApp, required: ["app"]),
        ]
        return [
            "name": "computer",
            "title": "Computer Use for Mac",
            "description": "Observe and control macOS apps without disturbing the user. The runtime picks the lowest-disruption control level automatically: background Accessibility actions, then background directed input (keyboard and mouse delivered straight to the target process without activating it), and a visible foreground takeover only when foreground_policy asks for it. By default the user's frontmost app and real pointer stay put. Observe returns a canonical app target and fresh UI state. Input actions only deliver the requested event; they do not infer success from AX or pixels. Call observe after an action when the outcome matters. wait_for_change waits up to timeout seconds and returns changed=false when nothing changes; a normal timeout is not an error. conceal_app moves the target window off-screen so the user never sees it while background control and live capture keep working; reveal_app restores it.",
            "inputSchema": [
                "type": "object",
                "required": ["action"],
                "additionalProperties": false,
                "oneOf": variants,
                "properties": [
                    "action": ["type": "string", "enum": ComputerAction.allCases.map(\.rawValue)],
                    "app": ["type": "string", "description": "Target app display name, bundle identifier, or path. Required for every action except permission_status, request_permissions, and list_apps."],
                    "element_id": ["type": "integer"],
                    "x": ["type": "number", "description": "Horizontal coordinate in the declared coordinate_space. For scroll, this targets the intended scrollable region; omit x/y only when the primary window center is correct."],
                    "y": ["type": "number", "description": "Vertical coordinate in the declared coordinate_space. For scroll, this targets the intended scrollable region; omit x/y only when the primary window center is correct."],
                    "coordinate_space": ["type": "string", "enum": ["normalized", "screenshot", "screen"], "description": "Use normalized for visual coordinates on a 0-1000 grid independent of image resizing; use screenshot for pixels in the latest captured image; use screen for global coordinates copied from an AX frame."],
                    "to_x": ["type": "number", "description": "Drag destination horizontal coordinate in the declared coordinate_space."],
                    "to_y": ["type": "number", "description": "Drag destination vertical coordinate in the declared coordinate_space."],
                    "from_x": ["type": "number", "description": "Drag start horizontal coordinate in the declared coordinate_space."],
                    "from_y": ["type": "number", "description": "Drag start vertical coordinate in the declared coordinate_space."],
                    "mouse_button": ["type": "string", "enum": ["left", "right", "middle"]],
                    "click_count": ["type": "integer", "minimum": 1, "maximum": 3],
                    "key": stringProperty,
                    "keys": ["type": "array", "minItems": 1, "maxItems": 64, "items": stringProperty, "description": "Ordered key sequence, for example [\"3\", \"7\", \"*\", \"2\", \"4\", \"return\"]."],
                    "text": stringProperty,
                    "value": stringProperty,
                    "direction": ["type": "string", "enum": ["up", "down", "left", "right"]],
                    "pages": ["type": "integer", "minimum": 1, "maximum": 20, "description": "Approximate scroll distance. The runtime emits a short sequence of natural line-scroll events rather than one oversized wheel event."],
                    "action_name": stringProperty,
                    "prefix": stringProperty,
                    "suffix": stringProperty,
                    "timeout": ["type": "number", "minimum": 0.1, "maximum": 30, "description": "Maximum wait in seconds. wait_for_change returns changed=false and timed_out=true when this duration elapses without an Accessibility change."],
                    "foreground_policy": [
                        "type": "string",
                        "enum": ["avoid", "allow", "require"],
                        "default": "avoid",
                        "description": "Foreground escalation policy. avoid (default) keeps everything in the background: level-1 Accessibility and level-2 directed input both run without activating the app, moving the user's pointer, or changing focus. allow and require perform a visible level-3 foreground takeover for this action (activating the app and posting global input); require additionally brings the app forward before observing. Use allow when a background attempt did not achieve the goal and a takeover is acceptable; use require only when the user asked to show or drive the app.",
                    ],
                    "scope": [
                        "type": "string",
                        "enum": ["window", "app", "screen"],
                        "default": "window",
                        "description": "Capture scope for observe. window (default) captures the single most relevant window and keeps the established coordinate space. app composites every on-screen window of the target app into one image, drawn back-to-front in true z-order so overlapping and secondary windows are all visible; screen composites every on-screen window across all apps. For app and screen the returned screenshot and click mapping cover the whole composited region (its union frame), and structured screenshot.windows lists each window's pixel rectangle and z_index (0 is frontmost). Concealed (off-screen) windows are not part of a composite.",
                    ],
                    "disable_diff": [
                        "type": "boolean",
                        "default": false,
                        "description": "When true, observe returns the full accessibility snapshot text every time instead of a compact diff against the previous observe. The structured changes list is still populated, so wait_for_change is unaffected. Use only when the whole tree is needed regardless of how little changed.",
                    ],
                    "steps": [
                        "type": "array",
                        "minItems": 1,
                        "maxItems": 64,
                        "description": "Ordered actions. Every step must declare risk=safe, external_side_effect, or destructive. Non-safe steps also require confirmed=true.",
                        "items": [
                            "type": "object",
                            "required": ["action", "risk"],
                            "additionalProperties": true,
                            "properties": [
                                "action": ["type": "string"],
                                "risk": ["type": "string", "enum": ["safe", "external_side_effect", "destructive"]],
                                "confirmed": ["type": "boolean"],
                                "description": ["type": "string", "description": "Exact AXDescription for activate_control."],
                                "title": ["type": "string", "description": "Exact AXTitle for activate_control."],
                                "role": ["type": "string"],
                                "element_id": ["type": "integer"],
                                "action_name": ["type": "string"],
                                "key": ["type": "string"],
                                "text": ["type": "string"],
                                "value": ["type": "string"],
                            ],
                        ],
                    ],
                    "role": stringProperty,
                    "title": stringProperty,
                    "description": stringProperty,
                ],
            ],
            "annotations": [
                "readOnlyHint": false,
                "destructiveHint": true,
                "idempotentHint": false,
                "openWorldHint": true,
            ],
        ]
    }

    private func response(id: Any?, result: [String: Any]) -> [String: Any] {
        ["jsonrpc": "2.0", "id": id ?? NSNull(), "result": result]
    }

    private func rpcError(id: Any?, code: Int, message: String) -> [String: Any] {
        [
            "jsonrpc": "2.0",
            "id": id ?? NSNull(),
            "error": ["code": code, "message": message],
        ]
    }
}
