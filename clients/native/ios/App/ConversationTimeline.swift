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
                    LazyVStack(alignment: .leading, spacing: 18) {
                        if model.live?.historyCursor.isEmpty == false {
                            Button(model.loadingHistory ? "正在读取…" : "加载更早的消息") { model.perform { try await model.loadOlder() } }
                                .disabled(!model.connected || model.loadingHistory)
                        }
                        ForEach(ConversationRow.grouped(model.messages)) { row in
                            if row.isToolGroup {
                                ToolGroupView(messages: row.messages, settings: model.live?.settings,
                                    active: model.live?.running == true && row.messages.last?.id == model.messages.last?.id)
                            } else {
                                MessageBubble(model: model, message: row.messages[0])
                            }
                        }
                        ForEach(model.live?.pending ?? []) { message in
                            PendingMessageView(model: model, message: message)
                        }
                        if model.live?.running == true && model.messages.last?.tool == nil {
                            ConversationActivityMark(activity: model.messages.last?.role == "assistant" ? "responding" : "thinking", settings: model.live?.settings)
                                .accessibilityElement().accessibilityLabel("正在处理")
                        }
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

private struct ToolGroupView: View {
    let messages: [ChatMessage]
    let settings: ThreadSettings?
    let active: Bool
    @Environment(\.mobileTextSize) private var fontSize
    @State private var measuredHeight: CGFloat?
    private var height: CGFloat { measuredHeight ?? max(36, fontSize * 3 + 4) }
    var body: some View {
        GeometryReader { geometry in
            SharedAvatar(value: ["tools": .array(messages.compactMap { $0.tool?.presentation }),
                "active": .bool(active), "fontSize": .number(fontSize),
                "provider": .string(settings?.provider ?? ""), "model": .string(settings?.model ?? "")],
                size: height, width: geometry.size.width, accessible: true,
                onHeightChange: { measuredHeight = $0 })
        }.frame(height: height).accessibilityIdentifier("tool-group")
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
                MessageAttachments(model: model, message: message)
                if !message.contentRef.isEmpty {
                    Button(model.loadingContent.contains(message.id) ? "正在读取…" : "加载完整消息") { model.perform { try await model.expand(message) } }
                        .disabled(!model.connected || model.loadingContent.contains(message.id))
                }
            }.padding(.horizontal, message.role == "user" ? 14 : 0)
                .padding(.vertical, message.role == "user" ? 10 : 0)
                .background(message.role == "user" ? Color.secondary.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 18))
            if message.role != "user" { Spacer(minLength: 0) }
        }
    }
}
