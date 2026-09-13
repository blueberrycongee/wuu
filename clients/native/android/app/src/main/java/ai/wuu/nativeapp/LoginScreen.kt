package ai.wuu.nativeapp

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

@Composable internal fun LoginScreen(model: AppModel) {
    LoginContent(model, isSystemInDarkTheme())
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable private fun LoginContent(model: AppModel, dark: Boolean) {
    val context = LocalContext.current
    val focus = LocalFocusManager.current
    val prefs = remember { context.getSharedPreferences("wuu-settings", 0) }
    var server by rememberSaveable { mutableStateOf(prefs.getString("server", "") ?: "") }
    var serverDraft by rememberSaveable { mutableStateOf(server) }
    var settings by rememberSaveable { mutableStateOf(false) }
    var passwordLogin by rememberSaveable { mutableStateOf(false) }
    var username by rememberSaveable { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var showPassword by remember { mutableStateOf(false) }
    var register by rememberSaveable { mutableStateOf(false) }
    var recover by rememberSaveable { mutableStateOf(false) }
    val github = model.configuration?.optBoolean("github") == true
    val passwordForm = passwordLogin || (!github && model.configuration != null)
    val canLogin = !model.busy && username.isNotBlank() && password.isNotEmpty()
    val saveServer: () -> Unit = {
        try {
            val origin = AccountAPI(serverDraft).origin.toString()
            focus.clearFocus()
            model.cancelLogin()
            register = false; passwordLogin = false; password = ""
            prefs.edit().putString("server", origin).apply()
            if (origin == server) model.perform { model.loadConfiguration(origin) }
            server = origin; serverDraft = origin; settings = false
        } catch (e: Exception) { model.error = e.message }
    }
    LaunchedEffect(server) { model.loadConfiguration(server) }
    LaunchedEffect(model.resetRecovery) {
        if (model.resetRecovery != null) { recover = false; register = false; password = "" }
    }
    BackHandler(enabled = (passwordLogin || register) && !settings && !recover) {
        model.cancelLogin()
        if (register) register = false else passwordLogin = false
        password = ""
    }
    val hero = if (dark) listOf(Color(0xFF233E32), Color(0xFF1D3029))
        else listOf(Color(0xFFD1F8EB), Color(0xFFECF8DB))
    val keyboardVisible = WindowInsets.ime.getBottom(LocalDensity.current) > 0
    BoxWithConstraints(Modifier.fillMaxSize().background(Brush.horizontalGradient(hero)).statusBarsPadding().imePadding()) {
        val heroHeight = (maxHeight * if (keyboardVisible) 0.18f else 0.28f)
            .coerceAtMost(if (keyboardVisible) 96.dp else 218.dp)
        Column(Modifier.fillMaxSize()) {
            Box(Modifier.fillMaxWidth().height(heroHeight).clip(RoundedCornerShape(0.dp))) {
                Column(Modifier.align(Alignment.CenterStart).padding(start = 28.dp)) {
                    Text("Hello!", fontSize = if (keyboardVisible) 28.sp else 36.sp, fontWeight = FontWeight.ExtraBold,
                        color = MaterialTheme.colorScheme.onSurface)
                    if (heroHeight >= 128.dp) {
                        Spacer(Modifier.height(6.dp))
                        Text("欢迎来到 Wuu", style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                Image(painterResource(R.drawable.login_mascot), null,
                    Modifier.align(Alignment.BottomEnd).offset(x = 32.dp, y = 18.dp).size(if (keyboardVisible) 112.dp else 204.dp))
            }
            Surface(Modifier.fillMaxWidth().weight(1f), shape = RoundedCornerShape(topStart = 28.dp, topEnd = 28.dp)) {
                Column(Modifier.widthIn(max = 480.dp).navigationBarsPadding().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp, vertical = if (keyboardVisible) 16.dp else 30.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp)) {
                    Text(if (server.isBlank()) "连接你的电脑" else if (register) "创建账号" else "登录 Wuu",
                        style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                    when {
                        server.isBlank() -> {
                            Text("使用与你的电脑相同的 Wuu 服务器。", style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
                            LoginField(serverDraft, { serverDraft = it }, "HTTPS 服务器地址",
                                KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done),
                                KeyboardActions(onDone = { if (serverDraft.isNotBlank()) saveServer() }))
                            LoginButton("继续", serverDraft.isNotBlank(), saveServer)
                        }
                        model.githubURL != null -> {
                            LinearProgressIndicator(Modifier.fillMaxWidth())
                            Text("在浏览器完成 GitHub 登录后返回", style = MaterialTheme.typography.bodyMedium)
                            LoginButton("重新打开浏览器", true) { model.browserURL = model.githubURL }
                            TextButton(model::cancelLogin, Modifier.fillMaxWidth()) { Text("取消登录") }
                        }
                        passwordForm -> {
                            LoginField(username, { username = it }, "用户名", KeyboardOptions(imeAction = ImeAction.Next))
                            TextField(password, { password = it }, Modifier.fillMaxWidth(),
                                placeholder = { Text("密码") }, singleLine = true,
                                visualTransformation = if (showPassword) VisualTransformation.None else PasswordVisualTransformation(),
                                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done),
                                keyboardActions = KeyboardActions(onDone = {
                                    if (canLogin) { focus.clearFocus(); model.login(server, username, password, register) }
                                }),
                                trailingIcon = { IconButton({ showPassword = !showPassword }) {
                                    Icon(if (showPassword) Icons.Default.VisibilityOff else Icons.Default.Visibility,
                                        if (showPassword) "隐藏密码" else "显示密码")
                                } }, shape = RoundedCornerShape(16.dp), colors = wuuFieldColors())
                            LoginButton(if (model.busy) "正在登录…" else if (register) "注册并登录" else "登录", canLogin) {
                                focus.clearFocus(); model.login(server, username, password, register)
                            }
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                                if (model.configuration?.optBoolean("registration") == true) {
                                    TextButton({ register = !register }, enabled = !model.busy) { Text(if (register) "已有账号，登录" else "注册账号") }
                                } else Spacer(Modifier.width(1.dp))
                                TextButton({ model.cancelLogin(); password = ""; recover = true }, enabled = !model.busy) { Text("忘记密码") }
                            }
                            if (github) TextButton({ passwordLogin = false; register = false; password = "" },
                                Modifier.fillMaxWidth(), enabled = !model.busy) { Text("使用 GitHub 继续") }
                        }
                        github -> {
                            LoginButton(if (model.busy) "正在连接…" else "使用 GitHub 继续", !model.busy) { model.github(server) }
                            TextButton({ passwordLogin = true }, Modifier.fillMaxWidth(), enabled = !model.busy) { Text("更多登录方式") }
                        }
                        else -> {
                            Text("正在读取服务器的登录方式", style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
                            TextButton({ model.perform { model.loadConfiguration(server) } }, Modifier.fillMaxWidth()) { Text("重新读取服务器设置") }
                        }
                    }
                    if (server.isNotBlank()) {
                        Spacer(Modifier.height(4.dp))
                        TextButton({ serverDraft = server; settings = true }, Modifier.fillMaxWidth(), enabled = !model.busy) {
                            Icon(Icons.Default.Tune, null, Modifier.size(16.dp))
                            Spacer(Modifier.width(8.dp))
                            Text(android.net.Uri.parse(server).authority ?: server, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Spacer(Modifier.width(6.dp))
                            Icon(Icons.Default.ExpandMore, "连接设置", Modifier.size(16.dp))
                        }
                    }
                }
            }
        }
    }
    if (settings) ModalBottomSheet(onDismissRequest = { settings = false }, containerColor = MaterialTheme.colorScheme.surface) {
        Column(Modifier.imePadding().verticalScroll(rememberScrollState()).padding(24.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("连接设置", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
            LoginField(serverDraft, { serverDraft = it }, "HTTPS 服务器地址",
                KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done),
                KeyboardActions(onDone = { if (serverDraft.isNotBlank()) saveServer() }))
            Text("使用与你的电脑相同的 Wuu 服务器。", style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            LoginButton("完成", serverDraft.isNotBlank(), saveServer)
        }
    }
    if (recover) PasswordResetSheet(model, server, false) { recover = false }
}

@Composable private fun LoginField(value: String, change: (String) -> Unit, placeholder: String,
    options: KeyboardOptions, actions: KeyboardActions = KeyboardActions.Default) {
    TextField(value, change, Modifier.fillMaxWidth(), placeholder = { Text(placeholder) }, singleLine = true,
        keyboardOptions = options, keyboardActions = actions, shape = RoundedCornerShape(16.dp), colors = wuuFieldColors())
}

@Composable private fun LoginButton(label: String, enabled: Boolean, click: () -> Unit) {
    Button(click, Modifier.fillMaxWidth().heightIn(min = 52.dp), enabled = enabled, shape = RoundedCornerShape(26.dp)) {
        Text(label, style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold)
    }
}
