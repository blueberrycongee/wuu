package ai.wuu.nativeapp

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import org.json.JSONObject
import org.json.JSONArray
import java.time.ZoneId
import java.time.ZonedDateTime
import java.util.UUID

/** Public client services only; no workspace/session lookup and no authority from cached state. */
@Composable fun PersonalToolsScreen(personal: PersonalModel, section: Int, modifier: Modifier = Modifier, selectSection: (Int) -> Unit) {
    val scope = rememberCoroutineScope()
    var proposal by remember { mutableStateOf<JSONObject?>(null) }
    var agentPause by remember { mutableStateOf<JSONObject?>(null) }
    var name by remember { mutableStateOf("") }
    var communication by remember { mutableStateOf("") }
    var responsibility by remember { mutableStateOf("") }
    var data by remember(section) { mutableStateOf<JSONObject?>(null) }
    var query by remember { mutableStateOf("") }
    var historyFrom by remember { mutableStateOf("") }
    var historyThrough by remember { mutableStateOf("") }
    var calendarID by remember { mutableStateOf("primary") }
    var notifications by remember { mutableStateOf(true) }
    var notificationBatchSeconds by remember { mutableStateOf(0) }
    var quiet by remember { mutableStateOf(false) }
    var start by remember { mutableStateOf("22:00") }
    var end by remember { mutableStateOf("08:00") }
    var timezone by remember { mutableStateOf(ZoneId.systemDefault().id) }
    var memoryEdit by remember { mutableStateOf<JSONObject?>(null) }
    var memoryContent by remember { mutableStateOf("") }
    var editRequestID by remember { mutableStateOf("") }
    var forget by remember { mutableStateOf<JSONObject?>(null) }
    var confirmation by remember { mutableStateOf<PersonalCalendarConfirmation?>(null) }
    var confirmationAttempted by remember { mutableStateOf(false) }
    var feedback by remember { mutableStateOf<String?>(null) }
    val enabled = personal.available && !personal.controlBusy
    LaunchedEffect(query, historyFrom, historyThrough, section) { if (section == 6) data = null }
    suspend fun load(refresh: Boolean = false, historyCursor: String? = null) {
        val searchQuery = query; val searchFrom = historyFrom; val searchThrough = historyThrough
        val zone = ZoneId.systemDefault(); val (from, to) = personalCalendarDayWindow(ZonedDateTime.now(zone).toLocalDate(), zone)
        val result = personal.control { client ->
            when (section) {
                3 -> client.memory(query)
                6 -> {
                    if (searchQuery.isBlank()) json("matches" to JSONArray()) else {
                        val lower = searchFrom.takeIf { it.isNotBlank() }?.let { personalCalendarDayWindow(java.time.LocalDate.parse(it), zone).first }
                        val upper = searchThrough.takeIf { it.isNotBlank() }?.let { personalCalendarDayWindow(java.time.LocalDate.parse(it), zone).second }
                        client.searchHistory(searchQuery, lower, upper, historyCursor)
                    }
                }
                4 -> json("settings" to client.settings().getJSONObject("settings"), "agent" to client.selectAgent(checkNotNull(client.selectedAgentID)).getJSONObject("agent"))
                else -> {
                    val value = JSONObject()
                    if (section == 2) {
                        value.put("tasks", client.listTasks().getJSONArray("tasks"))
                        try { value.put("proposals", client.specialistProposals().getJSONArray("proposals")) } catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (e: Exception) { value.put("proposal_error", e.message) }
                        try { value.put("routines", client.routines().getJSONArray("routines")) } catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (e: Exception) { value.put("routine_error", e.message) }
                    }
                    try { value.put("agenda", client.calendarAgenda(calendarID, from, to, zone.id, refresh)) }
                    catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (e: Exception) { value.put("calendar_error", e.message) }
                    if (section == 5 || section == 2) try { value.put("operations", client.calendarOperations().getJSONArray("operations")) } catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (e: Exception) { value.put("operations_error", e.message) }
                    value
                }
            }
        } ?: return
        if (section == 6 && (query != searchQuery || historyFrom != searchFrom || historyThrough != searchThrough)) return
        data = result
        if (section == 4) {
            val agent = result.getJSONObject("agent"); name = agent.optString("name"); communication = agent.optString("communication"); responsibility = agent.optString("responsibility")
            val settings = result.getJSONObject("settings"); val hours = settings.getJSONObject("quiet_hours")
            notificationBatchSeconds = settings.optInt("notification_batch_seconds", 0)
            notifications = settings.getBoolean("notifications"); quiet = hours.getBoolean("enabled"); start = hours.getString("start"); end = hours.getString("end"); timezone = hours.getString("timezone")
        }
    }
    LaunchedEffect(section, personal.available) {
        if (personal.available) {
            while (personal.controlBusy) kotlinx.coroutines.delay(50)
            load()
        }
    }
    LazyColumn(modifier.fillMaxWidth(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
        item {
            if (section == 3) OutlinedTextField(query, { query = it }, label = { Text("搜索记忆") }, modifier = Modifier.fillMaxWidth())
            if (section == 5) OutlinedTextField(calendarID, { calendarID = it }, label = { Text("日历 ID") }, modifier = Modifier.fillMaxWidth())
            TextButton(onClick = { scope.launch { load() } }, enabled = enabled) { Text("刷新") }
            feedback?.let { Text(it) }
            if (!personal.available) Text("离线时仅显示已经读取的内容，不能修改、确认或提交执行。")
        }
        when (section) {
            2 -> {
                item { Text("进行中的事项", style = MaterialTheme.typography.titleMedium) }
                val tasks = data?.optJSONArray("tasks")?.objects()?.filter { it.optString("state") !in listOf("completed", "failed", "stopped") } ?: emptyList()
                if (tasks.isEmpty()) item { Text(if (data == null) "等待读取" else "当前没有进行中的事项") }
                items(tasks) { task -> TextButton(onClick = { scope.launch { personal.openTask(task.getString("task_id")) } }) { Column { Text(task.optString("title")); Text(task.optString("state")) } } }
                item { Text("待决定", style = MaterialTheme.typography.titleMedium) }
                items(data?.optJSONArray("proposals")?.objects() ?: emptyList()) { value ->
                    TextButton(onClick = { proposal = JSONObject(value.toString()) }, enabled = enabled) { Text((if (value.optString("status") == "approved") "重试创建：" else "创建专属助理：") + value.optString("name")) }
                }
                items(data?.optJSONArray("operations")?.objects()?.filter { it.optString("status") in listOf("proposed", "uncertain") } ?: emptyList()) { value -> TextButton(onClick = { selectSection(5) }) { Text("日历：${value.optJSONObject("fields")?.optString("summary") ?: "待审阅操作"}") } }
                data?.optString("proposal_error")?.takeIf { it.isNotEmpty() }?.let { message -> item { Text(message) } }
                data?.optString("operations_error")?.takeIf { it.isNotEmpty() }?.let { message -> item { Text(message) } }
                item { Text("最近结果", style = MaterialTheme.typography.titleMedium) }
                items(data?.optJSONArray("tasks")?.objects()?.filter { it.optString("state") in listOf("completed", "failed", "stopped") }?.take(10) ?: emptyList()) { value ->
                    TextButton(onClick = { scope.launch { personal.openTask(value.getString("task_id")) } }) { Column { Text(value.optString("title")); Text(value.optString("state")); value.optJSONObject("result")?.let { Text(it.optString("summary")) } } }
                }
                item { Text("持续事项", style = MaterialTheme.typography.titleMedium) }
                items(data?.optJSONArray("routines")?.objects() ?: emptyList()) { routine -> Column { Text(routine.optString("title").ifEmpty { routine.optString("goal", "持续事项") }); Text(routine.optString("status")); routine.optJSONObject("binding")?.optString("next_run_at")?.takeIf { it.isNotEmpty() }?.let { Text("下次：$it") } } }
                data?.optString("routine_error")?.takeIf { it.isNotEmpty() }?.let { message -> item { Text(message) } }
                item { Text("今日日程", style = MaterialTheme.typography.titleMedium); PersonalAgenda(data?.optJSONObject("agenda")); data?.optString("calendar_error")?.let { Text(it) } }
            }
            6 -> {
                item {
                    OutlinedTextField(query, { query = it }, label = { Text("人物、事情或原话") })
                    OutlinedTextField(historyFrom, { historyFrom = it }, label = { Text("从 YYYY-MM-DD（可选）") })
                    OutlinedTextField(historyThrough, { historyThrough = it }, label = { Text("到 YYYY-MM-DD，含当天（可选）") })
                    Text("搜索当前助理的持久主对话；可在上方切换助理。")
                }
                val matches = data?.optJSONArray("matches")?.objects() ?: emptyList()
                items(matches) { match -> TextButton(onClick = { selectSection(0); scope.launch { personal.showSource(match.getLong("seq")) } }) {
                    Column { Text(match.optString("excerpt")); Text(match.optString("at", "时间未知")) }
                } }
                if (data != null && matches.isEmpty()) item { Text("没有匹配的消息") }
                if (data?.optBoolean("has_more") == true) data?.optString("next_cursor")?.takeIf { it.isNotBlank() }?.let { cursor -> item { TextButton(onClick = { scope.launch { load(historyCursor = cursor) } }, enabled = enabled) { Text("更早的匹配") } } }
            }
            3 -> {
                item { Text("共享记忆", style = MaterialTheme.typography.titleMedium) }
                val records = data?.optJSONArray("records")?.objects() ?: emptyList()
                if (records.isEmpty()) item { Text(if (data == null) "等待读取" else "没有匹配的记忆") }
                items(records) { record -> Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(if (record.optString("status") == "forgotten") "已忘记；此记录不再包含记忆内容" else record.optString("content")); Text("${record.optString("availability", record.optString("status"))} · ${if (record.optString("evidence") == "inferred") "推断" else "明确记录"}", style = MaterialTheme.typography.bodyMedium)
                    record.optJSONObject("conflict")?.let { Text("待解决的冲突：${it.optString("content")}") }
                    if (record.has("effective_at")) Text("生效：${record.optString("effective_at")}", style = MaterialTheme.typography.bodyMedium)
                    if (record.has("expires_at")) Text("到期：${record.optString("expires_at")}", style = MaterialTheme.typography.bodyMedium)
                    record.optJSONArray("sources")?.objects()?.forEach { Text("来源：${it.optString("kind")} · ${it.optString("ref")} · ${it.optString("occurred_at")}", style = MaterialTheme.typography.bodyMedium) }
                    record.optString("contributor_agent_id").takeIf { it.isNotEmpty() }?.let { Text("贡献助理：$it") }
                    record.optString("updated_by_agent_id").takeIf { it.isNotEmpty() }?.let { Text("最近更新：$it") }
                    record.optJSONArray("contributions")?.objects()?.forEach { contribution ->
                        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            Text("修订 ${contribution.optInt("revision")} · ${contribution.optString("operation")}")
                            Text("贡献助理：${contribution.optString("agent_id")}")
                            Text("执行来源：${contribution.optString("executor_session_id")}")
                            Text("来源时间：${contribution.optString("source_at")}")
                            Text("生效时间：${contribution.optString("effective_at")}")
                            Text("记录时间：${contribution.optString("written_at")}")
                            contribution.optJSONArray("sources")?.objects()?.forEach { Text("来源：${it.optString("ref")}") }
                        }
                    }
                    Row {
                        TextButton(onClick = { memoryEdit = JSONObject(record.toString()); memoryContent = record.optString("content"); editRequestID = UUID.randomUUID().toString() }, enabled = enabled && record.optString("status") != "forgotten") { Text("更正") }
                        TextButton(onClick = { forget = JSONObject(record.toString()) }, enabled = enabled && record.optString("status") != "forgotten") { Text("忘记") }
                    }
                } }
            }
            4 -> data?.optJSONObject("settings")?.let { settings ->
                item {
                    val agent = data?.optJSONObject("agent")
                    Text("具名助理", style = MaterialTheme.typography.titleMedium)
                    OutlinedTextField(name, { name = it }, label = { Text("名称") })
                    if (agent?.optString("kind") == "specialist") OutlinedTextField(responsibility, { responsibility = it }, label = { Text("职责") })
                    OutlinedTextField(communication, { communication = it }, label = { Text("沟通方式") }, minLines = 2)
                    Text("状态：${agent?.optString("status").orEmpty()}")
                    if (agent?.optBoolean("cancellation_pending") == true) Text("停止请求尚待核对")
                    agent?.optString("cancellation_error")?.takeIf { it.isNotEmpty() }?.let { Text(it) }
                    TextButton(onClick = { scope.launch {
                        if (agent != null && personal.control { it.updateAgent(agent.getLong("revision"), name, communication, if (agent.optString("kind") == "specialist") responsibility else null) } != null) { load(); personal.reload() }
                    } }, enabled = enabled && name.isNotBlank()) { Text("保存助理设置") }
                    TextButton(onClick = { agentPause = agent?.let { JSONObject(it.toString()) } }, enabled = enabled && agent?.optString("status") in listOf("active", "paused")) { Text(if (agent?.optString("status") == "paused") "恢复助理" else "暂停助理") }
                    Text("Personal 通知", style = MaterialTheme.typography.titleMedium)
                    Row { Checkbox(notifications, { notifications = it }); Text("事项通知") }
                    Text("同一助理的通知聚合")
                    listOf(0 to "关闭", 60 to "1 分钟", 300 to "5 分钟", 900 to "15 分钟").forEach { (seconds, label) ->
                        Row { RadioButton(selected = notificationBatchSeconds == seconds, onClick = { notificationBatchSeconds = seconds }); Text(label) }
                    }
                    Text("只合并通知，所有回复和事项结果仍保留在对话中。")
                    Row { Checkbox(quiet, { quiet = it }); Text("安静时段") }
                    OutlinedTextField(start, { start = it }, label = { Text("开始 HH:mm") })
                    OutlinedTextField(end, { end = it }, label = { Text("结束 HH:mm") })
                    OutlinedTextField(timezone, { timezone = it }, label = { Text("IANA 时区") })
                    Text("这是 Personal 宿主的通知偏好。手机系统推送权限仍需在账号与系统设置中管理。", style = MaterialTheme.typography.bodyMedium)
                    TextButton(onClick = { scope.launch {
                        personal.control { it.updateSettings(settings.getLong("revision"), notifications, json("enabled" to quiet, "start" to start, "end" to end, "timezone" to timezone), notificationBatchSeconds) }?.let { data = json("settings" to it.getJSONObject("settings"), "agent" to data?.optJSONObject("agent")); feedback = "已保存" }
                    } }, enabled = enabled) { Text("保存设置") }
                }
            }
            5 -> {
                item {
                    Text("今日日程", style = MaterialTheme.typography.titleMedium); PersonalAgenda(data?.optJSONObject("agenda"))
                    TextButton(onClick = { scope.launch { load(true) } }, enabled = enabled) { Text("从已授权日历重新读取") }
                    data?.optString("calendar_error")?.let { Text(it) }
                    Text("待确认与执行结果", style = MaterialTheme.typography.titleMedium)
                    data?.optString("operations_error")?.let { Text(it) }
                }
                items(data?.optJSONArray("operations")?.objects() ?: emptyList()) { operation -> Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(operation.optJSONObject("fields")?.optString("summary") ?: "日历修改"); Text(personalCalendarOutcome(operation))
                    if (operation.optString("status") == "proposed") TextButton(onClick = { scope.launch {
                        val preview = personal.control { it.calendarOperation(operation.getString("operation_id")) } ?: return@launch
                        personal.control { confirmation = it.prepareCalendarConfirmation(preview); confirmationAttempted = false; JSONObject() }
                    } }, enabled = enabled) { Text("审阅完整提案") }
                    else if (operation.optString("status") in listOf("uncertain", "running")) TextButton(onClick = { scope.launch {
                        personal.control { it.calendarOperation(operation.getString("operation_id"), true) }?.let { feedback = personalCalendarOutcome(it); load() }
                    } }, enabled = enabled) { Text("只读核对远端结果") }
                    operation.optJSONObject("result")?.optJSONObject("source")?.optString("verified_at")?.let { Text("远端读取核验：$it", style = MaterialTheme.typography.bodyMedium) }
                } }
            }
        }
    }
    proposal?.let { value -> AlertDialog(onDismissRequest = { proposal = null }, title = { Text("创建具名专属助理？") }, text = {
        Text("${value.optString("name")}\n职责：${value.optString("responsibility")}\n沟通方式：${value.optString("communication")}\n这个不可变提案会建立独立具名助理及其持久主对话。")
    }, confirmButton = { TextButton(onClick = { proposal = null; scope.launch {
        if (personal.control { it.decideSpecialist(value.getString("proposal_id"), true) } != null) { load(); personal.reload() }
    } }, enabled = enabled) { Text(if (value.optString("status") == "approved") "重试原创建请求" else "确认创建") } }, dismissButton = { Row {
        if (value.optString("status") == "pending") TextButton(onClick = { proposal = null; scope.launch { if (personal.control { it.decideSpecialist(value.getString("proposal_id"), false) } != null) load() } }, enabled = enabled) { Text("暂不创建") }
        TextButton(onClick = { proposal = null }) { Text("取消") }
    } }) }
    agentPause?.let { value -> AlertDialog(onDismissRequest = { agentPause = null }, title = { Text("更新助理运行状态？") }, text = { Text("暂停会请求停止持续工作，可能先进入正在停止状态。已有历史仍保留，实际状态以宿主为准。") }, confirmButton = { TextButton(onClick = { agentPause = null; scope.launch {
        if (personal.control { it.setAgentPaused(value.optString("status") != "paused", value.getLong("revision")) } != null) { load(); personal.reload() }
    } }, enabled = enabled) { Text(if (value.optString("status") == "paused") "恢复助理" else "暂停助理") } }, dismissButton = { TextButton(onClick = { agentPause = null }) { Text("取消") } }) }
    memoryEdit?.let { record -> AlertDialog(onDismissRequest = { memoryEdit = null }, title = { Text("更正共享记忆") }, text = {
        Column { OutlinedTextField(memoryContent, { memoryContent = it }, minLines = 3); Text("将作为你的明确更正供所有 Personal 助理使用。原始聊天和文件不会改写。"); personal.error?.let { Text(it) } }
    }, confirmButton = { TextButton(onClick = { scope.launch {
        if (personal.control { it.correctMemory(record.getString("id"), record.getLong("revision"), memoryContent, editRequestID) } != null) { memoryEdit = null; load() }
    } }, enabled = enabled && memoryContent.isNotBlank()) { Text("保存更正") } }, dismissButton = { TextButton(onClick = { memoryEdit = null }) { Text("取消") } }) }
    forget?.let { record -> AlertDialog(onDismissRequest = { forget = null }, title = { Text("忘记这条共享记忆？") }, text = { Text("所有 Personal 助理将停止使用此记忆。原始聊天、文件、导出和备份仍保留；不能保证识别所有不同表述。") }, confirmButton = { TextButton(onClick = {
        forget = null; scope.launch { personal.control { it.forgetMemory(record.getString("id"), record.getLong("revision")) }?.let { feedback = it.optJSONObject("retention")?.optString("history") ?: "记忆已移除，原始资料仍保留"; load() } }
    }, enabled = enabled) { Text("忘记记忆") } }, dismissButton = { TextButton(onClick = { forget = null }) { Text("取消") } }) }
    confirmation?.let { frozen ->
        androidx.compose.ui.window.Dialog(onDismissRequest = { confirmation = null }, properties = androidx.compose.ui.window.DialogProperties(usePlatformDefaultWidth = false)) {
            Surface(Modifier.fillMaxSize()) {
                Column(Modifier.fillMaxSize().padding(16.dp)) {
                    Text("确认日历修改", style = MaterialTheme.typography.titleLarge)
                    LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        val value = frozen.preview
                        item { Text(if (value.optString("kind") == "reschedule") "改期" else "创建日程"); Text("账号：${value.optString("account_id")}"); Text("日历：${value.optString("calendar_id")}"); Text("事项：${value.optString("task_id")} · 要求版本 ${value.optLong("requirements_revision")}") }
                        value.optJSONObject("original_event")?.optJSONObject("fields")?.let { fields -> item { Text("修改前"); PersonalEventFields(fields) } }
                        item { Text("将提交的内容"); PersonalEventFields(value.getJSONObject("fields")) }
                        item {
                            Text("通知选项：${value.getJSONObject("fields").optString("send_updates")}"); Text(value.optString("notification_notice"))
                            val availability = value.getJSONObject("availability"); Text("检查时间：${availability.optString("checked_at")}")
                            val conflicts = availability.getJSONArray("conflicting_event_ids"); Text("仅检查选定日历；冲突数：${conflicts.length()}"); repeat(conflicts.length()) { Text(conflicts.optString(it)) }
                            Text("确认会按此不可变预览向 Google Calendar 提交修改。刷新或修改事项后，必须重新审阅新的提案。")
                            personal.error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                        }
                    }
                    if (confirmationAttempted) {
                        Text("提交结果尚待核对。请读取状态，不会再次提交。")
                        TextButton(onClick = { scope.launch {
                            personal.control { it.calendarOperation(frozen.preview.getString("operation_id")) }?.let { feedback = personalCalendarOutcome(it); confirmation = null; load() }
                        } }, enabled = enabled) { Text("读取操作状态") }
                    }
                    Button(onClick = { confirmationAttempted = true; scope.launch {
                        personal.control { it.confirmCalendar(frozen) }?.let { feedback = personalCalendarOutcome(it); confirmation = null; load() }
                    } }, enabled = enabled && !confirmationAttempted) { Text("确认并提交日历修改") }
                    TextButton(onClick = { confirmation = null }) { Text("取消") }
                }
            }
        }
    }
}
@Composable private fun PersonalAgenda(value: JSONObject?) {
    if (value == null) return
    Text("状态：${value.optString("state", "未知")} · ${value.optString("freshness", "未知新鲜度")}")
    value.optString("last_success_at").takeIf { it.isNotEmpty() }?.let { Text("最近读取：$it", style = MaterialTheme.typography.bodyMedium) }
    Text("空列表或过期缓存不能证明这个时段有空。冲突检查只覆盖选定日历。", style = MaterialTheme.typography.bodyMedium)
    value.optJSONArray("events")?.objects()?.forEach { event -> Text(event.optString("summary", "日程")); Text("${personalCalendarTime(event.optJSONObject("start"))} → ${personalCalendarTime(event.optJSONObject("end"))}", style = MaterialTheme.typography.bodyMedium) }
}
@Composable private fun PersonalEventFields(fields: JSONObject) {
    Text(fields.optString("summary")); Text("地点：${fields.optString("location")}")
    Text("开始：${personalCalendarTime(fields.optJSONObject("start"))}"); Text("结束：${personalCalendarTime(fields.optJSONObject("end"))}")
    val attendees = fields.getJSONArray("attendees").objects()
    if (attendees.isEmpty()) Text("无受邀人")
    attendees.forEach { Text(it.optString("email") + if (it.optBoolean("optional")) "（可选）" else "（必需）") }
}
private fun personalCalendarTime(value: JSONObject?): String = if (value == null) "时间未知" else value.optString("date_time").ifEmpty { value.optString("date", "时间未知") } + " " + value.optString("timezone")
private fun personalCalendarOutcome(value: JSONObject): String {
    val state = value.optString("status", "未知")
    if (state == "uncertain" || value.optBoolean("reconciliation_required")) return "结果尚不确定，请只读核对远端状态。不会自动重复提交。"
    if (state == "completed") return "远端日程已读取核验；通知是否送达仍未验证。"
    return "状态：$state" + value.optString("error_code").takeIf { it.isNotEmpty() }?.let { " · $it" }.orEmpty()
}
