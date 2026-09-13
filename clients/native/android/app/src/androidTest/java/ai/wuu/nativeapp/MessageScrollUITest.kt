package ai.wuu.nativeapp

import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test

class MessageScrollUITest {
    @get:Rule val ui = createAndroidComposeRule<MainActivity>()

    private fun textView(view: View): TextView? {
        if (view is TextView && view.text.startsWith("Reply")) return view
        if (view is ViewGroup) for (index in 0 until view.childCount) {
            textView(view.getChildAt(index))?.let { return it }
        }
        return null
    }

    @Test fun growingMarkdownRemainsFullyScrollable() {
        val text = mutableStateOf("Reply")
        ui.activity.runOnUiThread {
            ui.activity.setContent {
                WuuTheme {
                    val list = rememberLazyListState()
                    val viewport = list.layoutInfo.viewportSize
                    LaunchedEffect(text.value, viewport) { list.requestScrollToItem(3) }
                    LazyColumn(Modifier.fillMaxSize().testTag("messages"), state = list) {
                        item { Column { SelectionContainer { Surface { MessageText(text.value, true, Modifier.fillMaxWidth()) } } } }
                        item { Text("End of conversation") }
                    }
                }
            }
        }
        ui.waitUntil(5_000) { ui.runOnIdle { textView(ui.activity.window.decorView) != null } }
        ui.runOnIdle { text.value = "Reply\n\n" + (1..80).joinToString("\n\n") { "Paragraph $it with **formatted** content." } }
        ui.waitUntil(5_000) { ui.runOnIdle { textView(ui.activity.window.decorView)?.text?.contains("Paragraph 80") == true } }
        ui.runOnIdle {
            val view = requireNotNull(textView(ui.activity.window.decorView))
            assertTrue("Markdown is clipped inside its list item", view.height >= view.layout.height)
        }
        ui.onNodeWithTag("messages").performScrollToIndex(0)
        ui.onNodeWithText("End of conversation").assertIsNotDisplayed()
        ui.onNodeWithTag("messages").performScrollToNode(hasText("End of conversation"))
        ui.onNodeWithText("End of conversation").assertIsDisplayed()
    }
    @Test fun recycledMarkdownKeepsItsFullLayout() {
        ui.activity.runOnUiThread {
            ui.activity.setContent {
                WuuTheme {
                    LazyColumn(Modifier.fillMaxSize().testTag("messages")) {
                        items(30) { index ->
                            Column { SelectionContainer { Surface {
                                MessageText("Reply $index\n\n" + (1..8).joinToString("\n\n") {
                                    "这是一段用于检查列表回收后完整显示的正文，包含 **粗体** 和普通文字。"
                                }, true, Modifier.fillMaxWidth())
                            } } }
                        }
                    }
                }
            }
        }
        ui.waitUntil(5_000) { ui.runOnIdle { textView(ui.activity.window.decorView) != null } }
        repeat(6) {
            ui.onNodeWithTag("messages").performTouchInput { swipeUp(durationMillis = 300) }
            ui.waitForIdle()
        }
        repeat(6) {
            ui.onNodeWithTag("messages").performTouchInput { swipeDown(durationMillis = 300) }
            ui.waitForIdle()
            ui.runOnIdle {
                fun check(view: View) {
                    if (view is TextView && view.text.startsWith("Reply") && view.isShown && view.height > 0) {
                        assertTrue("Text layout width ${view.layout.width} differs from view width ${view.width}",
                            view.layout.width == view.width - view.compoundPaddingLeft - view.compoundPaddingRight)
                        assertTrue("Recycled reply is clipped: ${view.height} < ${view.layout.height}", view.height >= view.layout.height)
                    }
                    if (view is ViewGroup) for (index in 0 until view.childCount) check(view.getChildAt(index))
                }
                check(ui.activity.window.decorView)
            }
        }
    }

}
