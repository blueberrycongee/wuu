#if DEBUG
import SwiftUI
import WuuCore

/// Read-only, local fixtures for repeatable simulator checks without touching an account.
enum NativeUIFixture {
    static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--native-ui-fixture") }
    static func thread() -> ChatThread {
        let image = UIGraphicsImageRenderer(size: CGSize(width: 240, height: 480)).image { context in
            UIColor.systemTeal.setFill(); context.fill(CGRect(x: 0, y: 0, width: 240, height: 480))
            UIColor.systemIndigo.setFill(); context.fill(CGRect(x: 20, y: 30, width: 200, height: 160))
            ("Wuu\n图片预览" as NSString).draw(at: CGPoint(x: 35, y: 220), withAttributes: [.font: UIFont.systemFont(ofSize: 26), .foregroundColor: UIColor.white])
        }
        let attachment: JSONValue = ["media_type": "image/jpeg", "data": .string(image.jpegData(compressionQuality: 0.75)!.base64EncodedString()), "filename": "preview.jpg"]
        let turns: [JSONValue] = (0..<80).map { index in
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
    var body: some View {
        NavigationStack {
            ConversationTimeline(model: model)
                .navigationTitle("长会话与图片验收").navigationBarTitleDisplayMode(.inline)
                .toolbarBackground(Color(uiColor: .systemBackground), for: .navigationBar)
                .toolbarBackground(.visible, for: .navigationBar)
                .toolbar {
                    Button("末尾追加") {
                        model.live?.apply("turn/started", ["thread_id": "local-ui-fixture", "turn": ["id": "stream", "status": "in_progress",
                            "items": [["id": "reply", "type": "agent_message", "text": "追加的新消息"]]]])
                    }
                }
        }
    }
}
#endif
