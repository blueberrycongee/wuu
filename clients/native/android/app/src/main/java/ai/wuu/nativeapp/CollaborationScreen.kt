package ai.wuu.nativeapp

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.delay
import org.json.JSONObject

@OptIn(ExperimentalLayoutApi::class)
@Composable internal fun HostScreen(model: AppModel, conversation: @Composable () -> Unit) {
    val state = model.collaboration
    val saved = rememberSaveableStateHolder()
    val owner = LocalLifecycleOwner.current
    LaunchedEffect(state.visible, state.selectedID, model.connected, owner) {
        if (state.visible && model.connected) owner.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) { state.refresh(); delay(2_000) }
        }
    }
    Column(Modifier.fillMaxSize().imePadding()) {
        Box(Modifier.weight(1f)) {
            saved.SaveableStateProvider(if (state.visible) "collaboration" else "harness") {
                if (state.visible) CollaborationScreen(model) else conversation()
            }
        }
        if (!WindowInsets.isImeVisible) NavigationBar(windowInsets = WindowInsets.navigationBars) {
            NavigationBarItem(selected = !state.visible, onClick = { state.mode(false) },
                icon = { Icon(Icons.Default.ChatBubbleOutline, null) }, label = { Text("会话") })
            NavigationBarItem(selected = state.visible, onClick = { state.mode(true) },
                icon = { Icon(Icons.Default.Groups, null) }, label = { Text("协作") })
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable internal fun CollaborationScreen(model: AppModel) {
    val state = model.collaboration
    var create by remember { mutableStateOf(false) }
    var details by remember { mutableStateOf(false) }
    val room = state.selectedRoom
    val roomState = rememberSaveableStateHolder()
    fun back() {
        if (state.selectedID != null) state.select(null)
        else model.perform { model.leaveHost(); model.foreground() }
    }
    BackHandler(onBack = ::back)
    Scaffold(topBar = {
        TopAppBar(title = { Column {
            Text(room?.let(state::roomName) ?: "协作", style = MaterialTheme.typography.titleMedium,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (!model.connected) Text(if (model.connecting) "正在连接…" else "离线 · 已加载内容只读",
                style = MaterialTheme.typography.labelSmall)
        } }, navigationIcon = {
            IconButton(onClick = ::back) { Icon(Icons.AutoMirrored.Filled.ArrowBack, if (room == null) "电脑列表" else "协作列表") }
        }, actions = {
            if (room != null) IconButton(onClick = { details = true }) { Icon(Icons.Default.Info, "成员与任务") }
            else IconButton(onClick = { create = true }, enabled = model.connected) { Icon(Icons.Default.Add, "新对话或群聊") }
        })
    }, bottomBar = {
        state.selectedID?.let { id ->
            Column(Modifier.imePadding()) {
                val attachments = state.attachments[id].orEmpty()
                attachments.forEach { file ->
                    TextButton(onClick = { state.attachments[id] = attachments.filterNot { it.id == file.id } }) { Text("${file.filename} ×", maxLines = 1) }
                }
                Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.Bottom) {
                    key(id) { AttachmentPicker(attachments, { state.attachments[id] = it }, model) }
                    OutlinedTextField(state.drafts[id].orEmpty(), { state.drafts[id] = it }, Modifier.weight(1f).testTag("native-composer"),
                        placeholder = { Text("发送消息") }, maxLines = 5)
                    IconButton(onClick = { model.perform { state.send() } }, enabled = model.connected && !state.sending &&
                        (state.drafts[id].orEmpty().isNotBlank() || attachments.isNotEmpty())) {
                        Icon(Icons.AutoMirrored.Filled.Send, if (state.sending) "正在发送" else "发送")
                    }
                }
            }
        }
    }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            state.error?.let { Text(it, Modifier.padding(horizontal = 16.dp, vertical = 8.dp), color = MaterialTheme.colorScheme.error) }
            if (state.loading && state.rooms.isEmpty()) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (room == null) {
                LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(vertical = 8.dp)) {
                    if (state.rooms.isEmpty() && !state.loading) item {
                        Text(if (model.connected) "还没有协作对话" else "连接电脑后查看协作", Modifier.padding(20.dp), style = MaterialTheme.typography.bodyMedium)
                    }
                    items(state.rooms, key = { it.getString("id") }) { row ->
                        ListItem(modifier = Modifier.clickable { state.select(row.getString("id")) },
                            headlineContent = { Text(state.roomName(row), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                            leadingContent = { Icon(if (row.optString("kind") == "dm") Icons.Default.PersonOutline else Icons.Default.Groups, null) },
                            supportingContent = { row.optJSONObject("last_message")?.let {
                                Text(it.optString("body").ifBlank { if (it.optBoolean("has_attachments")) "附件" else "" }, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            } }, trailingContent = {
                                if (row.optInt("unread_count") > 0) Badge { Text(row.optInt("unread_count").coerceAtMost(99).toString()) }
                            })
                    }
                }
            } else roomState.SaveableStateProvider(room.getString("id")) { RoomTimeline(model) }
        }
    }
    if (create) NewCollaborationSheet(model) { create = false }
    if (details && room != null) ModalBottomSheet(onDismissRequest = { details = false }) {
        LazyColumn(Modifier.fillMaxWidth().heightIn(max = 520.dp), contentPadding = PaddingValues(horizontal = 20.dp, vertical = 8.dp)) {
            item { Text("成员", style = MaterialTheme.typography.titleSmall) }
            items(room.optJSONArray("members")?.objects().orEmpty()) { member ->
                Text(if (member.optString("member_type") == "human") "你" else state.agentName(member.optString("member_id")), Modifier.padding(vertical = 10.dp))
            }
            item { Text("已加载的任务", Modifier.padding(top = 16.dp, bottom = 8.dp), style = MaterialTheme.typography.titleSmall) }
            val tasks = state.messages.filter { it.optString("kind") == "task" }
            if (tasks.isEmpty()) item { Text("暂无任务", Modifier.padding(vertical = 8.dp)) }
            items(tasks, key = { it.getString("id") }) { TaskSummary(it) }
        }
    }
}

@Composable private fun RoomTimeline(model: AppModel) {
    val state = model.collaboration
    val list = rememberLazyListState()
    var initialized by rememberSaveable { mutableStateOf(false) }
    var following by rememberSaveable { mutableStateOf(true) }
    LaunchedEffect(list) {
        snapshotFlow { list.isScrollInProgress to list.canScrollForward }.collect { (scrolling, forward) ->
            if (scrolling) following = !forward
        }
    }
    LaunchedEffect(state.messages.lastOrNull()?.optString("id"), state.responses.lastOrNull()?.optString("body")) {
        if (state.messages.isNotEmpty() && (!initialized || following)) {
            list.scrollToItem(state.messages.size + state.responses.size)
            initialized = true
        }
    }
    LazyColumn(Modifier.fillMaxSize().testTag("room-timeline"), state = list, contentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(18.dp)) {
        item(key = "older") {
            if (state.hasOlder) TextButton(onClick = { model.perform { state.loadOlder() } }, enabled = model.connected && !state.loading) { Text("加载更早消息") }
        }
        items(state.messages, key = { it.getString("id") }) { message ->
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(if (message.optString("author_type") == "human") "你" else state.agentName(message.optString("author_id")),
                    style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                val reply = message.optString("reply_to")
                if (reply.isNotEmpty()) Text("回复：" + (state.messages.firstOrNull { it.optString("id") == reply }?.optString("body") ?: "较早消息"),
                    style = MaterialTheme.typography.labelMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                if (message.optString("kind") == "task") TaskSummary(message)
                if (message.optString("body").isNotEmpty()) SelectionContainer { MessageText(message.optString("body"), message.optString("author_type") != "human") }
                val images = message.optJSONArray("images")?.length() ?: 0
                if (images > 0) Text("$images 张图片 · 在电脑上查看", style = MaterialTheme.typography.labelMedium)
                message.optJSONArray("files")?.objects()?.forEach { Text("${it.optString("filename").ifBlank { "附件" }} · 在电脑上查看", style = MaterialTheme.typography.labelMedium) }
                if (message.has("agent_creation_proposal")) Text("Agent 创建申请 · 在电脑上处理", style = MaterialTheme.typography.labelMedium)
            }
        }
        items(state.responses, key = { "response:${it.getString("id")}" }) { response ->
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(state.agentName(response.optString("agent_id")) + " · " + if (response.optString("state") == "failed") "回复失败" else "正在回复",
                    style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                MessageText(response.optString("body"), true)
                if (response.optString("error").isNotBlank()) Text(response.optString("error"), color = MaterialTheme.colorScheme.error)
            }
        }
    }
}

@Composable private fun TaskSummary(message: JSONObject) {
    Column(Modifier.fillMaxWidth().padding(vertical = 6.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(message.optString("task_title").ifBlank { "任务" }, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold)
        val status = message.optJSONObject("work")?.optString("state")?.takeIf { it.isNotBlank() } ?: message.optString("task_state")
        Text(when (status) { "done", "completed" -> "已完成"; "doing", "working" -> "进行中"; "checking" -> "检查中"; "needs_human" -> "需要你处理"; "open" -> "待处理"; "cancelled" -> "已取消"; else -> status },
            style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable private fun NewCollaborationSheet(model: AppModel, dismiss: () -> Unit) {
    val state = model.collaboration
    var group by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var members by remember { mutableStateOf(emptySet<String>()) }
    var creating by remember { mutableStateOf(false) }
    ModalBottomSheet(onDismissRequest = dismiss) {
        LazyColumn(Modifier.fillMaxWidth().heightIn(max = 560.dp), contentPadding = PaddingValues(16.dp)) {
            item {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                    Text(if (group) "新群聊" else "新对话", style = MaterialTheme.typography.titleMedium)
                    TextButton(onClick = { group = !group }) { Text(if (group) "私聊" else "建群") }
                }
                if (group) OutlinedTextField(name, { name = it }, Modifier.fillMaxWidth(), label = { Text("群名") }, singleLine = true)
                if (state.agents.isEmpty()) Text("在电脑上创建 Agent 后开始对话", Modifier.padding(vertical = 16.dp))
            }
            items(state.agents, key = { it.getString("id") }) { agent ->
                val id = agent.getString("id")
                ListItem(headlineContent = { Text(agent.optString("name")) }, modifier = Modifier.testTag("new-agent-${agent.optString("name")}").clickable(enabled = model.connected && !creating) {
                    if (group) members = if (id in members) members - id else members + id
                    else { creating = true; model.perform { try { state.direct(id); dismiss() } finally { creating = false } } }
                }, trailingContent = { if (group) Checkbox(id in members, null) })
            }
            if (group) item {
                Button(onClick = { creating = true; model.perform { try { state.create(name, members); dismiss() } finally { creating = false } } },
                    enabled = model.connected && !creating && name.isNotBlank() && members.isNotEmpty(), modifier = Modifier.fillMaxWidth()) { Text("创建群聊") }
            }
        }
    }
}
