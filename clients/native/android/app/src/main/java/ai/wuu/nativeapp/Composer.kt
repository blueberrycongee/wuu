package ai.wuu.nativeapp

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Image
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

@Composable internal fun ComposerRow(
    value: String,
    onChange: (String) -> Unit,
    onSend: () -> Unit,
    enabled: Boolean,
    sending: Boolean,
    attachments: List<InputAttachment>,
    onAttachments: (List<InputAttachment>) -> Unit,
    model: AppModel,
    draftKey: Any,
    placeholder: String = "发送消息",
    showAttach: Boolean = true,
) {
    if (attachments.isNotEmpty()) LazyRow(
        contentPadding = PaddingValues(start = 52.dp, bottom = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        items(attachments, key = { it.id }) { attachment ->
            if (attachment.isImage) Box(Modifier.size(64.dp)) {
                Surface(shape = MaterialTheme.shapes.small, modifier = Modifier.fillMaxSize()) { DraftImage(attachment) }
                IconButton(onClick = { onAttachments(attachments.filterNot { it.id == attachment.id }) }, enabled = !sending,
                    modifier = Modifier.size(32.dp).align(Alignment.TopEnd)) {
                    Surface(shape = CircleShape, color = MaterialTheme.colorScheme.surface.copy(alpha = 0.9f)) {
                        Icon(Icons.Default.Close, "移除附件 ${attachment.filename}", Modifier.size(22.dp).padding(3.dp))
                    }
                }
            } else InputChip(selected = false, onClick = { onAttachments(attachments.filterNot { it.id == attachment.id }) }, enabled = !sending,
                label = { Text(attachment.filename, Modifier.widthIn(max = 160.dp), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                leadingIcon = { Icon(if (attachment.isImage) Icons.Default.Image else Icons.Default.Description, null, Modifier.size(16.dp)) },
                trailingIcon = { Icon(Icons.Default.Close, "移除附件 ${attachment.filename}", Modifier.size(16.dp)) })
        }
    }
    Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        if (showAttach) key(draftKey) { AttachmentPicker(attachments, onAttachments, model, enabled && !sending) }
        Surface(Modifier.weight(1f), shape = MaterialTheme.shapes.large,
            color = MaterialTheme.colorScheme.surfaceContainerLow,
            border = BorderStroke(0.5.dp, MaterialTheme.colorScheme.outlineVariant)) {
            Row(verticalAlignment = Alignment.Bottom, modifier = Modifier.padding(end = 3.dp)) {
                key(draftKey) {
                    BasicTextField(value, onChange, enabled = enabled, maxLines = 6,
                        textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurface),
                        cursorBrush = SolidColor(MaterialTheme.colorScheme.onSurface),
                        modifier = Modifier.weight(1f).heightIn(min = 44.dp).padding(start = 15.dp, end = 4.dp, top = 11.dp, bottom = 11.dp).testTag("native-composer"),
                        decorationBox = { field ->
                            Box {
                                if (value.isEmpty()) Text(if (enabled) placeholder else "连接电脑后发送", style = MaterialTheme.typography.bodyLarge,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                field()
                            }
                        })
                }
                IconButton(onClick = onSend, enabled = enabled && !sending && (value.isNotBlank() || attachments.isNotEmpty()), modifier = Modifier.size(44.dp)) {
                    Surface(shape = CircleShape,
                        color = if (enabled && (value.isNotBlank() || attachments.isNotEmpty())) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.surfaceContainerHighest,
                        contentColor = if (enabled && (value.isNotBlank() || attachments.isNotEmpty())) MaterialTheme.colorScheme.surface else MaterialTheme.colorScheme.onSurfaceVariant) {
                        if (sending) CircularProgressIndicator(Modifier.size(28.dp).padding(6.dp), strokeWidth = 2.dp, color = LocalContentColor.current)
                        else Icon(Icons.Default.ArrowUpward, "发送", Modifier.size(28.dp).padding(5.dp))
                    }
                }
            }
        }
    }
}
