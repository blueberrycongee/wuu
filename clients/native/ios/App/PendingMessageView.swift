import SwiftUI
import WuuCore

struct PendingMessageView: View {
    let model: AppModel
    let message: PendingMessage
    @State private var working = false
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(message.held ? "已暂停 · 等待继续" : "等待处理").font(.caption).foregroundStyle(.secondary)
            if !message.text.isEmpty { Text(message.text).textSelection(.enabled) }
            ForEach(Array(message.value["images"].array.enumerated()), id: \.offset) { index, attachment in
                MessageImage(key: "pending:\(message.id):\(index)", connected: true, loader: model.imagePreviews,
                    read: { try await read(attachment) },
                    open: { model.perform { model.attachmentPreview = try await read(attachment) } })
            }
            ForEach(Array(message.value["files"].array.enumerated()), id: \.offset) { _, attachment in
                Button(attachment["filename"].string ?? "文件") { model.perform { model.attachmentPreview = try await read(attachment) } }
            }
            HStack {
                if message.held {
                    Button("继续处理") { act(resume: true) }.disabled(model.live?.running == true)
                }
                Button("移除", role: .destructive) { act(resume: false) }
            }.disabled(working || !model.connected || model.live?.readOnly == true)
        }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
            .background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
    }
    private func read(_ attachment: JSONValue) async throws -> LoadedAttachment {
        try await readMessageAttachment(attachment, scopeID: message.value["thread_id"].string ?? "", messageID: message.id) { _, _ in
            throw NativeError.invalid("排队附件内容不可用")
        }
    }
    private func act(resume: Bool) {
        working = true
        model.perform { defer { working = false }; try await model.pendingAction(message, resume: resume) }
    }
}
