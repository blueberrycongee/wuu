import SwiftUI
import WuuCore

struct ConversationTimeline: View {
    @Bindable var model: AppModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var nearBottom = true
    @State private var following = true
    @State private var scrollState = TimelineScrollState()
    @State private var historyRequest: Task<Void, Never>?
    @State private var historyFailed = false
    var body: some View {
        GeometryReader { _ in
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 18) {
                        if model.live?.historyCursor.isEmpty == false {
                            HStack {
                                Spacer()
                                if historyFailed {
                                    Button("读取失败，轻点重试") { loadHistory() }.disabled(!model.connected)
                                } else if model.loadingHistory {
                                    ProgressView().accessibilityLabel("正在读取更早的消息")
                                }
                                Spacer()
                            }.frame(minHeight: 24)
                        }
                        ForEach(model.conversationRows) { row in
                            VStack(alignment: .leading, spacing: 8) {
                                if row.isToolGroup {
                                    ToolGroupView(messages: row.messages, settings: model.live?.settings,
                                        active: model.live?.running == true && row.messages.last?.id == model.messages.last?.id)
                                    ForEach(row.messages.filter { !$0.attachments.isEmpty }) { message in
                                        MessageAttachments(model: model, message: message)
                                    }
                                } else {
                                    MessageBubble(model: model, message: row.messages[0])
                                }
                            }.id(row.id).background { TimelineRowAnchor(id: row.id, state: scrollState) }
                        }
                        if model.live?.running == true && model.messages.last?.tool == nil {
                            ConversationActivityMark(activity: model.messages.last?.role == "assistant" ? "responding" : "thinking", settings: model.live?.settings)
                                .accessibilityElement().accessibilityLabel("正在处理")
                        }
                        Color.clear.frame(height: 1).id("bottom")
                    }.padding(16).background {
                        TimelineScrollReader(state: scrollState) { next in
                            let update = scrollState.update(next, following: following)
                            if nearBottom != next.atBottom { nearBottom = next.atBottom }
                            if following != update.following { following = update.following }
                            if update.scrollToBottom { proxy.scrollTo("bottom", anchor: .bottom) }
                            if next.interacting && next.offset < 160 && !historyFailed { loadHistory() }
                        }
                    }
                }.defaultScrollAnchor(.bottom)
                    .scrollDismissesKeyboard(.interactively)
                    .onChange(of: model.messages.last) { _, _ in
                        if following && scrollState.metrics?.interacting != true { proxy.scrollTo("bottom", anchor: .bottom) }
                    }
                    .overlay(alignment: .bottomTrailing) {
                        ZStack {
                            if !nearBottom {
                                Button { following = true; proxy.scrollTo("bottom", anchor: .bottom) } label: {
                                    Label("最新消息", systemImage: "arrow.down").font(.caption).padding(10).background(.regularMaterial, in: Capsule())
                                        .frame(minHeight: 44)
                                }.padding(12).transition(.opacity)
                            }
                        }.animation(chromeAnimation(reduceMotion), value: nearBottom)
                    }
            }
        }.sheet(item: $model.attachmentPreview) { attachment in AttachmentPreview(attachment: attachment) }
            .onDisappear { historyRequest?.cancel() }
    }
    private func loadHistory() {
        guard historyRequest == nil, model.connected, !model.loadingHistory,
              model.live?.historyCursor.isEmpty == false else { return }
        historyFailed = false
        historyRequest = Task {
            defer { historyRequest = nil }
            do { try await model.loadOlder(beforePrepend: scrollState.preservePosition) }
            catch is CancellationError {}
            catch { if !Task.isCancelled { historyFailed = true } }
        }
    }
}

private struct MessageBubble: View {
    @Bindable var model: AppModel
    let message: ChatMessage
    @State private var expanded = false
    private var collapsible: Bool { !message.sourceSessionID.isEmpty && (message.text.prefix(401).count > 400 || message.text.filter { $0 == "\n" }.count > 6) }
    var body: some View {
        HStack {
            if message.role == "user" { Spacer(minLength: 36) }
            VStack(alignment: message.role == "user" ? .trailing : .leading, spacing: 8) {
                if !message.sourceSessionID.isEmpty {
                    Button { model.perform { try await model.open(message.sourceSessionID) } } label: {
                        Label("由 Wuu 从「\(message.sourceSessionName.isEmpty ? message.sourceSessionID : message.sourceSessionName)」发送", systemImage: "bubble.left.and.bubble.right")
                            .multilineTextAlignment(.trailing).foregroundStyle(.secondary)
                    }.buttonStyle(.plain).disabled(!model.connected)
                }
                if !message.text.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        MessageText(text: collapsible && !expanded ? String(message.text.prefix(240)) + "…" : message.text, markdown: message.role == "assistant")
                            .foregroundStyle(message.role == "error" ? Color.red : Color.primary)
                        if collapsible {
                            Button(expanded ? "收起" : "显示更多", systemImage: expanded ? "chevron.up" : "chevron.down") { expanded.toggle() }
                                .buttonStyle(.plain).foregroundStyle(.secondary)
                        }
                    }.padding(.horizontal, message.role == "user" ? 14 : 0)
                        .padding(.vertical, message.role == "user" ? 10 : 0)
                        .background(message.role == "user" ? Color.secondary.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 18))
                }
                MessageAttachments(model: model, message: message)
                if !message.contentRef.isEmpty {
                    Button(model.loadingContent.contains(message.id) ? "正在读取…" : "加载完整消息") { model.perform { try await model.expand(message) } }
                        .disabled(!model.connected || model.loadingContent.contains(message.id))
                }
                if !message.sourceSessionID.isEmpty {
                    Button("复制", systemImage: "doc.on.doc") { UIPasteboard.general.string = message.text; Haptics.tap() }
                        .labelStyle(.iconOnly).buttonStyle(.plain).foregroundStyle(.secondary)
                }
            }
            if message.role != "user" { Spacer(minLength: 0) }
        }
    }
}
