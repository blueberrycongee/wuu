import SwiftUI
import WuuCore

struct ConversationTimeline: View {
    @Bindable var model: AppModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.verticalSizeClass) private var verticalSizeClass
    @State private var nearBottom = true
    @State private var following = true
    @State private var scrollState = TimelineScrollState()
    @State private var historyRequest: Task<Void, Never>?
    @State private var historyFailed = false
    @State private var inspectedProcess: ActivityInspection?
    private let inset: CGFloat = 16
    private let rowSpacing: CGFloat = 18
    var body: some View {
        GeometryReader { geometry in
            ScrollViewReader { proxy in
                ScrollView {
                    let rows = model.conversationRows
                    let recentStart = rows.firstIndex { $0.messages[0].turnID == rows.last?.messages[0].turnID } ?? rows.endIndex
                    VStack(alignment: .leading, spacing: rowSpacing) {
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
                        if recentStart > rows.startIndex {
                            LazyVStack(alignment: .leading, spacing: rowSpacing) {
                                ForEach(rows[..<recentStart]) { row in timelineRow(row, width: geometry.size.width) }
                            }
                        }
                        // Keep the newest turn mounted while keyboard/menu transitions resize the viewport.
                        // Rejoin the lazy history when iOS no longer loops row phases around embedded UIKit views.
                        ForEach(rows[recentStart...]) { row in timelineRow(row, width: geometry.size.width) }
                        if model.live?.running == true && model.messages.last?.tool == nil && model.conversationRows.last?.processActive != true {
                            ConversationActivityMark(activity: model.messages.last?.role == "assistant" ? "responding" : "thinking", settings: model.live?.settings)
                                .accessibilityElement().accessibilityLabel("正在处理")
                        }
                        Color.clear.frame(height: 1).id("bottom")
                    }.padding(inset).background {
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
            .sheet(item: $model.pendingFork) { request in
                MessageForkSheet(model: model, request: request)
                    .presentationDetents([.medium, .large])
            }
            .sheet(isPresented: Binding(get: { model.showingHistoryEdit && model.historyEdit != nil },
                                        set: { model.showingHistoryEdit = $0 })) {
                if let edit = model.historyEdit { HistoryMessageEditView(model: model, edit: edit) }
            }
            .onDisappear { historyRequest?.cancel() }
    }
    private func timelineRow(_ row: ConversationRow, width: CGFloat) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            // Process groups also contain tools, so they are checked first.
            if row.isProcessGroup {
                ProcessGroupView(messages: row.messages, settings: model.live?.settings, active: row.processActive) {
                    inspectedProcess = ActivityInspection(id: row.messages[0].id, messages: row.messages)
                }
            } else if row.isToolGroup {
                ToolGroupView(model: model, messages: row.messages, settings: model.live?.settings,
                    active: model.live?.running == true && row.messages.last?.id == model.messages.last?.id) { summary in
                    inspectedProcess = ActivityInspection(id: row.id, messages: row.messages, toolsOnly: true, summary: summary)
                }
            } else {
                MessageBubble(model: model, message: row.messages[0],
                    // Compact-height menus sit beside the preview; avoid UIKit resizing its container.
                    menuPreviewWidth: min(width - 2 * inset, verticalSizeClass == .compact ? 250 : 360))
            }
            // Published artifacts stay in the timeline; inspection images belong to the folded details.
            if row.isProcessGroup || row.isToolGroup, row.messages.contains(where: \.hasInlineAttachments) {
                MessageAttachments(model: model, messages: row.messages)
            }
        }.id(row.id).background { TimelineRowAnchor(id: row.id, state: scrollState) }
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
    let menuPreviewWidth: CGFloat
    @State private var expanded = false
    @State private var selection: MessageTextSelection?
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
                        MessageText(text: collapsible && !expanded ? String(message.text.prefix(240)) + "…" : message.text,
                                    markdown: message.role == "assistant", contextMenuEnabled: message.role != "user")
                            .foregroundStyle(message.role == "error" ? Color.red : Color.primary)
                        if collapsible {
                            Button(expanded ? "收起" : "显示更多", systemImage: expanded ? "chevron.up" : "chevron.down") { expanded.toggle() }
                                .buttonStyle(.plain).foregroundStyle(.secondary)
                        }
                    }.padding(.horizontal, message.role == "user" ? 14 : 0)
                        .padding(.vertical, message.role == "user" ? 10 : 0)
                        .background(message.role == "user" ? Color.secondary.opacity(0.1) : .clear, in: RoundedRectangle(cornerRadius: 18))
                        .contextMenu { if message.role == "user" { userActions } } preview: { userMenuPreview }
                }
                MessageAttachments(model: model, message: message)
                    .contextMenu { if message.role == "user" { userActions } } preview: { userMenuPreview }
                if !message.contentRef.isEmpty {
                    Button(model.loadingContent.contains(message.id) ? "正在读取…" : "加载完整消息") { model.perform { try await model.expand(message) } }
                        .disabled(!model.connected || model.loadingContent.contains(message.id))
                }
                if message.role == "assistant", !message.turnActive { ReplyActions(model: model, message: message) }
            }
            .sheet(item: $selection) { TextSelectionSheet(text: $0.text) }
            if message.role != "user" { Spacer(minLength: 0) }
        }
    }
    @ViewBuilder private var userMenuPreview: some View {
        if message.role == "user" {
            VStack(alignment: .trailing, spacing: 8) {
                if !message.text.isEmpty {
                    MessageText(text: message.text, markdown: false, contextMenuEnabled: false)
                        .lineLimit(6)
                }
                MessageAttachments(model: model, message: message)
            }
            .padding(14)
            .frame(width: menuPreviewWidth)
            .buttonStyle(.plain)
            .background(Color(uiColor: .secondarySystemBackground))
        }
    }
    @ViewBuilder private var userActions: some View {
        Button("复制", systemImage: "doc.on.doc") { model.perform { try await model.copyMessage(message) } }
            .accessibilityIdentifier("message-copy")
            .disabled(message.text.isEmpty && message.contentRef.isEmpty)
        Button("编辑", systemImage: "pencil") { model.perform { try await model.beginHistoryEdit(message) } }
            .accessibilityIdentifier("message-edit")
            .disabled(model.historyActionBusy || !model.canEdit(message))
        Button("选择文本", systemImage: "selection.pin.in.out") {
            model.perform { selection = MessageTextSelection(text: try await model.messageText(message)) }
        }.disabled(message.text.isEmpty && message.contentRef.isEmpty)
        ShareLink(item: message.text) { Label("分享", systemImage: "square.and.arrow.up") }
            .disabled(message.text.isEmpty)
    }
}

private struct MessageForkSheet: View {
    let model: AppModel
    let request: MessageForkRequest
    @State private var choosing: MessageForkMode?
    @State private var error: String?
    var body: some View {
        NavigationStack {
            List {
                destination(.local, title: "当前目录", detail: "继续使用当前工作目录", icon: "folder")
                destination(.worktree, title: "新的工作树", detail: "基于当前 Git 提交，改动不影响当前目录", icon: "arrow.triangle.branch")
                if let error { Text(error).foregroundStyle(.red).font(.footnote) }
            }
            .navigationTitle("分叉到哪里").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { model.pendingFork = nil }.disabled(choosing != nil)
                }
            }
        }.interactiveDismissDisabled(choosing != nil)
    }
    private func destination(_ mode: MessageForkMode, title: String, detail: String, icon: String) -> some View {
        Button {
            guard choosing == nil else { return }
            choosing = mode; error = nil
            Task {
                defer { choosing = nil }
                do { try await model.forkMessage(request, mode: mode) }
                catch is CancellationError {}
                catch { self.error = error.localizedDescription }
            }
        } label: {
            HStack(spacing: 12) {
                Image(systemName: icon).frame(width: 24).foregroundStyle(.secondary)
                VStack(alignment: .leading, spacing: 4) {
                    Text(title).foregroundStyle(.primary)
                    Text(detail).font(.footnote).foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
                if choosing == mode { ProgressView() }
            }.padding(.vertical, 8)
        }
        .accessibilityIdentifier("message-fork-" + mode.rawValue)
        .disabled(choosing != nil || !model.connected || model.activeID != request.threadID || model.host?.pub != request.hostID)
    }
}

private struct MessageTextSelection: Identifiable {
    let id = UUID()
    let text: String
}

/// Finished replies keep their quick actions; user messages own a context menu.
private struct ReplyActions: View {
    let model: AppModel
    let message: ChatMessage
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var copied = false
    @State private var reset: Task<Void, Never>?
    var body: some View {
        HStack(spacing: 0) {
            action(copied ? "WuuCheck" : "WuuCopy", label: copied ? "已复制消息" : "复制消息", id: "message-copy", perform: copy)
                .disabled(message.text.isEmpty && message.contentRef.isEmpty)
            action("WuuSplit", label: "从这条消息分叉", id: "message-fork") {
                model.beginFork(message)
            }.disabled(!model.canFork(message))
        }
        .disabled(model.historyActionBusy)
        .foregroundStyle(.secondary)
        // Align glyphs with the message edge; compact horizontal targets retain 44pt height.
        .padding(.leading, -9)
        .padding(.vertical, -6)
        .onDisappear { reset?.cancel() }
    }
    private func action(_ artwork: String, label: String, id: String, perform: @escaping () -> Void) -> some View {
        Button(action: perform) {
            Image(artwork).renderingMode(.template).resizable()
                .frame(width: 14, height: 14)
                .contentTransition(reduceMotion ? .identity : .opacity)
                .frame(width: 32, height: 44).contentShape(Rectangle())
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
            ToolGroupDetails(model: model, messages: messages, summary: inspection.summary, active: active)
        } else {
            ProcessGroupDetails(model: model, messages: messages, settings: model.live?.settings, active: active)
        }
    }
}
