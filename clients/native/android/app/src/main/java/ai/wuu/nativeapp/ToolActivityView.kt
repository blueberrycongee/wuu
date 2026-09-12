package ai.wuu.nativeapp

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.*
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp

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
