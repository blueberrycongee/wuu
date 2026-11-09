import Foundation
import JavaScriptCore

public struct ToolSummary: Decodable, Sendable, Equatable {
    public let text: String
    public let activity: String
    public let failed: Bool
}

/// Executes the bundled desktop presentation rules once per changed group, off the UI actor.
/// No browser, networking, native bridge or per-row JavaScript runtime is needed.
public actor ToolSummaryEngine {
    private let script: String
    private var context: JSContext?
    public init(script: String) { self.script = script }

    public func summarize(_ tools: [JSONValue]) throws -> ToolSummary {
        try Task.checkCancellation()
        if context == nil {
            guard let created = JSContext() else { throw NativeError.invalid("无法初始化动作摘要") }
            created.evaluateScript(script)
            guard created.exception == nil else { throw NativeError.invalid("无法加载动作摘要") }
            context = created
        }
        guard let context else { throw NativeError.invalid("动作摘要不可用") }
        context.exception = nil
        let json = String(decoding: try JSONEncoder().encode(tools), as: UTF8.self)
        let result = context.objectForKeyedSubscript("WuuProcess")?.objectForKeyedSubscript("summarizeJSON")?.call(withArguments: [json])
        guard context.exception == nil, let value = result?.toString(), let data = value.data(using: .utf8) else {
            throw NativeError.invalid("无法读取动作摘要")
        }
        return try JSONDecoder().decode(ToolSummary.self, from: data)
    }
}
