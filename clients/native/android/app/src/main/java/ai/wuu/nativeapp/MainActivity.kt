package ai.wuu.nativeapp

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
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
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState); enableEdgeToEdge()
        setContent {
            val dark = androidx.compose.foundation.isSystemInDarkTheme()
            MaterialTheme(colorScheme = if (dark) darkColorScheme() else lightColorScheme()) {
                Surface(Modifier.fillMaxSize()) { WuuRoot(viewModel()) }
            }
        }
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
        else -> ConversationScreen(model)
    }
    model.account?.recovery?.takeIf { it.isNotEmpty() }?.let { secret ->
        AlertDialog(onDismissRequest = {}, title = { Text("保存账号恢复密钥") },
            text = { androidx.compose.foundation.text.selection.SelectionContainer {
                Text("请将下面的密钥保存到密码管理器。它可用于重置密码，切勿分享。确认后此页面不再展示。\n\n$secret")
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
    Scaffold(topBar = { CenterAlignedTopAppBar(title = { Text("连接") }) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).padding(28.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Spacer(Modifier.weight(1f))
            Text("wuu", fontSize = 100.sp, fontWeight = FontWeight.Black)
            Text("连接你的电脑", style = MaterialTheme.typography.headlineLarge, fontWeight = FontWeight.Bold)
            Text("随时继续对话，让电脑为你工作。", color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.weight(1f))
            if (model.busy) { LinearProgressIndicator(Modifier.fillMaxWidth()); Text("请在浏览器完成登录后返回，或等待登录完成。") }
            Button(onClick = { model.github(server) }, enabled = server.isNotBlank() && !model.busy, modifier = Modifier.fillMaxWidth().height(54.dp)) { Text("使用 GitHub 继续") }
            TextButton(onClick = { passwordLogin = true }, modifier = Modifier.fillMaxWidth()) { Text("更多登录方式") }
            TextButton(onClick = { settings = true }, modifier = Modifier.fillMaxWidth()) { Icon(Icons.Default.Tune, null); Spacer(Modifier.width(8.dp)); Text("连接设置") }
        }
    }
    if (settings) ModalBottomSheet(onDismissRequest = { settings = false }) {
        Column(Modifier.padding(24.dp).imePadding(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("连接设置", style = MaterialTheme.typography.headlineSmall)
            OutlinedTextField(server, { server = it }, label = { Text("HTTPS 服务器地址") }, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri), singleLine = true, modifier = Modifier.fillMaxWidth())
            Text("使用与你的电脑相同的 Wuu 服务器。")
            Button(onClick = {
                try { server = AccountAPI(server).origin.toString(); prefs.edit().putString("server", server).apply(); settings = false }
                catch (e: Exception) { model.error = e.message }
            }, modifier = Modifier.fillMaxWidth()) { Text("完成") }
        }
    }
    if (passwordLogin) ModalBottomSheet(onDismissRequest = { passwordLogin = false; password = "" }) {
        Column(Modifier.padding(24.dp).imePadding(), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(if (register) "注册账号" else "账号登录", style = MaterialTheme.typography.headlineSmall)
            OutlinedTextField(username, { username = it }, label = { Text("用户名") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(password, { password = it }, label = { Text("密码") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
            Button(onClick = { model.login(server, username, password, register) }, enabled = !model.busy && username.isNotBlank() && password.isNotEmpty(), modifier = Modifier.fillMaxWidth()) { Text(if (register) "注册并登录" else "登录") }
            TextButton(onClick = { register = !register }) { Text(if (register) "已有账号，登录" else "注册账号（需要服务器开放注册）") }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable private fun DevicesScreen(model: AppModel) {
    var accountPanel by remember { mutableStateOf(false) }
    var revokeTarget by remember { mutableStateOf<org.json.JSONObject?>(null) }
    Scaffold(topBar = {
        TopAppBar(title = { Text("你的电脑") }, actions = {
            IconButton(onClick = { model.perform { model.loadDevices() } }) { Icon(Icons.Default.Refresh, "刷新") }
            IconButton(onClick = { accountPanel = true }) { Icon(Icons.Default.AccountCircle, "账号") }
        })
    }) { padding ->
        LazyColumn(Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
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
        Column(Modifier.padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(model.account?.username ?: "", style = MaterialTheme.typography.headlineSmall)
            Text(model.account?.server ?: "")
            LazyColumn(Modifier.heightIn(max = 300.dp)) {
                items(model.devices, key = { it.getString("pub") }) { device ->
                    ListItem(headlineContent = { Text(device.optString("name")) }, trailingContent = {
                        TextButton(onClick = { revokeTarget = device }) { Text("移除") }
                    })
                }
            }
            Text("退出登录会删除这部手机缓存的会话文本。")
            Button(onClick = { accountPanel = false; model.logout() }, modifier = Modifier.fillMaxWidth()) { Text("退出登录") }
        }
    }
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
    var archive by remember { mutableStateOf<ThreadRow?>(null) }
    var historyConsent by remember { mutableStateOf<Boolean?>(null) }
    var workspaceMenu by remember { mutableStateOf(false) }
    val drafts = remember { mutableStateMapOf<String, String>() }
    val draftKey = model.activeID ?: "new"
    val draft = drafts[draftKey] ?: ""
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
                actions = { IconButton(onClick = { model.perform { model.newThread() } }, enabled = model.connected) { Icon(Icons.Default.Add, "新会话") } })
        }, bottomBar = {
            Column(Modifier.navigationBarsPadding().imePadding().padding(12.dp)) {
                model.questions.firstOrNull { it.optString("thread_id") == model.activeID }?.let { QuestionView(model, it) }
                if (model.running) Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("电脑正在工作", Modifier.weight(1f), style = MaterialTheme.typography.labelMedium)
                    TextButton(onClick = { model.perform { model.stop() } }, enabled = model.connected) { Text("停止") }
                }
                Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedTextField(draft, { drafts[draftKey] = it }, placeholder = { Text(if (model.running) "添加后续消息" else "发送消息") }, maxLines = 6, modifier = Modifier.weight(1f), enabled = model.connected && !model.readOnly)
                    IconButton(onClick = {
                        val text = draft; val key = draftKey
                        model.perform { if (model.send(text) && drafts[key] == text) drafts[key] = "" }
                    }, enabled = model.connected && !model.readOnly && !model.sending && draft.isNotBlank()) { Icon(Icons.AutoMirrored.Filled.Send, "发送") }
                }
            }
        }) { padding ->
            val list = rememberLazyListState()
            val nearBottom by remember { derivedStateOf { list.layoutInfo.visibleItemsInfo.lastOrNull()?.index?.let { it >= model.messages.lastIndex - 1 } ?: true } }
            LaunchedEffect(model.messages.lastOrNull()) { if (nearBottom && model.messages.isNotEmpty()) list.animateScrollToItem(model.messages.lastIndex) }
            LazyColumn(state = list, modifier = Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(20.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
                if (model.messages.isEmpty()) item { Text(if (model.activeID == null) "选择会话，或创建新会话。" else "暂无文本消息。", color = MaterialTheme.colorScheme.onSurfaceVariant) }
                items(model.messages, key = { it.id }) { message ->
                    Column(Modifier.fillMaxWidth(), horizontalAlignment = if (message.role == "user") Alignment.End else Alignment.Start) {
                        Text(if (message.role == "user") "你" else if (message.role == "pending") "等待处理" else if (message.role == "error") "错误" else "Wuu", style = MaterialTheme.typography.labelMedium)
                        SelectionContainer {
                            Surface(color = if (message.role == "user") MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface,
                                shape = MaterialTheme.shapes.medium) { Text(message.text, Modifier.padding(12.dp)) }
                        }
                    }
                }
            }
        }
    }
    archive?.let { row -> AlertDialog(onDismissRequest = { archive = null }, title = { Text("归档会话？") }, text = { Text(row.title) },
        confirmButton = { TextButton(onClick = { archive = null; model.perform { model.archive(row) } }) { Text("归档") } },
        dismissButton = { TextButton(onClick = { archive = null }) { Text("取消") } }) }
    historyConsent?.let { enabled -> AlertDialog(onDismissRequest = { historyConsent = null }, title = { Text(if (enabled) "开启服务器历史？" else "关闭服务器历史？") },
        text = { Text(if (enabled) "电脑会把用户与助手的对话文本保存到服务器。服务器管理员能够读取这些文本；不包含附件和工具输出。开启后可在电脑离线时阅读。" else "服务器将删除已保存的文本，手机也会清除对应缓存。电脑上的原始会话仍然保留。") },
        confirmButton = { TextButton(onClick = { historyConsent = null; model.perform { model.setHistory(enabled) } }) { Text("确认") } },
        dismissButton = { TextButton(onClick = { historyConsent = null }) { Text("取消") } }) }
}
