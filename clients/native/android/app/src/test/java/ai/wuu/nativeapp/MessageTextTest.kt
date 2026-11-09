package ai.wuu.nativeapp

import android.text.style.ClickableSpan
import android.widget.TextView
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MessageTextTest {
    @Test fun nativeMarkdownPreservesCodeAndOnlyOpensWebLinks() {
        val context = RuntimeEnvironment.getApplication()
        val opened = mutableListOf<String>()
        val renderer = messageMarkdown(context) { opened.add(it.toString()) }
        val body = renderer.toMarkdown("**Answer**\n\n```kotlin\nval x = \"<b>literal</b>\"\n```\n\n[web](https://example.test/path) [local](file:///private) [app](intent://launch)")
        assertTrue(body.toString().contains("val x = \"<b>literal</b>\""))
        assertFalse(body.toString().contains("**Answer**"))
        val view = TextView(context)
        renderer.setParsedMarkdown(view, body)
        body.getSpans(0, body.length, ClickableSpan::class.java).forEach { it.onClick(view) }
        assertEquals(listOf("https://example.test/path"), opened)
    }
}
