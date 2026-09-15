package ai.wuu.nativeapp

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.background
import androidx.compose.foundation.interaction.collectIsDraggedAsState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.contentDescription
import kotlinx.coroutines.launch
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
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
    var initialized by rememberSaveable(model.host?.optString("pub")) { mutableStateOf(false) }
    LaunchedEffect(Unit) { if (!initialized) { state.mode(true); initialized = true } }
    val saved = rememberSaveableStateHolder()
    val owner = LocalLifecycleOwner.current
    LaunchedEffect(state.visible, state.selectedID, model.connected, owner) {
        if (state.visible && model.connected) owner.lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) { state.refresh(); delay(2_000) }
        }
    }
    Column(Modifier.fillMaxSize().navigationBarsPadding().imePadding()) {
        Box(Modifier.weight(1f)) {
            saved.SaveableStateProvider(if (state.visible) "collaboration" else "harness") {
                if (state.visible) CollaborationScreen(model) else conversation()
            }
        }
        // Hide host tabs in concrete chat surfaces (open collab room or open session),
        // and while the IME is up. Keep tabs on collab list / empty 「新会话」.
        val sessionOpen = !state.visible && model.activeID != null
        val roomOpen = state.visible && state.selectedID != null
        if (!WindowInsets.isImeVisible && !sessionOpen && !roomOpen) {
            Row(Modifier.fillMaxWidth().height(52.dp).padding(horizontal = 40.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
                listOf(true to "协作", false to "会话").forEach { (collaboration, label) ->
                    TextButton(onClick = { state.mode(collaboration) }, modifier = Modifier.weight(1f)) {
                        Icon(if (collaboration) Icons.Default.Forum else Icons.Default.Terminal, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(7.dp))
                        Text(label, fontWeight = if (state.visible == collaboration) FontWeight.SemiBold else FontWeight.Normal,
                            color = if (state.visible == collaboration) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable internal fun CollaborationScreen(model: AppModel) {
    val state = model.collaboration
    var account by remember { mutableStateOf(false) }
    var create by remember { mutableStateOf(false) }
    var details by remember { mutableStateOf(false) }
    var searching by rememberSaveable { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    val room = state.selectedRoom
    val roomState = rememberSaveableStateHolder()
    DisposableEffect(state.selectedID) { onDispose { model.attachmentPreview = null } }
    fun back() {
        if (searching) { searching = false; query = "" }
        else if (state.selectedID != null) state.select(null)
        else model.perform { model.leaveHost(); model.foreground() }
    }
    BackHandler(onBack = ::back)
    Scaffold(topBar = {
        Column(Modifier.statusBarsPadding()) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                ChromeButton(if (room == null) Icons.Default.AccountCircle else Icons.AutoMirrored.Filled.ArrowBack,
                    if (room == null) "账号设置" else "协作列表") { if (room == null) account = true else back() }
                if (room != null) {
                    Row(Modifier.weight(1f).heightIn(min = 44.dp), verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                        RoomMark(room, state.agents, 27.dp)
                        Text(state.roomName(room), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold,
                            maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    ChromeButton(Icons.Default.MoreHoriz, "成员与任务") { details = true }
                } else {
                    Text("协作", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                    ChromeButton(if (searching) Icons.Default.Close else Icons.Default.Search, if (searching) "关闭搜索" else "搜索协作") { searching = !searching; query = "" }
                    ChromeButton(Icons.Default.Add, "新对话或群聊", model.connected) { create = true }
                }
            }
            if (searching && room == null) TextField(query, { query = it }, singleLine = true,
                placeholder = { Text("搜索对话或群聊") }, leadingIcon = { Icon(Icons.Default.Search, null, Modifier.size(20.dp)) },
                shape = MaterialTheme.shapes.large, colors = wuuFieldColors(), modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp))
            if (!model.connected) Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(if (model.connecting) "正在连接…" else "电脑离线 · 消息只读", Modifier.weight(1f), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                TextButton(onClick = model::reconnect, enabled = !model.connecting) { Text("重连") }
            }
        }
    }, bottomBar = {
        state.selectedID?.let { id ->
            val attachments = state.attachments[id].orEmpty()
            // Activity strip + composer share this column so HostScreen imePadding
            // (and adjustResize) push both above the IME together.
            Column(Modifier.padding(horizontal = 12.dp, vertical = 8.dp)) {
                val activities = state.responses.distinctBy { it.optString("agent_id") }
                if (activities.isNotEmpty()) {
                    Column(
                        Modifier.fillMaxWidth().padding(bottom = 6.dp),
                        verticalArrangement = Arrangement.spacedBy(4.dp),
                    ) {
                        activities.forEach { response ->
                            CollaborationActivity(model, response)
                        }
                    }
                }
                ComposerRow(
                    value = state.drafts[id].orEmpty(),
                    onChange = { state.drafts[id] = it },
                    onSend = { model.perform { state.send() } },
                    enabled = model.connected,
                    sending = state.sending,
                    attachments = attachments,
                    onAttachments = { state.attachments[id] = it },
                    model = model,
                    draftKey = id,
                    placeholder = "发消息给 ${room?.let(state::roomName).orEmpty()}",
                )
            }
        }
    }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            state.error?.let { Text(it, Modifier.padding(horizontal = 16.dp, vertical = 8.dp), color = MaterialTheme.colorScheme.error) }
            if (state.loading && state.rooms.isEmpty()) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (room == null) {
                val groups = state.rooms.filter { it.optString("kind") != "dm" }
                val rows = state.rooms.filter { if (query.isBlank()) it.optString("kind") == "dm" else state.roomName(it).contains(query, ignoreCase = true) }
                LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 16.dp)) {
                    if (groups.isNotEmpty() && query.isBlank()) item(key = "groups") {
                        LazyRow(contentPadding = PaddingValues(horizontal = 14.dp, vertical = 16.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            items(groups, key = { it.getString("id") }) { group ->
                                Column(Modifier.width(80.dp).clickable { state.select(group.getString("id")) }.padding(4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                                    Box {
                                        RoomMark(group, state.agents, 52.dp)
                                        if (group.optInt("unread_count") > 0) Box(Modifier.align(Alignment.TopEnd).size(7.dp).background(MaterialTheme.colorScheme.primary, CircleShape))
                                    }
                                    Spacer(Modifier.height(6.dp))
                                    Text(state.roomName(group), style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                }
                            }
                        }
                    }
                    if (state.rooms.isEmpty() && !state.loading) item {
                        Column(Modifier.fillMaxWidth().padding(40.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                            Icon(Icons.Default.Forum, null, Modifier.size(32.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                            Text(if (model.connected) "开始一段对话" else "连接电脑后查看协作", Modifier.padding(top = 16.dp), style = MaterialTheme.typography.titleMedium)
                            if (model.connected) TextButton(onClick = { create = true }) { Text("选择 Agent") }
                        }
                    }
                    if (query.isNotBlank() && rows.isEmpty()) item { Text("没有找到相关对话", Modifier.padding(24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    items(rows, key = { it.getString("id") }) { row ->
                        Row(Modifier.fillMaxWidth().clickable { state.select(row.getString("id")) }.padding(horizontal = 18.dp, vertical = 11.dp),
                            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                            RoomMark(row, state.agents)
                            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    Text(state.roomName(row), Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Text(messageTime(row.optJSONObject("last_message")?.optString("created_at").orEmpty()), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                                    Text(row.optJSONObject("last_message")?.let { it.optString("body").ifBlank { if (it.optBoolean("has_attachments")) "[附件]" else "" } }.orEmpty(),
                                        Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    if (row.optInt("unread_count") > 0) Box(Modifier.size(7.dp).background(MaterialTheme.colorScheme.primary, CircleShape)
                                        .semantics { contentDescription = "${row.optInt("unread_count")} 条未读" })
                                }
                            }
                        }
                    }
                }
            } else roomState.SaveableStateProvider(room.getString("id")) { RoomTimeline(model) }
        }
    }
    model.attachmentPreview?.let { AttachmentPreview(model, it) }
    if (account) AccountSettingsSheet(model) { account = false }
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
    val scope = rememberCoroutineScope()
    val dragging by list.interactionSource.collectIsDraggedAsState()
    LaunchedEffect(list) {
        snapshotFlow { dragging to list.canScrollForward }.collect { (drag, forward) ->
            if (drag || !forward) following = !forward
        }
    }
    val viewport by remember { derivedStateOf { list.layoutInfo.viewportSize } }
    LaunchedEffect(state.messages.lastOrNull()?.toString(), viewport) {
        if (state.messages.isNotEmpty() && (!initialized || following)) {
            withFrameNanos { }
            list.scrollToItem((list.layoutInfo.totalItemsCount - 1).coerceAtLeast(0))
            initialized = true
        }
    }
    Box(Modifier.fillMaxSize()) {
    LazyColumn(Modifier.fillMaxSize().testTag("room-timeline"), state = list, contentPadding = PaddingValues(horizontal = 10.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        item(key = "older") {
            if (state.hasOlder) TextButton(onClick = { model.perform { state.loadOlder() } }, enabled = model.connected && !state.loading) { Text("加载更早消息") }
        }
        val group = state.selectedRoom?.optString("kind") != "dm"
        itemsIndexed(state.messages, key = { _, item -> item.getString("id") }) { index, message ->
            val time = messageDate(message.optString("created_at"))
            val previous = state.messages.getOrNull(index - 1)?.let { messageDate(it.optString("created_at")) }
            if (time != null && (previous == null || java.time.Duration.between(previous, time).toMinutes() >= 5)) {
                Text(messageTime(message.optString("created_at"), true), Modifier.fillMaxWidth().padding(vertical = 10.dp),
                    style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = androidx.compose.ui.text.style.TextAlign.Center)
            }
            if (message.optString("kind") == "system") {
                Text(message.optString("body"), Modifier.fillMaxWidth().padding(vertical = 8.dp), style = MaterialTheme.typography.labelSmall,
                    textAlign = androidx.compose.ui.text.style.TextAlign.Center, color = MaterialTheme.colorScheme.onSurfaceVariant)
                return@itemsIndexed
            }
            val own = message.optString("author_type") == "human"
            val mark = if (!own && group) state.agents.firstOrNull { it.optString("id") == message.optString("author_id") } else null
            // First-of-streak: name + mascot on the first bubble; later same-author rows keep the avatar gutter empty.
            val previousMessage = state.messages.getOrNull(index - 1)
            val sameAuthorStreak = previousMessage != null
                && previousMessage.optString("kind") != "system"
                && previousMessage.optString("author_type") == message.optString("author_type")
                && previousMessage.optString("author_id") == message.optString("author_id")
            val firstOfStreak = mark != null && !sameAuthorStreak
            RoomBubble(
                own = own,
                mark = mark,
                showMark = firstOfStreak,
                senderName = if (firstOfStreak) state.agentName(message.optString("author_id")) else null,
                streakContinue = sameAuthorStreak,
            ) {
                val reply = message.optString("reply_to")
                if (reply.isNotEmpty()) ReplyOrnament(state.messages.firstOrNull { it.optString("id") == reply }?.optString("body")?.takeIf { it.isNotBlank() } ?: "回复较早的消息")
                if (message.optString("kind") == "task") TaskSummary(message)
                if (message.optString("body").isNotEmpty()) SelectionContainer { MessageText(message.optString("body"), !own) }
                listOf("images", "markdown_images", "files").forEach { field ->
                    message.optJSONArray(field)?.objects()?.forEachIndexed { index, file ->
                        if (field != "files") MessageImage("${state.selectedID}:${message.optString("id")}:$index:${file.optString("remote_ref")}", model.connected, model.imagePreviews,
                            read = { state.readAttachment(message, field, index, preview = true) },
                            open = { model.perform { model.previewCollaborationAttachment(message, field, index) } })
                        else TextButton(onClick = { model.perform { model.previewCollaborationAttachment(message, field, index) } }, enabled = model.connected && !model.loadingAttachment,
                            colors = ButtonDefaults.textButtonColors(contentColor = LocalContentColor.current), contentPadding = PaddingValues(horizontal = 0.dp, vertical = 4.dp)) {
                            Icon(if (field == "images") Icons.Default.Image else Icons.Default.Description, null, Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text(file.optString("filename").ifBlank { "查看图片" }, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
                if (!message.isNull("agent_creation_proposal")) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        Icon(Icons.Default.PersonAdd, null, Modifier.size(14.dp))
                        Text("Agent 创建请求 · 在电脑上处理", style = MaterialTheme.typography.labelMedium)
                    }
                }
            }
        }
        item(key = "bottom") { Spacer(Modifier.height(1.dp)) }
    }
        if (!following && list.canScrollForward) Box(Modifier.align(Alignment.BottomEnd).padding(12.dp)) {
            ChromeButton(Icons.Default.KeyboardArrowDown, "回到最新消息") { following = true; scope.launch { list.animateScrollToItem((list.layoutInfo.totalItemsCount - 1).coerceAtLeast(0)) } }
        }
    }
}

@Composable internal fun CollaborationActivity(model: AppModel, response: JSONObject) {
    val state = response.optString("state")
    val failed = state in listOf("failed", "interrupted")
    // Prefer shared collaborationStatus copy; unknown response states fall back to 处理中.
    val statusCopy = collaborationStatus(state)
    val label = if (statusCopy.isBlank() || statusCopy == state) "处理中" else statusCopy
    var resuming by remember(response.optString("id")) { mutableStateOf(false) }
    val agentID = response.optString("agent_id")
    val name = model.collaboration.agentName(agentID)
    Row(
        Modifier.fillMaxWidth().padding(vertical = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        AgentMark(model.collaboration.agents.firstOrNull { it.optString("id") == agentID }, 22.dp, status = state)
        Text(
            "$name $label",
            Modifier.weight(1f),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (failed) TextButton(enabled = model.connected && !resuming, onClick = {
            resuming = true
            model.perform { try { model.collaboration.resume(response) } finally { resuming = false } }
        }) { Text(if (resuming) "正在继续…" else if (state == "interrupted") "继续" else "重试") }
    }
}

@Composable private fun RoomBubble(
    own: Boolean,
    mark: JSONObject?,
    showMark: Boolean = mark != null,
    senderName: String? = null,
    streakContinue: Boolean = false,
    status: String? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    // ~78% max like iOS trailing/leading spacers; Surface wraps content (short 「好的」 stays small).
    val maxFrac = 0.78f
    val avatarSize = 26.dp
    val gutterGap = 8.dp
    // Tighter gap inside a streak; slightly larger between different authors.
    val topPad = if (streakContinue) 0.dp else 6.dp
    Box(Modifier.fillMaxWidth().padding(top = topPad)) {
        when {
            own -> Row(
                Modifier.fillMaxWidth(maxFrac).align(Alignment.CenterEnd),
                horizontalArrangement = Arrangement.End,
            ) {
                BubbleSurface(own = true, content = content)
            }
            mark != null -> Row(
                Modifier.fillMaxWidth(maxFrac).align(Alignment.CenterStart),
                horizontalArrangement = Arrangement.Start,
                verticalAlignment = Alignment.Top,
            ) {
                // Fixed avatar column BESIDE the bubble (never overlapping the corner).
                // When a name sits above the first streak bubble, pad the mark so it top-aligns to the bubble.
                val namePad = if (senderName != null) 17.dp else 0.dp
                Box(
                    Modifier.width(avatarSize).padding(top = namePad),
                    contentAlignment = Alignment.TopCenter,
                ) {
                    if (showMark) AgentMark(mark, avatarSize, status = status, subtle = status == null)
                }
                Spacer(Modifier.width(gutterGap))
                Column(horizontalAlignment = Alignment.Start) {
                    if (senderName != null) {
                        Text(
                            senderName,
                            Modifier.padding(start = 2.dp, bottom = 3.dp),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    BubbleSurface(own = false, content = content)
                }
            }
            else -> Row(
                Modifier.fillMaxWidth(maxFrac).align(Alignment.CenterStart),
                horizontalArrangement = Arrangement.Start,
            ) {
                // DM incoming: no avatar / name column (unchanged).
                BubbleSurface(own = false, content = content)
            }
        }
    }
}

@Composable private fun BubbleSurface(own: Boolean, content: @Composable ColumnScope.() -> Unit) {
    Surface(
        color = if (own) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.surfaceContainer,
        contentColor = if (own) MaterialTheme.colorScheme.surface else MaterialTheme.colorScheme.onSurface,
        shape = RoundedCornerShape(18.dp),
    ) {
        Column(Modifier.padding(horizontal = 14.dp, vertical = 9.dp), verticalArrangement = Arrangement.spacedBy(6.dp), content = content)
    }
}

@Composable private fun ReplyOrnament(body: String) {
    Row(Modifier.height(IntrinsicSize.Min), verticalAlignment = Alignment.Top) {
        Box(
            Modifier
                .width(2.dp)
                .fillMaxHeight()
                .background(LocalContentColor.current.copy(alpha = 0.2f), RoundedCornerShape(50)),
        )
        Text(
            body,
            Modifier.padding(start = 8.dp),
            style = MaterialTheme.typography.labelMedium,
            color = LocalContentColor.current.copy(alpha = 0.7f),
            maxLines = 3,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

@Composable private fun TaskSummary(message: JSONObject) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Icon(Icons.Default.Checklist, null, Modifier.size(16.dp))
            Text(message.optString("task_title").ifBlank { "任务" }, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        }
        val status = message.optJSONObject("work")?.optString("state")?.takeIf { it.isNotBlank() } ?: message.optString("task_state")
        Text(
            collaborationStatus(status),
            style = MaterialTheme.typography.labelMedium,
            color = LocalContentColor.current.copy(alpha = 0.7f),
        )
    }
}

private fun collaborationStatus(state: String): String = when (state) {
    "thinking", "responding", "sending" -> "正在工作"
    "waiting" -> "等待后续消息"
    "open" -> "待处理"
    "doing", "running", "streaming", "working" -> "处理中"
    "checking" -> "检查中"
    "revising" -> "修改中"
    "needs_human" -> "需要你处理"
    "done", "completed" -> "已完成"
    "queued", "pending" -> "等待中"
    "failed" -> "失败"
    "interrupted", "cancelled", "stopped" -> "已停止"
    else -> state
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable private fun NewCollaborationSheet(model: AppModel, dismiss: () -> Unit) {
    val state = model.collaboration
    var group by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var members by remember { mutableStateOf(emptySet<String>()) }
    var creating by remember { mutableStateOf(false) }
    ModalBottomSheet(onDismissRequest = { if (!creating) dismiss() }) {
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
                ListItem(headlineContent = { Text(agent.optString("name")) }, leadingContent = { AgentMark(agent) }, modifier = Modifier.testTag("new-agent-${agent.optString("name")}").clickable(enabled = model.connected && !creating) {
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
