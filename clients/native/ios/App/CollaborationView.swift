import SwiftUI
import WuuCore

struct HostView: View {
    @Bindable var model: AppModel
    @State private var mode = "harness"
    @Environment(\.scenePhase) private var phase
    private var pollKey: String { "\(mode)-\(phase == .active)-\(model.connected)-\(model.collaboration.roomID ?? "")" }
    var body: some View {
        TabView(selection: $mode) {
            ConversationView(model: model)
                .tabItem { Label("会话", systemImage: "terminal") }.tag("harness")
            CollaborationView(app: model, model: model.collaboration)
                .tabItem { Label("协作", systemImage: "person.2") }.tag("collaboration")
        }
        .task(id: pollKey) {
            if mode == "collaboration", phase == .active, model.connected { await model.collaboration.poll(app: model) }
        }
    }
}

struct KeyboardDismissToolbar: ViewModifier {
    let dismiss: () -> Void
    func body(content: Content) -> some View {
        content.toolbar {
            ToolbarItemGroup(placement: .keyboard) {
                Spacer()
                Button("收起键盘", action: dismiss)
            }
        }
    }
}

struct CollaborationView: View {
    @Bindable var app: AppModel
    @Bindable var model: CollaborationModel
    @State private var compose = false
    @State private var details = false
    @State private var query = ""
    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                if !app.connected {
                    HStack {
                        Text(app.connecting ? "正在连接电脑…" : "电脑未连接 · 已加载消息只读")
                        Spacer()
                        Button("重连") { Task { await app.connect() } }.disabled(app.connecting)
                    }.font(.caption).padding(12).background(.secondary.opacity(0.08))
                }
                if let error = model.error {
                    Text(error).font(.caption).foregroundStyle(.red).frame(maxWidth: .infinity, alignment: .leading).padding(12)
                }
                if let room = model.room {
                    CollaborationRoomView(app: app, model: model, room: room).id(room.id)
                } else {
                    directory
                }
            }
            .navigationTitle(model.room?.title(agents: model.agents) ?? "协作")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    if model.roomID != nil {
                        Button { model.select(nil) } label: { Label("协作列表", systemImage: "chevron.left") }
                    } else {
                        Button("电脑") { Task { await app.leaveHost(); app.foreground() } }
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    if model.roomID != nil {
                        Button { details = true } label: { Image(systemName: "person.2") }.accessibilityLabel("成员与任务")
                    } else {
                        Button { compose = true } label: { Image(systemName: "square.and.pencil") }
                            .accessibilityLabel("新协作对话").disabled(!app.connected)
                    }
                }
            }
            .sheet(isPresented: $compose) { NewCollaborationView(app: app, model: model) }
            .sheet(isPresented: $details) { roomDetails }
        }
    }

    private var directory: some View {
        List {
            if model.loading { ProgressView("正在读取协作") }
            if model.rooms.isEmpty, !model.loading {
                ContentUnavailableView("还没有协作对话", systemImage: "person.2", description: Text(model.agents.isEmpty ? "在电脑上创建 Agent 后，即可在这里开始对话。" : "点击右上角开始与 Agent 对话。"))
            }
            ForEach(model.rooms.filter { query.isEmpty || $0.title(agents: model.agents).localizedCaseInsensitiveContains(query) }) { room in
                Button { model.select(room.id) } label: {
                    HStack(spacing: 12) {
                        Image(systemName: room.value["kind"].string == "dm" ? "person.crop.circle" : "person.2.circle").font(.title2).accessibilityHidden(true)
                        VStack(alignment: .leading, spacing: 4) {
                            Text(room.title(agents: model.agents)).font(.headline).lineLimit(1)
                            Text(room.value["last_message"]["body"].string ?? "开始对话").font(.subheadline).foregroundStyle(.secondary).lineLimit(2)
                        }
                        Spacer(minLength: 4)
                        if room.unread > 0 { Text("\(room.unread)").font(.caption.bold()).padding(6).background(.secondary.opacity(0.12), in: Capsule()).accessibilityLabel("\(room.unread) 条未读") }
                    }.padding(.vertical, 4)
                }.foregroundStyle(.primary)
            }
        }.listStyle(.plain).searchable(text: $query, prompt: "搜索协作对话")
    }

    private var roomDetails: some View {
        NavigationStack {
            List {
                Section("成员") {
                    ForEach(Array((model.room?.members ?? []).enumerated()), id: \.offset) { _, member in
                        let id = member["member_id"].string ?? ""
                        let agent = model.agents.first { $0.id == id }
                        VStack(alignment: .leading, spacing: 4) {
                            Text(member["member_type"].string == "human" ? "你" : agent?.name ?? id)
                            if let role = agent?.role, !role.isEmpty { Text(role).font(.caption).foregroundStyle(.secondary) }
                        }
                    }
                }
                Section("已加载的任务") {
                    let tasks = model.timeline.messages.filter { !$0.taskTitle.isEmpty }
                    if tasks.isEmpty { Text("暂无任务").foregroundStyle(.secondary) }
                    ForEach(tasks) { task in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(task.taskTitle)
                            Text(collaborationStatus(task.taskState)).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }.navigationTitle("成员与任务").navigationBarTitleDisplayMode(.inline)
                .toolbar { Button("完成") { details = false } }
        }
    }
}

private struct NewCollaborationView: View {
    @Bindable var app: AppModel
    @Bindable var model: CollaborationModel
    @Environment(\.dismiss) private var dismiss
    @State private var group = false
    @State private var name = ""
    @State private var selected: Set<String> = []
    var body: some View {
        NavigationStack {
            Form {
                Toggle("创建群聊", isOn: $group)
                if group { TextField("群聊名称", text: $name) }
                Section("Agent") {
                    if model.agents.isEmpty { Text("请先在电脑上创建 Agent").foregroundStyle(.secondary) }
                    ForEach(model.agents) { agent in
                        Button {
                            if group {
                                if !selected.insert(agent.id).inserted { selected.remove(agent.id) }
                            } else {
                                app.perform { try await model.openAgent(agent.id, app: app); dismiss() }
                            }
                        } label: {
                            HStack {
                                Text(agent.name)
                                Spacer()
                                if group, selected.contains(agent.id) { Image(systemName: "checkmark") }
                            }
                        }.disabled(!app.connected || model.creating)
                    }
                }
            }.navigationTitle("新协作对话").navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("取消") { model.select(model.roomID); dismiss() } }
                    ToolbarItem(placement: .confirmationAction) {
                        if group {
                            Button("创建") { app.perform { try await model.createRoom(name: name.trimmingCharacters(in: .whitespacesAndNewlines), agents: selected, app: app); dismiss() } }
                                .disabled(!app.connected || model.creating || name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || selected.isEmpty)
                        }
                    }
                }
        }.interactiveDismissDisabled(model.creating)
    }
}

private struct CollaborationBottom: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = nextValue() }
}

private struct CollaborationRoomView: View {
    @Bindable var app: AppModel
    @Bindable var model: CollaborationModel
    let room: CollaborationRoom
    @State private var nearBottom = true
    @FocusState private var composerFocused: Bool
    private var timeline: CollaborationTimeline { model.timelines[room.id] ?? CollaborationTimeline() }
    private var draft: Binding<String> { Binding(get: { model.drafts[room.id] ?? "" }, set: { model.drafts[room.id] = $0 }) }
    private var files: Binding<[InputAttachment]> { Binding(get: { model.attachments[room.id] ?? [] }, set: { model.attachments[room.id] = $0 }) }
    private var position: Binding<String?> { Binding(get: { model.positions[room.id] }, set: { model.positions[room.id] = $0 }) }
    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { viewport in
            ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 22) {
                    if model.hasOlder[room.id] == true {
                        Button(model.loadingOlder ? "正在读取…" : "加载更早的消息") { app.perform { try await model.loadOlder(app: app) } }
                            .disabled(!app.connected || model.loadingOlder)
                    }
                    if timeline.messages.isEmpty, app.connected { Text("开始对话").foregroundStyle(.secondary) }
                    ForEach(timeline.messages) { message in
                        VStack(alignment: .leading, spacing: 6) {
                            Text(message.isHuman ? "你" : model.agents.first(where: { $0.id == message.authorID })?.name ?? message.authorID).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                            if let replyID = message.replyID, !replyID.isEmpty {
                                let reply = timeline.messages.first { $0.id == replyID }
                                Text(reply?.body ?? "回复较早的消息").font(.caption).foregroundStyle(.secondary).lineLimit(3).padding(.leading, 10)
                            }
                            if !message.taskTitle.isEmpty {
                                Label(message.taskTitle, systemImage: "checklist").font(.headline)
                                Text(collaborationStatus(message.taskState)).font(.caption).foregroundStyle(.secondary)
                            }
                            if !message.body.isEmpty { MessageText(text: message.body, markdown: !message.isHuman) }
                            CollaborationAttachments(value: message.value)
                            if message.value["agent_creation_proposal"] != .null {
                                Label("Agent 创建请求 · 请在电脑上处理", systemImage: "person.badge.plus").font(.caption)
                            }
                        }.frame(maxWidth: .infinity, alignment: .leading).id(message.id)
                    }
                    ForEach(Array(timeline.responses.enumerated()), id: \.offset) { _, response in
                        VStack(alignment: .leading, spacing: 6) {
                            Text(model.agents.first { $0.id == response["agent_id"].string }?.name ?? "Agent").font(.caption.weight(.semibold))
                            MessageText(text: response["body"].string ?? "", markdown: true)
                            Text(collaborationStatus(response["state"].string ?? "")).font(.caption).foregroundStyle(.secondary)
                            if let error = response["error"].string, !error.isEmpty { Text(error).font(.caption).foregroundStyle(.red) }
                        }
                    }
                    Color.clear.frame(height: 1).id("bottom").background {
                        GeometryReader { geometry in
                            Color.clear.preference(key: CollaborationBottom.self, value: geometry.frame(in: .named("collaboration-timeline")).maxY)
                        }
                    }
                }.scrollTargetLayout().padding(16)
            }.coordinateSpace(name: "collaboration-timeline").defaultScrollAnchor(.bottom).scrollPosition(id: position, anchor: .top)
                .onPreferenceChange(CollaborationBottom.self) { nearBottom = $0 <= viewport.size.height + 48 }
                .onChange(of: timeline.messages.last) { _, _ in if nearBottom { proxy.scrollTo("bottom", anchor: .bottom) } }
                .onChange(of: timeline.responses) { _, _ in if nearBottom { proxy.scrollTo("bottom", anchor: .bottom) } }
                .overlay(alignment: .bottomTrailing) {
                    if !nearBottom {
                        Button { proxy.scrollTo("bottom", anchor: .bottom) } label: {
                            Label("最新消息", systemImage: "arrow.down").font(.caption).padding(10).background(.regularMaterial, in: Capsule())
                        }.padding(12)
                    }
                }
            }
            }
            if !files.wrappedValue.isEmpty {
                ScrollView(.horizontal) {
                    HStack {
                        ForEach(files.wrappedValue) { file in
                            Button { model.attachments[room.id]?.removeAll { $0.id == file.id } } label: { Label(file.filename, systemImage: "xmark.circle").font(.caption) }
                        }
                    }.padding(.horizontal, 12)
                }.disabled(model.sending)
            }
            HStack(alignment: .bottom, spacing: 12) {
                AttachmentPicker(attachments: files, model: app).id(room.id).disabled(!app.connected || model.sending)
                TextField(app.connected ? "发送到协作" : "连接电脑后发送", text: draft, axis: .vertical)
                    .accessibilityIdentifier("collaboration-composer")
                    .focused($composerFocused)
                    .lineLimit(1...6).padding(12).background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 18))
                Button { app.perform { try await model.send(app: app) } } label: { Image(systemName: "arrow.up.circle.fill").font(.title) }
                    .accessibilityLabel("发送协作消息").disabled(!app.connected || model.sending || (draft.wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && files.wrappedValue.isEmpty))
            }.padding(12)
        }.modifier(KeyboardDismissToolbar { composerFocused = false })
    }
}

private struct CollaborationAttachments: View {
    let value: JSONValue
    var body: some View {
        ForEach(Array(value["images"].array.enumerated()), id: \.offset) { _, image in
            if let encoded = image["data"].string, encoded.count <= 8 * 1024 * 1024,
               let data = Data(base64Encoded: encoded), let picture = UIImage(data: data) {
                Image(uiImage: picture).resizable().scaledToFit().frame(maxHeight: 220).accessibilityLabel("消息图片")
            } else { Label("图片 · 请在电脑上查看", systemImage: "photo").font(.caption) }
        }
        ForEach(Array(value["files"].array.enumerated()), id: \.offset) { _, file in
            Label((file["filename"].string ?? "附件") + " · 请在电脑上查看", systemImage: "doc").font(.caption)
        }
    }
}

private func collaborationStatus(_ state: String) -> String {
    switch state {
    case "open": return "待处理"
    case "doing", "running", "streaming": return "处理中"
    case "checking": return "检查中"
    case "revising": return "修改中"
    case "needs_human": return "需要你处理"
    case "done", "completed": return "已完成"
    case "queued", "pending": return "等待中"
    case "failed": return "失败"
    case "interrupted", "cancelled", "stopped": return "已停止"
    default: return state
    }
}
