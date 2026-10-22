package ai.wuu.nativeapp

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay

/** Debug-only native fixture. It reads no account, host, conversation, or cached user data. */
class PersonalFixtureActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val keyboard = intent.getBooleanExtra("keyboard", false)
        val pending = intent.getBooleanExtra("pending", false)
        setContent { WuuTheme { PersonalFixture(keyboard, pending) } }
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalComposeUiApi::class)
@Composable private fun PersonalFixture(showKeyboard: Boolean, showPending: Boolean) {
    var value by remember { mutableStateOf(if (showKeyboard) "再补充一个细节" else "") }
    val focusRequester = remember { FocusRequester() }
    val keyboard = LocalSoftwareKeyboardController.current
    LaunchedEffect(showKeyboard) {
        if (showKeyboard) {
            delay(350)
            focusRequester.requestFocus()
            delay(150)
            keyboard?.show()
        }
    }
    Scaffold(topBar = {
        CenterAlignedTopAppBar(title = {
            TextButton(onClick = {}) { PersonalIdentityTitle("twin", "就绪", false) }
        }, navigationIcon = {
            IconButton(onClick = {}) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "电脑列表") }
        }, actions = {
            IconButton(onClick = {}) { Icon(Icons.Default.MoreVert, "活动与设置") }
        })
    }, bottomBar = {
        Surface(color = MaterialTheme.colorScheme.surface, tonalElevation = 2.dp) {
            Column(Modifier.fillMaxWidth().navigationBarsPadding().imePadding().padding(horizontal = 12.dp, vertical = 8.dp)) {
                if (showPending) {
                    PersonalPendingMessage("把这一版发给我确认", 1, {}, {}, retryEnabled = true, discardEnabled = true)
                } else PersonalFixtureComposer(value, { value = it }, focusRequester)
            }
        }
    }) { padding ->
        LazyColumn(Modifier.fillMaxSize().padding(padding), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 20.dp),
            verticalArrangement = Arrangement.spacedBy(20.dp)) {
            item { PersonalMessageBubble("今天想先推进哪件事？我可以帮你整理步骤，也可以继续上次的工作。", false) }
            item { PersonalMessageBubble("把移动端 Twin 的聊天页对齐桌面端，并保留附件和活动入口。", true) }
            item {
                PersonalMessageBubble("我会先完成界面壳层：\n\n- 对话与输入保持在同一页\n- 活动从右上角进入\n- 键盘出现时输入区随安全区上移", false) {
                    FilledTonalButton(onClick = {}) {
                        Icon(Icons.Default.Description, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text("移动端验收说明.pdf", maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
    }
}

@Composable private fun PersonalFixtureComposer(value: String, onChange: (String) -> Unit, focusRequester: FocusRequester) {
    Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        IconButton(onClick = {}) { Icon(Icons.Default.Add, "添加附件") }
        Surface(Modifier.weight(1f), shape = MaterialTheme.shapes.large,
            color = MaterialTheme.colorScheme.surfaceContainerLow,
            border = BorderStroke(0.5.dp, MaterialTheme.colorScheme.outlineVariant)) {
            Row(verticalAlignment = Alignment.Bottom, modifier = Modifier.padding(end = 3.dp)) {
                BasicTextField(value, onChange, maxLines = 6,
                    textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurface),
                    cursorBrush = SolidColor(MaterialTheme.colorScheme.onSurface),
                    modifier = Modifier.weight(1f).heightIn(min = 44.dp).padding(start = 15.dp, end = 4.dp, top = 11.dp, bottom = 11.dp)
                        .focusRequester(focusRequester),
                    decorationBox = { field -> Box { if (value.isEmpty()) Text("给 twin 发消息", color = MaterialTheme.colorScheme.onSurfaceVariant); field() } })
                IconButton(onClick = {}, enabled = value.isNotBlank(), modifier = Modifier.size(44.dp)) {
                    Surface(shape = CircleShape,
                        color = if (value.isNotBlank()) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.surfaceContainerHighest,
                        contentColor = if (value.isNotBlank()) MaterialTheme.colorScheme.surface else MaterialTheme.colorScheme.onSurfaceVariant) {
                        Icon(Icons.Default.ArrowUpward, "发送", Modifier.size(28.dp).padding(5.dp))
                    }
                }
            }
        }
    }
}
