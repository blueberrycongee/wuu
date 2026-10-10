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
    @State private var inspectedProcess: ActivityInspection?
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
                                // Process groups also contain tools, so they are checked first.
                                if row.isProcessGroup {
                                    ProcessGroupView(messages: row.messages, settings: model.live?.settings, active: row.processActive) {
                                        inspectedProcess = ActivityInspection(id: row.messages[0].id, messages: row.messages)
                                    }
                                } else if row.isToolGroup {
                                    ToolGroupView(messages: row.messages, settings: model.live?.settings,
                                        active: model.live?.running == true && row.messages.last?.id == model.messages.last?.id) { summary in
                                        inspectedProcess = ActivityInspection(id: row.id, messages: row.messages, toolsOnly: true, summary: summary)
                                    }
                                } else {
                                    MessageBubble(model: model, message: row.messages[0])
                                }
                                // Folded rows keep their images visible, in order, in one grid.
                                if row.isProcessGroup || row.isToolGroup, row.messages.contains(where: { !$0.attachments.isEmpty }) {
                                    MessageAttachments(model: model, messages: row.messages)
                                }
                            }.id(row.id).background { TimelineRowAnchor(id: row.id, state: scrollState) }
                        }
                        if model.live?.running == true && model.messages.last?.tool == nil && model.conversationRows.last?.processActive != true {
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
                    .safeAreaInset(edge: .bottom) {
                        ZStack {
                            if let edit = pausedEdit {
                                HistoryEditResumeBanner(model: model, edit: edit)
                                    .padding(.horizontal, 12).padding(.vertical, 8)
                                    .transition(.opacity)
                            }
                        }.animation(chromeAnimation(reduceMotion), value: pausedEdit?.id)
                    }
            }
        }.sheet(item: $model.attachmentPreview) { attachment in AttachmentPreview(attachment: attachment) }
            // Owned here, not by the row, so a turn finishing or regrouping cannot close it.
            .sheet(item: $inspectedProcess) { inspection in
                ActivityInspectionSheet(model: model, inspection: inspection)
                    .presentationDetents([.medium, .large])
            }
            .sheet(isPresented: Binding(get: { model.showingHistoryEdit && model.historyEdit != nil },
                                        set: { model.showingHistoryEdit = $0 })) {
                if let edit = model.historyEdit { HistoryMessageEditView(model: model, edit: edit) }
            }
            .onDisappear { historyRequest?.cancel() }
    }
    private var pausedEdit: HistoryMessageEdit? {
        guard let edit = model.historyEdit, edit.prepared, edit.threadID == model.activeID, !model.showingHistoryEdit else { return nil }
        return edit
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
                if hasActions { MessageActions(model: model, message: message) }
            }
            if message.role != "user" { Spacer(minLength: 0) }
        }
    }
    /// A reply still streaming gets its actions once it settles, as a finished message.
    private var hasActions: Bool {
        guard message.role == "user" || message.role == "assistant" else { return false }
        return !(message.role == "assistant" && model.live?.running == true && message.id == model.messages.last?.id)
    }
}

/// Copy, fork and edit under a message. The model decides availability and does the work.
private struct MessageActions: View {
    let model: AppModel
    let message: ChatMessage
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var copied = false
    @State private var reset: Task<Void, Never>?
    var body: some View {
        HStack(spacing: 0) {
            action(copied ? "checkmark" : "doc.on.doc", label: copied ? "已复制消息" : "复制消息", id: "message-copy", perform: copy)
                .disabled(message.text.isEmpty && message.contentRef.isEmpty)
            action("arrow.triangle.branch", label: "从这条消息分叉", id: "message-fork") {
                model.perform { try await model.forkMessage(message) }
            }.disabled(!model.canFork(message))
            if message.role == "user" {
                action("pencil", label: "编辑消息", id: "message-edit") {
                    model.perform { try await model.beginHistoryEdit(message) }
                }.disabled(!model.canEdit(message))
            }
        }
        .disabled(model.historyActionBusy)
        .foregroundStyle(.secondary)
        // Glyphs line up with the message edge and sit close to it; targets keep 44pt.
        .padding(message.role == "user" ? .trailing : .leading, -13)
        .padding(.vertical, -6)
        .onDisappear { reset?.cancel() }
    }
    private func action(_ symbol: String, label: String, id: String, perform: @escaping () -> Void) -> some View {
        Button(action: perform) {
            Image(systemName: symbol).font(.system(size: 15))
                .contentTransition(reduceMotion ? .identity : .symbolEffect(.replace))
                .frame(width: 44, height: 44).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(label)
        .accessibilityIdentifier(id)
    }
    private func copy() {
        model.perform {
            // The model loads paged content, writes the pasteboard and gives feedback.
            try await model.copyMessage(message)
            copied = true
            reset?.cancel()
            reset = Task {
                try? await Task.sleep(for: .seconds(1.5))
                if !Task.isCancelled { copied = false }
            }
        }
    }
}

private struct ActivityInspection: Identifiable {
    /// A member of the inspected group, retained when a final answer regroups the rows.
    let id: String
    let messages: [ChatMessage]
    var toolsOnly = false
    var summary: String?
}

/// Follows the live group while open, so new steps appear instead of the sheet closing.
private struct ActivityInspectionSheet: View {
    let model: AppModel
    let inspection: ActivityInspection
    var body: some View {
        let row = model.conversationRows.first { $0.messages.contains { $0.id == inspection.id } }
        let messages = row?.messages ?? inspection.messages
        let active = row?.messages.contains { $0.turnActive } ?? false
        if inspection.toolsOnly {
            ToolGroupDetails(tools: messages.compactMap(\.tool), summary: inspection.summary, active: active)
        } else {
            ProcessGroupDetails(model: model, messages: messages, settings: model.live?.settings, active: active)
        }
    }
}
