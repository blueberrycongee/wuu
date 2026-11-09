package ai.wuu.nativeapp

import kotlinx.coroutines.*
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.test.resetMain
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeNotNull
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class, DelicateCoroutinesApi::class)
class RemoteIntegrationTest {
    @Test fun realGoHostSendAndFreshReconnect() {
        val executable = System.getenv("WUU_NATIVE_TESTHOST")
        assumeNotNull(executable)
        val process = ProcessBuilder(executable!!).redirectError(ProcessBuilder.Redirect.INHERIT).start()
        val main = newSingleThreadContext("native-test-main")
        Dispatchers.setMain(main)
        try {
            val fixture = JSONObject(process.inputStream.bufferedReader().readLine())
            val session = AccountSession.decode(fixture)
            val host = fixture.getString("host")
            val commands = process.outputStream.bufferedWriter()
            runBlocking(main) {
                withTimeout(25_000) {
                    val remote = Remote(session, host)
                    try {
                        remote.connect(); remote.call("initialize")
                        val choices = remote.call("config/read").getJSONArray("providers").objects().map(RemoteProvider::from)
                        assertTrue(choices.any { it.id == "native-test" && it.models.any { it.id == "alternate" && "careful" in it.variants } })
                        val settingsThread = remote.call("thread/start").getJSONObject("thread")
                        val settingsID = settingsThread.getString("id")
                        val selection = ThreadSettings.from(settingsThread).copy(model = "alternate", variant = "careful", permission = "read_only")
                        assertThrows(IllegalArgumentException::class.java) { selection.updateParams(" ") }
                        remote.call("config/model/update", selection.updateParams(settingsID))
                        val settingsSnapshot = remote.call("thread/resume", json("session_id" to settingsID, "response_only" to true))
                        assertEquals(selection, ThreadSettings.from(settingsSnapshot.getJSONObject("thread")))
                        val metadataProjection = ChatThread(settingsThread)
                        metadataProjection.apply("thread/updated", json("thread" to settingsSnapshot.getJSONObject("thread")))
                        assertEquals(selection, metadataProjection.settings)
                        assertEquals("native-test", remote.call("config/read").getString("model"))
                        val unaffected = ThreadSettings.from(remote.call("thread/start").getJSONObject("thread"))
                        assertEquals("native-test", unaffected.model)
                        assertEquals("standard", unaffected.permission)
                        val pagedID = fixture.getString("paged_thread")
                        val firstPage = remote.call("thread/resume", json("session_id" to pagedID, "response_only" to true, "history_page" to true))
                        val history = ChatThread(firstPage.getJSONObject("thread"))
                        assertTrue(history.historyCursor.isNotEmpty())
                        repeat(10) {
                            if (history.historyCursor.isNotEmpty()) history.prependHistory(remote.call("thread/history/read", json("thread_id" to pagedID, "cursor" to history.historyCursor)))
                        }
                        assertEquals("", history.historyCursor)
                        assertEquals(86, history.messages.size)
                        assertEquals("message 0", history.messages.first().text)
                        val large = history.messages.last()
                        assertTrue(large.contentRef.isNotEmpty())
                        history.expandContent(large.contentRef, remote.readContent(large.contentRef, pagedID))
                        assertEquals("长消息🌱\n".repeat(20_000), history.messages.last().text)
                        assertEquals("", history.messages.last().contentRef)
                        val toolID = fixture.getString("tool_thread")
                        val toolHistory = ChatThread(remote.call("thread/resume", json("session_id" to toolID, "response_only" to true, "history_page" to true)).getJSONObject("thread"))
                        val activity = toolHistory.messages.single { it.tool != null }
                        assertEquals("completed", activity.tool!!.status)
                        assertTrue(activity.tool.result.toByteArray().size < 64 * 1024)
                        assertTrue(activity.contentRef.isNotEmpty())
                        toolHistory.expandContent(activity.contentRef, remote.readContent(activity.contentRef, toolID))
                        val expanded = toolHistory.messages.single { it.tool != null }
                        assertEquals("工具结果🌱\n".repeat(20_000), expanded.tool!!.result)
                        assertEquals("""{"path":"fixture.txt"}""", expanded.tool.arguments)
                        assertEquals("", expanded.contentRef)
                        val thread = remote.call("thread/start", json("cwd" to fixture.getString("workspace"))).getJSONObject("thread")
                        val id = thread.getString("id")
                        val completed = async {
                            var projected: ChatThread? = null
                            for (event in remote.events) {
                                if (event is Remote.Event.Snapshot && event.tag == 1L) {
                                    projected = ChatThread(event.result.getJSONObject("thread"), event.result.getJSONArray("pending_user_messages").objects())
                                }
                                if (event is Remote.Event.Notification) {
                                    projected?.apply(event.method, event.params)
                                    if (event.method == "turn/completed" && event.params.optString("thread_id") == id) return@async projected
                                }
                            }
                            error("connection closed before completion")
                        }
                        val image = InputAttachment("photo.png", "image/png", java.util.Base64.getDecoder().decode(fixture.getString("image")))
                        val pdf = InputAttachment("document.pdf", "application/pdf", java.util.Base64.getDecoder().decode(fixture.getString("pdf")))
                        remote.call("turn/start", ChatInput("native phone message", listOf(image, pdf)).params(id, queued = false))
                        val queued = remote.call("turn/queue", ChatInput("follow up").params(id, queued = true))
                        val pendingSnapshot = remote.call("thread/resume", json("session_id" to id, "response_only" to true), snapshotTag = 1L)
                        val pending = ChatThread(pendingSnapshot.getJSONObject("thread"), pendingSnapshot.getJSONArray("pending_user_messages").objects())
                        assertTrue(pending.pending.any { !it.held && it.text == "follow up" })
                        remote.call("turn/dequeue", json("thread_id" to id, "queue_id" to queued.getJSONObject("queued").getString("id")))
                        commands.write("release\n"); commands.flush(); completed.await()
                        val snapshot = remote.call("thread/resume", json("session_id" to id, "response_only" to true))
                        val messages = ChatThread(snapshot.getJSONObject("thread")).messages
                        assertEquals(messages, completed.await()?.messages)
                        assertEquals(false, completed.await()?.running)
                        assertTrue(messages.any { it.role == "user" && it.text == "native phone message" })
                        assertEquals(2, messages.first { it.role == "user" }.attachments.size)
                        assertEquals("document.pdf", JSONObject(messages.first { it.role == "user" }.attachments.last()).getString("filename"))
                        assertTrue(messages.any { it.role == "assistant" && it.text == "native transport verified" })
                        val pagedAttachments = remote.call("thread/resume", json("session_id" to id, "response_only" to true, "history_page" to true))
                        val user = ChatThread(pagedAttachments.getJSONObject("thread")).messages.first { it.role == "user" }
                        val reference = JSONObject(user.attachments[0])
                        assertTrue(reference.getString("remote_ref").isNotEmpty())
                        assertArrayEquals(image.data, remote.readAttachment(reference, id, user.id).data)
                        assertArrayEquals(pdf.data, remote.readAttachment(JSONObject(user.attachments[1]), id, user.id).data)
                        try { remote.readAttachment(reference, pagedID, user.id); fail("reference crossed conversation boundary") }
                        catch (_: IllegalArgumentException) {}
                        try { remote.readAttachment(reference, id, "other:item"); fail("reference crossed message boundary") }
                        catch (_: IllegalArgumentException) {}
                        remote.close()
                        val restored = Remote(session, host)
                        try {
                            restored.connect(); restored.call("initialize")
                            val resumed = restored.call("thread/resume", json("session_id" to id, "response_only" to true))
                            assertEquals(messages, ChatThread(resumed.getJSONObject("thread")).messages)
                            val renamed = restored.call("thread/rename", json("thread_id" to id, "title" to "Searchable native conversation"))
                            assertEquals("Searchable native conversation", renamed.getJSONObject("thread").getString("title"))
                            val found = restored.call("thread/search", json("query" to "native phone message", "limit" to 100))
                            assertTrue(found.getJSONArray("results").objects().any { it.getJSONObject("thread").getString("id") == id })
                            restored.call("thread/archive", json("thread_id" to id, "archived" to true))
                            val active = restored.call("thread/listAll", json("summary_only" to true))
                            assertFalse(active.getJSONArray("threads").objects().any { it.getString("id") == id })
                            val archive = restored.call("thread/listArchived", json("summary_only" to true))
                            assertTrue(archive.getJSONArray("threads").objects().any { ThreadRow.from(it).let { it.id == id && it.archived } })
                            restored.call("thread/archive", json("thread_id" to id, "archived" to false))
                            val unarchived = restored.call("thread/listAll", json("summary_only" to true))
                            assertTrue(unarchived.getJSONArray("threads").objects().any { it.getString("id") == id })

                            val interrupted = async {
                                val projection = ChatThread(resumed.getJSONObject("thread"))
                                for (event in restored.events) {
                                    if (event is Remote.Event.Notification) {
                                        projection.apply(event.method, event.params)
                                        if (!projection.running && projection.pending.any { it.held && it.id == "held-native" }) return@async projection
                                    }
                                }
                                error("closed before interruption")
                            }
                            restored.call("turn/start", json("thread_id" to id, "prompt" to "interrupt this"))
                            restored.call("turn/queue", json("thread_id" to id, "prompt" to "continue once", "client_id" to "held-native"))
                            restored.call("turn/interrupt", json("thread_id" to id))
                            val heldProjection = interrupted.await()
                            assertTrue(heldProjection.pending.any { it.held && it.id == "held-native" })
                            val heldSnapshot = restored.call("thread/resume", json("session_id" to id, "response_only" to true))
                            val held = ChatThread(heldSnapshot.getJSONObject("thread"), held = heldSnapshot.getJSONArray("held_user_messages").objects())
                            restored.call("turn/steer", held.pending.first { it.id == "held-native" }.resumeParams())
                            val consumed = restored.call("thread/resume", json("session_id" to id, "response_only" to true))
                            assertEquals(0, consumed.optJSONArray("held_user_messages")?.length() ?: 0)
                            assertEquals(1, ChatThread(consumed.getJSONObject("thread")).messages.count { it.text == "continue once" })
                            restored.call("turn/interrupt", json("thread_id" to id))
                        } finally { restored.close() }
                    } finally { remote.close() }
                }
            }
        } finally {
            process.outputStream.write("quit\n".toByteArray()); process.outputStream.flush(); process.waitFor()
            Dispatchers.resetMain(); main.close()
        }
    }
}
