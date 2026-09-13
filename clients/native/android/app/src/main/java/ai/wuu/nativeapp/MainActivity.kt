package ai.wuu.nativeapp

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.collectIsDraggedAsState
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
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
            val dark = androidx.compose.foundation.isSystemInDarkTheme()
            MaterialTheme(colorScheme = if (dark) darkColorScheme() else lightColorScheme()) {
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
@Composable private fun LoginScreen(model: AppModel) {
    val context = LocalContext.current
    val prefs = remember { context.getSharedPreferences("wuu-settings", 0) }
    var server by rememberSaveable { mutableStateOf(prefs.getString("server", "") ?: "") }
    var settings by rememberSaveable { mutableStateOf(server.isBlank()) }
    var passwordLogin by rememberSaveable { mutableStateOf(false) }
    var username by rememberSaveable { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var register by rememberSaveable { mutableStateOf(false) }
    var recover by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(settings, server) { if (!settings) model.loadConfiguration(server) }
    LaunchedEffect(model.resetRecovery) { if (model.resetRecovery != null) { recover = false; passwordLogin = false; password = "" } }
    Scaffold(topBar = { CenterAlignedTopAppBar(title = { Text("连接") }) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).padding(28.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Spacer(Modifier.weight(1f))
            Text("wuu", fontSize = 100.sp, fontWeight = FontWeight.Black)
            Text("连接你的电脑", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
            Text("随时继续对话，让电脑为你工作。", color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.weight(1f))
            if (model.busy) { LinearProgressIndicator(Modifier.fillMaxWidth()); Text("请在浏览器完成登录后返回，或等待登录完成。") }
            if (model.githubURL != null) {
                TextButton(onClick = { model.browserURL = model.githubURL }) { Text("重新打开浏览器") }
                TextButton(onClick = model::cancelLogin) { Text("取消登录") }
            } else {
                if (model.configuration?.optBoolean("github") == true) {
                    Button(onClick = { model.github(server) }, enabled = server.isNotBlank() && !model.busy, modifier = Modifier.fillMaxWidth().height(54.dp)) { Text("使用 GitHub 继续") }
                }
                TextButton(onClick = { passwordLogin = true }, enabled = server.isNotBlank() && !model.busy, modifier = Modifier.fillMaxWidth()) { Text(if (model.configuration?.optBoolean("github") == true) "更多登录方式" else "密码登录") }
                if (model.configuration == null && server.isNotBlank()) {
                    TextButton(onClick = { model.perform { model.loadConfiguration(server) } }) { Text("重新读取服务器设置") }
                }
            }
            TextButton(onClick = { settings = true }, modifier = Modifier.fillMaxWidth()) { Icon(Icons.Default.Tune, null); Spacer(Modifier.width(8.dp)); Text("连接设置") }
        }
    }
    if (settings) ModalBottomSheet(onDismissRequest = { settings = false }) {
        Column(Modifier.padding(24.dp).imePadding(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("连接设置", style = MaterialTheme.typography.headlineSmall)
            OutlinedTextField(server, { server = it }, label = { Text("HTTPS 服务器地址") }, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri), singleLine = true, modifier = Modifier.fillMaxWidth())
            Text("使用与你的电脑相同的 Wuu 服务器。")
            Button(onClick = {
                try { server = AccountAPI(server).origin.toString(); model.cancelLogin(); register = false; prefs.edit().putString("server", server).apply(); settings = false }
                catch (e: Exception) { model.error = e.message }
            }, modifier = Modifier.fillMaxWidth()) { Text("完成") }
        }
    }
    if (passwordLogin) ModalBottomSheet(onDismissRequest = { model.cancelLogin(); passwordLogin = false; password = "" }) {
        Column(Modifier.padding(24.dp).imePadding(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(if (register) "注册账号" else "账号登录", style = MaterialTheme.typography.headlineSmall)
            OutlinedTextField(username, { username = it }, label = { Text("用户名") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(password, { password = it }, label = { Text("密码") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
            Button(onClick = { model.login(server, username, password, register) }, enabled = !model.busy && username.isNotBlank() && password.isNotEmpty(), modifier = Modifier.fillMaxWidth()) { Text(if (register) "注册并登录" else "登录") }
            if (model.configuration?.optBoolean("registration") == true) {
                TextButton(onClick = { register = !register }) { Text(if (register) "已有账号，登录" else "注册账号") }
            }
            TextButton(onClick = { model.cancelLogin(); passwordLogin = false; password = ""; recover = true }) { Text("忘记密码？使用恢复密钥") }
        }
    }
    if (recover) PasswordResetSheet(model, server, false) { recover = false }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable private fun DevicesScreen(model: AppModel) {
    var accountPanel by remember { mutableStateOf(false) }
    var licenses by remember { mutableStateOf(false) }
    var notifications by remember { mutableStateOf(false) }
    var changePassword by remember { mutableStateOf(false) }
    var revokeTarget by remember { mutableStateOf<org.json.JSONObject?>(null) }
    Scaffold(topBar = {
        TopAppBar(title = { Text("你的电脑") }, actions = {
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
                        supportingContent = { Text(if (online) "连接并继续对话" else "查看服务器保存的历史") },
                        leadingContent = { Icon(Icons.Default.Computer, null) }, modifier = Modifier.clickable { model.selectHost(device) })
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
    var historyConsent by remember { mutableStateOf<Boolean?>(null) }
    var workspaceMenu by remember { mutableStateOf(false) }
    val drafts = model.conversationDrafts
    val attachmentDrafts = model.conversationAttachments
    val draftKey = model.activeID ?: "new"
    val draft = drafts[draftKey] ?: ""
    val attachments = attachmentDrafts[draftKey] ?: emptyList()
    BackHandler { if (drawer.isOpen) scope.launch { drawer.close() } else model.perform { model.leaveHost(); model.foreground() } }
    ModalNavigationDrawer(drawerState = drawer, gesturesEnabled = drawer.isOpen, drawerContent = {
        ModalDrawerSheet(Modifier.width(310.dp)) {
            Row(Modifier.fillMaxWidth().padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = { scope.launch { drawer.close() } }) { Icon(Icons.Default.Menu, "关闭会话列表") }
                Text("会话", style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
                IconButton(onClick = { model.perform { model.newThread(); drawer.close() } }, enabled = model.connected) { Icon(Icons.Default.Add, "新会话") }
            }
            TextButton(onClick = { model.perform { model.leaveHost(); model.foreground() } }) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, null)
                Text(model.host?.optString("name") ?: "电脑列表", maxLines = 1)
            }
            TextButton(onClick = { workspaceMenu = true }, enabled = model.connected) { Text(model.workspace.ifBlank { "所有工作区" }, maxLines = 1) }
            OutlinedTextField(model.search, { model.search = it }, singleLine = true,
                placeholder = { Text(if (model.connected) "搜索全部会话内容" else "搜索历史标题") }, modifier = Modifier.padding(horizontal = 12.dp))
            Row(Modifier.padding(horizontal = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("已归档", Modifier.weight(1f))
                Switch(model.archivedList, { model.archivedList = it }, enabled = model.connected)
            }
            DropdownMenu(expanded = workspaceMenu, onDismissRequest = { workspaceMenu = false }) {
                DropdownMenuItem(text = { Text("所有工作区") }, onClick = { workspaceMenu = false; model.workspace = ""; model.perform { model.loadThreads() } })
                model.workspaces.forEach { ws ->
                    DropdownMenuItem(text = { Text(ws.optString("name", ws.optString("path"))) }, onClick = {
                        workspaceMenu = false; model.workspace = ws.optString("path"); model.perform { model.loadThreads() }
                    })
                }
            }
            LazyColumn(Modifier.weight(1f)) {
                items(model.rows, key = { it.id }) { row ->
                    ConversationRow(row, model.activeID == row.id, model.connected && !row.saved,
                        open = { model.perform { drawer.close(); model.open(row.id) } },
                        pin = { model.perform { model.pin(row) } }, archive = { archive = row })
                }
            }
            HorizontalDivider()
            ListItem(headlineContent = { Text("服务器保存对话文本") }, trailingContent = {
                Switch(checked = model.historyEnabled, onCheckedChange = { historyConsent = it })
            }, supportingContent = { Text("电脑离线时也能阅读") })
        }
    }) {
        Scaffold(topBar = {
            TopAppBar(title = { Column { Text(model.title, maxLines = 1); Text(if (model.connected) "已连接" else if (model.connecting) "正在连接…" else "离线 · 只读历史", style = MaterialTheme.typography.labelSmall) } },
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
                    OutlinedTextField(draft, { drafts[draftKey] = it }, placeholder = { Text(if (model.running) "添加后续消息" else "发送消息") }, maxLines = 6, modifier = Modifier.weight(1f).testTag("native-composer"), enabled = model.connected && !model.readOnly)
                    IconButton(onClick = {
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
                if (following && model.messages.isNotEmpty()) list.requestScrollToItem(model.messages.size + model.pending.size + 1)
            }
            LazyColumn(state = list, modifier = Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
                item(key = "history") {
                    if (model.hasOlder) TextButton(onClick = { model.perform { model.loadOlder() } }, enabled = model.connected && !model.loadingHistory) {
                        Text(if (model.loadingHistory) "正在读取…" else "加载更早的消息")
                    }
                }
                if (model.messages.isEmpty()) item { Text(if (model.activeID == null) "选择会话，或创建新会话。" else "暂无文本消息。", color = MaterialTheme.colorScheme.onSurfaceVariant) }
                items(model.messages, key = { it.id }) { message ->
                    if (message.tool != null) {
                        ToolActivityView(model, message, message.tool)
                    } else {
                        Column(Modifier.fillMaxWidth(), horizontalAlignment = if (message.role == "user") Alignment.End else Alignment.Start) {
                            Text(if (message.role == "user") "你" else if (message.role == "pending") "等待处理" else if (message.role == "error") "错误" else "Wuu", style = MaterialTheme.typography.labelMedium)
                            SelectionContainer {
                                Surface(color = if (message.role == "user") MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface,
                                    shape = MaterialTheme.shapes.medium) { MessageText(message.text, message.role == "assistant", Modifier.padding(12.dp)) }
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
    historyConsent?.let { enabled -> AlertDialog(onDismissRequest = { historyConsent = null }, title = { Text(if (enabled) "开启服务器历史？" else "关闭服务器历史？") },
        text = { Text(if (enabled) "电脑会把用户与助手的对话文本保存到服务器。服务器管理员能够读取这些文本；不包含附件和工具输出。开启后可在电脑离线时阅读。" else "服务器将删除已保存的文本，手机也会清除对应缓存。电脑上的原始会话仍然保留。") },
        confirmButton = { TextButton(onClick = { historyConsent = null; model.perform { model.setHistory(enabled) } }) { Text("确认") } },
        dismissButton = { TextButton(onClick = { historyConsent = null }) { Text("取消") } }) }
}
