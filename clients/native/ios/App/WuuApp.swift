import SwiftUI
import WuuCore

@main struct WuuApp: App {
    @UIApplicationDelegateAdaptor(PushDelegate.self) private var pushDelegate
    @State private var model = AppModel()
    @Environment(\.scenePhase) private var phase
    var body: some Scene {
        WindowGroup {
            RootView(model: model)
                .modifier(MobileTypography())
                .task { model.foreground() }
                .onChange(of: pushDelegate.openedHost, initial: true) { _, host in
                    if let host { pushDelegate.openedHost = nil; model.perform { try await model.openPushHost(host) } }
                }
                .onOpenURL { url in
                    guard url.scheme == "wuu" else { return }
                    // A pairing link only fills the pairing sheet; the user still chooses to connect.
                    if url.host == "pair" { model.receivedPairingLink = url.absoluteString }
                    else if url.host == "account", url.path == "/github" { model.foreground() }
                }
                .onChange(of: phase) { _, value in
                    if value == .active { model.foreground() }
                    else if value == .background { Task { await model.background() } }
                }
        }
    }
}

private struct PairingRequest: Identifiable {
    let id = UUID()
    let link: String
}

/// Unsent composer text and attachments by computer and conversation. Owned above the
/// per-computer view so switching computers or conversations keeps them.
struct ComposerDrafts {
    var text: [String: String] = [:]
    var attachments: [String: [InputAttachment]] = [:]
}

struct RootView: View {
    @Bindable var model: AppModel
    @Environment(\.colorScheme) private var scheme
    @State private var pairing: PairingRequest?
    @State private var drafts = ComposerDrafts()
    private var isFixture: Bool {
        #if DEBUG
        NativeUIFixture.enabled
        #else
        false
        #endif
    }
    @ViewBuilder private var fixture: some View {
        #if DEBUG
        NativeUIFixtureView(model: model)
        #endif
    }
    var body: some View {
        Group {
            if isFixture { fixture }
            else if model.recovery != nil { RecoveryView(model: model) }
            else if model.host != nil { ConversationView(model: model, drafts: $drafts).id(model.host?.pub) }
            else if model.hasSavedConnections { ComputersView(model: model, addComputer: pair) }
            else { WelcomeView(model: model, openPairing: pair) }
        }
        .sheet(item: $pairing, onDismiss: { model.receivedPairingLink = nil }) { request in
            PairComputerView(model: model, link: request.link).modelErrorAlert(model)
        }
        .onChange(of: model.receivedPairingLink, initial: true) { _, link in
            if let link, pairing?.link != link { pairing = PairingRequest(link: link) }
        }
        .modelErrorAlert(model)
        // A semantic SwiftUI primary tint can feed back into UIKit trait resolution.
        .tint(scheme == .dark ? Color.white : Color.black)
    }
    private func pair() { pairing = PairingRequest(link: "") }
}

struct ConversationView: View {
    @Bindable var model: AppModel
    @Binding var drafts: ComposerDrafts
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var conversationPresented = false
    @State private var newConversation = false
    @State private var searching = false
    @FocusState private var searchFocused: Bool
    @State private var consent = false
    @State private var disableHistory = false
    @State private var archiveTarget: ChatThread?
    @State private var renaming = false
    @State private var renameID = ""
    @State private var renameTitle = ""
    @State private var exporting = false
    @State private var exportDocument = ConversationDocument(text: "")
    @State private var settingsThread: ChatThread?
    @State private var accountSettings = false
    init(model: AppModel, drafts: Binding<ComposerDrafts>) {
        self.model = model
        _drafts = drafts
        _conversationPresented = State(initialValue: model.activeID != nil)
    }
    /// Server history sync belongs to the account's own host connection, not to direct pairs.
    private var accountHistory: Bool { model.account != nil && model.selectedPair == nil }
    private var hostName: String { (model.host?.name).flatMap { $0.isEmpty ? nil : $0 } ?? "电脑" }
    private var query: String { model.search.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var draftKey: String { (model.host?.pub ?? "") + ":" + (model.activeID ?? "new") }
    private var draft: Binding<String> {
        let key = draftKey
        return Binding(get: { drafts.text[key] ?? "" }, set: { drafts.text[key] = $0 })
    }
    private var attachments: Binding<[InputAttachment]> {
        let key = draftKey
        return Binding(get: { drafts.attachments[key] ?? [] }, set: { drafts.attachments[key] = $0 })
    }
    var body: some View {
        NavigationStack {
            conversationList
                .navigationTitle("会话")
                .toolbar(.hidden, for: .navigationBar)
                .navigationDestination(isPresented: $conversationPresented) {
                    conversation
                        .toolbar(.visible, for: .navigationBar)
                }
        }
        .onChange(of: conversationPresented) { _, presented in
            if !presented { searching = !model.search.isEmpty }
        }
        .confirmationDialog("同步对话到服务器？", isPresented: $consent, titleVisibility: .visible) {
            Button("开启文字历史同步") { model.perform { try await model.setHistory(true) } }
        } message: { Text("服务器将保存可读取的用户消息和助手回复，电脑离线时仍可查看。不包含附件或工具输出。关闭后删除服务器副本。") }
        .confirmationDialog("关闭并删除服务器历史？", isPresented: $disableHistory, titleVisibility: .visible) {
            Button("关闭并删除", role: .destructive) { model.perform { try await model.setHistory(false) } }
        } message: { Text("此电脑已同步的服务器副本将被删除，电脑上的原始会话仍然保留。") }
        .confirmationDialog(archiveTarget?.archived == true ? "恢复会话？" : "归档会话？",
                            isPresented: Binding(get: { archiveTarget != nil }, set: { if !$0 { archiveTarget = nil } }),
                            titleVisibility: .visible, presenting: archiveTarget) { thread in
            Button(thread.archived ? "恢复" : "归档") { act { try await model.archive(thread) } }
        } message: { thread in
            Text(thread.archived ? "会话将回到会话列表。" : "会话会移到「已归档会话」，可以随时恢复。")
        }
        .alert("重命名会话", isPresented: $renaming) {
            TextField("标题", text: $renameTitle)
            Button("保存") { let id = renameID, title = renameTitle; act { try await model.rename(id, title: title) } }
            Button("取消", role: .cancel) {}
        }
        .fileExporter(isPresented: $exporting, document: exportDocument, contentType: .plainText, defaultFilename: "conversation.txt") { result in
            if case .failure(let error) = result { model.error = error.localizedDescription }
        }
        .sheet(item: $settingsThread) { thread in ThreadSettingsView(model: model, thread: thread) }
        .sheet(isPresented: $accountSettings) { AccountSettingsView(model: model).modelErrorAlert(model) }
        .sheet(isPresented: $newConversation) {
            NewConversationView(model: model) {
                searchFocused = false
                conversationPresented = true
            }
        }
    }

    /// Runs an action the user explicitly asked for; failures get error feedback.
    private func act(_ operation: @escaping @MainActor () async throws -> Void) {
        Task {
            do { try await operation() }
            catch is CancellationError {}
            catch { model.error = error.localizedDescription; Haptics.failure() }
        }
    }

    // MARK: Conversation

    private var conversationTitle: String {
        let title = model.live?.title ?? model.saved?.title ?? ""
        return title.isEmpty ? "新会话" : title
    }
    private var threadFolder: String? {
        guard let cwd = model.live?.cwd, !cwd.isEmpty else { return nil }
        return cwd
    }
    private var activeQuestion: JSONValue? { model.questions.first { $0["thread_id"].string == model.activeID } }
    private var composerEnabled: Bool {
        model.connected && model.live != nil && model.live?.readOnly != true && model.live?.archived != true
    }
    private var composerPlaceholder: String {
        guard let live = model.live else { return model.connected ? "正在打开会话…" : "发送消息" }
        if live.archived { return "已归档的会话" }
        if live.readOnly { return "此会话只读" }
        return live.running ? "添加后续消息" : "发送消息"
    }
    private var freshConversation: Bool {
        guard let live = model.live else { return false }
        return model.messages.isEmpty && live.pending.isEmpty && !live.running
    }
    private var conversation: some View {
        VStack(spacing: 0) {
            if !model.connected {
                ConnectionStatusView(connecting: model.connecting, offlineMessage: "电脑未连接 · 连接后才能发送", detail: model.connectionStatus) {
                    Task { await model.connect() }
                }.transition(.opacity)
            }
            if model.activeID == nil {
                ContentUnavailableView {
                    Label("选择或新建会话", systemImage: "bubble.left.and.bubble.right")
                } actions: {
                    Button("新会话") { newConversation = true }.disabled(!model.connected)
                }.frame(maxHeight: .infinity)
            } else {
                ConversationTimeline(model: model).id(model.activeID)
                    .overlay { if freshConversation { freshConversationHint } }
            }
            if let request = model.pendingApproval {
                VStack(alignment: .leading, spacing: 8) {
                    Text("电脑需要处理请求：" + (request["method"].string ?? "")).font(.footnote).foregroundStyle(.secondary)
                    Button("拒绝并返回对话") { act { try await model.rejectRequest() } }.frame(minHeight: 44)
                }
                .padding(14).frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 16))
                .padding(.horizontal, 12).padding(.top, 8)
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if let request = activeQuestion {
                QuestionView(model: model, request: request).id(request["request_id"].string)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if model.activeID != nil {
                MobileComposer(text: draft, attachments: attachments, model: model, enabled: composerEnabled,
                    sending: model.sending, placeholder: composerPlaceholder, identifier: "harness-composer",
                    stop: model.live?.running == true ? stopTurn : nil,
                    send: send)
            }
        }
        // Each value changes in its own update, so the transcript is not animated with them.
        .animation(chromeAnimation(reduceMotion), value: model.connected)
        .animation(chromeAnimation(reduceMotion), value: model.pendingApproval != nil)
        .animation(chromeAnimation(reduceMotion), value: activeQuestion?["request_id"].string)
        .navigationTitle(conversationTitle).navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(Color(uiColor: .systemBackground), for: .navigationBar)
        .toolbarBackground(.visible, for: .navigationBar)
        .toolbar {
            ToolbarItem(placement: .principal) {
                VStack(spacing: 0) {
                    Text(conversationTitle).font(.headline).lineLimit(1)
                    Text(threadFolder.map(folderName) ?? hostName).font(.caption).foregroundStyle(.secondary)
                        .lineLimit(1).truncationMode(.middle)
                }
                .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
                .accessibilityElement(children: .combine).accessibilityAddTraits(.isHeader)
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button { newConversation = true } label: { Image(systemName: "square.and.pencil").font(.system(size: 19)) }
                    .disabled(!model.connected).accessibilityLabel("新会话")
            }
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    if let folder = threadFolder {
                        Section(folder) {
                            Button("复制文件夹路径", systemImage: "doc.on.doc") { UIPasteboard.general.string = folder; Haptics.tap() }
                        }
                    }
                    Button("会话设置", systemImage: "slider.horizontal.3") { settingsThread = model.live }
                        .disabled(!model.connected || model.live == nil)
                    Button("重命名", systemImage: "pencil") {
                        renameID = model.activeID ?? ""; renameTitle = model.live?.title ?? ""; renaming = true
                    }.disabled(!model.connected || model.live == nil)
                    Button(model.live?.historyCursor.isEmpty == false || model.messages.contains(where: { !$0.contentRef.isEmpty }) ? "导出已加载文本" : "导出对话文本", systemImage: "square.and.arrow.up") {
                        exportDocument = ConversationDocument(text: model.messages.filter { $0.tool == nil }.map { "## \($0.role)\n\n\($0.text)" }.joined(separator: "\n\n"))
                        exporting = true
                    }.disabled(model.messages.isEmpty)
                } label: { Image(systemName: "ellipsis.circle").font(.system(size: 19)) }.accessibilityLabel("会话操作")
            }
        }
    }
    private var freshConversationHint: some View {
        VStack(spacing: 6) {
            Text(threadFolder.map(folderName) ?? hostName).font(.title3).multilineTextAlignment(.center)
            Text("发送消息，在「\(hostName)」上开始任务。").foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
        .padding(32).allowsHitTesting(false)
        .accessibilityElement(children: .combine)
    }
    private func stopTurn() {
        Haptics.tap()
        act { try await model.stop() }
    }
    private func send() {
        let key = draftKey
        let original = drafts.text[key] ?? ""
        let text = original.trimmingCharacters(in: .whitespacesAndNewlines)
        let files = drafts.attachments[key] ?? []
        Haptics.tap()
        act {
            try await model.send(text, attachments: files)
            if drafts.text[key] == original { drafts.text[key] = "" }
            drafts.attachments[key]?.removeAll { file in files.contains { $0.id == file.id } }
        }
    }

    // MARK: Conversation list

    private var visibleThreads: [ChatThread] {
        // Connected search runs on the computer; retained rows are filtered here while offline.
        let threads = model.connected || query.isEmpty ? model.threads : model.threads.filter { $0.title.localizedCaseInsensitiveContains(query) }
        return threads.sorted { $0.pinned != $1.pinned ? $0.pinned : $0.updatedAt > $1.updatedAt }
    }
    private var visibleEntries: [HistoryEntry] {
        guard !model.connected, model.threads.isEmpty else { return [] }
        return model.entries.filter { query.isEmpty || $0.title.localizedCaseInsensitiveContains(query) }
    }
    private var listEmpty: Bool { visibleThreads.isEmpty && visibleEntries.isEmpty }
    /// The empty list offers its own start action; the floating one would repeat it.
    private var emptyListStarts: Bool { listEmpty && model.connected && !model.archivedList && query.isEmpty }
    private var folderOptions: [String] {
        var paths = model.workspaces.compactMap { $0["path"].string }.filter { !$0.isEmpty }
        if !model.workspace.isEmpty && !paths.contains(model.workspace) { paths.insert(model.workspace, at: 0) }
        return paths
    }
    private var conversationList: some View {
        VStack(spacing: 0) {
            listHeader
            if searching { searchField.transition(.opacity) }
            if !model.connected && !model.connecting {
                ConnectionStatusView(connecting: false, offlineMessage: "电脑未连接", detail: model.connectionStatus) {
                    Task { await model.connect() }
                }.transition(.opacity)
            }
            if !folderOptions.isEmpty && query.isEmpty { folderPicker }
            List {
                ForEach(visibleThreads) { thread in
                    Button { openThread(thread.id) } label: {
                        ThreadRow(title: thread.title, pinned: thread.pinned, running: thread.running)
                    }
                    .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                        if model.connected {
                            Button(thread.archived ? "恢复" : "归档", systemImage: "archivebox") { archiveTarget = thread }
                            Button(thread.pinned ? "取消置顶" : "置顶", systemImage: "pin") { act { try await model.pin(thread) } }.tint(.orange)
                        }
                    }
                    .contextMenu {
                        Group {
                            Button("重命名", systemImage: "pencil") { renameID = thread.id; renameTitle = thread.title; renaming = true }
                            Button(thread.pinned ? "取消置顶" : "置顶", systemImage: thread.pinned ? "pin.slash" : "pin") { act { try await model.pin(thread) } }
                            Button(thread.archived ? "恢复" : "归档", systemImage: thread.archived ? "tray.and.arrow.up" : "archivebox") { archiveTarget = thread }
                        }.disabled(!model.connected)
                    }
                }
                ForEach(visibleEntries) { entry in
                    Button { openThread(entry.id) } label: {
                        ThreadRow(title: entry.title, pinned: false, running: false)
                    }
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .scrollDismissesKeyboard(.interactively)
            .overlay { if listEmpty { emptyList } }
            .refreshable {
                do { try await model.syncHistory(); try await model.loadThreads() }
                catch is CancellationError {} catch { model.error = error.localizedDescription }
            }
        }
        .animation(chromeAnimation(reduceMotion), value: searching)
        .animation(chromeAnimation(reduceMotion), value: model.connected || model.connecting)
        .background(Color(uiColor: .systemBackground))
        .safeAreaInset(edge: .bottom) {
            if !emptyListStarts && !model.archivedList {
                Button { searchFocused = false; newConversation = true } label: {
                    Label("新会话", systemImage: "square.and.pencil").padding(.horizontal, 8).frame(minHeight: 44)
                }
                .mobilePrimaryAction().buttonBorderShape(.capsule).controlSize(.large)
                .disabled(!model.connected)
                .padding(.bottom, 8)
            }
        }
        .task(id: model.search) {
            do { try await Task.sleep(for: .milliseconds(250)); try await model.loadThreads() }
            catch is CancellationError {} catch { model.error = error.localizedDescription }
        }
        .onChange(of: model.workspace) { _, _ in model.perform { try await model.loadThreads() } }
    }
    private var listHeader: some View {
        HStack(spacing: 8) {
            MobileCircleButton(symbol: "chevron.left", label: "切换电脑") {
                searchFocused = false
                Task { await model.leaveHost(); model.foreground() }
            }
            VStack(spacing: 1) {
                Text(model.archivedList ? "已归档会话" : hostName).font(.headline).lineLimit(1)
                Text(model.archivedList ? hostName : model.connected ? "已连接" : model.connecting ? "正在连接…" : "未连接")
                    .font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .frame(maxWidth: .infinity)
            .accessibilityElement(children: .combine).accessibilityAddTraits(.isHeader)
            MobileCircleButton(symbol: searching ? "xmark" : "magnifyingglass", label: searching ? "关闭搜索" : "搜索会话") {
                searching.toggle()
                if !searching { model.search = ""; searchFocused = false }
                else { searchFocused = true }
            }
            Menu {
                Button(model.archivedList ? "返回会话列表" : "已归档会话", systemImage: model.archivedList ? "bubble.left.and.bubble.right" : "archivebox") {
                    model.archivedList.toggle()
                    model.search = ""
                    model.perform { try await model.loadThreads() }
                }.disabled(!model.connected)
                if accountHistory {
                    if model.historyEnabled {
                        Button("关闭并删除服务器历史", systemImage: "icloud.slash", role: .destructive) { disableHistory = true }
                    } else {
                        Button("开启服务器历史同步", systemImage: "icloud") { consent = true }
                    }
                }
                if model.account != nil {
                    Divider()
                    Button("账号设置", systemImage: "gearshape") { accountSettings = true }
                }
            } label: {
                Image(systemName: "ellipsis").font(.system(size: 19))
                    .frame(width: 44, height: 44)
                    .background(Color(uiColor: .systemBackground), in: Circle())
                    .overlay(Circle().stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
            }.accessibilityLabel("会话列表选项")
        }
        .padding(.horizontal, 16).padding(.vertical, 8)
        .dynamicTypeSize(...DynamicTypeSize.accessibility2)
    }
    private var searchField: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField(model.connected ? "搜索全部会话内容" : "搜索会话标题", text: $model.search)
                .textInputAutocapitalization(.never).autocorrectionDisabled().submitLabel(.search)
                .accessibilityLabel("搜索会话")
                .focused($searchFocused).onSubmit { searchFocused = false }
            if !model.search.isEmpty {
                Button { model.search = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary) }
                    .frame(minWidth: 44, minHeight: 44).accessibilityLabel("清除搜索")
            }
        }
        .padding(.horizontal, 12).frame(minHeight: 44)
        .background(Color(uiColor: .tertiarySystemFill), in: RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal, 16).padding(.bottom, 8)
    }
    private var folderPicker: some View {
        Menu {
            Picker("文件夹", selection: $model.workspace) {
                Text("所有文件夹").tag("")
                ForEach(folderOptions, id: \.self) { path in Text(folderName(path)).tag(path) }
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "folder")
                Text(model.workspace.isEmpty ? "所有文件夹" : folderName(model.workspace)).lineLimit(1).truncationMode(.middle)
                Image(systemName: "chevron.down").font(.caption2.weight(.semibold))
                Spacer(minLength: 0)
            }
            .font(.subheadline).foregroundStyle(.secondary)
            .frame(minHeight: 44).contentShape(Rectangle())
        }
        .padding(.horizontal, 20)
        .accessibilityLabel("文件夹")
        .accessibilityValue(model.workspace.isEmpty ? "所有文件夹" : model.workspace)
    }
    @ViewBuilder private var emptyList: some View {
        if !query.isEmpty {
            ContentUnavailableView.search(text: query)
        } else if model.connected {
            if model.archivedList {
                ContentUnavailableView("没有已归档的会话", systemImage: "archivebox")
            } else {
                ContentUnavailableView {
                    Label("还没有会话", systemImage: "bubble.left.and.bubble.right")
                } description: {
                    Text(model.workspace.isEmpty ? "选择文件夹，在这台电脑上开始第一个会话。" : "在「\(folderName(model.workspace))」或其他文件夹中开始会话。")
                } actions: {
                    Button("新会话") { newConversation = true }.mobilePrimaryAction()
                }
            }
        } else if model.connecting {
            ProgressView("正在连接电脑…")
        } else {
            ContentUnavailableView("电脑未连接", systemImage: "desktopcomputer", description: Text("确认电脑已开机，并且 Wuu 正在运行。"))
        }
    }
    private func openThread(_ id: String) {
        searchFocused = false
        conversationPresented = true
        model.perform { try await model.open(id) }
    }
}

private struct ThreadRow: View {
    let title: String
    let pinned: Bool
    let running: Bool
    var body: some View {
        HStack(spacing: 10) {
            Text(title.isEmpty ? "新会话" : title).foregroundStyle(.primary).lineLimit(2)
                .frame(maxWidth: .infinity, alignment: .leading)
            if running {
                ProgressView().controlSize(.small).accessibilityLabel("正在运行")
            } else if pinned {
                Image(systemName: "pin.fill").font(.caption).foregroundStyle(.secondary).accessibilityLabel("已置顶")
            }
        }
        .padding(.vertical, 8).contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}
