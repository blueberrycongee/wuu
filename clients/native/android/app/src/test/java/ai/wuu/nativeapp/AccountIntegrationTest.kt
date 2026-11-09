package ai.wuu.nativeapp

import java.io.Closeable
import java.nio.file.Files
import kotlinx.coroutines.*
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Assume.assumeNotNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

private class AccountFixture(github: Boolean = false) : Closeable {
    private val process: Process
    private val output: java.io.BufferedReader
    val seed: JSONObject
    val api: AccountAPI
    init {
        val executable = System.getenv("WUU_NATIVE_TESTACCOUNT")
        assumeNotNull(executable)
        process = ProcessBuilder(listOf(executable!!) + if (github) listOf("-github") else emptyList()).redirectError(ProcessBuilder.Redirect.INHERIT).start()
        output = process.inputStream.bufferedReader()
        seed = JSONObject(output.readLine()); api = AccountAPI(seed.getString("server"))
    }
    fun command(value: String) { process.outputStream.write("$value\n".toByteArray()); process.outputStream.flush() }
    suspend fun line(): String = withContext(Dispatchers.IO) { output.readLine() ?: error("Account fixture exited") }
    suspend fun login() = api.login(seed.getString("username"), seed.getString("password"), Identity(), "Android test")
    override fun close() { command("quit"); check(process.waitFor(10, java.util.concurrent.TimeUnit.SECONDS)) { "Fixture did not exit" } }
}

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [28], manifest = Config.NONE)
class AccountIntegrationTest {
    private suspend fun unauthorized(block: suspend () -> Unit) {
        try { block(); fail("Revoked or incorrect credentials were accepted") }
        catch (e: HttpFailure) { assertEquals(401, e.status) }
    }

    @Test fun githubBrowserReturnRestorationCancellationAndEnrollment() = runBlocking {
        withTimeout(60_000) {
            AccountFixture(github = true).use { fixture ->
                val api = fixture.api
                assertTrue(api.request("/config").getBoolean("github"))
                val cancelled = api.startGitHub(); api.cancelGitHub(cancelled)
                unauthorized { api.pollGitHub(cancelled) }
                val denied = api.startGitHub()
                fixture.command("deny ${denied.getString("request_id")}"); assertEquals("browser-returned", fixture.line())
                try { api.pollGitHub(denied); fail("Browser denial was ignored") } catch (e: HttpFailure) { assertEquals(400, e.status) }
                api.cancelGitHub(denied)

                val pending = JSONObject(api.startGitHub().toString())
                assertEquals("pending", api.pollGitHub(pending).getString("status"))
                unauthorized { api.pollGitHub(JSONObject(pending.toString()).put("verifier", ByteArray(32) { 1 }.b64())) }
                fixture.command("authorize ${pending.getString("request_id")}"); assertEquals("browser-returned", fixture.line())
                val authorized = api.pollGitHub(pending); assertEquals("authorized", authorized.getString("status"))
                val user = authorized.getString("username"); val identity = Identity()
                val session = api.completeGitHub(pending, user, identity, "test")
                assertEquals(session, api.completeGitHub(pending, user, identity, "test"))
                val directory = api.request("/devices", session.token)
                assertEquals("github", directory.getString("auth_method")); assertEquals(session.pub, directory.getJSONArray("devices").getJSONObject(0).getString("pub"))
                unauthorized { api.completeGitHub(pending, user, Identity(), "other") }
                api.logout(session.token)
                unauthorized { api.completeGitHub(pending, user, identity, "test") }
            }
        }
    }

    @Test fun registerPasswordRecoveryAndServerLogout() = runBlocking {
        withTimeout(60_000) {
            AccountFixture().use { fixture ->
                val api = fixture.api
                val config = api.request("/config")
                assertTrue(config.getBoolean("registration")); assertFalse(config.getBoolean("github"))
                val identity = Identity()
                val session = api.login(" ALICE ", "initial-password", identity, "test", true)
                val other = api.login("alice", "initial-password", Identity(), "other")
                unauthorized { api.resetPassword("alice", "wrong-password", "changed-password", session.token) }
                assertEquals(2, api.request("/devices", session.token).getJSONArray("devices").length())
                val recovery = api.resetPassword("alice", "initial-password", "changed-password", session.token)
                assertNotEquals(session.recovery, recovery)
                for (token in listOf(session.token, other.token)) unauthorized { api.request("/devices", token) }
                unauthorized { api.resetPassword("alice", session.recovery, "recovered-password") }
                val next = api.resetPassword("alice", recovery, "recovered-password")
                assertNotEquals(recovery, next)
                unauthorized { api.login("alice", "changed-password", identity, "test") }
                val restored = api.login("alice", "recovered-password", identity, "test")
                api.logout(restored.token)
                unauthorized { api.request("/devices", restored.token) }
                api.logout(restored.token)
            }
        }
    }

    @Test fun pushRegistrationRotationDisableAndLoginIsolation() = runBlocking {
        withTimeout(60_000) {
            AccountFixture().use { fixture ->
                val api = fixture.api; val session = fixture.login()
                assertTrue(api.request("/config").getJSONArray("push_platforms").toString().contains("android"))
                val consent = JSONObject(PushConsent.encode(session, true).toString())
                assertTrue(PushConsent.matches(session, consent))
                api.setPush(session.token, "android", "simulated-fcm-token")
                val first = api.pushStatus(session.token)
                assertTrue(first.getBoolean("enabled")); assertEquals("android", first.getString("platform"))
                val other = api.login(session.username, "native-test-password", Identity(), "other")
                assertFalse(PushConsent.matches(other, consent)); assertFalse(api.pushStatus(other.token).getBoolean("enabled"))
                api.setPush(session.token, "android", "rotated-fcm-token")
                api.setPush(session.token, "android", null)
                assertFalse(api.pushStatus(session.token).getBoolean("enabled"))
                api.setPush(session.token, "android", "simulated-fcm-token")
                api.logout(session.token)
                unauthorized { api.setPush(session.token, "android", "late-registration") }
                val relogin = api.login(session.username, "native-test-password", Identity(session.seed.unb64()), "test")
                assertFalse(PushConsent.matches(relogin, consent)); assertFalse(api.pushStatus(relogin.token).getBoolean("enabled"))
            }
        }
    }

    @Test fun historyDiskRestorePaginationDeletionAndLateResponse() = runBlocking {
        withTimeout(60_000) {
            AccountFixture().use { fixture ->
                val api = fixture.api; val session = fixture.login()
                val directory = Files.createTempDirectory("native-history").toFile()
                try {
                    val host = fixture.seed.getString("host"); val token = fixture.seed.getString("hostToken")
                    val store = History(session, host, directory)
                    assertFalse(store.sync().enabled)
                    store.setEnabled(true); val enabled = store.sync().generation
                    suspend fun upload(id: String, text: String, revision: String = "0", deleted: Boolean = false): JSONObject =
                        api.request("/history/thread", token, json("generation" to enabled, "expected" to revision, "deleted" to deleted,
                            "thread" to json("id" to id, "title" to id, "updated_at" to "2026-09-11T00:00:00Z",
                                "messages" to JSONArray().put(json("id" to "m", "turn_id" to "t", "role" to "user", "text" to text)))))
                    val entry = upload("chosen", "offline text")
                    repeat(100) { upload("page-$it", "text") }
                    val pages = listOf(async { store.sync() }, async { store.sync() }).awaitAll()
                    pages.forEach { assertEquals(101, it.entries.size) }
                    assertEquals("offline text", store.thread("chosen").getJSONArray("messages").getJSONObject(0).getString("text"))
                    store.invalidate()
                    val restored = History(session, host, directory); restored.restore()
                    assertEquals("offline text", restored.snapshot.cached("chosen")!!.getJSONArray("messages").getJSONObject(0).getString("text"))
                    val changed = upload("chosen", "edited text", entry.getString("revision"))
                    restored.sync()
                    fixture.command("pause-history"); assertEquals("paused", fixture.line())
                    val delayed = async { restored.thread("chosen") }
                    assertEquals("blocked", fixture.line())
                    upload("chosen", "edited text", changed.getString("revision"), true)
                    assertFalse(restored.sync().entries.any { it.getString("id") == "chosen" })
                    restored.invalidate(true)
                    fixture.command("release-history")
                    try { delayed.await(); fail("Late history response restored a removed cache") } catch (_: CancellationException) {}
                    assertTrue(directory.listFiles()!!.isEmpty())
                    val current = History(session, host, directory); current.sync()
                    current.setEnabled(false)
                    val disabled = current.sync(); assertFalse(disabled.enabled); assertTrue(disabled.entries.isEmpty())
                    current.invalidate()
                    History.prune(session, emptyList(), directory)
                    assertTrue(directory.listFiles()!!.isEmpty())
                    val stranger = api.login("stranger", "stranger-password", Identity(), "test", true)
                    unauthorized { api.request("/history", stranger.token, query = mapOf("host" to host)) }
                    api.request("/devices/$host", session.token, method = "DELETE")
                    unauthorized { api.request("/history", session.token, query = mapOf("host" to host)) }
                } finally { directory.deleteRecursively() }
            }
        }
    }
}
