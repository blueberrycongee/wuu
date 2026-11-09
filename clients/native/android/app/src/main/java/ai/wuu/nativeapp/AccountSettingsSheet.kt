package ai.wuu.nativeapp

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import org.json.JSONObject

@OptIn(ExperimentalMaterial3Api::class)
@Composable internal fun AccountSettingsSheet(model: AppModel, dismiss: () -> Unit) {
    var page by remember { mutableStateOf("") }
    var revoke by remember { mutableStateOf<JSONObject?>(null) }
    when (page) {
        "password" -> PasswordResetSheet(model, model.account?.server.orEmpty(), true) { page = "" }
        "licenses" -> LicensesSheet { page = "" }
        "notifications" -> PushSettingsSheet(model.push) { page = "" }
        else -> ModalBottomSheet(onDismissRequest = dismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
            LazyColumn(contentPadding = PaddingValues(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                item {
                    Surface(shape = MaterialTheme.shapes.medium, color = MaterialTheme.colorScheme.surfaceContainerLow) {
                        ListItem(headlineContent = { Text(model.account?.username.orEmpty(), style = MaterialTheme.typography.titleMedium) },
                            supportingContent = { Text(model.account?.server.orEmpty(), style = MaterialTheme.typography.bodySmall) },
                            leadingContent = { Icon(Icons.Default.AccountCircle, null) }, colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow))
                    }
                }
                if (model.host != null) item {
                    SettingsRow("切换电脑", Icons.Default.Computer) { dismiss(); model.perform { model.leaveHost(); model.foreground() } }
                }
                item {
                    Surface(shape = MaterialTheme.shapes.medium, color = MaterialTheme.colorScheme.surfaceContainerLow) {
                        Column {
                            SettingsRow("通知", Icons.Default.NotificationsNone) { page = "notifications" }
                            if (model.authMethod == "password") SettingsRow("修改密码", Icons.Default.LockReset) { page = "password" }
                            SettingsRow("开源许可", Icons.Default.Description) { page = "licenses" }
                        }
                    }
                }
                item { Text("设备", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                items(model.devices, key = { it.optString("pub") }) { device ->
                    ListItem(headlineContent = { Text(device.optString("name"), style = MaterialTheme.typography.bodyMedium) },
                        leadingContent = { Icon(if (device.optString("role") == "host") Icons.Default.Computer else Icons.Default.Smartphone, null, Modifier.size(20.dp)) },
                        trailingContent = { IconButton(onClick = { revoke = device }) { Icon(Icons.Default.RemoveCircleOutline, "移除 ${device.optString("name")}", Modifier.size(20.dp)) } })
                }
                item {
                    TextButton(onClick = { dismiss(); model.logout() }, enabled = !model.busy, modifier = Modifier.fillMaxWidth(),
                        colors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error)) { Text("退出登录") }
                    Spacer(Modifier.height(16.dp))
                }
            }
        }
    }
    revoke?.let { device ->
        AlertDialog(onDismissRequest = { revoke = null }, title = { Text("移除设备？") },
            text = { Text("移除 ${device.optString("name")} 的账号访问权限。") },
            confirmButton = { TextButton(onClick = { revoke = null; model.perform { model.revoke(device) } }) { Text("移除") } },
            dismissButton = { TextButton(onClick = { revoke = null }) { Text("取消") } })
    }
}

@Composable private fun SettingsRow(title: String, icon: androidx.compose.ui.graphics.vector.ImageVector, action: () -> Unit) {
    ListItem(headlineContent = { Text(title, style = MaterialTheme.typography.bodyLarge) },
        leadingContent = { Icon(icon, null, Modifier.size(20.dp)) },
        trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null, Modifier.size(18.dp)) },
        colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow), modifier = Modifier.clickable(onClick = action))
}
