package ai.wuu.nativeapp

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class)
@Composable fun LicensesSheet(dismiss: () -> Unit) {
    val context = LocalContext.current
    var paragraphs by remember { mutableStateOf<List<String>>(emptyList()) }
    LaunchedEffect(Unit) {
        paragraphs = withContext(Dispatchers.IO) {
            try { listOf("Notices.txt", "Apache-2.0.txt", "Google-Notices.txt").flatMap { context.assets.open(it).bufferedReader().use { reader -> reader.readText().split(Regex("\\n\\s*\\n")) } } }
            catch (e: Exception) { listOf("无法读取许可文件：${e.message}") }
        }
    }
    ModalBottomSheet(onDismissRequest = dismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxSize().padding(horizontal = 20.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("开源许可", style = MaterialTheme.typography.titleMedium)
                TextButton(onClick = dismiss) { Text("完成") }
            }
            LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                itemsIndexed(paragraphs) { _, text -> SelectionContainer { Text(text, style = MaterialTheme.typography.bodySmall) } }
            }
        }
    }
}
