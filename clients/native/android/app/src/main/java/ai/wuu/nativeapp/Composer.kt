package ai.wuu.nativeapp

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.key
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp

/** Shared dock used by 会话 and 协作. */
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
    if (attachments.isNotEmpty()) LazyRow {
        items(attachments, key = { it.id }) { attachment ->
            TextButton(onClick = { onAttachments(attachments.filterNot { it.id == attachment.id }) }, enabled = !sending) {
                Text("移除 ${attachment.filename}", maxLines = 1)
            }
        }
    }
    Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        if (showAttach && enabled && !sending) key(draftKey) {
            AttachmentPicker(attachments, onAttachments, model)
        }
        Box(Modifier.weight(1f)) {
            key(draftKey) {
                TextField(value, onChange, placeholder = { Text(placeholder) }, maxLines = 6,
                    shape = MaterialTheme.shapes.large, colors = wuuFieldColors(),
                    modifier = Modifier.fillMaxWidth().testTag("native-composer"), enabled = enabled)
            }
        }
        FilledIconButton(onClick = onSend, enabled = enabled && !sending && (value.isNotBlank() || attachments.isNotEmpty())) {
            Icon(Icons.AutoMirrored.Filled.Send, if (sending) "正在发送" else "发送")
        }
    }
}
