package ai.wuu.nativeapp

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.layout.*
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import kotlin.math.roundToInt

@Composable fun ConversationRow(row: ThreadRow, selected: Boolean, enabled: Boolean, open: () -> Unit, pin: () -> Unit, archive: () -> Unit) {
    val width = with(LocalDensity.current) { 136.dp.toPx() }
    var offset by remember(row.id) { mutableFloatStateOf(0f) }
    LaunchedEffect(enabled) { if (!enabled) offset = 0f }
    val pinLabel = if (row.pinned) "取消置顶" else "置顶"
    val archiveLabel = if (row.archived) "恢复" else "归档"
    Box(Modifier.fillMaxWidth().clipToBounds()) {
        if (offset < 0f) Row(Modifier.matchParentSize().background(MaterialTheme.colorScheme.surfaceContainer), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
            TextButton(onClick = { offset = 0f; pin() }, enabled = enabled, modifier = Modifier.width(68.dp)) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) { Icon(Icons.Default.PushPin, null); Text(pinLabel, style = MaterialTheme.typography.labelSmall) }
            }
            TextButton(onClick = { offset = 0f; archive() }, enabled = enabled, modifier = Modifier.width(68.dp)) {
                Column(horizontalAlignment = Alignment.CenterHorizontally) { Icon(Icons.Default.Archive, null); Text(archiveLabel, style = MaterialTheme.typography.labelSmall) }
            }
        }
        ListItem(headlineContent = { Text(row.title.ifBlank { "新会话" }, maxLines = 2) },
            supportingContent = { if (row.saved) Text("服务器历史") },
            leadingContent = { if (row.pinned) Icon(Icons.Default.PushPin, "已置顶") },
            colors = ListItemDefaults.colors(containerColor = if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface),
            modifier = Modifier.offset { IntOffset(offset.roundToInt(), 0) }
                .pointerInput(enabled, width) {
                    if (enabled) detectHorizontalDragGestures(
                        onDragEnd = { offset = if (offset < -width / 3) -width else 0f },
                        onDragCancel = { offset = 0f },
                        onHorizontalDrag = { change, amount -> change.consume(); offset = (offset + amount).coerceIn(-width, 0f) })
                }.clickable { if (offset != 0f) offset = 0f else open() }
                .semantics {
                    if (enabled) customActions = listOf(CustomAccessibilityAction(pinLabel) { pin(); true }, CustomAccessibilityAction(archiveLabel) { archive(); true })
                })
    }
}
