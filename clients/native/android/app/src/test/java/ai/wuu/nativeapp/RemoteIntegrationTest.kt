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
                        remote.call("turn/start", json("thread_id" to id, "prompt" to "native phone message"))
                        val queued = remote.call("turn/queue", json("thread_id" to id, "prompt" to "follow up"))
                        val pendingSnapshot = remote.call("thread/resume", json("session_id" to id, "response_only" to true), snapshotTag = 1L)
                        val pending = ChatThread(pendingSnapshot.getJSONObject("thread"), pendingSnapshot.getJSONArray("pending_user_messages").objects())
                        assertTrue(pending.messages.any { it.role == "pending" && it.text == "follow up" })
                        remote.call("turn/dequeue", json("thread_id" to id, "queue_id" to queued.getJSONObject("queued").getString("id")))
                        commands.write("release\n"); commands.flush(); completed.await()
                        val snapshot = remote.call("thread/resume", json("session_id" to id, "response_only" to true))
                        val messages = ChatThread(snapshot.getJSONObject("thread")).messages
                        assertEquals(messages, completed.await()?.messages)
                        assertEquals(false, completed.await()?.running)
                        assertTrue(messages.any { it.role == "user" && it.text == "native phone message" })
                        assertTrue(messages.any { it.role == "assistant" && it.text == "native transport verified" })
                        remote.close()
                        val restored = Remote(session, host)
                        try {
                            restored.connect(); restored.call("initialize")
                            val resumed = restored.call("thread/resume", json("session_id" to id, "response_only" to true))
                            assertEquals(messages, ChatThread(resumed.getJSONObject("thread")).messages)
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
