import SwiftUI
import WuuCore

struct PendingMessageView: View {
    let model: AppModel
    let message: PendingMessage
    @State private var working = false
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(message.held ? "已暂停 · 等待继续" : "等待处理").font(.caption).foregroundStyle(.secondary)
            Text(message.text).textSelection(.enabled)
            HStack {
                if message.held {
                    Button("继续处理") { act(resume: true) }.disabled(model.live?.running == true)
                }
                Button("移除", role: .destructive) { act(resume: false) }
            }.disabled(working || !model.connected || model.live?.readOnly == true)
        }.padding(12).frame(maxWidth: .infinity, alignment: .leading)
            .background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
    }
    private func act(resume: Bool) {
        working = true
        model.perform { defer { working = false }; try await model.pendingAction(message, resume: resume) }
    }
}
