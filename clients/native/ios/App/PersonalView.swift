import SwiftUI
import WuuCore

/// Personal UI state belongs to one account and host. Nothing here is written into Coding history.
@MainActor @Observable final class PersonalModel {
    var active = false
    private(set) var available = false
    private(set) var busy = false
    private(set) var controlBusy = false
    var contextID: UUID { generation }
    private(set) var agents: [JSONValue] = []
    private(set) var agent: JSONValue = .null
    private(set) var records: [JSONValue] = []
    private(set) var tasks: [JSONValue] = []
    private(set) var detail: JSONValue = .null
    private(set) var taskRecords: [JSONValue] = []
    private(set) var hasEarlier = false
    private(set) var firstSequence = 1
    var text = ""
    var attachments: [InputAttachment] = []
    var preview: LoadedAttachment?
    private(set) var loadingAttachment = false
    private(set) var composerID = UUID()
    private var cache: PersonalHistoryCache?
    private var cacheIdentity: [String]?
    private var draftStore: PersonalDraftStore?
    private var restoredDraft: PersonalStoredDraft?
    private var draftRecoveryBlocked = false
    var hasPending: Bool { pending != nil || restoredDraft != nil || draftRecoveryBlocked }
    var error: String?
    private(set) var notice: String?
    private(set) var pending: PersonalMessageDraft?
    private var client: PersonalClient?
    private var generation = UUID()
    private var desiredAgent: String?
    private var desiredTask: String?
    private(set) var messageSequence: Int?
    var selectedID: String? { agent["agent_id"].string }
    var canSend: Bool { available && !busy && !controlBusy && !draftRecoveryBlocked && selectedID != nil && agent["status"].string == "active" }

    func configureCache(directory: URL, draftDirectory: URL, identity: [String]) async {
        guard cacheIdentity != identity else { return }
        let old = cache, oldDrafts = draftStore; cacheIdentity = identity
        let next = PersonalHistoryCache(directory: directory, identity: identity); cache = next
        let drafts = PersonalDraftStore(directory: draftDirectory, identity: identity); draftStore = drafts
        let stamp = generation
        try? await old?.invalidate(removeCache: false); try? await oldDrafts?.invalidate(remove: false)
        do {
            let recovered = try await drafts.load()
            guard generation == stamp, draftStore === drafts else { return }
            restoredDraft = recovered; draftRecoveryBlocked = false
            if let recovered { desiredAgent = recovered.agentID; text = recovered.prompt; attachments = recovered.attachments; notice = "已恢复尚未确认的原消息。连接后可明确重试；不会自动发送。" }
            if let snapshot = try await next.load(agentID: recovered?.agentID), generation == stamp, cache === next {
                agent = snapshot["agent"]; desiredAgent = agent["agent_id"].string; agents = [agent]; records = snapshot["records"].array
                firstSequence = Int(records.first?["seq"].number ?? 1); hasEarlier = firstSequence > 1
                if recovered == nil { notice = "显示此账号和宿主的缓存历史；连接后才能发送或读取附件。" }
            }
        } catch { if generation == stamp { draftRecoveryBlocked = true; self.error = error.localizedDescription; notice = "本机未确认消息无法恢复，发送已暂停。请在宿主核对历史后放下本机重试。" } }
    }
    func inventoryChanged(_ event: JSONValue) async {
        let stamp = UUID(); generation = stamp; controlBusy = false; busy = false; loadingAttachment = false; preview = nil
        do {
            guard let client else { throw PersonalClientError.unavailable }
            try await client.refreshInventory(event)
            guard generation == stamp else { return }
            available = true; await reload()
        } catch { if generation == stamp { available = false; self.error = error.localizedDescription } }
    }
    func loadAttachment(_ descriptor: JSONValue, sequence: Int, task: JSONValue? = nil) async {
        guard available, !loadingAttachment, let client else { return }
        let stamp = generation; loadingAttachment = true
        defer { if generation == stamp { loadingAttachment = false } }
        do {
            let value = try await client.attachment(sequence: sequence, descriptor: descriptor, taskID: task?["task_id"].string,
                requirementsRevision: task.flatMap { $0["requirements_revision"].number }.map { Int($0) })
            if generation == stamp { preview = value }
        } catch { if generation == stamp { self.error = error.localizedDescription } }
    }
    func bind(_ remote: RemoteConnection, initialized: JSONValue) async {
        let stamp = UUID(); generation = stamp; controlBusy = false; busy = false; loadingAttachment = false; preview = nil
        let adapter = client ?? PersonalClient(connection: remote); client = adapter
        do {
            try await adapter.reconnect(remote, initialized: initialized)
            guard generation == stamp else { return }
            available = true; error = nil
            if active { await reload() }
        } catch {
            guard generation == stamp else { return }
            available = false; busy = false; self.error = error.localizedDescription
        }
    }
    func disconnect(reason: String? = nil) async {
        generation = UUID(); controlBusy = false; available = false; busy = false; preview = nil; loadingAttachment = false
        if let reason { error = reason }
        if pending != nil { notice = "发送结果尚未确认。重新连接后可重试同一条消息，不会新建聊天。" }
        await client?.invalidate()
    }
    func reset(removeCache: Bool = false) async {
        let oldCache = cache, oldDrafts = draftStore; cache = nil; cacheIdentity = nil; draftStore = nil; restoredDraft = nil; draftRecoveryBlocked = false
        composerID = UUID(); attachments = []; preview = nil; loadingAttachment = false
        let old = client; client = nil; generation = UUID()
        active = false; available = false; busy = false; controlBusy = false; agents = []; agent = .null; records = []; tasks = []; detail = .null; taskRecords = []
        desiredAgent = nil; desiredTask = nil; messageSequence = nil; pending = nil; text = ""; error = nil; notice = nil
        await old?.invalidate()
        try? await oldCache?.invalidate(removeCache: removeCache)
        try? await oldDrafts?.invalidate(remove: removeCache)
    }
    func locate(_ location: PersonalLocation) async {
        guard !hasPending || (pending?.agentID ?? restoredDraft?.agentID) == location.agentID else { error = "请先重试或放下尚未确认的消息，再切换助理。"; return }
        desiredAgent = location.agentID; desiredTask = location.taskID; messageSequence = location.messageSequence
        agent = .null; records = []; tasks = []; detail = .null; taskRecords = []
        if !hasPending { text = ""; attachments = []; composerID = UUID() }
        generation = UUID(); controlBusy = false; busy = false; active = true; loadingAttachment = false; preview = nil
        if available { await reload() }
    }
    func choose(_ id: String) async {
        guard !hasPending else { error = "请先重试或放下尚未确认的消息，再切换助理。"; return }
        desiredAgent = id; desiredTask = nil; messageSequence = nil
        agent = .null; records = []; tasks = []; detail = .null; taskRecords = []; text = ""; attachments = []; preview = nil; composerID = UUID()
        generation = UUID(); controlBusy = false; busy = false; loadingAttachment = false; preview = nil
        let stamp = generation
        if !available, let cache, let snapshot = try? await cache.load(agentID: id) {
            guard generation == stamp, self.cache === cache else { return }
            agent = snapshot["agent"]; records = snapshot["records"].array
            firstSequence = Int(records.first?["seq"].number ?? 1); hasEarlier = firstSequence > 1
            return
        }
        await reload()
    }
    func reload() async {
        guard active, available, !busy, !controlBusy, !loadingAttachment, let client else { return }
        busy = true; error = nil
        let stamp = generation
        defer { if generation == stamp { busy = false } }
        do {
            let listed = try await client.listAgents()
            guard generation == stamp else { return }
            var values = listed["agents"].array
            let target: String
            if let desiredAgent { target = desiredAgent }
            else if let main = values.first(where: { $0["kind"].string == "main" })?["agent_id"].string { target = main }
            else {
                let created = try await client.ensureDefaultAgent()
                guard let id = created["agent"]["agent_id"].string else { throw PersonalClientError.invalidResponse }
                values.append(created["agent"]); target = id
            }
            let opened = try await client.selectAgent(target)
            if let restoredDraft {
                let recovered = try await client.restoreDraft(restoredDraft)
                guard generation == stamp else { return }
                pending = recovered; self.restoredDraft = nil
            }
            let history = try await client.history(endSequence: messageSequence)
            let matters = try await client.listTasks()
            var task: JSONValue = .null, taskHistory: [JSONValue] = []
            if let desiredTask {
                task = (try await client.task(desiredTask))["task"]
                taskHistory = personalDisplayRecords((try await client.taskHistory(desiredTask, requirementsRevision: task["requirements_revision"].number.map { Int($0) }))["records"].array)
            }
            guard generation == stamp else { return }
            let keepEarlier = selectedID == target && messageSequence == nil
            let page = personalDisplayRecords(history["records"].array)
            let pageStart = Int(history["first_seq"].number ?? 1)
            records = (keepEarlier ? records.filter { Int($0["seq"].number ?? 0) < pageStart } : []) + page
            hasEarlier = keepEarlier && firstSequence < pageStart ? hasEarlier : history["has_earlier"].bool
            firstSequence = keepEarlier ? min(firstSequence, pageStart) : pageStart
            agents = values; agent = opened["agent"]; desiredAgent = target
            tasks = matters["tasks"].array; detail = task; taskRecords = taskHistory
            if messageSequence == nil, let cache { try await cache.save(agent: agent, records: records) }
            guard generation == stamp else { return }
            if let messageSequence, !records.contains(where: { Int($0["seq"].number ?? 0) == messageSequence }) {
                notice = "原消息暂时无法定位。仍保留原助理，不会创建新聊天。"
            }
        } catch {
            guard generation == stamp else { return }
            self.error = error.localizedDescription
            if let failure = error as? PersonalClientError, failure == .unavailable || failure == .isolationRequired { available = false }
        }
    }
    func earlier() async {
        guard available, !busy, hasEarlier, let client else { return }
        busy = true; let stamp = generation
        defer { if generation == stamp { busy = false } }
        do {
            let history = try await client.history(endSequence: firstSequence - 1)
            guard generation == stamp else { return }
            let existing = Set(records.compactMap { $0["seq"].number })
            records = history["records"].array.filter { !existing.contains($0["seq"].number ?? -1) } + records
            hasEarlier = history["has_earlier"].bool; firstSequence = Int(history["first_seq"].number ?? 1)
        } catch { if generation == stamp { self.error = error.localizedDescription } }
    }
    func send() async {
        guard canSend, let client else { return }
        busy = true; let stamp = generation; error = nil
        var submitted = false
        do {
            let draft: PersonalMessageDraft
            if let pending { draft = pending }
            else {
                draft = try await client.prepareMessage(text, attachments: attachments)
                guard generation == stamp else { return }
                pending = draft
            }
            guard let draftStore else { throw NativeError.invalid("无法保存原消息，尚未发送") }
            try await draftStore.save(draft)
            guard generation == stamp else { return }
            let result = try await client.send(draft)
            guard generation == stamp else { return }
            // An actual response acknowledges only submission, never task completion.
            guard result["state"].string != nil || result["turn_id"].string != nil || result["queue_id"].string != nil else { throw PersonalClientError.invalidResponse }
            try await draftStore.clear()
            guard generation == stamp else { return }
            submitted = true; pending = nil; restoredDraft = nil; text = ""; attachments = []; composerID = UUID(); notice = "消息状态：\(result["state"].string ?? "已提交")，结果以助理历史为准。"; messageSequence = nil
        } catch {
            if generation == stamp { self.error = error.localizedDescription; notice = pending == nil ? "消息尚未发送。" : "发送结果尚未确认。可重试同一条消息；不会自动重发。" }
        }
        guard generation == stamp else { return }
        busy = false; if submitted { await reload() }
    }
    func abandonPending() async {
        let stamp = generation
        do {
            try await draftStore?.clear()
            guard generation == stamp else { return }
            pending = nil; restoredDraft = nil; draftRecoveryBlocked = false; attachments = []; composerID = UUID(); notice = "已放下本机重试。原请求仍可能已被接收，请先检查历史。"; text = ""
        } catch { if generation == stamp { self.error = error.localizedDescription } }
    }
    func stopReply() async {
        guard canSend, let client else { return }
        let stamp = generation; busy = true
        do { _ = try await client.stopReply() }
        catch { if generation == stamp { self.error = error.localizedDescription } }
        if generation == stamp { busy = false; await reload() }
    }
    func openTask(_ id: String?) async { generation = UUID(); controlBusy = false; busy = false; loadingAttachment = false; preview = nil; desiredTask = id; detail = .null; taskRecords = []; await reload() }
    func showSource(_ seq: Int) async { generation = UUID(); controlBusy = false; busy = false; loadingAttachment = false; preview = nil; desiredTask = nil; messageSequence = seq; await reload() }
    func control(_ operation: (PersonalClient) async throws -> JSONValue) async -> JSONValue? {
        guard available, !controlBusy, let client else { return nil }
        let stamp = generation; controlBusy = true; error = nil
        defer { if generation == stamp { controlBusy = false } }
        do { let result = try await operation(client); return generation == stamp ? result : nil }
        catch { if generation == stamp { self.error = error.localizedDescription }; return nil }
    }
    func changeTask(_ snapshot: JSONValue, action: PersonalTaskAction, goal: String? = nil) async {
        guard let id = snapshot["task_id"].string, let revision = snapshot["revision"].number else { return }
        if let result = await control({ try await $0.changeTask(id, expectedRevision: Int(revision), action: action, goal: goal) }) {
            detail = result["task"]; notice = "事项状态：\(detail["state"].string ?? "已更新")。停止和恢复可能需要等待正在结束的工作。"
            await reload()
        }
    }
    func latest() async { messageSequence = nil; await reload() }
}

struct PersonalView: View {
    @Bindable var model: AppModel
    @Bindable var personal: PersonalModel
    @State private var section = 0
    @State private var discard = false
    @State private var editingTask: JSONValue?
    @State private var taskGoal = ""
    @State private var taskAction: PersonalTaskAction = .update
    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                if !personal.available && personal.selectedID == nil && !personal.hasPending {
                    ContentUnavailableView {
                        Label("Personal 暂不可用", systemImage: "person.crop.circle.badge.exclamationmark")
                    } description: {
                        Text(personal.error ?? "连接已启用 Personal 的可信宿主后，可以继续与同一个助理交流。离线时不能提交执行。")
                    } actions: {
                        Button("重新连接") { Task { await model.connect(); await personal.reload() } }.disabled(model.connecting)
                    }
                } else {
                    if !personal.available { Text("连接已断开，显示上次读取的内容。重新连接后才能发送或读取附件。").foregroundStyle(.secondary).padding(.horizontal) }
                    if !personal.agents.isEmpty {
                        Menu {
                            ForEach(personal.agents, id: \.agentIdentity) { item in
                                Button(agentLabel(item)) { Task { await personal.choose(item["agent_id"].string ?? "") } }
                            }
                        } label: { Label(agentLabel(personal.agent), systemImage: "person.crop.circle").frame(maxWidth: .infinity, alignment: .leading).padding() }
                        .disabled(personal.busy)
                    }
                    Picker("Personal 内容", selection: $section) { Text("对话").tag(0); Text("事项").tag(1); Text("今天").tag(2); Text("记忆").tag(3); Text("设置").tag(4); Text("日历").tag(5); Text("查找历史").tag(6) }
                        .pickerStyle(.menu).padding(.horizontal)
                    if let error = personal.error { Text(error).foregroundStyle(.red).padding(.horizontal).accessibilityLabel("错误：" + error) }
                    if let notice = personal.notice { Text(notice).foregroundStyle(.secondary).padding(.horizontal) }
                    if section == 0 { conversation } else if section == 1 { matters } else { PersonalToolsView(personal: personal, section: section, selectSection: { section = $0 }).id(personal.contextID) }
                    if personal.busy || personal.controlBusy { ProgressView().padding(8) }
                }
            }
            .navigationTitle("Personal").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) { Button("电脑") { model.perform { await model.leaveHost(); model.foreground() } } }
                ToolbarItem(placement: .topBarTrailing) { Button("刷新", systemImage: "arrow.clockwise") { Task { await personal.reload() } }.disabled(personal.busy || !personal.available) }
            }
        }
        .task(id: personal.active && personal.available) {
            guard personal.active, personal.available else { return }
            while !Task.isCancelled {
                await personal.reload()
                do { try await Task.sleep(for: .seconds(3)) } catch { return }
            }
        }
        .onChange(of: personal.detail["task_id"].string) { _, value in if value != nil { section = 1 } }
        .onChange(of: personal.contextID) { _, _ in editingTask = nil }
        .sheet(isPresented: Binding(get: { editingTask != nil }, set: { if !$0 { editingTask = nil } })) {
            NavigationStack {
                Form {
                    Text(editingTask?["title"].string ?? "事项")
                    if taskAction == .update { TextField("新的事项要求", text: $taskGoal, axis: .vertical).lineLimit(3...10) }
                    Text(taskAction == .stop ? "请求停止后台事项。远端已开始的操作可能仍需核对，状态以宿主返回为准。" : "会提交当前显示版本的修改；版本冲突时请刷新后重新审阅。")
                    Button(taskAction == .stop ? "确认停止" : taskAction == .resume ? "确认恢复" : "保存要求") {
                        guard let snapshot = editingTask else { return }; editingTask = nil
                        Task { await personal.changeTask(snapshot, action: taskAction, goal: taskAction == .update ? taskGoal : nil) }
                    }.disabled(!personal.available || personal.busy || personal.controlBusy || (taskAction == .update && taskGoal.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty))
                }.navigationTitle("更新事项").toolbar { Button("取消") { editingTask = nil } }
            }
        }
        .sheet(item: $personal.preview) { AttachmentPreview(attachment: $0) }
        .confirmationDialog("放下尚未确认的消息？", isPresented: $discard, titleVisibility: .visible) {
            Button("放下本机重试", role: .destructive) { Task { await personal.abandonPending() } }
        } message: { Text("原请求可能已被宿主接收。请先查看历史，避免重新输入相同请求。") }
    }
    private func agentLabel(_ value: JSONValue) -> String {
        let name = value["name"].string ?? "助理"
        guard value["kind"].string == "specialist", let tag = value["responsibility"].string, !tag.isEmpty else { return name }
        return name + " · " + tag
    }
    private var conversation: some View {
        let composer = personal.composerID
        return VStack(spacing: 0) {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 16) {
                        if personal.hasEarlier { Button("更早的消息") { Task { await personal.earlier() } }.disabled(personal.busy) }
                        if personal.messageSequence != nil { Button("返回最新消息") { Task { await personal.latest() } } }
                        if personal.records.isEmpty && !personal.busy { Text("直接发消息，助理会在这里继续与你交流。").foregroundStyle(.secondary) }
                        ForEach(personal.records, id: \.recordIdentity) { record in
                            VStack(alignment: .leading, spacing: 6) {
                                Text(record["role"].string == "user" ? "你" : (personal.agent["name"].string ?? "助理")).font(.body).foregroundStyle(.secondary)
                                MessageText(text: record["content"].string ?? "", markdown: record["role"].string != "user")
                                attachmentButtons(record)
                            }.id(Int(record["seq"].number ?? 0)).frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }.padding()
                }
                .onChange(of: personal.records) { _, _ in if let seq = personal.messageSequence { proxy.scrollTo(seq, anchor: .center) } }
            }
            Divider()
            ForEach(personal.attachments) { file in
                HStack { Text(file.filename).lineLimit(1); Spacer(); Button("移除") { personal.attachments.removeAll { $0.id == file.id } }.disabled(personal.hasPending) }.padding(.horizontal)
            }
            HStack(alignment: .bottom) {
                AttachmentPicker(attachments: Binding(get: { personal.attachments }, set: { if personal.composerID == composer { personal.attachments = $0 } }), model: model)
                    .id(composer).disabled(!personal.canSend || personal.hasPending)
                TextField("发消息", text: $personal.text, axis: .vertical).lineLimit(1...6).disabled(personal.hasPending)
                Button(!personal.hasPending ? "发送" : "重试") { Task { await personal.send() } }
                    .disabled(!personal.canSend || (!personal.hasPending && personal.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && personal.attachments.isEmpty))
            }.padding()
            HStack {
                Button("停止当前回复") { Task { await personal.stopReply() } }.disabled(!personal.canSend)
                if personal.hasPending { Button("放下重试") { discard = true }.disabled(personal.busy) }
            }.font(.footnote).padding(.bottom, 8)
        }
    }
    private func attachmentButtons(_ record: JSONValue, task: JSONValue? = nil) -> some View {
        ForEach(Array(record["attachments"].array.enumerated()), id: \.offset) { _, attachment in
            Button(attachment["name"].string ?? "查看附件", systemImage: "paperclip") {
                Task { await personal.loadAttachment(attachment, sequence: Int(record["seq"].number ?? 0), task: task) }
            }.disabled(!personal.available || personal.loadingAttachment)
        }
    }
    private var matters: some View {
        List {
            if personal.detail != .null {
                Section {
                    Button("所有事项") { Task { await personal.openTask(nil) } }
                    Text(personal.detail["title"].string ?? "事项").font(.headline)
                    Text(personal.detail["goal"].string ?? "")
                    Text(personal.detail["state"].string ?? "").foregroundStyle(.secondary)
                    if personal.detail["cancel_requested"].bool { Text("正在请求停止；尚未确认结束") }
                    if personal.detail["reconciliation_required"].bool { Text("远端结果尚待核对，请勿重复发起相同事项") }
                    HStack {
                        Button("修改要求") { taskAction = .update; taskGoal = personal.detail["goal"].string ?? ""; editingTask = personal.detail }
                        Button("停止") { taskAction = .stop; editingTask = personal.detail }
                        Button("恢复") { taskAction = .resume; editingTask = personal.detail }
                    }.disabled(!personal.available || personal.busy || personal.controlBusy)
                    if let summary = personal.detail["result"]["summary"].string { MessageText(text: summary, markdown: true) }
                    if let seq = personal.detail["source"]["message_seq"].number {
                        Button("查看来源消息") { section = 0; Task { await personal.showSource(Int(seq)) } }
                    }
                }
                Section("结果依据") {
                    ForEach(Array(personal.detail["result"]["evidence"].array.enumerated()), id: \.offset) { _, item in Text(item["description"].string ?? "") }
                }
                Section("事项执行记录") {
                    ForEach(personal.taskRecords, id: \.recordIdentity) { record in
                        MessageText(text: record["display_content"].string ?? record["content"].string ?? "", markdown: record["role"].string == "assistant")
                        attachmentButtons(record, task: personal.detail)
                    }
                }
            } else {
                if personal.tasks.isEmpty { Text(personal.available ? "还没有事项" : "连接宿主后刷新事项状态").foregroundStyle(.secondary) }
                ForEach(personal.tasks, id: \.taskIdentity) { task in
                    Button { Task { await personal.openTask(task["task_id"].string) } } label: {
                        VStack(alignment: .leading, spacing: 6) { Text(task["title"].string ?? "事项"); Text(task["state"].string ?? "").font(.body).foregroundStyle(.secondary) }
                    }
                }
            }
        }.listStyle(.plain)
    }
}

private extension JSONValue {
    var agentIdentity: String { self["agent_id"].string ?? "" }
    var taskIdentity: String { self["task_id"].string ?? "" }
    var recordIdentity: Double { self["seq"].number ?? 0 }
}
