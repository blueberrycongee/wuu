import SwiftUI
import WuuCore

struct HostView: View {
    @Bindable var model: AppModel
    @Environment(\.scenePhase) private var phase
    private var pollKey: String { "\(model.mode)-\(phase == .active)-\(model.connected)-\(model.collaboration.roomID ?? "")" }
    var body: some View {
        TabView(selection: $model.mode) {
            CollaborationView(app: model, model: model.collaboration)
                .tabItem { Label("协作", systemImage: "bubble.left.and.bubble.right") }.tag("collaboration")
            ConversationView(model: model)
                .tabItem { Label("会话", systemImage: "terminal") }.tag("harness")
        }
        .task(id: pollKey) {
            if model.mode == "collaboration", phase == .active, model.connected { await model.collaboration.poll(app: model) }
        }
        .onChange(of: model.collaboration.roomID) { _, _ in model.rememberLocation() }
    }
}

struct CollaborationView: View {
    @Environment(\.mobileTextSize) private var textSize
    @Bindable var app: AppModel
    @Bindable var model: CollaborationModel
    @State private var account = false
    @State private var compose = false
    @State private var details = false
    @Environment(\.colorScheme) private var scheme
    @State private var searching = false
    @State private var query = ""
    private var roomPresented: Binding<Bool> {
        Binding(get: { model.roomID != nil }, set: { if !$0 { model.select(nil) } })
    }
    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                connectionNotice
                if searching {
                    HStack(spacing: 8) {
                        Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                        TextField("搜索对话或群聊", text: $query).font(.subheadline)
                    }.padding(12).background(Color(uiColor: .secondarySystemBackground), in: Capsule()).padding(.horizontal, 16).padding(.vertical, 6)
                }
                directory
            }
            .navigationTitle("协作").navigationBarTitleDisplayMode(.inline)
            .toolbar(searching ? .hidden : .visible, for: .tabBar)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button { account = true } label: { Image(systemName: "person.crop.circle") }.accessibilityLabel("账号设置")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { searching.toggle(); query = "" } label: { Image(systemName: searching ? "xmark" : "magnifyingglass") }.accessibilityLabel(searching ? "关闭搜索" : "搜索协作")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { compose = true } label: { Image(systemName: "plus") }.accessibilityLabel("新对话或群聊").disabled(!app.connected)
                }
            }
            .navigationDestination(isPresented: roomPresented) {
                if let room = model.room {
                    VStack(spacing: 0) {
                        connectionNotice
                        CollaborationRoomView(app: app, model: model, room: room).id(room.id)
                    }
                    .navigationTitle("").navigationBarTitleDisplayMode(.inline)
                    .toolbar(.hidden, for: .tabBar)
                    .toolbarColorScheme(scheme, for: .navigationBar)
                    .toolbarBackground(Color(uiColor: UIColor.systemBackground.resolvedColor(with: UITraitCollection(userInterfaceStyle: scheme == .dark ? .dark : .light))), for: .navigationBar)
                    .toolbarBackground(.visible, for: .navigationBar)
                    .toolbar {
                        ToolbarItem(placement: .principal) {
                            HStack(spacing: 7) {
                                RoomMark(room: room, agents: model.agents, size: 27)
                                Text(room.title(agents: model.agents)).font(.system(size: textSize, weight: .medium)).lineLimit(1)
                            }.accessibilityElement(children: .combine)
                        }
                        ToolbarItem(placement: .topBarTrailing) {
                            Button { details = true } label: { Image(systemName: "ellipsis") }.accessibilityLabel("成员与任务")
                        }
                    }
                }
            }
            .sheet(isPresented: $account) { AccountSettingsView(model: app) }
            .sheet(isPresented: $compose) { NewCollaborationView(app: app, model: model) }
            .sheet(isPresented: $details) { roomDetails }
        }
    }
    @ViewBuilder private var connectionNotice: some View {
        if !app.connected {
            HStack {
                Text(app.connecting ? "正在连接…" : "电脑离线 · 消息只读")
                Spacer()
                Button("重连") { Task { await app.connect() } }.disabled(app.connecting)
            }.font(.caption).padding(.horizontal, 20).padding(.vertical, 8)
        }
        if let error = model.error { Text(error).font(.caption).foregroundStyle(.red).padding(12) }
    }
    private var directory: some View {
        let groups = model.rooms.filter { $0.value["kind"].string != "dm" }
        let rows = model.rooms.filter { query.isEmpty ? $0.value["kind"].string == "dm" : $0.title(agents: model.agents).localizedCaseInsensitiveContains(query) }
        return ScrollView {
            LazyVStack(spacing: 0) {
                if model.loading && model.rooms.isEmpty { ProgressView().padding(24) }
                if !groups.isEmpty && query.isEmpty {
                    ScrollView(.horizontal) {
                        HStack(spacing: 8) {
                            ForEach(groups) { room in
                                Button { model.select(room.id) } label: {
                                    VStack(spacing: 6) {
                                        RoomMark(room: room, agents: model.agents, size: 52)
                                            .overlay(alignment: .topTrailing) { if room.unread > 0 { Circle().fill(Color.primary).frame(width: 7, height: 7) } }
                                        Text(room.title(agents: model.agents)).font(.caption).lineLimit(1)
                                    }.frame(width: 80).padding(.vertical, 4)
                                }.buttonStyle(.plain)
                            }
                        }.padding(.horizontal, 14).padding(.vertical, 16)
                    }.scrollIndicators(.hidden)
                }
                if model.rooms.isEmpty && !model.loading {
                    VStack(spacing: 16) {
                        Image(systemName: "bubble.left.and.bubble.right").font(.system(size: 30)).foregroundStyle(.secondary)
                        Text(app.connected ? "开始一段对话" : "连接电脑后查看协作").font(.headline)
                        if app.connected { Button("选择 Agent") { compose = true } }
                    }.frame(maxWidth: .infinity).padding(40)
                }
                if !query.isEmpty && rows.isEmpty { Text("没有找到相关对话").font(.subheadline).foregroundStyle(.secondary).padding(24) }
                ForEach(rows) { room in
                    Button { model.select(room.id) } label: {
                        HStack(spacing: 12) {
                            RoomMark(room: room, agents: model.agents)
                            VStack(alignment: .leading, spacing: 3) {
                                HStack {
                                    Text(room.title(agents: model.agents)).font(.system(size: textSize, weight: .medium)).lineLimit(1)
                                    Spacer(minLength: 8)
                                    Text(mobileMessageTime(room.updated)).font(.system(size: 11)).foregroundStyle(.secondary)
                                }
                                HStack {
                                    Text(room.value["last_message"]["body"].string ?? (room.value["last_message"]["has_attachments"].bool == true ? "[附件]" : ""))
                                        .font(.system(size: 13)).foregroundStyle(.secondary).lineLimit(1)
                                    Spacer(minLength: 8)
                                    if room.unread > 0 { Circle().fill(Color.primary).frame(width: 7, height: 7).accessibilityLabel("\(room.unread) 条未读") }
                                }
                            }
                        }.padding(.horizontal, 18).padding(.vertical, 11).contentShape(Rectangle())
                    }.buttonStyle(.plain)
                }
            }.padding(.bottom, 16)
        }.scrollDismissesKeyboard(.interactively)
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

private struct CollaborationBottom: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = nextValue() }
}

private struct CollaborationRoomView: View {
    @Bindable var app: AppModel
    @Bindable var model: CollaborationModel
    let room: CollaborationRoom
    @State private var nearBottom = true
    @GestureState private var dragging = false
    private var timeline: CollaborationTimeline { model.timelines[room.id] ?? CollaborationTimeline() }
    private var draft: Binding<String> { Binding(get: { model.drafts[room.id] ?? "" }, set: { model.drafts[room.id] = $0 }) }
    private var files: Binding<[InputAttachment]> { Binding(get: { model.attachments[room.id] ?? [] }, set: { model.attachments[room.id] = $0 }) }
    private var position: Binding<String?> { Binding(get: { model.positions[room.id] }, set: { model.positions[room.id] = $0 }) }
    private var following: Bool { model.followingLatest[room.id] ?? true }
    var body: some View {
        VStack(spacing: 0) {
            GeometryReader { viewport in
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 10) {
                            if model.hasOlder[room.id] == true {
                                Button(model.loadingOlder ? "正在读取…" : "加载更早的消息") { app.perform { try await model.loadOlder(app: app) } }
                                    .font(.caption).frame(maxWidth: .infinity).padding(8).disabled(!app.connected || model.loadingOlder)
                            }
                            ForEach(Array(timeline.messages.enumerated()), id: \.element.id) { index, message in
                                VStack(spacing: 10) {
                                    if showTime(at: index) {
                                        Text(mobileMessageTime(message.value["created_at"].string ?? "", separator: true))
                                            .font(.system(size: 11)).foregroundStyle(.secondary).frame(maxWidth: .infinity).padding(.vertical, 10)
                                    }
                                    if message.value["kind"].string == "system" {
                                        Text(message.body).font(.system(size: 11)).foregroundStyle(.secondary).frame(maxWidth: .infinity).padding(.vertical, 8)
                                    } else {
                                        CollaborationMessageRow(app: app, message: message, agents: model.agents, group: room.value["kind"].string != "dm", messages: timeline.messages)
                                    }
                                }.id(message.id)
                            }
                            ForEach(Array(timeline.responses.enumerated()), id: \.offset) { _, response in
                                CollaborationActivity(app: app, model: model, response: response)
                            }
                            Color.clear.frame(height: 1).id("bottom").background {
                                GeometryReader { geometry in
                                    Color.clear.preference(key: CollaborationBottom.self, value: geometry.frame(in: .named("collaboration-timeline")).maxY)
                                }
                            }
                        }.scrollTargetLayout().padding(.horizontal, 12).padding(.vertical, 12)
                    }.coordinateSpace(name: "collaboration-timeline").defaultScrollAnchor(.bottom).scrollPosition(id: position, anchor: .top)
                        .scrollDismissesKeyboard(.interactively)
                        .simultaneousGesture(DragGesture(minimumDistance: 3).updating($dragging) { _, state, _ in state = true })
                        .onPreferenceChange(CollaborationBottom.self) {
                            nearBottom = $0 <= viewport.size.height + 36
                            if dragging || nearBottom { model.followingLatest[room.id] = nearBottom }
                        }
                        .onChange(of: timeline.messages.last) { _, _ in if following { proxy.scrollTo("bottom", anchor: .bottom) } }
                        .onChange(of: timeline.responses) { _, _ in if following { proxy.scrollTo("bottom", anchor: .bottom) } }
                        .onChange(of: viewport.size.height) { _, _ in if following { proxy.scrollTo("bottom", anchor: .bottom) } }
                        .overlay(alignment: .bottomTrailing) {
                            if !following && !nearBottom {
                                MobileCircleButton(symbol: "chevron.down", label: "回到最新消息") {
                                    model.followingLatest[room.id] = true
                                    proxy.scrollTo("bottom", anchor: .bottom)
                                }.padding(12)
                            }
                        }
                }
            }
            MobileComposer(text: draft, attachments: files, model: app, enabled: app.connected, sending: model.sending,
                placeholder: "发消息给 " + room.title(agents: model.agents), identifier: "collaboration-composer") {
                model.followingLatest[room.id] = true
                app.perform { try await model.send(app: app) }
            }
        }.sheet(item: $app.attachmentPreview) { AttachmentPreview(attachment: $0) }
            .onDisappear { app.attachmentPreview = nil }
    }
    private func showTime(at index: Int) -> Bool {
        guard let date = mobileMessageDate(timeline.messages[index].value["created_at"].string ?? "") else { return false }
        guard index > 0, let previous = mobileMessageDate(timeline.messages[index - 1].value["created_at"].string ?? "") else { return true }
        return date.timeIntervalSince(previous) >= 300
    }
}

private struct CollaborationActivity: View {
    @Bindable var app: AppModel
    @Bindable var model: CollaborationModel
    let response: JSONValue
    @State private var resuming = false
    private var state: String { response["state"].string ?? "" }
    private var failed: Bool { ["failed", "interrupted"].contains(state) }
    private var agent: CollaborationAgent? { model.agents.first { $0.id == response["agent_id"].string } }
    var body: some View {
        HStack(spacing: 10) {
            AgentMark(agent: agent, size: 36, status: state)
            VStack(alignment: .leading, spacing: 3) {
                Text(agent?.name ?? "Agent").font(.system(size: 12, weight: .medium))
                Text(collaborationStatus(state)).font(.system(size: 11)).foregroundStyle(.secondary)
                if failed, let error = response["error"].string, !error.isEmpty { Text(error).font(.caption).foregroundStyle(.red) }
            }.frame(maxWidth: .infinity, alignment: .leading)
            if failed {
                Button(resuming ? "正在继续…" : state == "interrupted" ? "继续" : "重试") {
                    resuming = true
                    app.perform { defer { resuming = false }; try await model.resume(response, app: app) }
                }.font(.subheadline).disabled(!app.connected || resuming)
            }
        }.padding(.vertical, 6)
    }
}

private struct CollaborationMessageRow: View {
    @Bindable var app: AppModel
    let message: CollaborationMessage
    let agents: [CollaborationAgent]
    let group: Bool
    let messages: [CollaborationMessage]
    var body: some View {
        CollaborationBubble(own: message.isHuman, agent: !message.isHuman && group ? agents.first { $0.id == message.authorID } : nil) {
            if let replyID = message.replyID, !replyID.isEmpty {
                Text(messages.first { $0.id == replyID }?.body ?? "回复较早的消息").font(.caption).opacity(0.7).lineLimit(3).padding(.leading, 8)
                    .overlay(alignment: .leading) { Capsule().fill(Color.primary.opacity(0.2)).frame(width: 2) }
            }
            if !message.taskTitle.isEmpty {
                Label(message.taskTitle, systemImage: "checklist").font(.subheadline.weight(.semibold))
                Text(collaborationStatus(message.taskState)).font(.caption).opacity(0.7)
            }
            if !message.body.isEmpty { MessageText(text: message.body, markdown: !message.isHuman) }
            ForEach(["images", "markdown_images", "files"], id: \.self) { field in
                ForEach(Array(message.value[field].array.enumerated()), id: \.offset) { index, file in
                    if field != "files" {
                        MessageImage(key: "\(app.collaboration.roomID ?? ""):\(message.id):\(index):\(file["remote_ref"].string ?? "")",
                            connected: app.connected, loader: app.imagePreviews,
                            read: { try await app.collaboration.readAttachment(message, field: field, index: index, preview: true, app: app) },
                            open: { app.perform { try await app.previewCollaborationAttachment(message, field: field, index: index) } })
                    } else {
                    Button { app.perform { try await app.previewCollaborationAttachment(message, field: field, index: index) } } label: {
                        Label(file["filename"].string ?? (field == "images" ? "查看图片" : "查看文件"), systemImage: field == "images" ? "photo" : "doc")
                            .font(.subheadline).padding(.vertical, 6)
                    }.disabled(!app.connected || app.loadingAttachment)
                    }
                }
            }
            if message.value["agent_creation_proposal"] != .null { Label("Agent 创建请求 · 在电脑上处理", systemImage: "person.badge.plus").font(.caption) }
        }
    }
}

private struct CollaborationBubble<Content: View>: View {
    @Environment(\.mobileTextSize) private var textSize
    let own: Bool
    let agent: CollaborationAgent?
    var status: String? = nil
    @ViewBuilder var content: () -> Content
    var body: some View {
        HStack(alignment: .bottom, spacing: 6) {
            if own { Spacer(minLength: 40) }
            if !own, let agent { AgentMark(agent: agent, size: 22, status: status, subtle: status == nil) }
            VStack(alignment: .leading, spacing: 6, content: content)
                .font(.system(size: textSize))
                .foregroundStyle(own ? Color(uiColor: .systemBackground) : Color.primary)
                .padding(.horizontal, 14).padding(.vertical, 9)
                .background(own ? Color.primary : Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 18))
            if !own { Spacer(minLength: 30) }
        }.frame(maxWidth: .infinity, alignment: own ? .trailing : .leading)
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
                            HStack(spacing: 12) {
                                AgentMark(agent: agent)
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


private func collaborationStatus(_ state: String) -> String {
    switch state {
    case "thinking", "responding", "sending": return "正在工作"
    case "waiting": return "等待后续消息"
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
