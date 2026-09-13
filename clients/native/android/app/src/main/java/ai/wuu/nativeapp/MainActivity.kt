package ai.wuu.nativeapp

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.collectIsDraggedAsState
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    private val model: AppModel by viewModels()
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState); enableEdgeToEdge()
        consumeNotification(intent)
        setContent {
            WuuTheme {
                Surface(Modifier.fillMaxSize()) { WuuRoot(model) }
            }
        }
    }
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent); setIntent(intent); consumeNotification(intent)
    }
    private fun consumeNotification(intent: Intent) {
        val host = intent.getStringExtra("host") ?: return
        intent.removeExtra("host")
        model.openPushHost(host)
    }
}

@Composable private fun WuuRoot(model: AppModel) {
    val owner = LocalLifecycleOwner.current
    val context = LocalContext.current
    DisposableEffect(owner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_START) model.foreground()
            if (event == Lifecycle.Event.ON_STOP) model.background()
        }
        owner.lifecycle.addObserver(observer)
        if (owner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) model.foreground()
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
    LaunchedEffect(model.browserURL) {
        model.browserURL?.let {
            try { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(it))) }
            catch (e: Exception) { model.error = e.message }
            model.browserURL = null
        }
    }
    when {
        model.account == null -> LoginScreen(model)
        model.host == null -> DevicesScreen(model)
        else -> HostScreen(model) { ConversationScreen(model) }
    }
    model.recovery?.let { secret ->
        AlertDialog(onDismissRequest = {}, title = { Text("保存账号恢复密钥") },
            text = { androidx.compose.foundation.text.selection.SelectionContainer {
                Text((if (model.resetRecovery != null) "密码已更新，所有设备已退出。旧恢复密钥已失效，请保存新密钥后重新登录。\n\n" else "") + "请将下面的密钥保存到密码管理器。它可用于重置密码，切勿分享。确认后此页面不再展示。\n\n$secret")
            } },
            confirmButton = { TextButton(onClick = model::acknowledgeRecovery) { Text("我已安全保存") } })
    }
    model.error?.let { message ->
        AlertDialog(onDismissRequest = { model.error = null }, title = { Text("提示") }, text = { Text(message) },
            confirmButton = { TextButton(onClick = { model.error = null }) { Text("知道了") } })
    }
    model.approval?.let { request ->
        AlertDialog(onDismissRequest = {}, title = { Text("需要在电脑上处理") },
            text = { Text("电脑请求 ${request.optString("method")}。此请求尚不能在手机上处理，可以拒绝后在电脑上继续。") },
            confirmButton = { TextButton(onClick = { model.perform { model.rejectApproval() } }) { Text("拒绝请求") } })
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable private fun DevicesScreen(model: AppModel) {
    var accountPanel by remember { mutableStateOf(false) }
    var licenses by remember { mutableStateOf(false) }
    var notifications by remember { mutableStateOf(false) }
    var changePassword by remember { mutableStateOf(false) }
    var revokeTarget by remember { mutableStateOf<org.json.JSONObject?>(null) }
    Scaffold(topBar = {
        TopAppBar(title = { Text("你的电脑", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold) }, actions = {
            IconButton(onClick = { model.perform { model.loadDevices() } }) { Icon(Icons.Default.Refresh, "刷新") }
            IconButton(onClick = { accountPanel = true }) { Icon(Icons.Default.AccountCircle, "账号") }
        })
    }) { padding ->
        LazyColumn(Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (model.directoryCached) item { Text("显示上次登录保存的电脑；联网后会更新访问权限和在线状态。", style = MaterialTheme.typography.bodySmall) }
            val hosts = model.devices.filter { it.optString("role") == "host" }
            if (hosts.isEmpty()) item { Text("在电脑上登录同一账号后，它会出现在这里。") }
            listOf(true, false).forEach { online ->
                val group = hosts.filter { it.optBoolean("online") == online }
                if (group.isNotEmpty()) item { Text(if (online) "在线" else "离线", style = MaterialTheme.typography.labelLarge) }
                items(group, key = { it.getString("pub") }) { device ->
                    ListItem(headlineContent = { Text(device.optString("name", "电脑")) },
                        supportingContent = { Text(if (online) "连接并继续对话" else "查看服务器保存的历史", style = MaterialTheme.typography.bodySmall) },
                        leadingContent = { Surface(color = MaterialTheme.colorScheme.secondaryContainer, shape = MaterialTheme.shapes.small) {
                            Icon(Icons.Default.Computer, null, Modifier.padding(12.dp).size(22.dp))
                        } }, trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null) },
                        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                        modifier = Modifier.clip(MaterialTheme.shapes.medium).clickable { model.selectHost(device) })
                }
            }
        }
    }
    if (accountPanel) ModalBottomSheet(onDismissRequest = { accountPanel = false }) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(model.account?.username ?: "", style = MaterialTheme.typography.headlineSmall)
            Text(model.account?.server ?: "")
            if (model.authMethod == "password") {
                TextButton(onClick = { accountPanel = false; changePassword = true }) { Text("修改密码") }
            }
            LazyColumn(Modifier.heightIn(max = 300.dp)) {
                items(model.devices, key = { it.getString("pub") }) { device ->
                    ListItem(headlineContent = { Text(device.optString("name")) }, trailingContent = {
                        TextButton(onClick = { revokeTarget = device }) { Text("移除") }
                    })
                }
            }
            Text("退出登录会删除这部手机缓存的会话文本。")
            Button(onClick = { accountPanel = false; model.logout() }, enabled = !model.busy, modifier = Modifier.fillMaxWidth()) { Text("退出登录") }
            TextButton(onClick = { accountPanel = false; licenses = true }) { Text("开源许可") }
            TextButton(onClick = { accountPanel = false; notifications = true }) { Text("通知") }
        }
    }
    if (changePassword) PasswordResetSheet(model, model.account?.server ?: "", true) { changePassword = false }
    if (licenses) LicensesSheet { licenses = false }
    if (notifications) PushSettingsSheet(model.push) { notifications = false }
    revokeTarget?.let { device ->
        AlertDialog(onDismissRequest = { revokeTarget = null }, title = { Text("移除设备？") },
            text = { Text("移除 ${device.optString("name")} 的账号访问权限。") },
            confirmButton = { TextButton(onClick = { revokeTarget = null; model.perform { model.revoke(device) } }) { Text("移除") } },
            dismissButton = { TextButton(onClick = { revokeTarget = null }) { Text("取消") } })
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable private fun ConversationScreen(model: AppModel) {
    val scope = rememberCoroutineScope()
    val drawer = rememberDrawerState(DrawerValue.Closed)
    val context = LocalContext.current
    var menu by remember { mutableStateOf(false) }
    var renameID by remember { mutableStateOf<String?>(null) }
    var renameTitle by remember { mutableStateOf("") }
    var settings by remember { mutableStateOf<Pair<String, ThreadSettings>?>(null) }
    LaunchedEffect(model.search, model.archivedList) {
        kotlinx.coroutines.delay(250)
        model.perform { model.loadThreads() }
    }
    var archive by remember { mutableStateOf<ThreadRow?>(null) }
    var workspaceMenu by remember { mutableStateOf(false) }
    val drafts = model.conversationDrafts
    val attachmentDrafts = model.conversationAttachments
    val draftKey = model.activeID ?: "new"
    val draft = drafts[draftKey] ?: ""
    val attachments = attachmentDrafts[draftKey] ?: emptyList()
    BackHandler { if (drawer.isOpen) scope.launch { drawer.close() } else model.perform { model.leaveHost(); model.foreground() } }
    ModalNavigationDrawer(drawerState = drawer, gesturesEnabled = true, drawerContent = {
        ModalDrawerSheet(Modifier.fillMaxWidth(0.88f).widthIn(max = 360.dp), drawerContainerColor = MaterialTheme.colorScheme.surfaceContainerLow) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                TextButton(onClick = { model.perform { model.leaveHost(); model.foreground() } },
                    modifier = Modifier.weight(1f), contentPadding = PaddingValues(12.dp)) {
                    Icon(Icons.AutoMirrored.Filled.ArrowBack, null, Modifier.size(24.dp))
                    Spacer(Modifier.width(16.dp))
                    Text(model.host?.optString("name") ?: "电脑列表", modifier = Modifier.weight(1f),
                        style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                IconButton(onClick = { scope.launch { drawer.close() } }) { Icon(Icons.Default.Close, "关闭会话列表") }
                IconButton(onClick = { scope.launch { drawer.close(); model.perform { model.newThread() } } }, enabled = model.connected) { Icon(Icons.Default.Add, "新会话") }
            }
            BoxWithConstraints(Modifier.fillMaxWidth().padding(horizontal = 16.dp)) {
                TextButton(onClick = { workspaceMenu = true }, enabled = model.connected,
                    modifier = Modifier.fillMaxWidth(), contentPadding = PaddingValues(12.dp)) {
                    Icon(Icons.Default.FolderOpen, null, Modifier.size(24.dp))
                    Spacer(Modifier.width(16.dp))
                    Text(if (model.workspace.isBlank()) "所有工作区" else
                        model.workspaces.firstOrNull { it.optString("path") == model.workspace }?.optString("name")?.takeIf { it.isNotBlank() }
                            ?: java.io.File(model.workspace).name.ifBlank { model.workspace },
                        modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis,
                        style = MaterialTheme.typography.bodyMedium)
                    Icon(Icons.Default.ExpandMore, "选择工作区", Modifier.size(24.dp))
                }
                DropdownMenu(expanded = workspaceMenu, onDismissRequest = { workspaceMenu = false },
                    modifier = Modifier.width(maxWidth).heightIn(max = 360.dp)) {
                    DropdownMenuItem(text = { Text("所有工作区") }, onClick = { workspaceMenu = false; model.workspace = ""; model.perform { model.loadThreads() } })
                    model.workspaces.forEach { ws ->
                        DropdownMenuItem(text = { Text(ws.optString("name").ifBlank { ws.optString("path") }, maxLines = 2, overflow = TextOverflow.Ellipsis) }, onClick = {
                            workspaceMenu = false; model.workspace = ws.optString("path"); model.perform { model.loadThreads() }
                        })
                    }
                }
            }
            Spacer(Modifier.height(8.dp))
            TextField(model.search, { model.search = it }, singleLine = true,
                leadingIcon = { Icon(Icons.Default.Search, null, Modifier.size(20.dp)) },
                placeholder = { Text(if (model.connected) "搜索会话" else "搜索历史标题", style = MaterialTheme.typography.bodyMedium) },
                shape = MaterialTheme.shapes.medium, colors = wuuFieldColors(),
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp))
            LazyColumn(Modifier.weight(1f), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                items(model.rows, key = { it.id }) { row ->
                    ConversationRow(row, model.activeID == row.id, model.connected && !row.saved,
                        open = { scope.launch { drawer.close(); model.perform { model.open(row.id) } } },
                        pin = { model.perform { model.pin(row) } }, archive = { archive = row })
                }
            }

        }
    }) {
        Scaffold(topBar = {
            TopAppBar(title = { Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(model.title, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                Text(if (model.connected) "已连接" else if (model.connecting) "正在连接…" else "离线 · 只读历史", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            } },
                navigationIcon = { IconButton(onClick = { scope.launch { drawer.open() } }) { Icon(Icons.Default.Menu, "会话列表") } },
                actions = {
                    IconButton(onClick = { model.perform { model.newThread() } }, enabled = model.connected) { Icon(Icons.Default.Add, "新会话") }
                    IconButton(onClick = { menu = true }) { Icon(Icons.Default.MoreVert, "会话操作") }
                    DropdownMenu(menu, { menu = false }) {
                        DropdownMenuItem(text = { Text("会话设置") }, enabled = model.connected && model.threadSettings != null && model.activeID != null,
                            onClick = { menu = false; settings = model.activeID!! to model.threadSettings!! })
                        DropdownMenuItem(text = { Text("重命名") }, enabled = model.connected && model.activeID != null,
                            onClick = { menu = false; renameID = model.activeID; renameTitle = model.title })
                        DropdownMenuItem(text = { Text(if (model.hasOlder || model.messages.any { it.contentRef.isNotEmpty() }) "导出已加载文本" else "导出对话文本") }, onClick = {
                            menu = false
                            model.perform { model.export(context) }
                        })
                    }
                })
        }, bottomBar = {
            Column(Modifier.navigationBarsPadding().imePadding().padding(12.dp)) {
                model.questions.firstOrNull { it.optString("thread_id") == model.activeID }?.let { QuestionView(model, it) }
                if (model.running) Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("电脑正在工作", Modifier.weight(1f), style = MaterialTheme.typography.labelMedium)
                    TextButton(onClick = { model.perform { model.stop() } }, enabled = model.connected) { Text("停止") }
                }
                if (attachments.isNotEmpty()) androidx.compose.foundation.lazy.LazyRow {
                    items(attachments, key = { it.id }) { attachment ->
                        TextButton(onClick = { attachmentDrafts[draftKey] = attachments.filterNot { it.id == attachment.id } }, enabled = !model.sending) {
                            Text("移除 ${attachment.filename}", maxLines = 1)
                        }
                    }
                }
                Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (model.connected && model.activeID != null && !model.readOnly && !model.sending) key(draftKey) {
                        AttachmentPicker(attachments, { attachmentDrafts[draftKey] = it }, model)
                    }
                    TextField(draft, { drafts[draftKey] = it }, placeholder = { Text(if (model.running) "添加后续消息" else "发送消息") }, maxLines = 6,
                        shape = MaterialTheme.shapes.large, colors = wuuFieldColors(),
                        modifier = Modifier.weight(1f).testTag("native-composer"), enabled = model.connected && !model.readOnly)
                    FilledIconButton(onClick = {
                        val text = draft; val key = draftKey; val files = attachments
                        model.perform {
                            if (model.send(text, files)) {
                                if (drafts[key] == text) drafts[key] = ""
                                attachmentDrafts[key] = (attachmentDrafts[key] ?: emptyList()).filterNot { file -> files.any { it.id == file.id } }
                            }
                        }
                    }, enabled = model.connected && !model.readOnly && !model.sending && (draft.isNotBlank() || attachments.isNotEmpty())) { Icon(Icons.AutoMirrored.Filled.Send, "发送") }
                }
            }
        }) { padding ->
            val timeline = remember(model.messages) { conversationTimeline(model.messages) }
            val list = key(model.activeID) { rememberLazyListState() }
            var following by remember(model.activeID) { mutableStateOf(true) }
            val dragging by list.interactionSource.collectIsDraggedAsState()
            LaunchedEffect(list, model.activeID) {
                snapshotFlow { dragging to !list.canScrollForward }.collect { (userDrag, atBottom) ->
                    if (userDrag || atBottom) following = atBottom
                }
            }
            // Keep the user's follow choice across insertion and keyboard resize;
            // measuring canScrollForward after insertion loses that choice.
            val viewportSize by remember(list) { derivedStateOf { list.layoutInfo.viewportSize } }
            LaunchedEffect(model.messages.lastOrNull(), model.pending.lastOrNull()?.id, viewportSize) {
                if (following && model.messages.isNotEmpty()) list.requestScrollToItem(timeline.size + model.pending.size + 1)
            }
            LazyColumn(state = list, modifier = Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 20.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
                item(key = "history") {
                    if (model.hasOlder) TextButton(onClick = { model.perform { model.loadOlder() } }, enabled = model.connected && !model.loadingHistory) {
                        Text(if (model.loadingHistory) "正在读取…" else "加载更早的消息")
                    }
                }
                items(timeline, key = { it.key }) { entry ->
                    if (entry is ConversationEntry.Tools) {
                        ToolProcessView(model, entry)
                    } else {
                        val message = (entry as ConversationEntry.Message).message
                        Column(Modifier.fillMaxWidth(), horizontalAlignment = if (message.role == "user") Alignment.End else Alignment.Start) {
                            if (message.role == "pending" || message.role == "error") Text(if (message.role == "pending") "等待处理" else "错误",
                                modifier = Modifier.padding(bottom = 8.dp), style = MaterialTheme.typography.labelMedium,
                                fontWeight = FontWeight.SemiBold, color = if (message.role == "error") MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
                            SelectionContainer {
                                Surface(color = if (message.role == "user") MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface,
                                    shape = MaterialTheme.shapes.medium) { MessageText(message.text, message.role == "assistant",
                                        if (message.role == "user") Modifier.padding(horizontal = 12.dp, vertical = 12.dp) else Modifier.fillMaxWidth()) }
                            }
                            if (message.contentRef.isNotEmpty()) TextButton(onClick = { model.perform { model.expand(message) } }, enabled = model.connected && message.id !in model.loadingContent) {
                                Text(if (message.id in model.loadingContent) "正在读取…" else "加载完整消息")
                            }
                            message.attachments.forEachIndexed { index, raw ->
                                val attachment = org.json.JSONObject(raw)
                                TextButton(onClick = { model.perform { model.previewAttachment(message, index) } }, enabled = model.connected && !model.loadingAttachment) {
                                    Text(attachment.optString("filename", "查看图片"))
                                }
                            }
                        }
                    }
                }
                items(model.pending, key = { "pending:${it.id}" }) { PendingMessageView(model, it) }
                item(key = "bottom") { Spacer(Modifier.height(1.dp)) }
            }
        }
    }
    model.attachmentPreview?.let { AttachmentPreview(model, it) }
    settings?.let { (id, initial) -> ThreadSettingsSheet(model, id, initial) { settings = null } }
    renameID?.let { id -> AlertDialog(onDismissRequest = { renameID = null }, title = { Text("重命名会话") },
        text = { OutlinedTextField(renameTitle, { renameTitle = it }, label = { Text("标题") }) },
        confirmButton = { TextButton(onClick = { val title = renameTitle; renameID = null; model.perform { model.rename(id, title) } }) { Text("保存") } },
        dismissButton = { TextButton(onClick = { renameID = null }) { Text("取消") } }) }
    archive?.let { row -> AlertDialog(onDismissRequest = { archive = null }, title = { Text(if (row.archived) "恢复会话？" else "归档会话？") }, text = { Text(row.title) },
        confirmButton = { TextButton(onClick = { archive = null; model.perform { model.archive(row) } }) { Text(if (row.archived) "恢复" else "归档") } },
        dismissButton = { TextButton(onClick = { archive = null }) { Text("取消") } }) }

}
