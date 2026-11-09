import SwiftUI
import WuuCore

/// Public Personal services only. These views never infer authority from cached data.
struct PersonalToolsView: View {
    @Bindable var personal: PersonalModel
    let section: Int
    let selectSection: (Int) -> Void
    @State private var proposal: JSONValue?
    @State private var agentPause: JSONValue?
    @State private var name = ""
    @State private var communication = ""
    @State private var responsibility = ""
    @State private var data: JSONValue = .null
    @State private var query = ""
    @State private var historyDated = false
    @State private var historyFrom = Date()
    @State private var historyThrough = Date()
    @State private var notifications = true
    @State private var notificationBatchSeconds = 0
    @State private var quiet = false
    @State private var start = "22:00"
    @State private var end = "08:00"
    @State private var timezone = TimeZone.current.identifier
    @State private var memoryEdit: JSONValue?
    @State private var memoryContent = ""
    @State private var editRequestID = UUID().uuidString
    @State private var forget: JSONValue?
    @State private var feedback: String?
    private var enabled: Bool { personal.available && !personal.controlBusy }

    var body: some View {
        List {
            Section {
                if section == 3 { TextField("搜索记忆", text: $query) }
                if section == 5 {
                    TextField("人物、事情或原话", text: $query)
                    Toggle("按消息日期筛选", isOn: $historyDated)
                    if historyDated {
                        DatePicker("从", selection: $historyFrom, displayedComponents: .date)
                        DatePicker("到（含当天）", selection: $historyThrough, displayedComponents: .date)
                    }
                    Text("搜索当前助理的持久主对话；可在上方切换助理。").foregroundStyle(.secondary)
                }
                Button("刷新") { Task { await load() } }.disabled(!enabled)
                if let feedback { Text(feedback).foregroundStyle(.secondary) }
                if !personal.available { Text("离线时仅显示已经读取的内容，不能修改、确认或提交执行。").foregroundStyle(.secondary) }
            }
            if section == 2 { today }
            if section == 3 { memories }
            if section == 4 { settings }
            if section == 5 {
                Section("原消息") {
                    ForEach(Array(data["matches"].array.enumerated()), id: \.offset) { _, match in
                        Button { selectSection(0); Task { await personal.showSource(Int(match["seq"].number ?? 0)) } } label: {
                            VStack(alignment: .leading, spacing: 8) { Text(match["excerpt"].string ?? ""); Text(match["at"].string ?? "时间未知").foregroundStyle(.secondary) }
                        }
                    }
                    if data["matches"].array.isEmpty && data != .null { Text("没有匹配的消息") }
                    if data["has_more"].bool, let cursor = data["next_cursor"].string { Button("更早的匹配") { Task { await load(historyCursor: cursor) } }.disabled(!enabled) }
                }
            }
        }.listStyle(.insetGrouped)
        .task(id: section) {
            data = .null; feedback = nil
            while personal.controlBusy {
                do { try await Task.sleep(for: .milliseconds(50)) } catch { return }
            }
            guard !Task.isCancelled else { return }
            await load()
        }
        .onChange(of: query) { _, _ in if section == 5 { data = .null } }
        .onChange(of: historyDated) { _, _ in if section == 5 { data = .null } }
        .onChange(of: historyFrom) { _, _ in if section == 5 { data = .null } }
        .onChange(of: historyThrough) { _, _ in if section == 5 { data = .null } }
        .confirmationDialog("创建具名专属助理？", isPresented: Binding(get: { proposal != nil }, set: { if !$0 { proposal = nil } }), titleVisibility: .visible) {
            Button(proposal?["status"].string == "approved" ? "重试原创建请求" : "确认创建") {
                guard let snapshot = proposal else { return }; proposal = nil
                Task { if await personal.control({ try await $0.decideSpecialist(snapshot["proposal_id"].string ?? "", approved: true) }) != nil { await load(); await personal.reload() } }
            }
            if proposal?["status"].string == "pending" {
                Button("暂不创建", role: .destructive) {
                    guard let snapshot = proposal else { return }; proposal = nil
                    Task { if await personal.control({ try await $0.decideSpecialist(snapshot["proposal_id"].string ?? "", approved: false) }) != nil { await load() } }
                }
            }
        } message: { Text("\(proposal?["name"].string ?? "")\n职责：\(proposal?["responsibility"].string ?? "")\n沟通方式：\(proposal?["communication"].string ?? "")\n这个不可变提案会建立独立具名助理及其持久主对话。") }
        .confirmationDialog("更新助理运行状态？", isPresented: Binding(get: { agentPause != nil }, set: { if !$0 { agentPause = nil } }), titleVisibility: .visible) {
            Button(agentPause?["status"].string == "paused" ? "恢复助理" : "暂停助理") {
                guard let snapshot = agentPause else { return }; agentPause = nil
                Task {
                    if await personal.control({ try await $0.setAgentPaused(snapshot["status"].string != "paused", expectedRevision: Int(snapshot["revision"].number ?? -1)) }) != nil { await load(); await personal.reload() }
                }
            }
        } message: { Text("暂停会请求停止持续工作，可能先进入正在停止状态。已有历史仍保留，实际状态以宿主为准。") }
        .sheet(isPresented: Binding(get: { memoryEdit != nil }, set: { if !$0 { memoryEdit = nil } })) {
            NavigationStack {
                Form {
                    TextField("更正后的事实", text: $memoryContent, axis: .vertical).lineLimit(3...12)
                    Text("将作为你的明确更正供所有 Personal 助理使用。原始聊天和文件不会改写。")
                    if let error = personal.error { Text(error).foregroundStyle(.red) }
                    Button("保存更正") { Task {
                        guard let record = memoryEdit, let id = record["id"].string, let revision = record["revision"].number else { return }
                        if await personal.control({ try await $0.correctMemory(id, expectedRevision: Int(revision), content: memoryContent, requestID: editRequestID) }) != nil {
                            memoryEdit = nil; await load()
                        }
                    } }.disabled(!enabled || memoryContent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }.navigationTitle("更正记忆").toolbar { Button("取消") { memoryEdit = nil } }
            }
        }
        .confirmationDialog("忘记这条共享记忆？", isPresented: Binding(get: { forget != nil }, set: { if !$0 { forget = nil } }), titleVisibility: .visible) {
            Button("忘记记忆", role: .destructive) {
                guard let record = forget else { return }; forget = nil
                Task {
                    if let result = await personal.control({ try await $0.forgetMemory(record["id"].string ?? "", expectedRevision: Int(record["revision"].number ?? -1)) }) {
                        feedback = result["retention"]["history"].string ?? "记忆已移除；原始聊天、文件、导出和备份仍保留。"; await load()
                    }
                }
            }
        } message: { Text("所有 Personal 助理将停止使用此记忆。原始聊天、文件、导出和备份仍保留；不能保证识别所有不同表述。") }
    }
    @ViewBuilder private var today: some View {
        Section("进行中的事项") {
            let rows = data["tasks"].array.filter { !["completed", "failed", "stopped"].contains($0["state"].string ?? "") }
            if rows.isEmpty { Text(data == .null ? "等待读取" : "当前没有进行中的事项") }
            ForEach(Array(rows.enumerated()), id: \.offset) { _, row in
                Button { Task { await personal.openTask(row["task_id"].string) } } label: { VStack(alignment: .leading) { Text(row["title"].string ?? "事项"); Text(row["state"].string ?? "").foregroundStyle(.secondary) } }
            }
        }
        Section("待决定") {
            ForEach(Array(data["proposals"].array.enumerated()), id: \.offset) { _, value in
                Button(value["status"].string == "approved" ? "重试创建：" + (value["name"].string ?? "") : "创建专属助理：" + (value["name"].string ?? "")) { proposal = value }.disabled(!enabled)
            }
            if let error = data["proposal_error"].string { Text(error).foregroundStyle(.secondary) }
        }
        Section("最近结果") {
            ForEach(Array(data["tasks"].array.filter { ["completed", "failed", "stopped"].contains($0["state"].string ?? "") }.prefix(10).enumerated()), id: \.offset) { _, value in
                Button { Task { await personal.openTask(value["task_id"].string) } } label: {
                    VStack(alignment: .leading) { Text(value["title"].string ?? "事项"); Text(value["state"].string ?? "").foregroundStyle(.secondary); if let summary = value["result"]["summary"].string { Text(summary) } }
                }
            }
        }
        Section("持续事项") {
            ForEach(Array(data["routines"].array.enumerated()), id: \.offset) { _, row in
                VStack(alignment: .leading) { Text(row["title"].string ?? row["goal"].string ?? "持续事项"); Text(row["status"].string ?? ""); if let next = row["binding"]["next_run_at"].string { Text("下次：" + next).foregroundStyle(.secondary) } }
            }
            if let error = data["routine_error"].string { Text(error).foregroundStyle(.secondary) }
        }
    }
    @ViewBuilder private var memories: some View {
        Section("共享记忆") {
            if data["records"].array.isEmpty { Text(data == .null ? "等待读取" : "没有匹配的记忆") }
            ForEach(Array(data["records"].array.enumerated()), id: \.offset) { _, record in
                VStack(alignment: .leading, spacing: 8) {
                    Text(record["status"].string == "forgotten" ? "已忘记；此记录不再包含记忆内容" : (record["content"].string ?? ""))
                    Text("\(record["availability"].string ?? record["status"].string ?? "") · \(record["evidence"].string == "inferred" ? "推断" : "明确记录")").font(.body).foregroundStyle(.secondary)
                    if let conflict = record["conflict"]["content"].string { Text("待解决的冲突：" + conflict) }
                    if let effective = record["effective_at"].string { Text("生效：" + effective).font(.body) }
                    if let expires = record["expires_at"].string { Text("到期：" + expires).font(.body) }
                    ForEach(Array(record["sources"].array.enumerated()), id: \.offset) { _, source in Text("来源：\(source["kind"].string ?? "") · \(source["ref"].string ?? "") · \(source["occurred_at"].string ?? "")").font(.body).textSelection(.enabled) }
                    if let author = record["contributor_agent_id"].string { Text("贡献助理：" + author).textSelection(.enabled) }
                    if let editor = record["updated_by_agent_id"].string { Text("最近更新：" + editor).textSelection(.enabled) }
                    ForEach(Array(record["contributions"].array.enumerated()), id: \.offset) { _, contribution in
                        DisclosureGroup("修订 \(Int(contribution["revision"].number ?? 0)) · \(contribution["operation"].string ?? "")") {
                            Text("贡献助理：" + (contribution["agent_id"].string ?? ""))
                            Text("执行来源：" + (contribution["executor_session_id"].string ?? "")).textSelection(.enabled)
                            Text("来源时间：" + (contribution["source_at"].string ?? ""))
                            Text("生效时间：" + (contribution["effective_at"].string ?? ""))
                            Text("记录时间：" + (contribution["written_at"].string ?? ""))
                            ForEach(Array(contribution["sources"].array.enumerated()), id: \.offset) { _, source in Text("来源：" + (source["ref"].string ?? "")).textSelection(.enabled) }
                        }
                    }
                    HStack {
                        Button("更正") { memoryEdit = record; memoryContent = record["content"].string ?? ""; editRequestID = UUID().uuidString }
                        Button("忘记", role: .destructive) { forget = record }
                    }.disabled(!enabled || record["status"].string == "forgotten")
                }
            }
        }
    }
    @ViewBuilder private var settings: some View {
        if data["settings"] != .null {
            Section("具名助理") {
                TextField("名称", text: $name)
                if data["agent"]["kind"].string == "specialist" { TextField("职责", text: $responsibility, axis: .vertical) }
                TextField("沟通方式", text: $communication, axis: .vertical).lineLimit(2...6)
                Text("状态：" + (data["agent"]["status"].string ?? ""))
                if data["agent"]["cancellation_pending"].bool { Text("停止请求尚待核对") }
                if let error = data["agent"]["cancellation_error"].string { Text(error).foregroundStyle(.secondary) }
                Button("保存助理设置") { Task {
                    guard let revision = data["agent"]["revision"].number else { return }
                    if await personal.control({ try await $0.updateAgent(expectedRevision: Int(revision), name: name, communication: communication, responsibility: data["agent"]["kind"].string == "specialist" ? responsibility : nil) }) != nil { await load(); await personal.reload() }
                } }.disabled(!enabled || name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                Button(data["agent"]["status"].string == "paused" ? "恢复助理" : "暂停助理") { agentPause = data["agent"] }
                    .disabled(!enabled || !["active", "paused"].contains(data["agent"]["status"].string ?? ""))
            }
            Section("Personal 通知") {
                Toggle("事项通知", isOn: $notifications)
                Picker("同一助理的通知聚合", selection: $notificationBatchSeconds) {
                    Text("关闭").tag(0); Text("1 分钟").tag(60); Text("5 分钟").tag(300); Text("15 分钟").tag(900)
                }
                Text("只合并通知，所有回复和事项结果仍保留在对话中。").foregroundStyle(.secondary)
                Toggle("安静时段", isOn: $quiet)
                TextField("开始 HH:mm", text: $start)
                TextField("结束 HH:mm", text: $end)
                TextField("IANA 时区", text: $timezone).textInputAutocapitalization(.never).autocorrectionDisabled()
                Text("这是 Personal 宿主的通知偏好。手机系统推送权限仍需在账号与系统设置中管理。").font(.body).foregroundStyle(.secondary)
                Button("保存设置") { Task {
                    guard let revision = data["settings"]["revision"].number else { return }
                    if let result = await personal.control({ try await $0.updateSettings(expectedRevision: Int(revision), notifications: notifications, quietHours: ["enabled": .bool(quiet), "start": .string(start), "end": .string(end), "timezone": .string(timezone)], notificationBatchSeconds: notificationBatchSeconds) }) {
                        data = ["settings": result["settings"], "agent": data["agent"]]; feedback = "已保存"
                    }
                } }.disabled(!enabled)
            }
        }
    }
    private func load(historyCursor: String? = nil) async {
        let section = section, query = query
        let dated = historyDated, historyStart = historyFrom, historyEnd = historyThrough
        let formatter = ISO8601DateFormatter()
        guard let result = await personal.control({ client in
            if section == 3 { return try await client.memory(query: query) }
            if section == 5 {
                guard !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return ["matches": []] }
                let lower = Calendar.current.startOfDay(for: historyStart)
                guard let upper = Calendar.current.date(byAdding: .day, value: 1, to: Calendar.current.startOfDay(for: historyEnd)) else { throw PersonalClientError.invalidResponse }
                return try await client.searchHistory(query: query, timeMin: dated ? formatter.string(from: lower) : nil, timeMax: dated ? formatter.string(from: upper) : nil, cursor: historyCursor)
            }
            if section == 4 {
                let settings = try await client.settings()
                guard let id = await client.selectedAgentID else { throw PersonalClientError.noAgentSelected }
                let agent = try await client.selectAgent(id)
                return ["settings": settings["settings"], "agent": agent["agent"]]
            }
            var result: [String: JSONValue] = [:]
            if section == 2 {
                result["tasks"] = try await client.listTasks()["tasks"]
                do { result["proposals"] = try await client.specialistProposals()["proposals"] } catch { result["proposal_error"] = .string(error.localizedDescription) }
                do { result["routines"] = try await client.routines()["routines"] } catch { result["routine_error"] = .string(error.localizedDescription) }
            }
            return .object(result)
        }) else { return }
        guard self.section == section else { return }
        if section == 5 && (self.query != query || historyDated != dated || historyFrom != historyStart || historyThrough != historyEnd) { return }
        data = result
        if section == 4 {
            name = result["agent"]["name"].string ?? ""; communication = result["agent"]["communication"].string ?? ""; responsibility = result["agent"]["responsibility"].string ?? ""
            notificationBatchSeconds = Int(result["settings"]["notification_batch_seconds"].number ?? 0)
            notifications = result["settings"]["notifications"].bool; quiet = result["settings"]["quiet_hours"]["enabled"].bool
            self.start = result["settings"]["quiet_hours"]["start"].string ?? "22:00"; self.end = result["settings"]["quiet_hours"]["end"].string ?? "08:00"; timezone = result["settings"]["quiet_hours"]["timezone"].string ?? "UTC"
        }
    }
}
