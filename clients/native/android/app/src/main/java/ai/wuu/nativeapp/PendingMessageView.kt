package ai.wuu.nativeapp

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

@Composable fun PendingMessageView(model: AppModel, message: PendingMessage) {
    var working by remember { mutableStateOf(false) }
    fun act(resume: Boolean) {
        working = true
        model.perform { try { model.pendingAction(message, resume) } finally { working = false } }
    }
    Surface(color = MaterialTheme.colorScheme.surfaceContainer, shape = MaterialTheme.shapes.medium) {
        Column(Modifier.fillMaxWidth().padding(12.dp)) {
            Text(if (message.held) "已暂停 · 等待继续" else "等待处理", style = MaterialTheme.typography.labelMedium)
            SelectionContainer { Text(message.text) }
            Row {
                if (message.held) TextButton(onClick = { act(true) }, enabled = !working && model.connected && !model.readOnly && !model.running) { Text("继续处理") }
                TextButton(onClick = { act(false) }, enabled = !working && model.connected && !model.readOnly) { Text("移除") }
            }
        }
    }
}
