package ai.wuu.nativeapp

import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.lifecycle.Lifecycle
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.UiDevice
import org.junit.Rule
import org.junit.Test

class ConversationUITest {
    @get:Rule val ui = createAndroidComposeRule<MainActivity>()

    private fun await(matcher: SemanticsMatcher) {
        ui.waitUntil(20_000) { ui.onAllNodes(matcher).fetchSemanticsNodes(atLeastOneRootRequired = false).isNotEmpty() }
    }
    private fun tap(text: String) { await(hasText(text)); ui.onNodeWithText(text).performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick) { it() } }
    private fun send(text: String) {
        await(hasTestTag("native-composer") and isEnabled())
        ui.onNodeWithTag("native-composer").performTextReplacement(text)
        ui.onNodeWithContentDescription("发送").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick) { it() }
    }
    private fun expectText(device: UiDevice, text: String) {
        try {
            // Drive Compose's test clock while waiting on a native AndroidView.
            ui.waitUntil(20_000) { device.hasObject(androidx.test.uiautomator.By.text(text)) }
        } catch (_: ComposeTimeoutException) {
            val hierarchy = java.io.ByteArrayOutputStream()
            device.dumpWindowHierarchy(hierarchy)
            val state = ui.runOnIdle {
                val model = androidx.lifecycle.ViewModelProvider(ui.activity)[AppModel::class.java]
                "connected=${model.connected}, sending=${model.sending}, running=${model.running}, error=${model.error}, messages=${model.messages.map { it.text }}"
            }
            error("Missing $text\n$state\n${hierarchy.toString("UTF-8")}")
        }
    }
    @Test fun loginSendStreamStopAndReconnect() {
        val server = requireNotNull(InstrumentationRegistry.getArguments().getString("server"))
        require(server.startsWith("http://127.0.0.1:"))
        await(hasText("HTTPS 服务器地址"))
        ui.onNode(hasSetTextAction() and hasText("HTTPS 服务器地址")).performTextInput(server)
        tap("继续"); await(hasText("用户名"))
        ui.onNode(hasSetTextAction() and hasText("用户名")).performTextInput("native-test")
        ui.onNode(hasSetTextAction() and hasText("密码")).performTextInput("native-test-password")
        tap("登录"); await(hasText("UI test computer"))
        val device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation())
        ui.onNodeWithContentDescription("账号").performClick()
        ui.onNodeWithText("通知").performScrollTo().performClick()
        await(hasText("开启或重新注册")); ui.onNodeWithText("开启或重新注册").assertIsNotEnabled()
        device.pressBack()
        ui.onNodeWithContentDescription("账号").performClick()
        ui.onNodeWithText("开源许可").performScrollTo().performClick()
        await(hasText("Wuu native mobile", substring = true))
        tap("完成"); tap("UI test computer")
        await(hasContentDescription("新会话") and isEnabled())
        val newButtons = ui.onAllNodesWithContentDescription("新会话")
        val visible = newButtons.fetchSemanticsNodes().indices.first { newButtons[it].isDisplayed() }
        newButtons[visible].performClick()
        await(hasContentDescription("添加附件"))
        send("ui-hello")
        // Assistant text is a selectable native TextView, outside Compose's semantics tree.
        expectText(device, "Received: ui-hello")
        ui.onNodeWithContentDescription("添加附件").assertIsDisplayed()
        send("ui-tools"); expectText(device, "Received: ui-tools")
        ui.onNodeWithTag("tool-process").performScrollTo().performClick()
        ui.onNodeWithTag("tool-activity").performScrollTo().performClick()
        await(hasText("Native tool output", substring = true))
        ui.onNodeWithTag("tool-process").performScrollTo().performClick()
        send("ui-wait")
        expectText(device, "Streaming preview")
        tap("停止")
        ui.waitUntil(20_000) { ui.onAllNodesWithText("停止").fetchSemanticsNodes().isEmpty() }
        device.pressHome()
        ui.waitUntil(20_000) { ui.activity.lifecycle.currentState == Lifecycle.State.CREATED }
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        // Relaunch as an external user action; the app is now subject to background launch limits.
        val component = requireNotNull(context.packageManager.getLaunchIntentForPackage(context.packageName)?.component)
        device.executeShellCommand("am start -W -a android.intent.action.MAIN -c android.intent.category.LAUNCHER -n ${component.flattenToString()}")
        await(hasContentDescription("添加附件") and isEnabled())
        send("ui-return")
        expectText(device, "Received: ui-return")
        ui.onNodeWithContentDescription("会话操作").performClick(); tap("会话设置")
        tap("标准"); tap("只读"); tap("保存")
        await(hasContentDescription("会话操作"))
        ui.onNodeWithContentDescription("会话操作").performClick(); tap("会话设置")
        await(hasText("只读")); tap("取消")
        ui.onNodeWithContentDescription("会话列表").performClick()
        await(hasContentDescription("关闭会话列表"))
        device.pressBack()
        ui.onNodeWithContentDescription("发送").assertExists()
        ui.onRoot().performTouchInput { swipeRight(startX = width * 0.1f, endX = width * 0.75f) }
        await(hasContentDescription("关闭会话列表"))
        ui.onNode(hasText("ui-hello") and hasClickAction()).performClick()
        ui.waitUntil(20_000) { !ui.onNodeWithContentDescription("关闭会话列表").isDisplayed() }
        ui.onAllNodesWithText("提示").assertCountEquals(0)
        ui.onNodeWithContentDescription("发送").assertExists()

        val model = ui.runOnIdle { androidx.lifecycle.ViewModelProvider(ui.activity)[AppModel::class.java] }
        val harnessID = ui.runOnIdle { model.activeID }
        ui.onNode(hasSetTextAction() and hasText("发送消息")).performTextInput("Harness draft")
        device.pressBack()
        tap("协作"); tap("Native collaboration")
        await(hasText("Collaboration ready"))
        ui.onNode(hasSetTextAction() and hasText("发送消息")).performTextInput("Room draft")
        device.pressBack()
        tap("会话")
        try { await(hasSetTextAction() and hasText("Harness draft")) }
        catch (e: Throwable) { error("Harness restore failed: " + ui.runOnIdle { "active=${model.activeID} host=${model.host?.optString("name")} drafts=${model.conversationDrafts} mode=${model.collaboration.visible}" } + "\n" + ui.onRoot().printToString()) }
        ui.onNode(hasSetTextAction() and hasText("Harness draft")).assertExists()
        org.junit.Assert.assertEquals(harnessID, ui.runOnIdle { model.activeID })
        tap("协作")
        await(hasSetTextAction() and hasText("Room draft"))
        ui.onNode(hasSetTextAction() and hasText("Room draft")).assertExists()
        ui.onNodeWithContentDescription("发送").performClick()
        await(hasText("Room draft") and !hasSetTextAction())
        ui.onNodeWithContentDescription("成员与任务").performClick()
        await(hasText("Native collaboration task")); await(hasText("Alpha")); await(hasText("Beta"))
        device.pressBack()
        ui.onNodeWithTag("room-timeline").performScrollToIndex(0)
        tap("加载更早消息")
        ui.waitUntil(20_000) { ui.runOnIdle { model.collaboration.messages.firstOrNull()?.optString("body")?.startsWith("Collaboration history 01") == true } }
        ui.onNodeWithTag("room-timeline").performScrollToIndex(0)
        await(hasText("Collaboration history 01", substring = true))
        device.executeShellCommand("screencap -p /sdcard/Download/wuu-native-room-light.png")
        ui.onNodeWithContentDescription("协作列表").performClick()
        ui.onNodeWithContentDescription("新对话或群聊").performClick()
        tap("Beta")
        await(hasContentDescription("成员与任务"))
        await(hasText("Beta"))
        send("Mobile direct message")
        await(hasText("Mobile direct message") and !hasSetTextAction())
        device.executeShellCommand("screencap -p /sdcard/Download/wuu-native-direct-light.png")
        ui.onNodeWithContentDescription("协作列表").performClick()
        ui.onNodeWithContentDescription("新对话或群聊").performClick()
        tap("建群")
        ui.onNode(hasSetTextAction() and hasText("群名")).performTextInput("Mobile group")
        ui.onNodeWithTag("new-agent-Alpha").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick) { it() }
        ui.onNodeWithTag("new-agent-Beta").performSemanticsAction(androidx.compose.ui.semantics.SemanticsActions.OnClick) { it() }
        tap("创建群聊")
        await(hasText("Mobile group"))
        ui.onNodeWithContentDescription("成员与任务").performClick()
        await(hasText("Alpha")); await(hasText("Beta")); device.pressBack()
        device.executeShellCommand("cmd uimode night yes")
        ui.waitForIdle()
        device.executeShellCommand("screencap -p /sdcard/Download/wuu-native-group-dark.png")
        device.executeShellCommand("cmd uimode night no")
    }
}
