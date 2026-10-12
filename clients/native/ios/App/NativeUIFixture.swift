#if DEBUG
import SwiftUI
import WuuCore

/// Read-only, local fixtures for repeatable simulator checks without touching an account.
enum NativeUIFixture {
    static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--native-ui-fixture") }
    static func thread() -> ChatThread {
        if ProcessInfo.processInfo.arguments.contains("--native-process-fixture") {
            return ChatThread(["id": "local-ui-fixture", "status": "in_progress", "turns": [
                ["id": "resumed", "status": "in_progress", "items": [
                    ["id": "check-1", "type": "tool_call", "name": "bash", "status": "completed"],
                    ["id": "reply-1", "type": "agent_message", "terminal": true, "text": "改动已提交，等待检查结果。"],
                    ["id": "check-2", "type": "tool_call", "name": "bash", "status": "completed"],
                    ["id": "reply-2", "type": "agent_message", "terminal": true, "text": "检查仍在运行，稍后继续处理。"],
                    ["id": "check-3", "type": "tool_call", "name": "bash", "status": "in_progress"]
                ]]
            ]])
        }
        let image = UIGraphicsImageRenderer(size: CGSize(width: 240, height: 480)).image { context in
            UIColor.systemTeal.setFill(); context.fill(CGRect(x: 0, y: 0, width: 240, height: 480))
            UIColor.systemIndigo.setFill(); context.fill(CGRect(x: 20, y: 30, width: 200, height: 160))
            ("Wuu\n图片预览" as NSString).draw(at: CGPoint(x: 35, y: 220), withAttributes: [.font: UIFont.systemFont(ofSize: 26), .foregroundColor: UIColor.white])
        }
        let attachment: JSONValue = ["media_type": "image/jpeg", "data": .string(image.jpegData(compressionQuality: 0.75)!.base64EncodedString()), "filename": "preview.jpg"]
        let arguments = ProcessInfo.processInfo.arguments
        let count = arguments.contains("--native-composer-fixture") && arguments.contains("--native-empty-fixture") ? 0 : 80
        let turns: [JSONValue] = (0..<count).map { index in
            let number = String(index)
            var items: [JSONValue] = [
                ["id": "user", "type": "user_message", "text": .string("第 \(index + 1) 轮：检查图片与消息布局"), "images": .array(index % 4 == 0 ? [attachment, attachment, attachment, attachment] : [])],
                ["id": "read", "type": "tool_call", "name": "read_file", "status": "completed", "arguments": "{\"path\":\"README.md\"}"],
                ["id": "command", "type": "tool_call", "name": "bash", "status": "completed", "arguments": "{\"command\":\"swift test\"}"],
                ["id": "answer", "type": "agent_message", "text": .string("### 检查结果 \(index + 1)\n\n正文和动作摘要保持一致的字号。缩略图在气泡外，点击查看大图。\n\n- 支持长会话滚动\n- **重点信息**与 `code`\n\n```swift\nlet message = \"Hello Wuu\"\n```\n\n[项目主页](https://wuu.ai)")]
            ]
            if index == 79 {
                items.append(["id": "image-only", "type": "user_message", "images": [attachment]])
                items.append(["id": "tool-image", "type": "tool_call", "name": "imagegen", "status": "completed",
                    "result_detail": ["content": [["type": "image", "mime_type": "image/jpeg", "data": attachment["data"]]]]])
            }
            return ["id": .string(number), "status": "completed", "items": .array(items)]
        }
        return ChatThread(["id": "local-ui-fixture", "title": "长会话与图片验收", "turns": .array(turns)])
    }
}

struct NativeUIFixtureView: View {
    @Bindable var model: AppModel
    @State private var stream: Task<Void, Never>?
    @State private var streaming = false
    var body: some View {
        NavigationStack {
            ConversationTimeline(model: model)
                .navigationTitle("长会话与图片验收").navigationBarTitleDisplayMode(.inline)
                .toolbarBackground(Color(uiColor: .systemBackground), for: .navigationBar)
                .toolbarBackground(.visible, for: .navigationBar)
                .toolbar {
                    Button(streaming ? "停止" : "流式动画") {
                        if streaming { stream?.cancel() } else { startStream() }
                    }
                    Button("末尾追加") {
                        model.live?.apply("turn/started", ["thread_id": "local-ui-fixture", "turn": ["id": "stream", "status": "in_progress",
                            "items": [["id": "reply", "type": "agent_message", "text": "追加的新消息"]]]])
                    }
                    // Two steps instead of a timer: inspect the retained state, then apply the snapshot.
                    Button(model.refreshing ? "应用快照" : "模拟重连") {
                        guard let live = model.live, model.refreshing else { model.refreshing = true; return }
                        // A recent page that starts mid-turn, plus a turn completed while disconnected.
                        var recent = Array(live.turns.suffix(20))
                        if recent[0]["items"].array.count > 1, case .object(var first) = recent[0] {
                            first["items"] = .array(Array(recent[0]["items"].array.dropFirst())); recent[0] = .object(first)
                        }
                        recent.append(["id": .string("resumed-\(live.turns.count)"), "status": "completed",
                            "items": [["id": "reply", "type": "agent_message", "text": "断开期间完成的回复"]]])
                        model.live?.resume(from: ChatThread(["id": .string(live.id), "title": .string(live.title),
                            "history_cursor": "fixture-page", "turns": .array(recent)]))
                        model.refreshing = false
                    }
                }
                .task {
                    if ProcessInfo.processInfo.arguments.contains("--native-stream-fixture") { startStream() }
                }
                .onDisappear { stream?.cancel() }
        }
    }

    private func startStream() {
        stream?.cancel()
        streaming = true
        stream = Task {
            let id = UUID().uuidString
            let source = """
            ### 流式动画

            新收到的文字柔和淡入，已经读过的内容保持稳定。连续输出时仍然可以滚动、选择文字和打开链接。

            **粗体文字**、`inline code`、[项目主页](https://wuu.ai)，以及中文、café、👨‍👩‍👧‍👦、🇨🇳 和 العربية。

            - 第一项持续增长，不会反复闪烁。
            - 第二项验证换行与列表布局。

            ```swift
            let message = "Hello Wuu"
            print(message)
            ```

            | 状态 | 结果 |
            | --- | --- |
            | 连续输出 | 持续刷新 |
            | 输出结束 | 完整保留 |

            动画结束，正文、代码复制和链接仍然可用。
            """
            var output = ""
            model.live?.apply("turn/started", ["thread_id": "local-ui-fixture", "turn": ["id": .string(id), "status": "in_progress",
                "items": [["id": "reply", "type": "agent_message", "text": ""]]]])
            // Continuous 20ms deltas exercise coalescing without a quiet debounce interval.
            var rest = source[...]
            while !rest.isEmpty && !Task.isCancelled {
                let chunk = String(rest.prefix(3)); rest = rest.dropFirst(chunk.count)
                output += chunk
                model.live?.apply("item/agentMessage/delta", ["thread_id": "local-ui-fixture", "turn_id": .string(id), "item_id": "reply", "delta": .string(chunk)])
                do { try await Task.sleep(for: .milliseconds(20)) } catch { break }
            }
            model.live?.apply("turn/completed", ["thread_id": "local-ui-fixture", "turn": ["id": .string(id), "status": "completed",
                "items": [["id": "reply", "type": "agent_message", "text": .string(output)]]]])
            streaming = false
        }
    }
}
#endif
