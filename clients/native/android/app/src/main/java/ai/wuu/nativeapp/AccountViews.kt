package ai.wuu.nativeapp

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp

@OptIn(ExperimentalMaterial3Api::class)
@Composable fun PasswordResetSheet(model: AppModel, server: String, changing: Boolean, dismiss: () -> Unit) {
    var username by remember { mutableStateOf("") }
    var secret by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var confirmation by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf(false) }
    ModalBottomSheet(onDismissRequest = dismiss) {
        Column(Modifier.padding(24.dp).imePadding().verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text(if (changing) "修改密码" else "找回账号", style = MaterialTheme.typography.headlineSmall)
            if (!changing) OutlinedTextField(username, { username = it }, label = { Text("用户名") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(secret, { secret = it }, label = { Text(if (changing) "当前密码" else "恢复密钥") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(password, { password = it }, label = { Text("新密码（至少 12 字节）") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(confirmation, { confirmation = it }, label = { Text("再次输入新密码") }, visualTransformation = PasswordVisualTransformation(), singleLine = true, modifier = Modifier.fillMaxWidth())
            Text("更新密码会撤销所有电脑和手机的登录，并生成新的恢复密钥。设备需要重新登录才能继续使用。")
            Button(onClick = { confirm = true }, enabled = !model.busy && (changing || username.isNotBlank()) && secret.isNotEmpty() && password.toByteArray().size in 12..1024 && password == confirmation) { Text("更新密码") }
        }
    }
    if (confirm) AlertDialog(onDismissRequest = { confirm = false }, title = { Text("更新密码并退出所有设备？") },
        confirmButton = { TextButton(onClick = { confirm = false; model.resetPassword(server, username, secret, password, changing) }) { Text("确认更新") } },
        dismissButton = { TextButton(onClick = { confirm = false }) { Text("取消") } })
}
