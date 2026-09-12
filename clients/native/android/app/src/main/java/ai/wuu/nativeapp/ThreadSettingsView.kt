package ai.wuu.nativeapp

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable fun ThreadSettingsSheet(model: AppModel, threadID: String, initial: ThreadSettings, dismiss: () -> Unit) {
    var selection by remember { mutableStateOf(initial) }
    var providers by remember { mutableStateOf(emptyList<RemoteProvider>()) }
    var loading by remember { mutableStateOf(true) }
    var saving by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var confirm by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val models = providers.firstOrNull { it.id == selection.provider }?.models.orEmpty()
    val variants = models.firstOrNull { it.id == selection.model }?.variants.orEmpty()
    val editable = model.connected && model.activeID == threadID && !model.running && !model.readOnly && model.threadEngine in listOf("", "wuu")
    fun save() {
        saving = true; error = null
        scope.launch {
            try { model.updateSettings(selection, threadID); dismiss() }
            catch (e: CancellationException) { dismiss(); throw e }
            catch (e: Exception) { error = e.message }
            finally { saving = false }
        }
    }
    LaunchedEffect(threadID) {
        try { providers = model.loadModelChoices(threadID) }
        catch (e: CancellationException) { throw e }
        catch (e: Exception) { error = e.message }
        finally { loading = false }
    }
    ModalBottomSheet(onDismissRequest = { if (!saving) dismiss() }, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(20.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("会话设置", style = MaterialTheme.typography.titleMedium)
            SettingsChoice("提供商", selection.provider, providers.map { it.id to it.id }, editable && !saving) { provider ->
                selection = selection.copy(provider = provider, model = providers.firstOrNull { it.id == provider }?.models?.firstOrNull()?.id.orEmpty(), variant = "")
            }
            SettingsChoice("模型", models.firstOrNull { it.id == selection.model }?.name ?: selection.model, models.map { it.id to it.name }, editable && !saving) {
                selection = selection.copy(model = it, variant = "")
            }
            if (variants.isNotEmpty() || selection.variant.isNotEmpty()) SettingsChoice("推理配置", selection.variant.ifEmpty { "模型默认" },
                listOf("" to "模型默认") + variants.map { it to it }, editable && !saving) { selection = selection.copy(variant = it) }
            Text("仅影响当前会话。模型连接和凭据在电脑上管理。", style = MaterialTheme.typography.bodySmall)
            val permissions = listOf("standard" to "标准", "read_only" to "只读", "unconfined" to "不受限")
            SettingsChoice("执行权限", permissions.firstOrNull { it.first == selection.permission }?.second ?: selection.permission,
                permissions, editable && !saving) { selection = selection.copy(permission = it) }
            Text(when (selection.permission) {
                "unconfined" -> "允许任务访问电脑上当前用户可访问的文件和网络。"
                "read_only" -> "限制为读取，不允许修改文件。"
                else -> "使用电脑上的工作区和网络访问边界。"
            }, style = MaterialTheme.typography.bodySmall)
            if (loading) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (!editable) Text("连接电脑并等待任务结束后可修改。其他执行引擎的设置请在电脑上管理。", style = MaterialTheme.typography.bodySmall)
            error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                TextButton(onClick = dismiss, enabled = !saving) { Text("取消") }
                TextButton(onClick = {
                    if (selection.permission == "unconfined" && initial.permission != "unconfined") confirm = true else save()
                }, enabled = editable && !saving && !loading && selection != initial && selection.provider.isNotBlank() && selection.model.isNotBlank()) { Text("保存") }
            }
        }
    }
    if (confirm) AlertDialog(onDismissRequest = { confirm = false }, title = { Text("允许此会话不受限地访问电脑？") },
        text = { Text("任务将不再受工作区文件和网络边界限制。仅对信任的任务开启。") },
        confirmButton = { TextButton(onClick = { confirm = false; save() }) { Text("允许不受限访问") } },
        dismissButton = { TextButton(onClick = { confirm = false }) { Text("取消") } })
}

@Composable private fun SettingsChoice(label: String, value: String, options: List<Pair<String, String>>, enabled: Boolean, select: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    Box {
        TextButton(onClick = { expanded = true }, enabled = enabled && options.isNotEmpty(), modifier = Modifier.fillMaxWidth()) {
            Text(label, Modifier.weight(1f)); Text(value, Modifier.weight(2f))
        }
        DropdownMenu(expanded, onDismissRequest = { expanded = false }) {
            options.forEach { (id, name) -> DropdownMenuItem(text = { Text(name) }, onClick = { expanded = false; select(id) }) }
        }
    }
}
