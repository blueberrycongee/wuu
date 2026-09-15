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
                    if url.scheme == "wuu", url.host == "account", url.path == "/github" { model.foreground() }
                }
                .onChange(of: phase) { _, value in
                    if value == .active { model.foreground() }
                    else if value == .background { Task { await model.background() } }
                }
        }
    }
}

struct RootView: View {
    @Bindable var model: AppModel
    @Environment(\.colorScheme) private var scheme
    var body: some View {
        Group {
            if model.recovery != nil { RecoveryView(model: model) }
            else if model.account == nil { LoginView(model: model) }
            else if model.host == nil { DevicesView(model: model) }
            else { HostView(model: model).id(model.host?.pub) }
        }
        // A semantic SwiftUI primary tint can feed back into UIKit trait resolution.
        .tint(scheme == .dark ? Color.white : Color.black)
        .alert("提示", isPresented: Binding(get: { model.error != nil }, set: { if !$0 { model.error = nil } })) {
            Button("知道了", role: .cancel) { model.error = nil }
        } message: { Text(model.error ?? "") }
    }
}

struct LoginView: View {
    @Bindable var model: AppModel
    @AppStorage("server") private var server = ""
    @State private var settings = false
    @State private var passwordLogin = false
    @State private var username = ""
    @State private var password = ""
    @State private var register = false
    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 24) {
                Spacer()
                Text("wuu").font(.system(size: 100, weight: .black, design: .rounded)).accessibilityHidden(true)
                Text("连接你的电脑").font(.largeTitle.bold())
                Text("随时继续对话，让电脑为你工作。").foregroundStyle(.secondary)
                Spacer()
                if model.githubURL != nil {
                    ProgressView("在浏览器完成 GitHub 登录后返回")
                    Button("重新打开浏览器") { if let url = model.githubURL { UIApplication.shared.open(url) } }
                    Button("取消登录") { model.perform { try model.cancelLogin() } }
                } else {
                    if model.configuration?.github == true {
                    Button { model.perform { try await model.github(server: server) } } label: {
                        Text("使用 GitHub 继续").frame(maxWidth: .infinity).padding(10)
                    }.buttonStyle(.borderedProminent).disabled(server.isEmpty || model.busy)
                    }
                    Button(model.configuration?.github == true ? "更多登录方式" : "密码登录") { passwordLogin = true }.frame(maxWidth: .infinity).disabled(server.isEmpty || model.busy)
                    if model.configuration == nil, !server.isEmpty {
                        Button("重新读取服务器设置") { Task { await model.loadConfiguration(server: server) } }.font(.footnote)
                    }
                }
                Button { settings = true } label: {
                    Label(server.isEmpty ? "设置连接服务器" : "连接设置", systemImage: "slider.horizontal.3")
                        .font(.footnote).frame(maxWidth: .infinity)
                }
            }.padding(28).padding(.bottom, 24)
                .navigationTitle("连接").navigationBarTitleDisplayMode(.inline)
                .task(id: settings ? "" : server) { if !settings { await model.loadConfiguration(server: server) } }
                .sheet(isPresented: $settings) {
                    NavigationStack {
                        Form {
                            Section("服务器") {
                                TextField("https://你的服务器", text: $server).textInputAutocapitalization(.never)
                                    .autocorrectionDisabled().keyboardType(.URL)
                                Text("使用与你的电脑相同的 Wuu 服务器。").font(.footnote)
                            }
                        }.navigationTitle("连接设置").toolbar {
                            Button("完成") {
                                do { try model.cancelLogin(); server = try AccountAPI.validateOrigin(server).absoluteString; register = false; settings = false }
                                catch { model.error = error.localizedDescription }
                            }
                        }
                    }
                }
                .sheet(isPresented: $passwordLogin, onDismiss: {
                    password = ""
                    if model.account == nil { model.perform { try model.cancelLogin() } }
                }) {
                    NavigationStack {
                        Form {
                            TextField("用户名", text: $username).textContentType(.username).textInputAutocapitalization(.never).autocorrectionDisabled()
                            SecureField("密码", text: $password).textContentType(register ? .newPassword : .password)
                            Button(register ? "注册并登录" : "登录") {
                                model.perform {
                                    try await model.login(server: server, username: username, password: password, register: register)
                                    password = ""; passwordLogin = false
                                }
                            }.disabled(model.busy || username.isEmpty || password.isEmpty)
                            if model.configuration?.registration == true {
                                Button(register ? "已有账号，登录" : "注册账号") { register.toggle() }
                            }
                            NavigationLink("忘记密码？使用恢复密钥") { PasswordResetView(model: model, server: server, changing: false) }
                        }.navigationTitle(register ? "注册账号" : "密码登录").toolbar {
                            Button("取消") { model.perform { try model.cancelLogin(); passwordLogin = false } }.disabled(model.resettingPassword)
                        }
                    }
                    .interactiveDismissDisabled(model.resettingPassword)
                }
        }
    }
}

struct DevicesView: View {
    @Bindable var model: AppModel
    @State private var settings = false
    var body: some View {
        NavigationStack {
            List {
                ForEach([true, false], id: \.self) { online in
                    let group = model.devices.filter { $0.role == "host" && $0.online == online }
                    if !group.isEmpty {
                        Section {
                            ForEach(group) { device in
                                Button { model.perform { try await model.selectHost(device) } } label: {
                                    HStack(spacing: 16) {
                                        Image(systemName: "desktopcomputer").font(.title2)
                                        Text(device.name.isEmpty ? "电脑" : device.name)
                                        Spacer()
                                        Circle().fill(online ? Color.green : Color.secondary).frame(width: 7, height: 7)
                                        Image(systemName: "chevron.right").foregroundStyle(.tertiary)
                                    }.padding(.vertical, 14)
                                }
                            }
                        }
                    }
                }
                if !model.devices.contains(where: { $0.role == "host" }) {
                    ContentUnavailableView("还没有电脑", systemImage: "desktopcomputer", description: Text("在电脑上的 Wuu 登录同一账号。"))
                }
            }.listStyle(.plain).navigationTitle("你的电脑").navigationBarTitleDisplayMode(.inline)
                .refreshable { model.perform { try await model.loadDevices() } }
                .toolbar { Button { settings = true } label: { Image(systemName: "gearshape") }.accessibilityLabel("账号设置") }
                .sheet(isPresented: $settings) { AccountSettingsView(model: model) }
        }
    }
}

struct ConversationView: View {
    @Bindable var model: AppModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var conversationPresented = false
    @State private var searching = false
    @FocusState private var searchFocused: Bool
    @State private var drafts: [String: String] = [:]
    @State private var attachmentDrafts: [String: [InputAttachment]] = [:]
    @State private var consent = false
    @State private var disableHistory = false
    @State private var archiveTarget: ChatThread?
    @State private var renaming = false
    @State private var renameID = ""
    @State private var renameTitle = ""
    @State private var exporting = false
    @State private var exportDocument = ConversationDocument(text: "")
    @State private var settingsThread: ChatThread?
    init(model: AppModel) {
        self.model = model
        _conversationPresented = State(initialValue: model.activeID != nil)
    }
    private var draftKey: String { (model.host?.pub ?? "") + ":" + (model.activeID ?? "new") }
    private var draft: Binding<String> {
        Binding(get: { drafts[draftKey] ?? "" }, set: { drafts[draftKey] = $0 })
    }
    private var attachments: Binding<[InputAttachment]> {
        let key = draftKey
        return Binding(get: { attachmentDrafts[key] ?? [] }, set: { attachmentDrafts[key] = $0 })
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
        .toolbar(conversationPresented || searching ? .hidden : .visible, for: .tabBar)
        .onChange(of: conversationPresented) { _, presented in
            if !presented { searching = !model.search.isEmpty }
        }
        .animation(reduceMotion ? nil : .easeOut(duration: 0.2), value: searching)
        .confirmationDialog("同步对话到服务器？", isPresented: $consent, titleVisibility: .visible) {
            Button("开启文字历史同步") { model.perform { try await model.setHistory(true) } }
        } message: { Text("服务器将保存可读取的用户消息和助手回复，电脑离线时仍可查看。不包含附件或工具输出。关闭后删除服务器副本。") }
        .confirmationDialog("关闭并删除服务器历史？", isPresented: $disableHistory, titleVisibility: .visible) {
            Button("关闭并删除", role: .destructive) { model.perform { try await model.setHistory(false) } }
        } message: { Text("此电脑已同步的服务器副本将被删除，电脑上的原始会话仍然保留。") }
        .confirmationDialog(archiveTarget?.archived == true ? "恢复会话？" : "归档会话？", isPresented: Binding(get: { archiveTarget != nil }, set: { if !$0 { archiveTarget = nil } }), titleVisibility: .visible) {
            Button(archiveTarget?.archived == true ? "恢复" : "归档") { if let thread = archiveTarget { model.perform { try await model.archive(thread) } }; archiveTarget = nil }
        }
        .alert("重命名会话", isPresented: $renaming) {
            TextField("标题", text: $renameTitle)
            Button("保存") { let id = renameID, title = renameTitle; model.perform { try await model.rename(id, title: title) } }
            Button("取消", role: .cancel) {}
        }
        .fileExporter(isPresented: $exporting, document: exportDocument, contentType: .plainText, defaultFilename: "conversation.txt") { result in
            if case .failure(let error) = result { model.error = error.localizedDescription }
        }
        .sheet(item: $settingsThread) { thread in ThreadSettingsView(model: model, thread: thread) }
    }
    private var conversationTitle: String {
        let title = model.live?.title ?? model.saved?.title ?? ""
        return title.isEmpty ? "新会话" : title
    }
    private var conversation: some View {
        VStack(spacing: 0) {
            if !model.connected {
                ConnectionStatusView(connecting: model.connecting, offlineMessage: "电脑未连接 · 历史记录只读", detail: model.connectionStatus) {
                    Task { await model.connect() }
                }
            }
            if model.activeID == nil {
                ContentUnavailableView("继续你的对话", systemImage: "bubble.left.and.bubble.right", description: Text("打开会话列表，或新建会话。"))
            } else {
                ConversationTimeline(model: model).id(model.activeID)
            }
            if let request = model.pendingApproval {
                VStack(alignment: .leading, spacing: 8) {
                    Text("电脑需要处理请求：" + (request["method"].string ?? "")).font(.caption)
                    Button("拒绝并返回对话") { model.perform { try await model.rejectRequest() } }
                }.padding()
            }
            if let request = model.questions.first(where: { $0["thread_id"].string == model.activeID }) {
                QuestionView(model: model, request: request).id(request["request_id"].string)
            }
            MobileComposer(text: draft, attachments: attachments, model: model,
                enabled: model.connected && model.live != nil && model.live?.readOnly != true && model.live?.archived != true,
                sending: model.sending, placeholder: model.live?.running == true ? "添加后续消息" : "发送消息", identifier: "harness-composer",
                stop: model.live?.running == true ? { model.perform { try await model.stop() } } : nil) {
                    let key = draftKey
                    let original = drafts[key] ?? ""
                    let text = original.trimmingCharacters(in: .whitespacesAndNewlines)
                    let files = attachmentDrafts[key] ?? []
                    model.perform {
                        try await model.send(text, attachments: files)
                        if drafts[key] == original { drafts[key] = "" }
                        attachmentDrafts[key]?.removeAll { file in files.contains { $0.id == file.id } }
                    }
            }
        }.navigationTitle(conversationTitle).navigationBarTitleDisplayMode(.inline)
            .toolbarBackground(Color(uiColor: .systemBackground), for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) { Button { model.perform { try await model.startThread() } } label: { Image(systemName: "square.and.pencil") }.disabled(!model.connected).accessibilityLabel("新会话") }
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button("会话设置", systemImage: "slider.horizontal.3") { settingsThread = model.live }
                            .disabled(!model.connected || model.live == nil)
                        Button("重命名") {
                            renameID = model.activeID ?? ""; renameTitle = model.live?.title ?? ""; renaming = true
                        }.disabled(!model.connected || model.live == nil)
                        Button(model.live?.historyCursor.isEmpty == false || model.messages.contains(where: { !$0.contentRef.isEmpty }) ? "导出已加载文本" : "导出对话文本", systemImage: "square.and.arrow.up") {
                            exportDocument = ConversationDocument(text: model.messages.filter { $0.tool == nil }.map { "## \($0.role)\n\n\($0.text)" }.joined(separator: "\n\n"))
                            exporting = true
                        }
                    } label: { Image(systemName: "ellipsis.circle") }.accessibilityLabel("会话操作")
                }
            }
    }
    private var conversationList: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                MobileCircleButton(symbol: searching ? "xmark" : "magnifyingglass", label: searching ? "关闭搜索" : "搜索会话") {
                    searching.toggle()
                    if !searching { model.search = ""; searchFocused = false }
                    else { searchFocused = true }
                }
                Text(model.archivedList && model.connected ? "已归档" : "会话")
                    .font(.headline).lineLimit(1).frame(maxWidth: .infinity)
                    .accessibilityAddTraits(.isHeader)
                MobileCircleButton(symbol: "square.and.pencil", label: "新会话") {
                    searchFocused = false
                    model.perform { try await model.startThread(); conversationPresented = true }
                }.disabled(!model.connected)
                Menu {
                    if !model.connected {
                        Button("重新连接", systemImage: "arrow.clockwise") { Task { await model.connect() } }.disabled(model.connecting)
                    }
                    Button(model.archivedList ? "返回会话列表" : "已归档会话", systemImage: model.archivedList ? "bubble.left.and.bubble.right" : "archivebox") {
                        model.archivedList.toggle()
                        model.search = ""
                        model.perform { try await model.loadThreads() }
                    }.disabled(!model.connected)
                    if model.historyEnabled {
                        Button("关闭并删除服务器历史", systemImage: "icloud.slash", role: .destructive) { disableHistory = true }
                    } else {
                        Button("开启服务器历史同步", systemImage: "icloud") { consent = true }
                    }
                } label: {
                    Image(systemName: "ellipsis").font(.system(size: 19))
                        .frame(width: 44, height: 44)
                        .background(Color(uiColor: .systemBackground), in: Circle())
                        .overlay(Circle().stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
                }.accessibilityLabel("会话列表选项")
            }.padding(.horizontal, 16).padding(.vertical, 8)
            if searching {
                HStack(spacing: 8) {
                    Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                    TextField(model.connected ? "搜索全部会话内容" : "搜索历史标题", text: $model.search)
                        .textInputAutocapitalization(.never).autocorrectionDisabled().submitLabel(.search)
                        .accessibilityLabel("搜索会话")
                        .focused($searchFocused).onSubmit { searchFocused = false }
                    if !model.search.isEmpty {
                        Button { model.search = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary) }
                            .accessibilityLabel("清除搜索")
                    }
                }.padding(12).background(Color(uiColor: .tertiarySystemFill), in: RoundedRectangle(cornerRadius: 12))
                    .padding(.horizontal, 16).padding(.bottom, 8)
            }
            List {
                Section {
                    Button { model.perform { await model.leaveHost(); model.foreground() } } label: {
                        Label {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(model.host?.name ?? "电脑").lineLimit(2)
                                if !model.connected {
                                    Text(model.connecting ? "正在连接…" : "电脑离线").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                        } icon: { Image(systemName: "desktopcomputer") }
                    }.accessibilityHint("切换电脑")
                    if !model.workspaces.isEmpty {
                        Menu {
                            Picker("工作区", selection: $model.workspace) {
                                Text("所有工作区").tag("")
                                ForEach(model.workspaces, id: \.selfDescription) { item in
                                    let path = item["path"].string ?? ""
                                    Text(URL(fileURLWithPath: path).lastPathComponent).tag(path)
                                }
                            }
                        } label: {
                            HStack {
                                Label(model.workspace.isEmpty ? "所有工作区" : URL(fileURLWithPath: model.workspace).lastPathComponent,
                                      systemImage: "folder")
                                    .lineLimit(1).truncationMode(.middle)
                                Spacer(minLength: 8)
                                Image(systemName: "chevron.down").font(.caption).foregroundStyle(.secondary)
                            }
                        }.accessibilityLabel("工作区")
                            .accessibilityValue(model.workspace.isEmpty ? "所有工作区" : URL(fileURLWithPath: model.workspace).lastPathComponent)
                            .onChange(of: model.workspace) { _, _ in model.perform { try await model.loadThreads() } }
                    }
                }
                Section {
                    ForEach((model.connected ? model.threads : []).sorted { $0.pinned != $1.pinned ? $0.pinned : $0.updatedAt > $1.updatedAt }) { thread in
                        Button { conversationPresented = true; searchFocused = false; model.perform { try await model.open(thread.id) } } label: {
                            HStack { if thread.pinned { Image(systemName: "pin.fill").font(.caption) }; Text(thread.title.isEmpty ? "新会话" : thread.title).lineLimit(2); Spacer(); if thread.running { ProgressView() } }.padding(.vertical, 8)
                        }.swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            Button(thread.archived ? "恢复" : "归档", systemImage: "archivebox") { archiveTarget = thread }
                            Button(thread.pinned ? "取消置顶" : "置顶", systemImage: "pin") { model.perform { try await model.pin(thread) } }.tint(.orange)
                        }
                    }
                    ForEach(model.connected ? [] : model.entries.filter { model.search.isEmpty || $0.title.localizedCaseInsensitiveContains(model.search) }) { entry in
                        Button { conversationPresented = true; searchFocused = false; model.perform { try await model.open(entry.id) } } label: { Label(entry.title.isEmpty ? "新会话" : entry.title, systemImage: "clock").padding(.vertical, 8) }
                    }
                }
            }
            .scrollContentBackground(.hidden)
            .contentMargins(.top, 8, for: .scrollContent)
            .listSectionSpacing(16)
            .scrollDismissesKeyboard(.interactively)
            .refreshable { model.perform { try await model.syncHistory(); try await model.loadThreads() } }
        }
        .background(Color(uiColor: .systemGroupedBackground))
        .task(id: model.search) {
            do { try await Task.sleep(for: .milliseconds(250)); try await model.loadThreads() }
            catch is CancellationError {} catch { model.error = error.localizedDescription }
        }
    }
}

private extension JSONValue {
    var selfDescription: String { self["path"].string ?? "" }
}
