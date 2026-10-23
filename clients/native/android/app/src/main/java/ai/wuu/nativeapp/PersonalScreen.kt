package ai.wuu.nativeapp

import androidx.compose.foundation.Image
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.layout.boundsInWindow
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.CalendarMonth
import androidx.compose.material.icons.filled.ChatBubbleOutline
import androidx.compose.material.icons.filled.Checklist
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Psychology
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.StopCircle
import androidx.compose.material.icons.filled.Today
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import kotlinx.coroutines.launch
import org.json.JSONObject

/** In-memory state scoped to one authenticated account and host; never part of Coding history. */
class PersonalModel(initialClient: PersonalClient? = null) {
    var active by mutableStateOf(false)
    var available by mutableStateOf(false); private set
    var busy by mutableStateOf(false); private set
    var controlBusy by mutableStateOf(false); private set
    val contextID get() = generation
    var agents by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var agent by mutableStateOf<JSONObject?>(null); private set
    var records by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var tasks by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var detail by mutableStateOf<JSONObject?>(null); private set
    var taskRecords by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var hasEarlier by mutableStateOf(false); private set
    private var firstSequence = 1L
    var text by mutableStateOf("")
    var attachments by mutableStateOf<List<InputAttachment>>(emptyList())
    var composerID by mutableStateOf(0L); private set
    var loadingAttachment by mutableStateOf(false); private set
    private var cache: PersonalHistoryCache? = null
    private var cacheIdentity: List<String>? = null
    private var draftStore: PersonalDraftStore? = null
    private var restoredDraft by mutableStateOf<PersonalStoredDraft?>(null)
    private var draftRecoveryBlocked by mutableStateOf(false)
    val hasPending get() = pending != null || restoredDraft != null || draftRecoveryBlocked
    var error by mutableStateOf<String?>(null); private set
    var notice by mutableStateOf<String?>(null); private set
    var pending by mutableStateOf<PersonalMessageDraft?>(null); private set
    var messageSequence by mutableStateOf<Long?>(null); private set
    private var client: PersonalClient? = initialClient
    private var generation = 0L
    private var desiredAgent: String? = null
    private var desiredTask: String? = null
    val selectedID get() = agent?.optString("agent_id")
    val canSend get() = available && !busy && !controlBusy && !draftRecoveryBlocked && selectedID != null && agent?.optString("status") == "active"

    suspend fun configureCache(directory: File, draftDirectory: File, identity: List<String>) {
        if (cacheIdentity == identity) return
        cache?.invalidate(false); draftStore?.invalidate(false); cacheIdentity = identity
        val next = PersonalHistoryCache(directory, identity); cache = next
        val drafts = PersonalDraftStore(draftDirectory, identity); draftStore = drafts
        val stamp = generation
        try {
            val recovered = withContext(Dispatchers.IO) { drafts.load() }
            if (generation != stamp || draftStore !== drafts) return
            restoredDraft = recovered; draftRecoveryBlocked = false
            if (recovered != null) { desiredAgent = recovered.agentID; text = recovered.prompt; attachments = recovered.attachments; notice = "已恢复尚未确认的原消息。连接后可明确重试；不会自动发送。" }
            val snapshot = withContext(Dispatchers.IO) { next.load(recovered?.agentID) }
            if (snapshot != null && generation == stamp && cache === next) {
                agent = snapshot.getJSONObject("agent"); desiredAgent = agent!!.getString("agent_id"); agents = listOf(agent!!)
                records = snapshot.getJSONArray("records").objects(); firstSequence = records.firstOrNull()?.optLong("seq") ?: 1
                hasEarlier = firstSequence > 1; if (recovered == null) notice = "显示此账号和宿主的缓存历史；连接后才能发送或读取附件。"
            }
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (generation == stamp) { draftRecoveryBlocked = true; error = e.message; notice = "本机未确认消息无法恢复，发送已暂停。请在宿主核对历史后放下本机重试。" } }
    }
    suspend fun inventoryChanged(event: JSONObject) {
        val stamp = ++generation; controlBusy = false; busy = false; loadingAttachment = false
        try {
            val adapter = client ?: throw PersonalClientException(PersonalClientFailure.UNAVAILABLE)
            adapter.refreshInventory(event)
            if (generation != stamp) return
            available = true; reload()
        } catch (e: Exception) { if (generation == stamp) { available = false; error = e.message } }
    }
    suspend fun loadAttachment(descriptor: JSONObject, sequence: Long, task: JSONObject? = null): LoadedAttachment? {
        val adapter = client ?: return null
        if (!available || loadingAttachment) return null
        loadingAttachment = true; val stamp = generation
        return try {
            val result = adapter.attachment(sequence, descriptor, task?.getString("task_id"), task?.getLong("requirements_revision"))
            if (generation == stamp) result else null
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (generation == stamp) error = e.message; null }
        finally { if (generation == stamp) loadingAttachment = false }
    }
    suspend fun bind(remote: Remote, initialized: JSONObject) {
        val stamp = ++generation; controlBusy = false; busy = false; loadingAttachment = false
        val adapter = client ?: PersonalClient(remote).also { client = it }
        try {
            adapter.reconnect(remote, initialized)
            if (generation != stamp) return
            available = true; error = null
            if (active) reload()
        } catch (e: Exception) {
            if (generation == stamp) { available = false; busy = false; error = e.message }
        }
    }
    fun disconnect(reason: String? = null) {
        if (reason != null) error = reason
        generation++; controlBusy = false; available = false; busy = false; loadingAttachment = false; client?.invalidate()
        if (pending != null) notice = "发送结果尚未确认。重新连接后可重试同一条消息，不会新建聊天。"
    }
    fun reset(removeCache: Boolean = false): String? {
        val oldDrafts = draftStore; val oldCache = cache
        // Revocation must close authority even when private-storage cleanup fails.
        disconnect(); client = null; active = false
        draftStore = null; restoredDraft = null; draftRecoveryBlocked = false
        cache = null; cacheIdentity = null; attachments = emptyList(); composerID++
        agents = emptyList(); agent = null; records = emptyList(); tasks = emptyList(); detail = null; taskRecords = emptyList()
        desiredAgent = null; desiredTask = null; messageSequence = null; pending = null; text = ""; error = null; notice = null
        val warnings = mutableListOf<String>()
        try { oldDrafts?.invalidate(removeCache) } catch (e: Exception) { warnings += e.message ?: "无法清除未确认消息" }
        try { oldCache?.invalidate(removeCache) } catch (e: Exception) { warnings += e.message ?: "无法清除缓存历史" }
        return warnings.takeIf { it.isNotEmpty() }?.joinToString("；")
    }
    suspend fun locate(location: PersonalLocation) {
        if (hasPending && (pending?.agentID ?: restoredDraft?.agentID) != location.agentID) { error = "请先重试或放下尚未确认的消息，再切换助理。"; return }
        desiredAgent = location.agentID; desiredTask = location.taskID; messageSequence = location.messageSequence
        agent = null; records = emptyList(); tasks = emptyList(); detail = null; taskRecords = emptyList()
        if (!hasPending) { text = ""; attachments = emptyList(); composerID++ }
        generation++; controlBusy = false; busy = false; active = true; loadingAttachment = false
        if (available) reload()
    }
    suspend fun choose(id: String) {
        if (hasPending) { error = "请先重试或放下尚未确认的消息，再切换助理。"; return }
        desiredAgent = id; desiredTask = null; messageSequence = null
        agent = null; records = emptyList(); tasks = emptyList(); detail = null; taskRecords = emptyList(); text = ""; attachments = emptyList(); composerID++
        generation++; controlBusy = false; busy = false; loadingAttachment = false
        if (!available) {
            try { cache?.load(id)?.let { snapshot ->
                agent = snapshot.getJSONObject("agent"); records = snapshot.getJSONArray("records").objects()
                firstSequence = records.firstOrNull()?.optLong("seq") ?: 1; hasEarlier = firstSequence > 1
            } } catch (e: Exception) { error = e.message }
            return
        }
        reload()
    }
    suspend fun reload() {
        val adapter = client ?: return
        if (!active || !available || busy || controlBusy || loadingAttachment) return
        busy = true; error = null; val stamp = generation
        try {
            val listed = adapter.listAgents()
            if (generation != stamp) return
            var values = listed.getJSONArray("agents").objects()
            val target = desiredAgent ?: values.firstOrNull { it.optString("kind") == "main" }?.getString("agent_id") ?: run {
                val created = adapter.ensureDefaultAgent().getJSONObject("agent")
                values = values + created; created.getString("agent_id")
            }
            val opened = adapter.selectAgent(target)
            restoredDraft?.let { stored ->
                val recovered = adapter.restoreDraft(stored)
                if (generation != stamp) return
                pending = recovered; restoredDraft = null
            }
            val history = adapter.history(messageSequence)
            val matters = adapter.listTasks()
            val task = desiredTask?.let { adapter.task(it).getJSONObject("task") }
            val historyRecords = desiredTask?.let { personalDisplayRecords(adapter.taskHistory(it, task?.getLong("requirements_revision")).getJSONArray("records").objects()) } ?: emptyList()
            if (generation != stamp) return
            val keepEarlier = selectedID == target && messageSequence == null
            val pageStart = history.optLong("first_seq", 1)
            records = (if (keepEarlier) records.filter { it.optLong("seq") < pageStart } else emptyList()) + personalDisplayRecords(history.getJSONArray("records").objects())
            hasEarlier = if (keepEarlier && firstSequence < pageStart) hasEarlier else history.optBoolean("has_earlier")
            firstSequence = if (keepEarlier) minOf(firstSequence, pageStart) else pageStart
            agents = values; agent = opened.getJSONObject("agent"); desiredAgent = target
            tasks = matters.getJSONArray("tasks").objects(); detail = task; taskRecords = historyRecords
            if (messageSequence == null) cache?.let { store -> val shown = agent!!; val historyRows = records; withContext(Dispatchers.IO) { store.save(shown, historyRows) } }
            if (generation != stamp) return
            if (messageSequence != null && records.none { it.optLong("seq") == messageSequence }) notice = "原消息暂时无法定位。仍保留原助理，不会创建新聊天。"
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) {
            if (generation == stamp) {
                error = e.message
                if (e is PersonalClientException && e.reason in listOf(PersonalClientFailure.UNAVAILABLE, PersonalClientFailure.ISOLATION_REQUIRED)) available = false
            }
        } finally { if (generation == stamp) busy = false }
    }
    suspend fun earlier() {
        val adapter = client ?: return
        if (!available || busy || !hasEarlier) return
        busy = true; val stamp = generation
        try {
            val history = adapter.history(firstSequence - 1)
            if (generation != stamp) return
            val existing = records.map { it.optLong("seq") }.toSet()
            records = history.getJSONArray("records").objects().filter { it.optLong("seq") !in existing } + records
            hasEarlier = history.optBoolean("has_earlier"); firstSequence = history.optLong("first_seq", 1)
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (generation == stamp) error = e.message }
        finally { if (generation == stamp) busy = false }
    }
    suspend fun send() {
        val adapter = client ?: return
        if (!canSend) return
        busy = true; error = null; val stamp = generation
        var submitted = false
        try {
            val draft = pending ?: adapter.prepareMessage(text, attachments).also { pending = it }
            val drafts = checkNotNull(draftStore) { "无法保存原消息，尚未发送" }
            withContext(Dispatchers.IO) { drafts.save(draft) }
            if (generation != stamp) return
            val result = adapter.send(draft)
            if (generation != stamp) return
            if (!result.has("state") && !result.has("turn_id") && !result.has("queue_id")) throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
            withContext(Dispatchers.IO) { drafts.clear() }
            if (generation != stamp) return
            submitted = true; pending = null; restoredDraft = null; text = ""; attachments = emptyList(); composerID++; notice = "消息状态：${result.optString("state", "已提交")}，结果以助理历史为准。"; messageSequence = null
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (generation == stamp) { error = e.message; notice = if (pending == null) "消息尚未发送。" else "发送结果尚未确认。可重试同一条消息；不会自动重发。" } }
        finally { if (generation == stamp) busy = false }
        if (generation == stamp && submitted) reload()
    }
    suspend fun abandonPending() {
        val stamp = generation
        try {
            val drafts = draftStore; withContext(Dispatchers.IO) { drafts?.clear() }
            if (generation != stamp) return
            pending = null; restoredDraft = null; draftRecoveryBlocked = false; attachments = emptyList(); composerID++; text = ""; notice = "已放下本机重试。原请求仍可能已被接收，请先检查历史。"
        } catch (e: CancellationException) { throw e } catch (e: Exception) { if (generation == stamp) error = e.message }
    }
    suspend fun stopReply() {
        val adapter = client ?: return
        if (!canSend) return
        busy = true; val stamp = generation
        try { adapter.stopReply() }
        catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (generation == stamp) error = e.message }
        finally { if (generation == stamp) busy = false }
        if (generation == stamp) reload()
    }
    suspend fun openTask(id: String?) { generation++; controlBusy = false; busy = false; loadingAttachment = false; desiredTask = id; detail = null; taskRecords = emptyList(); reload() }
    suspend fun showSource(seq: Long) { generation++; controlBusy = false; busy = false; loadingAttachment = false; desiredTask = null; messageSequence = seq; reload() }
    suspend fun control(operation: suspend (PersonalClient) -> JSONObject): JSONObject? {
        val adapter = client ?: return null
        if (!available || controlBusy) return null
        val stamp = generation; controlBusy = true; error = null
        return try { val result = operation(adapter); if (stamp == generation) result else null }
        catch (e: CancellationException) { throw e }
        catch (e: Exception) { if (stamp == generation) error = e.message; null }
        finally { if (stamp == generation) controlBusy = false }
    }
    suspend fun changeTask(snapshot: JSONObject, action: PersonalTaskAction, goal: String? = null) {
        val result = control { it.changeTask(snapshot.getString("task_id"), snapshot.getLong("revision"), action, goal) } ?: return
        detail = result.getJSONObject("task"); notice = "事项状态：${detail!!.optString("state")}。停止和恢复可能需要等待正在结束的工作。"
        reload()
    }
    suspend fun latest() { messageSequence = null; reload() }
}

private fun personalAgentLabel(agent: JSONObject?): String {
    val name = agent?.optString("name")?.takeIf { it.isNotEmpty() } ?: "助理"
    val tag = agent?.optString("responsibility").orEmpty()
    return if (agent?.optString("kind") == "specialist" && tag.isNotEmpty()) "$name · $tag" else name
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable fun PersonalScreen(model: AppModel) {
    val personal = model.personal
    val scope = rememberCoroutineScope()
    var section by remember { mutableIntStateOf(0) }
    var agentsOpen by remember { mutableStateOf(false) }
    var activityOpen by remember { mutableStateOf(false) }
    var discard by remember { mutableStateOf(false) }
    var editingTask by remember(personal.contextID) { mutableStateOf<JSONObject?>(null) }
    var taskGoal by remember { mutableStateOf("") }
    var taskAction by remember { mutableStateOf(PersonalTaskAction.UPDATE) }
    val list = rememberLazyListState()
    LaunchedEffect(personal.active, personal.available) {
        if (personal.active && personal.available) while (true) { personal.reload(); delay(3_000) }
    }
    LaunchedEffect(personal.detail?.optString("task_id")) { if (personal.detail != null) section = 1 }
    LaunchedEffect(personal.records, personal.messageSequence) {
        val index = personal.records.indexOfFirst { it.optLong("seq") == personal.messageSequence }
        if (index >= 0) list.scrollToItem(index + (if (personal.hasEarlier) 1 else 0) + (if (personal.messageSequence != null) 1 else 0))
    }
    PersonalMessageActionsHost(context = "${personal.contextID}-$section-${personal.active}-$activityOpen", reply = if (personal.canSend && !personal.hasPending) { quote ->
        personal.text += (if (personal.text.isBlank()) "" else "\n\n") + quote.lines().joinToString("\n") { "> $it" } + "\n\n"
    } else null) {
    Scaffold(topBar = { CenterAlignedTopAppBar(title = {
        Box {
            TextButton(onClick = { agentsOpen = true }, enabled = !personal.busy && personal.agents.isNotEmpty()) {
                PersonalIdentityTitle(personalAgentLabel(personal.agent), personalStatusText(personal), personal.busy || personal.controlBusy)
            }
            DropdownMenu(expanded = agentsOpen, onDismissRequest = { agentsOpen = false }) {
                personal.agents.forEach { agent -> DropdownMenuItem(text = { Text(personalAgentLabel(agent)) }, onClick = {
                    agentsOpen = false; scope.launch { personal.choose(agent.getString("agent_id")) }
                }) }
            }
        }
    }, navigationIcon = {
        IconButton(onClick = { model.perform { model.leaveHost(); model.foreground() } }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "电脑列表") }
    }, actions = {
        Box {
            IconButton(onClick = { activityOpen = true }) { Icon(Icons.Default.MoreVert, "活动与设置") }
            DropdownMenu(expanded = activityOpen, onDismissRequest = { activityOpen = false }) {
                personalSectionNames.forEachIndexed { index, label ->
                    DropdownMenuItem(text = { Text(label) }, leadingIcon = { Icon(personalSectionIcons[index], null) }, onClick = { section = index; activityOpen = false })
                }
                HorizontalDivider()
                DropdownMenuItem(text = { Text("刷新") }, leadingIcon = { Icon(Icons.Default.Refresh, null) }, enabled = personal.available && !personal.busy,
                    onClick = { activityOpen = false; scope.launch { personal.reload() } })
                DropdownMenuItem(text = { Text("停止当前回复") }, leadingIcon = { Icon(Icons.Default.StopCircle, null) }, enabled = personal.canSend,
                    onClick = { activityOpen = false; scope.launch { personal.stopReply() } })
            }
        }
    }) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            if (!personal.available) {
                PersonalNotice(personal.error ?: "Personal 暂不可用。连接可信宿主后可以继续与同一个助理交流，离线时不能提交执行。", personal.error != null)
                if (personal.selectedID != null) PersonalNotice("显示上次读取的内容。重新连接后才能发送或读取附件。", false)
                TextButton(onClick = model::reconnect, enabled = !model.connecting, modifier = Modifier.padding(horizontal = 8.dp)) { Text("重新连接") }
            }
            if (personal.available || personal.selectedID != null || personal.hasPending) {
                if (personal.available) personal.error?.let { PersonalNotice(it, true) }
                personal.notice?.let { PersonalNotice(it, false) }
                if (section == 0) {
                    LazyColumn(Modifier.weight(1f).fillMaxWidth(), state = list, contentPadding = PaddingValues(horizontal = 16.dp, vertical = 20.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
                        if (personal.hasEarlier) item { Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { OutlinedButton(onClick = { scope.launch { personal.earlier() } }, enabled = personal.available && !personal.busy) { Text("更早的消息") } } }
                        if (personal.messageSequence != null) item { Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) { OutlinedButton(onClick = { scope.launch { personal.latest() } }) { Text("返回最新消息") } } }
                        if (personal.records.isEmpty() && !personal.busy) item {
                            Column(Modifier.fillMaxWidth().padding(top = 72.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(14.dp)) {
                                Image(painterResource(R.drawable.login_mascot), null, Modifier.size(72.dp))
                                Text("直接发消息，twin 会在这里继续与你交流。", color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                        items(personal.records, key = { it.getLong("seq") }) { record -> PersonalMessageRow(model, record) }
                    }
                    Surface(color = MaterialTheme.colorScheme.surface, tonalElevation = 2.dp) {
                        Column(Modifier.fillMaxWidth().navigationBarsPadding().imePadding().padding(horizontal = 12.dp, vertical = 8.dp)) {
                            val composer = personal.composerID
                            if (!personal.hasPending) {
                                ComposerRow(personal.text, { personal.text = it }, { scope.launch { personal.send() } }, personal.canSend,
                                    personal.busy, personal.attachments, { if (personal.composerID == composer) personal.attachments = it }, model, composer,
                                    placeholder = "给 twin 发消息")
                            } else PersonalPendingMessage(personal.text, personal.attachments.size,
                                retry = { scope.launch { personal.send() } }, discard = { discard = true },
                                retryEnabled = personal.canSend, discardEnabled = !personal.busy)
                        }
                    }
                } else if (section == 1) {
                    PersonalSectionHeader(section) { section = 0 }
                    LazyColumn(Modifier.weight(1f).fillMaxWidth(), contentPadding = PaddingValues(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                        val detail = personal.detail
                        if (detail == null) {
                            if (personal.tasks.isEmpty()) item { Text(if (personal.available) "还没有事项" else "连接宿主后刷新事项状态") }
                            items(personal.tasks, key = { it.getString("task_id") }) { task ->
                                TextButton(onClick = { scope.launch { personal.openTask(task.getString("task_id")) } }) {
                                    Column(Modifier.fillMaxWidth()) { Text(task.optString("title")); Text(task.optString("state"), style = MaterialTheme.typography.bodyMedium) }
                                }
                            }
                        } else {
                            item { TextButton(onClick = { scope.launch { personal.openTask(null) } }) { Text("所有事项") } }
                            item {
                                Text(detail.optString("title"), style = MaterialTheme.typography.titleMedium); Text(detail.optString("goal")); Text(detail.optString("state"))
                                if (detail.optBoolean("cancel_requested")) Text("正在请求停止；尚未确认结束")
                                if (detail.optBoolean("reconciliation_required")) Text("远端结果尚待核对，请勿重复发起相同事项")
                                Row {
                                    TextButton(onClick = { taskAction = PersonalTaskAction.UPDATE; taskGoal = detail.optString("goal"); editingTask = JSONObject(detail.toString()) }, enabled = personal.available && !personal.busy && !personal.controlBusy) { Text("修改要求") }
                                    TextButton(onClick = { taskAction = PersonalTaskAction.STOP; editingTask = JSONObject(detail.toString()) }, enabled = personal.available && !personal.busy && !personal.controlBusy) { Text("停止") }
                                    TextButton(onClick = { taskAction = PersonalTaskAction.RESUME; editingTask = JSONObject(detail.toString()) }, enabled = personal.available && !personal.busy && !personal.controlBusy) { Text("恢复") }
                                }
                            }
                            detail.optJSONObject("result")?.let { result ->
                                item { MessageText(result.optString("summary"), true) }
                                items(result.optJSONArray("evidence")?.objects() ?: emptyList()) { Text(it.optString("description")) }
                            }
                            detail.optJSONObject("source")?.takeIf { it.has("message_seq") }?.let { source ->
                                item { TextButton(onClick = { section = 0; scope.launch { personal.showSource(source.getLong("message_seq")) } }) { Text("查看来源消息") } }
                            }
                            item { Text("事项执行记录", style = MaterialTheme.typography.titleSmall) }
                            items(personal.taskRecords, key = { it.getLong("seq") }) { record ->
                                MessageText(record.optString("display_content").ifEmpty { record.optString("content") }, record.optString("role") == "assistant")
                                PersonalAttachmentButtons(model, record, detail)
                            }
                        }
                    }
                }
                if (section >= 2) {
                    PersonalSectionHeader(section) { section = 0 }
                    key(personal.contextID) { PersonalToolsScreen(personal, section, Modifier.weight(1f), selectSection = { section = it }) }
                }
                if (personal.busy || personal.controlBusy) LinearProgressIndicator(Modifier.fillMaxWidth())
            }
        }
    }
    }
    editingTask?.let { snapshot -> AlertDialog(onDismissRequest = { editingTask = null }, title = { Text(snapshot.optString("title", "更新事项")) }, text = {
        Column { if (taskAction == PersonalTaskAction.UPDATE) OutlinedTextField(taskGoal, { taskGoal = it }, label = { Text("新的事项要求") }, minLines = 3)
            Text(if (taskAction == PersonalTaskAction.STOP) "请求停止后台事项。远端已开始的操作可能仍需核对，状态以宿主返回为准。" else "会提交当前显示版本的修改；版本冲突时请刷新后重新审阅。") }
    }, confirmButton = { TextButton(onClick = { editingTask = null; scope.launch { personal.changeTask(snapshot, taskAction, if (taskAction == PersonalTaskAction.UPDATE) taskGoal else null) } }, enabled = personal.available && !personal.busy && !personal.controlBusy && (taskAction != PersonalTaskAction.UPDATE || taskGoal.isNotBlank())) { Text("确认") } }, dismissButton = { TextButton(onClick = { editingTask = null }) { Text("取消") } }) }
    model.attachmentPreview?.let { AttachmentPreview(model, it) }
    if (discard) AlertDialog(onDismissRequest = { discard = false }, title = { Text("放下尚未确认的消息？") },
        text = { Text("原请求可能已被宿主接收。请先查看历史，避免重新输入相同请求。") },
        confirmButton = { TextButton(onClick = { discard = false; scope.launch { personal.abandonPending() } }) { Text("放下本机重试") } },
        dismissButton = { TextButton(onClick = { discard = false }) { Text("取消") } })
}

private val personalAccent = Color(0xFF3566F0)
private val personalSectionNames = listOf("对话", "事项", "今天", "记忆", "设置", "日历", "查找历史")
private val personalSectionIcons = listOf(Icons.Default.ChatBubbleOutline, Icons.Default.Checklist, Icons.Default.Today,
    Icons.Default.Psychology, Icons.Default.Tune, Icons.Default.CalendarMonth, Icons.Default.History)

private fun personalStatusText(personal: PersonalModel): String = when {
    !personal.available -> "离线"
    personal.busy || personal.controlBusy -> "正在处理"
    personal.hasPending -> "等待重试"
    else -> "就绪"
}

@Composable internal fun PersonalIdentityTitle(name: String, status: String, busy: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Surface(shape = CircleShape, color = MaterialTheme.colorScheme.surfaceContainerLow, modifier = Modifier.size(38.dp)) {
            Image(painterResource(R.drawable.login_mascot), null, Modifier.padding(3.dp))
        }
        Column {
            Text(name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(if (busy) "正在处理" else status, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
        }
    }
}

@Composable internal fun PersonalNotice(text: String, error: Boolean) {
    Surface(color = if (error) MaterialTheme.colorScheme.errorContainer.copy(alpha = 0.45f) else MaterialTheme.colorScheme.surfaceContainerLow) {
        Text(text, Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), style = MaterialTheme.typography.bodyMedium,
            color = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable internal fun PersonalPendingMessage(
    text: String,
    attachmentCount: Int,
    retry: () -> Unit,
    discard: () -> Unit,
    retryEnabled: Boolean,
    discardEnabled: Boolean,
) {
    Surface(shape = RoundedCornerShape(18.dp), color = MaterialTheme.colorScheme.surfaceContainerLow,
        border = BorderStroke(0.5.dp, MaterialTheme.colorScheme.outlineVariant)) {
        Column(Modifier.fillMaxWidth().padding(horizontal = 14.dp, vertical = 10.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("原消息等待确认", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (text.isNotBlank()) Text(text, maxLines = 3, overflow = TextOverflow.Ellipsis)
            if (attachmentCount > 0) Text("$attachmentCount 个附件", style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center) {
        TextButton(onClick = retry, enabled = retryEnabled) { Text("重试原消息") }
        TextButton(onClick = discard, enabled = discardEnabled) { Text("放下本机重试") }
    }
}

@Composable private fun PersonalSectionHeader(section: Int, close: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        TextButton(onClick = close) { Icon(Icons.AutoMirrored.Filled.ArrowBack, null); Spacer(Modifier.width(4.dp)); Text("返回对话") }
        Spacer(Modifier.weight(1f))
        Icon(personalSectionIcons[section], null, Modifier.size(18.dp))
        Spacer(Modifier.width(6.dp))
        Text(personalSectionNames[section], style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.width(8.dp))
    }
    HorizontalDivider()
}

@Composable private fun PersonalMessageRow(model: AppModel, record: JSONObject) {
    val user = record.optString("role") == "user"
    PersonalMessageBubble(record.optString("display_content").ifEmpty { record.optString("content") }, user, messageID = record.getLong("seq")) { PersonalAttachmentButtons(model, record, user = user) }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable internal fun PersonalMessageBubble(content: String, user: Boolean, messageID: Long, attachments: @Composable () -> Unit = {}) {
    val actions = LocalPersonalMessageActions.current
    var bounds by remember { mutableStateOf(Rect.Zero) }
    val open = { actions?.open(PersonalMessageSelection(messageID, content, user, bounds)); Unit }
    Box(Modifier.fillMaxWidth(), contentAlignment = if (user) Alignment.CenterEnd else Alignment.CenterStart) {
        Column(Modifier.widthIn(max = 320.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (content.isNotEmpty()) Surface(modifier = Modifier.onGloballyPositioned { bounds = it.boundsInWindow() }
                .graphicsLayer { alpha = if (actions?.selected?.id == messageID) 0f else 1f }
                .combinedClickable(onClick = {}, onLongClickLabel = "消息操作", onLongClick = open), shape = RoundedCornerShape(18.dp),
                color = if (user) personalAccent else Color.Transparent, contentColor = if (user) Color.White else MaterialTheme.colorScheme.onSurface) {
                PersonalMessageText(content, user, onLongClick = open)
            }
            attachments()
        }
    }
}

@Composable private fun PersonalAttachmentButtons(model: AppModel, record: JSONObject, task: JSONObject? = null, user: Boolean = false) {
    val scope = rememberCoroutineScope()
    record.optJSONArray("attachments")?.objects()?.forEach { descriptor ->
        FilledTonalButton(onClick = { scope.launch {
            model.personal.loadAttachment(descriptor, record.getLong("seq"), task)?.let { model.attachmentPreview = it }
        } }, enabled = model.personal.available && !model.personal.loadingAttachment,
            colors = if (user) ButtonDefaults.filledTonalButtonColors(containerColor = Color.White.copy(alpha = 0.16f), contentColor = Color.White)
            else ButtonDefaults.filledTonalButtonColors()) {
            Text(descriptor.optString("name", "查看附件"), maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}
