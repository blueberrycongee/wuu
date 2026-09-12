import SwiftUI
import WuuCore

@main struct WuuApp: App {
    @UIApplicationDelegateAdaptor(PushDelegate.self) private var pushDelegate
    @State private var model = AppModel()
    @Environment(\.scenePhase) private var phase
    var body: some Scene {
        WindowGroup {
            RootView(model: model)
                .tint(Color.primary)
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
    var body: some View {
        Group {
            if model.recovery != nil { RecoveryView(model: model) }
            else if model.account == nil { LoginView(model: model) }
            else if model.host == nil { DevicesView(model: model) }
            else { ConversationView(model: model) }
        }
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
    @State private var revokeTarget: AccountDevice?
    var body: some View {
        NavigationStack {
            List {
                if model.directoryCached { Text("显示上次登录保存的电脑；联网后会更新访问权限和在线状态。").font(.footnote).foregroundStyle(.secondary) }
                ForEach([true, false], id: \.self) { online in
                    let group = model.devices.filter { $0.role == "host" && $0.online == online }
                    if !group.isEmpty {
                        Section(online ? "可连接" : "离线 · 可查看已同步记录") {
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
            }.navigationTitle("你的电脑")
                .refreshable { model.perform { try await model.loadDevices() } }
                .toolbar { Button { settings = true } label: { Image(systemName: "gearshape") }.accessibilityLabel("账号设置") }
                .sheet(isPresented: $settings) {
                    NavigationStack {
                        Form {
                            Section("账号") { Text(model.account?.username ?? ""); Text(model.account?.server ?? "").font(.footnote) }
                            if model.authMethod == "password" {
                                NavigationLink("修改密码") { PasswordResetView(model: model, server: model.account?.server ?? "", changing: true) }
                            }
                            Section("设备") {
                                ForEach(model.devices) { device in
                                    HStack {
                                        Text(device.name)
                                        Spacer()
                                        Button("移除", role: .destructive) { revokeTarget = device }
                                    }
                                }
                            }
                            Button("退出此手机登录", role: .destructive) { model.perform { try await model.logout(); settings = false } }.disabled(model.busy)
                            NavigationLink("开源许可") { LicensesView() }
                            NavigationLink("通知") { PushSettingsView(push: model.push) }
                        }.navigationTitle("账号设置").toolbar { Button("完成") { settings = false } }
                            .confirmationDialog("移除这台设备的账号访问权限？", isPresented: Binding(get: { revokeTarget != nil }, set: { if !$0 { revokeTarget = nil } }), titleVisibility: .visible) {
                                Button("移除设备", role: .destructive) {
                                    if let device = revokeTarget { model.perform { try await model.revoke(device) } }
                                    revokeTarget = nil
                                }
                            }
                    }
                }
        }
    }
}

struct ConversationView: View {
    @Bindable var model: AppModel
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var drawer = true
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
            VStack(spacing: 0) {
                if !model.connected {
                    HStack {
                        Text(model.connecting ? "正在连接电脑…" : "电脑未连接 · 历史记录只读")
                            .accessibilityHint(model.connectionStatus)
                        Spacer()
                        Button("重连") { Task { await model.connect() } }.disabled(model.connecting)
                    }.font(.caption).padding(12).background(.secondary.opacity(0.08))
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
                if !attachments.wrappedValue.isEmpty {
                    ScrollView(.horizontal) {
                        HStack {
                            ForEach(attachments.wrappedValue) { attachment in
                                Button { attachmentDrafts[draftKey]?.removeAll { $0.id == attachment.id } } label: {
                                    Label(attachment.filename, systemImage: "xmark.circle").font(.caption).lineLimit(1)
                                }.accessibilityLabel("移除附件 " + attachment.filename)
                            }
                        }.padding(.horizontal, 12)
                    }.disabled(model.sending)
                }
                HStack(alignment: .bottom, spacing: 12) {
                    AttachmentPicker(attachments: attachments, model: model).id(draftKey)
                        .disabled(!model.connected || model.live == nil || model.sending || model.live?.readOnly == true)
                    TextField(model.connected ? "发送消息" : "连接电脑后发送", text: draft, axis: .vertical)
                        .lineLimit(1...6).padding(12).background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 18))
                    if model.live?.running == true {
                        Button { model.perform { try await model.stop() } } label: { Image(systemName: "stop.circle.fill").font(.title) }.accessibilityLabel("停止")
                    }
                    Button {
                        let key = draftKey
                        let original = drafts[key] ?? ""
                        let text = original.trimmingCharacters(in: .whitespacesAndNewlines)
                        let files = attachmentDrafts[key] ?? []
                        model.perform {
                            try await model.send(text, attachments: files)
                            if drafts[key] == original { drafts[key] = "" }
                            attachmentDrafts[key]?.removeAll { file in files.contains { $0.id == file.id } }
                        }
                    } label: { Image(systemName: "arrow.up.circle.fill").font(.title) }
                        .accessibilityLabel("发送").disabled(!model.connected || model.live == nil || model.sending || model.live?.readOnly == true || model.live?.archived == true || (draft.wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && attachments.wrappedValue.isEmpty))
                }.padding(12)
            }.navigationTitle(model.live?.title ?? model.saved?.title ?? "Wuu").navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) { Button { drawer.toggle() } label: { Image(systemName: "sidebar.left") }.accessibilityLabel(drawer ? "关闭会话列表" : "会话列表") }
                    ToolbarItem(placement: .topBarTrailing) { Button { model.perform { try await model.startThread(); drawer = false } } label: { Image(systemName: "square.and.pencil") }.disabled(!model.connected).accessibilityLabel("新会话") }
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
                .overlay(alignment: .leading) {
                    if drawer {
                        ZStack(alignment: .leading) {
                            Color.black.opacity(0.3).ignoresSafeArea()
                                .onTapGesture { drawer = false }.accessibilityLabel("关闭会话列表")
                            conversationList.frame(width: 310)
                                .background(.background)
                                .transition(.move(edge: .leading))
                        }.accessibilityAddTraits(.isModal)
                    }
                }
                .animation(reduceMotion ? nil : .easeOut(duration: 0.2), value: drawer)
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
    }
    private var conversationList: some View {
        NavigationStack {
            List {
                Section {
                    Button { model.perform { await model.leaveHost(); model.foreground(); drawer = false } } label: { Label(model.host?.name ?? "电脑", systemImage: "desktopcomputer") }
                    if !model.workspaces.isEmpty {
                        Picker("工作区", selection: $model.workspace) {
                            Text("所有工作区").tag("")
                            ForEach(model.workspaces, id: \.selfDescription) { item in Text(item["path"].string ?? "").tag(item["path"].string ?? "") }
                        }.onChange(of: model.workspace) { _, _ in model.perform { try await model.loadThreads() } }
                    }
                    Button { model.perform { try await model.startThread(); drawer = false } } label: { Label("新会话", systemImage: "plus") }.disabled(!model.connected)
                    Toggle("已归档", isOn: $model.archivedList).disabled(!model.connected)
                        .onChange(of: model.archivedList) { _, _ in model.perform { try await model.loadThreads() } }
                }
                Section("会话") {
                    ForEach((model.connected ? model.threads : []).sorted { $0.pinned != $1.pinned ? $0.pinned : $0.updatedAt > $1.updatedAt }) { thread in
                        Button { drawer = false; model.perform { try await model.open(thread.id) } } label: {
                            HStack { if thread.pinned { Image(systemName: "pin.fill").font(.caption) }; Text(thread.title).lineLimit(2); Spacer(); if thread.running { ProgressView() } }.padding(.vertical, 8)
                        }.swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            Button(thread.archived ? "恢复" : "归档", systemImage: "archivebox") { archiveTarget = thread }
                            Button(thread.pinned ? "取消置顶" : "置顶", systemImage: "pin") { model.perform { try await model.pin(thread) } }.tint(.orange)
                        }
                    }
                    ForEach(model.connected ? [] : model.entries.filter { model.search.isEmpty || $0.title.localizedCaseInsensitiveContains(model.search) }) { entry in
                        Button { drawer = false; model.perform { try await model.open(entry.id) } } label: { Label(entry.title, systemImage: "clock").padding(.vertical, 8) }
                    }
                }
                Section {
                    if model.historyEnabled { Button("关闭并删除服务器历史", role: .destructive) { disableHistory = true } }
                    else { Button("开启服务器历史同步") { consent = true } }
                    Text("发送消息需要电脑在线。历史记录会在前台自动同步。").font(.footnote).foregroundStyle(.secondary)
                }
            }.searchable(text: $model.search, prompt: model.connected ? "搜索全部会话内容" : "搜索历史标题")
                .task(id: model.search) {
                    do { try await Task.sleep(for: .milliseconds(250)); try await model.loadThreads() }
                    catch is CancellationError {} catch { model.error = error.localizedDescription }
                }
                .navigationTitle("会话").navigationBarTitleDisplayMode(.inline).toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button { drawer = false } label: { Image(systemName: "sidebar.left") }.accessibilityLabel("关闭会话列表")
                }
            }
                .refreshable { model.perform { try await model.syncHistory(); try await model.loadThreads() } }
        }
    }
}

private extension JSONValue {
    var selfDescription: String { self["path"].string ?? "" }
}
