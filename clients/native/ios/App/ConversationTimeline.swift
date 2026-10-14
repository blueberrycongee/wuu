import SwiftUI
import WuuCore

struct ConversationTimeline: View {
    @Bindable var model: AppModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.verticalSizeClass) private var verticalSizeClass
    @State private var nearBottom = true
    @State private var following = true
    @State private var scrollState = TimelineScrollState()
    @State private var historyRetry = 0
    @State private var historyFailed = false
    @State private var inspectedProcess: ActivityInspection?
    @State private var presentedMessages: Set<String>
    init(model: AppModel) {
        self.model = model
        _presentedMessages = State(initialValue: Set(model.messages.map(\.id)))
    }
    private let inset: CGFloat = 16
    private let rowSpacing: CGFloat = 18
    var body: some View {
        GeometryReader { geometry in
            let rows = model.conversationRows
            ScrollView {
                // Lay out from the newest edge so following never targets a lazy height estimate.
                LazyVStack(alignment: .leading, spacing: rowSpacing) {
                    if model.live?.running == true && model.messages.last?.tool == nil && model.conversationRows.last?.processActive != true {
                        ConversationActivityMark(activity: model.messages.last?.role == "assistant" ? "responding" : "thinking", settings: model.live?.settings)
                            .accessibilityElement().accessibilityLabel("正在处理")
                            .scaleEffect(x: 1, y: -1)
                    }
                    ForEach(rows.reversed()) { row in timelineRow(row, size: geometry.size).scaleEffect(x: 1, y: -1) }
                    if model.live?.historyCursor.isEmpty == false {
                        HStack {
                            Spacer()
                            if historyFailed {
                                Button("读取失败，轻点重试") { historyRetry += 1 }.disabled(!model.connected || model.refreshing)
                            } else if model.loadingHistory {
                                ProgressView().accessibilityLabel("正在读取更早的消息")
                            }
                            Spacer()
                        }.frame(minHeight: 24).scaleEffect(x: 1, y: -1)
                    }
                }.padding(inset)
                    // Fill the viewport even without rows so refresh overlays keep their width and center.
                    // Short conversations start at the top; once filled, normal bottom following takes over.
                    .frame(maxWidth: .infinity, minHeight: geometry.size.height, alignment: .bottomLeading)
                    .background {
                    TimelineScrollReader(state: scrollState) { next in
                        let update = scrollState.update(next, following: following)
                        if nearBottom != next.atBottom { nearBottom = next.atBottom }
                        if following != update.following { following = update.following }
                        if update.scrollToBottom { scrollState.scrollToBottom() }
                    }
                }
            }.scaleEffect(x: 1, y: -1)
                .accessibilityScrollAction { edge in
                    guard let scroll = scrollState.scroll, edge == .top || edge == .bottom else { return }
                    // Accessibility directions describe the display, not the flipped scroll axis.
                    scrollState.cancelRestoration(); following = false
                    let minimum = -scroll.adjustedContentInset.top
                    let maximum = max(minimum, scroll.contentSize.height - scroll.bounds.height + scroll.adjustedContentInset.bottom)
                    let offset = scroll.contentOffset.y + (edge == .top ? 1 : -1) * scroll.bounds.height * 0.9
                    scroll.setContentOffset(CGPoint(x: scroll.contentOffset.x, y: min(maximum, max(minimum, offset))), animated: false)
                }
                .coordinateSpace(name: "conversationViewport")
                // Interactive dismissal assumes an untransformed vertical scroll axis.
                .scrollDismissesKeyboard(.immediately)
                .onChange(of: geometry.size) { _, _ in
                    if !following && !scrollState.preservingPosition { scrollState.preservePosition() }
                }
                .onChange(of: model.messages.last) { _, _ in
                    // New rows and streamed text grow before the reader in reversed coordinates.
                    if !following && !scrollState.preservingPosition { scrollState.preservePosition() }
                }
                .overlay(alignment: .bottomTrailing) {
                    ZStack {
                        if !nearBottom {
                            Button { scrollState.cancelRestoration(); following = true; scrollState.scrollToBottom() } label: {
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
                // Floats over retained content so the reading position never moves.
                .overlay(alignment: .top) {
                    // Animate only the indicator; the snapshot's row changes apply without motion.
                    ZStack {
                        if model.updating {
                            Label { Text("正在更新…") } icon: { ProgressView().controlSize(.small) }
                                .font(.caption).foregroundStyle(.secondary)
                                .padding(10).background(.regularMaterial, in: Capsule())
                                .padding(12).transition(.opacity).allowsHitTesting(false)
                        } else if model.refreshing && model.connected, let id = model.activeID {
                            Button {
                                model.perform { try await model.open(id, preservingContent: true) }
                            } label: {
                                Label("重新加载会话", systemImage: "arrow.clockwise")
                                    .font(.caption).padding(10).background(.regularMaterial, in: Capsule())
                            }.padding(12)
                        }
                    }
                        .animation(reduceMotion ? nil : .easeOut(duration: 0.2), value: model.updating)
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
            .task(id: model.historyLoadID.map { "\($0):\(historyRetry)" }) { await loadHistory() }
            .onChange(of: model.activeID) { _, _ in inspectedProcess = nil }
    }
    private func timelineRow(_ row: ConversationRow, size: CGSize) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            // Process groups also contain tools, so they are checked first.
            if row.isProcessGroup {
                ProcessGroupView(messages: row.messages, settings: model.live?.settings, active: row.processActive) {
                    inspectedProcess = ActivityInspection(id: row.messages[0].id, threadID: model.live?.id ?? "", messages: row.messages)
                }
            } else if row.isToolGroup {
                ToolGroupView(model: model, messages: row.messages, settings: model.live?.settings,
                    active: model.live?.running == true && row.messages.last?.id == model.messages.last?.id) { summary in
                    inspectedProcess = ActivityInspection(id: row.id, threadID: model.live?.id ?? "", messages: row.messages, toolsOnly: true, summary: summary)
                }
            } else {
                MessageBubble(model: model, message: row.messages[0],
                    // Compact-height menus sit beside the preview; avoid UIKit resizing its container.
                    menuPreviewWidth: min(size.width - 2 * inset, verticalSizeClass == .compact ? 250 : 360))
                    .modifier(MessageArrivalMotion(viewportHeight: size.height, reduceMotion: reduceMotion) {
                        let message = row.messages[0]
                        let firstAppearance = presentedMessages.insert(message.id).inserted
                        return firstAppearance && following && !model.loadingHistory && message.turnActive
                            && message.role == "user" && message.id == model.messages.last(where: { $0.role == "user" })?.id
                    })
            }
            // Published artifacts stay in the timeline; inspection images belong to the folded details.
            if row.isProcessGroup || row.isToolGroup, row.messages.contains(where: \.hasInlineAttachments) {
                MessageAttachments(model: model, messages: row.messages)
            }
        }.id(row.id).background {
            let last = row.messages.last!
            TimelineRowAnchor(id: last.activity["last_item_id"].string.map { last.turnID + ":" + $0 } ?? last.id, state: scrollState)
        }
    }
    private var pausedEdit: HistoryMessageEdit? {
        guard let edit = model.historyEdit, edit.prepared, edit.threadID == model.activeID, !model.showingHistoryEdit else { return nil }
        return edit
    }
    private func loadHistory() async {
        guard let selection = model.historyLoadID else { return }
        historyFailed = false
        do {
            // Let the initial snapshot render, then read ahead without another scroll gesture.
            await Task.yield()
            while !Task.isCancelled, model.historyLoadID == selection, model.live?.historyCursor.isEmpty == false {
                // Lazy layout must finish restoring the previous page before capturing another anchor.
                // A cancelled request may also still be releasing the model's single request slot.
                if scrollState.preservingPosition || model.loadingHistory {
                    try await Task.sleep(for: .milliseconds(16))
                    continue
                }
                try await model.loadOlder {
                    if !following || scrollState.metrics?.interacting == true {
                        following = false
                        scrollState.preservePosition(at: .bottom)
                    }
                }
                await Task.yield()
            }
        } catch is CancellationError {} catch {
            if !Task.isCancelled, model.historyLoadID == selection { historyFailed = true }
        }
    }
}

// Inspired by Motionbook's chatgpt-dot-send travel track. Native layout remains authoritative
// as the keyboard moves; only the rendered bubble travels, never the scroll geometry.
private struct MessageArrivalMotion: ViewModifier {
    let viewportHeight: CGFloat
    let reduceMotion: Bool
    let shouldAnimate: () -> Bool
    @State private var arriving = false
    func body(content: Content) -> some View {
        content
            .visualEffect { effect, geometry in
                effect.offset(y: arriving ? max(24, viewportHeight - geometry.frame(in: .named("conversationViewport")).maxY - 16) : 0)
            }
            .opacity(arriving ? 0 : 1)
            .onAppear {
                guard shouldAnimate(), !reduceMotion else { return }
                var transaction = Transaction()
                transaction.disablesAnimations = true
                withTransaction(transaction) { arriving = true }
            }
            .task(id: arriving) {
                guard arriving else { return }
                // Commit the starting pose before beginning travel to the live layout position.
                await Task.yield()
                guard !Task.isCancelled else { return }
                withAnimation(.spring(duration: 0.5, bounce: 0.06)) { arriving = false }
            }
            .onDisappear { arriving = false }
            .onChange(of: reduceMotion) { _, reduced in
                if reduced {
                    var transaction = Transaction()
                    transaction.disablesAnimations = true
                    withTransaction(transaction) { arriving = false }
                }
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
                                    markdown: message.role == "assistant", contextMenuEnabled: message.role != "user",
                                    isLive: message.role == "assistant" && message.turnActive && message.id == model.messages.last?.id)
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

/// Detail pages stay in this sheet; reading them never mutates timeline rows or its cursor.
private struct PagedActivityInspection: View {
    let model: AppModel
    let summary: ChatMessage
    let threadID: String
    @State private var detail: ChatThread?
    @State private var cursor = ""
    @State private var loaded = false
    @State private var loading = false
    @State private var error: String?
    @State private var pageRequest = 0
    var body: some View {
        ProcessGroupDetails(model: model, messages: detail?.messages ?? [], settings: model.live?.settings,
            active: false, loading: loading, loadError: error,
            loadMore: !loaded || !cursor.isEmpty ? { pageRequest += 1 } : nil,
            expand: { message in
                model.perform {
                    let item = try await model.readActivityContent(message, threadID: threadID)
                    detail?.expandContent(message.contentRef, item: item)
                }
            })
            .task(id: pageRequest) { await loadPage() }
    }
    private func loadPage() async {
        guard !loading, !loaded || !cursor.isEmpty else { return }
        loading = true; error = nil
        defer { loading = false }
        do {
            let page = try await model.readActivity(summary, cursor: cursor, threadID: threadID)
            guard case .object(var turn) = page["turn"] else { throw NativeError.invalid("电脑未返回处理过程") }
            let previous = detail?.turns.first?["items"].array ?? []
            turn["items"] = .array(previous + page["turn"]["items"].array)
            detail = ChatThread(["id": .string(threadID), "turns": [.object(turn)]])
            cursor = page["next_cursor"].string ?? ""
            loaded = true
        } catch is CancellationError {} catch { self.error = error.localizedDescription }
    }
}

private struct ActivityInspection: Identifiable {
    /// A member of the inspected group, retained when a final answer regroups the rows.
    let id: String
    let threadID: String
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
        let active = row?.processActive == true || (row?.isToolGroup == true && model.live?.running == true &&
            row?.messages.last?.id == model.messages.last?.id)
        if let summary = messages.first, summary.activity != .null {
            PagedActivityInspection(model: model, summary: summary, threadID: inspection.threadID)
                .id(inspection.threadID + ":" + summary.id)
        } else if inspection.toolsOnly {
            ToolGroupDetails(model: model, messages: messages, summary: inspection.summary, active: active)
        } else {
            ProcessGroupDetails(model: model, messages: messages, settings: model.live?.settings, active: active)
        }
    }
}
