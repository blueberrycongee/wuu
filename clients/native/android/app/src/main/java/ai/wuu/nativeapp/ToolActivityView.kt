package ai.wuu.nativeapp

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.Alignment
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable
internal fun ToolProcessView(model: AppModel, group: ConversationEntry.Tools) {
    var expanded by rememberSaveable(group.key) { mutableStateOf(false) }
    val running = group.running
    val failures = group.failedCount
    val summary = when {
        running != null -> "正在调用 ${running.tool!!.name} · ${group.messages.size} 次调用"
        failures > 0 -> "${group.messages.size} 次工具调用"
        group.completed -> "已完成 ${group.messages.size} 次工具调用"
        else -> "${group.messages.size} 次工具调用"
    }
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth().testTag("tool-process")
            .semantics { stateDescription = if (expanded) "已展开" else "已折叠" }
            .clickable(role = Role.Button, onClickLabel = if (expanded) "收起工具过程" else "展开工具过程") { expanded = !expanded }
            .heightIn(min = 48.dp), verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (running != null) CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            Text(summary, Modifier.weight(1f), style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (failures > 0) Text("$failures 项失败", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.error)
            Icon(if (expanded) Icons.Default.KeyboardArrowDown else Icons.AutoMirrored.Filled.KeyboardArrowRight,
                null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (expanded) {
            Surface(color = MaterialTheme.colorScheme.surfaceContainerLow, shape = MaterialTheme.shapes.medium) {
                LazyColumn(Modifier.fillMaxWidth().heightIn(max = 280.dp), contentPadding = PaddingValues(12.dp)) {
                    items(group.messages, key = { it.id }) { message ->
                        ToolActivityView(model, message, checkNotNull(message.tool))
                    }
                }
            }
        }
    }
}

@Composable
fun ToolActivityView(model: AppModel, message: ChatMessage, tool: ToolActivity) {
    var expanded by rememberSaveable(message.id) { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(Modifier.fillMaxWidth().testTag("tool-activity").semantics { stateDescription = if (expanded) "已展开" else "已折叠" }
            .clickable(role = Role.Button, onClickLabel = if (expanded) "收起详情" else "展开详情") { expanded = !expanded }
            .padding(vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(if (expanded) Icons.Default.KeyboardArrowDown else Icons.AutoMirrored.Filled.KeyboardArrowRight, null, Modifier.size(20.dp))
            Text(tool.name, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, maxLines = 2)
            Text(tool.statusLabel, style = MaterialTheme.typography.bodyMedium,
                color = if (tool.status == "failed") MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (expanded) {
            if (tool.arguments.isNotEmpty()) ToolDetail("参数", tool.arguments)
            if (tool.result.isNotEmpty()) ToolDetail("结果", tool.result)
            if (tool.error.isNotEmpty()) SelectionContainer { Text(tool.error, color = MaterialTheme.colorScheme.error) }
            if (message.contentRef.isNotEmpty()) TextButton(onClick = { model.perform { model.expand(message) } },
                enabled = model.connected && message.id !in model.loadingContent) {
                Text(if (message.id in model.loadingContent) "正在读取…" else "加载完整详情")
            }
        }
    }
}

@Composable
private fun ToolDetail(title: String, text: String) {
    Text(title, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    SelectionContainer { Text(text, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace) }
}
