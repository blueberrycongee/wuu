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
    @State private var calendarID = "primary"
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
    @State private var confirmation: PersonalCalendarConfirmation?
    @State private var confirmationAttempted = false
    @State private var feedback: String?
    private var enabled: Bool { personal.available && !personal.controlBusy }

    var body: some View {
        List {
            Section {
                if section == 3 { TextField("搜索记忆", text: $query) }
                if section == 6 {
                    TextField("人物、事情或原话", text: $query)
                    Toggle("按消息日期筛选", isOn: $historyDated)
                    if historyDated {
                        DatePicker("从", selection: $historyFrom, displayedComponents: .date)
                        DatePicker("到（含当天）", selection: $historyThrough, displayedComponents: .date)
                    }
                    Text("搜索当前助理的持久主对话；可在上方切换助理。").foregroundStyle(.secondary)
                }
                if section == 5 { TextField("日历 ID", text: $calendarID).textInputAutocapitalization(.never).autocorrectionDisabled() }
                Button("刷新") { Task { await load() } }.disabled(!enabled)
                if let feedback { Text(feedback).foregroundStyle(.secondary) }
                if !personal.available { Text("离线时仅显示已经读取的内容，不能修改、确认或提交执行。").foregroundStyle(.secondary) }
            }
            if section == 2 { today }
            if section == 3 { memories }
            if section == 4 { settings }
            if section == 5 { calendar }
            if section == 6 {
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
        .onChange(of: query) { _, _ in if section == 6 { data = .null } }
        .onChange(of: historyDated) { _, _ in if section == 6 { data = .null } }
        .onChange(of: historyFrom) { _, _ in if section == 6 { data = .null } }
        .onChange(of: historyThrough) { _, _ in if section == 6 { data = .null } }
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
        .sheet(isPresented: Binding(get: { confirmation != nil }, set: { if !$0 { confirmation = nil } })) {
            NavigationStack {
                if let confirmation {
                    List {
                        calendarPreview(confirmation.preview)
                        Section {
                            Text("确认会按下列不可变预览向 Google Calendar 提交修改。刷新或修改事项后，必须重新审阅新的提案。")
                            if let error = personal.error { Text(error).foregroundStyle(.red) }
                            if confirmationAttempted {
                                Text("提交结果尚待核对。请读取状态，不会再次提交。")
                                Button("读取操作状态") { Task {
                                    if let result = await personal.control({ try await $0.calendarOperation(confirmation.preview["operation_id"].string ?? "") }) { feedback = calendarOutcome(result); self.confirmation = nil; await load() }
                                } }.disabled(!enabled)
                            }
                            Button("确认并提交日历修改") { confirmationAttempted = true; Task {
                                // Keep this snapshot and digest unchanged even if the list refreshes.
                                if let result = await personal.control({ try await $0.confirmCalendar(confirmation) }) {
                                    feedback = calendarOutcome(result); self.confirmation = nil; await load()
                                }
                            } }.disabled(!enabled || confirmationAttempted)
                        }
                    }.navigationTitle("确认日历修改").toolbar { Button("取消") { self.confirmation = nil } }
                }
            }
        }
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
            ForEach(Array(data["operations"].array.filter { ["proposed", "uncertain"].contains($0["status"].string ?? "") }.enumerated()), id: \.offset) { _, value in
                Button("日历：" + (value["fields"]["summary"].string ?? "待审阅操作")) { selectSection(5) }
            }
            if let error = data["proposal_error"].string { Text(error).foregroundStyle(.secondary) }
            if let error = data["operations_error"].string { Text(error).foregroundStyle(.secondary) }
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
        Section("今日日程") { agenda(data["agenda"]); if let error = data["calendar_error"].string { Text(error).foregroundStyle(.secondary) } }
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
    @ViewBuilder private var calendar: some View {
        Section("今日日程") {
            agenda(data["agenda"])
            Button("从已授权日历重新读取") { Task { await load(refreshCalendar: true) } }.disabled(!enabled)
            if let error = data["calendar_error"].string { Text(error).foregroundStyle(.secondary) }
        }
        Section("待确认与执行结果") {
            if let error = data["operations_error"].string { Text(error).foregroundStyle(.secondary) }
            ForEach(Array(data["operations"].array.enumerated()), id: \.offset) { _, operation in
                VStack(alignment: .leading, spacing: 8) {
                    Text(operation["fields"]["summary"].string ?? "日历修改")
                    Text(calendarOutcome(operation)).foregroundStyle(.secondary)
                    if operation["status"].string == "proposed" {
                        Button("审阅完整提案") { Task {
                            guard let result = await personal.control({ client in
                                let current = try await client.calendarOperation(operation["operation_id"].string ?? "")
                                let prepared = try await client.prepareCalendarConfirmation(current)
                                return prepared.preview
                            }) else { return }
                            _ = await personal.control { client in confirmation = try await client.prepareCalendarConfirmation(result); confirmationAttempted = false; return [:] }
                        } }.disabled(!enabled)
                    } else if ["uncertain", "running"].contains(operation["status"].string ?? "") {
                        Button("只读核对远端结果") { Task {
                            if let result = await personal.control({ try await $0.calendarOperation(operation["operation_id"].string ?? "", reconcile: true) }) { feedback = calendarOutcome(result); await load() }
                        } }.disabled(!enabled)
                    }
                    if let source = operation["result"]["source"]["verified_at"].string { Text("远端读取核验：" + source).font(.body) }
                }
            }
        }
    }
    @ViewBuilder private func agenda(_ value: JSONValue) -> some View {
        if value != .null {
            Text("状态：\(value["state"].string ?? "未知") · \(value["freshness"].string ?? "未知新鲜度")").foregroundStyle(.secondary)
            if let fetched = value["last_success_at"].string { Text("最近读取：" + fetched).font(.body) }
            Text("空列表或过期缓存不能证明这个时段有空。冲突检查只覆盖选定日历。").font(.body).foregroundStyle(.secondary)
            ForEach(Array(value["events"].array.enumerated()), id: \.offset) { _, event in
                VStack(alignment: .leading) { Text(event["summary"].string ?? "日程"); Text(calendarTime(event["start"]) + " → " + calendarTime(event["end"])).font(.body) }
            }
        }
    }
    @ViewBuilder private func calendarPreview(_ value: JSONValue) -> some View {
        Section("操作与归属") {
            Text(value["kind"].string == "reschedule" ? "改期" : "创建日程")
            Text("账号：" + (value["account_id"].string ?? "")); Text("日历：" + (value["calendar_id"].string ?? ""))
            Text("事项：\(value["task_id"].string ?? "") · 要求版本 \(Int(value["requirements_revision"].number ?? 0))")
        }
        if value["original_event"]["fields"] != .null { Section("修改前") { eventFields(value["original_event"]["fields"]) } }
        Section("将提交的内容") { eventFields(value["fields"]) }
        Section("通知与冲突") {
            Text("通知选项：" + (value["fields"]["send_updates"].string ?? ""))
            Text(value["notification_notice"].string ?? "")
            Text("检查时间：" + (value["availability"]["checked_at"].string ?? ""))
            Text("仅检查选定日历；冲突数：\(value["availability"]["conflicting_event_ids"].array.count)")
            ForEach(Array(value["availability"]["conflicting_event_ids"].array.enumerated()), id: \.offset) { _, id in Text(id.string ?? "") }
        }
    }
    @ViewBuilder private func eventFields(_ fields: JSONValue) -> some View {
        Text(fields["summary"].string ?? ""); Text("地点：" + (fields["location"].string ?? ""))
        Text("开始：" + calendarTime(fields["start"])); Text("结束：" + calendarTime(fields["end"]))
        ForEach(Array(fields["attendees"].array.enumerated()), id: \.offset) { _, attendee in Text((attendee["email"].string ?? "") + (attendee["optional"].bool ? "（可选）" : "（必需）")) }
        if fields["attendees"].array.isEmpty { Text("无受邀人") }
    }
    private func load(refreshCalendar: Bool = false, historyCursor: String? = nil) async {
        let section = section, query = query, calendarID = calendarID
        let dated = historyDated, historyStart = historyFrom, historyEnd = historyThrough
        let zone = TimeZone.current.identifier, start = Calendar.current.startOfDay(for: Date())
        let end = Calendar.current.date(byAdding: .day, value: 1, to: start) ?? start.addingTimeInterval(86_400)
        let formatter = ISO8601DateFormatter(), from = formatter.string(from: start), to = formatter.string(from: end)
        guard let result = await personal.control({ client in
            if section == 3 { return try await client.memory(query: query) }
            if section == 6 {
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
            do { result["agenda"] = try await client.calendarAgenda(calendarID: calendarID, start: from, end: to, timezone: zone, refresh: refreshCalendar) }
            catch { result["calendar_error"] = .string(error.localizedDescription) }
            if section == 5 || section == 2 {
                do { result["operations"] = try await client.calendarOperations()["operations"] } catch { result["operations_error"] = .string(error.localizedDescription) }
            }
            return .object(result)
        }) else { return }
        guard self.section == section else { return }
        if section == 6 && (self.query != query || historyDated != dated || historyFrom != historyStart || historyThrough != historyEnd) { return }
        data = result
        if section == 4 {
            name = result["agent"]["name"].string ?? ""; communication = result["agent"]["communication"].string ?? ""; responsibility = result["agent"]["responsibility"].string ?? ""
            notificationBatchSeconds = Int(result["settings"]["notification_batch_seconds"].number ?? 0)
            notifications = result["settings"]["notifications"].bool; quiet = result["settings"]["quiet_hours"]["enabled"].bool
            self.start = result["settings"]["quiet_hours"]["start"].string ?? "22:00"; self.end = result["settings"]["quiet_hours"]["end"].string ?? "08:00"; timezone = result["settings"]["quiet_hours"]["timezone"].string ?? "UTC"
        }
    }
}
private func calendarTime(_ value: JSONValue) -> String { (value["date_time"].string ?? value["date"].string ?? "时间未知") + " " + (value["timezone"].string ?? "") }
private func calendarOutcome(_ value: JSONValue) -> String {
    let state = value["status"].string ?? "未知"
    if state == "uncertain" || value["reconciliation_required"].bool { return "结果尚不确定，请只读核对远端状态。不会自动重复提交。" }
    if state == "completed" { return "远端日程已读取核验；通知是否送达仍未验证。" }
    return "状态：" + state + (value["error_code"].string.map { " · " + $0 } ?? "")
}
