import SwiftUI
import WuuCore

private struct TimelineBottom: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = nextValue() }
}

struct ConversationTimeline: View {
    @Bindable var model: AppModel
    @State private var nearBottom = true
    var body: some View {
        GeometryReader { viewport in
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 22) {
                        if model.live?.historyCursor.isEmpty == false {
                            Button(model.loadingHistory ? "正在读取…" : "加载更早的消息") { model.perform { try await model.loadOlder() } }
                                .disabled(!model.connected || model.loadingHistory)
                        }
                        ForEach(model.messages) { message in
                            if let tool = message.tool {
                                ToolActivityView(model: model, message: message, tool: tool).id(message.id)
                            } else {
                                MessageBubble(model: model, message: message).id(message.id)
                            }
                        }
                        ForEach(model.live?.pending ?? []) { message in
                            PendingMessageView(model: model, message: message)
                        }
                        if model.live?.running == true { ProgressView("正在处理").font(.caption) }
                        Color.clear.frame(height: 1).id("bottom").background {
                            GeometryReader { geometry in
                                Color.clear.preference(key: TimelineBottom.self, value: geometry.frame(in: .named("timeline")).maxY)
                            }
                        }
                    }.padding(16)
                }.coordinateSpace(name: "timeline").defaultScrollAnchor(.bottom)
                    .onPreferenceChange(TimelineBottom.self) { nearBottom = $0 <= viewport.size.height + 48 }
                    .onChange(of: model.messages.last) { _, _ in
                        if nearBottom { proxy.scrollTo("bottom", anchor: .bottom) }
                    }
                    .overlay(alignment: .bottomTrailing) {
                        if !nearBottom {
                            Button { proxy.scrollTo("bottom", anchor: .bottom) } label: {
                                Label("最新消息", systemImage: "arrow.down").font(.caption).padding(10).background(.regularMaterial, in: Capsule())
                            }.padding(12)
                        }
                    }
            }
        }.sheet(item: $model.attachmentPreview) { attachment in AttachmentPreview(attachment: attachment) }
    }
}

private struct ToolActivityView: View {
    @Bindable var model: AppModel
    let message: ChatMessage
    let tool: ToolActivity
    var body: some View {
        DisclosureGroup {
            VStack(alignment: .leading, spacing: 10) {
                if !tool.arguments.isEmpty { detail("参数", tool.arguments) }
                if !tool.result.isEmpty { detail("结果", tool.result) }
                if !tool.error.isEmpty { Text(tool.error).foregroundStyle(.red).textSelection(.enabled) }
                if !message.contentRef.isEmpty {
                    Button(model.loadingContent.contains(message.id) ? "正在读取…" : "加载完整详情") { model.perform { try await model.expand(message) } }
                        .disabled(!model.connected || model.loadingContent.contains(message.id))
                }
            }.padding(.top, 8)
        } label: {
            HStack {
                Text(tool.name).lineLimit(2)
                Spacer(minLength: 8)
                Text(tool.statusLabel).foregroundStyle(tool.status == "failed" ? Color.red : Color.secondary)
            }.font(.subheadline)
        }.accessibilityIdentifier("tool-activity")
    }
    private func detail(_ title: String, _ text: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.caption).foregroundStyle(.secondary)
            Text(text).font(.system(.footnote, design: .monospaced)).textSelection(.enabled)
        }
    }
}

private struct MessageBubble: View {
    @Bindable var model: AppModel
    let message: ChatMessage
    var body: some View {
        HStack {
            if message.role == "user" { Spacer(minLength: 36) }
            VStack(alignment: .leading, spacing: 8) {
                MessageText(text: message.text, markdown: message.role == "assistant")
                    .foregroundStyle(message.role == "error" ? Color.red : Color.primary)
                ForEach(Array(message.attachments.enumerated()), id: \.offset) { index, attachment in
                    Button { model.perform { try await model.previewAttachment(message, index: index) } } label: {
                        Label(attachment["filename"].string ?? "查看图片", systemImage: attachment["media_type"].string?.hasPrefix("image/") == true ? "photo" : "doc")
                    }.disabled(!model.connected || model.loadingAttachment)
                }
                if !message.contentRef.isEmpty {
                    Button(model.loadingContent.contains(message.id) ? "正在读取…" : "加载完整消息") { model.perform { try await model.expand(message) } }
                        .disabled(!model.connected || model.loadingContent.contains(message.id))
                }
            }.padding(14)
                .background(message.role == "user" ? Color.secondary.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 18))
            if message.role != "user" { Spacer(minLength: 0) }
        }
    }
}
