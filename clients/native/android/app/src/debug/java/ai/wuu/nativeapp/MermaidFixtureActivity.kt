package ai.wuu.nativeapp

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay

/** Local rendering fixture: no account, remote host or application model. */
class MermaidFixtureActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val source = """
            图表随回复逐步更新。

            ```mermaid title=流程
            flowchart TD
              A[收到消息] --> B[绘制图表]
              B --> C{语法完整？}
              C -->|是| D[更新图表]
              C -->|否| E[保留上一帧]
            ```

            普通 **Markdown** 和 `code` 继续使用原生排版。

            > ~~~mermaid
            > sequenceDiagram
            > Alice->>Bob: Hello
            > Bob-->>Alice: Done
            > ~~~

            [网页链接](https://example.com)
        """.trimIndent()
        setContent {
            var live by remember { mutableStateOf(intent.getBooleanExtra("stream", false)) }
            var text by remember { mutableStateOf(if (live) "" else source) }
            LaunchedEffect(Unit) {
                if (live) {
                    for (chunk in source.chunked(2)) { text += chunk; delay(40) }
                    live = false
                }
            }
            WuuTheme {
                Surface(Modifier.fillMaxSize()) {
                    Column(Modifier.safeDrawingPadding().verticalScroll(rememberScrollState()).padding(16.dp)) {
                        Text(if (live) "正在生成" else "Mermaid", style = MaterialTheme.typography.titleMedium)
                        MessageText(text, markdown = true, isLive = live)
                    }
                }
            }
        }
    }
}
