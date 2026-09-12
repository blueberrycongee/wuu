package ai.wuu.nativeapp

import android.app.Application
import android.os.Build
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.io.File
import java.util.UUID

class AppModel(application: Application) : AndroidViewModel(application) {
    private val vault = Vault(application)
    private val directory = File(application.cacheDir, "wuu-history")
    var account by mutableStateOf<AccountSession?>(null); private set
    var devices by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var host by mutableStateOf<JSONObject?>(null); private set
    var rows by mutableStateOf<List<ThreadRow>>(emptyList()); private set
    var messages by mutableStateOf<List<ChatMessage>>(emptyList()); private set
    var activeID by mutableStateOf<String?>(null); private set
    var title by mutableStateOf("新会话"); private set
    var connected by mutableStateOf(false); private set
    var connecting by mutableStateOf(false); private set
    var historyEnabled by mutableStateOf(false); private set
    var busy by mutableStateOf(false); private set
    var sending by mutableStateOf(false); private set
    var running by mutableStateOf(false); private set
    var readOnly by mutableStateOf(false); private set
    var error by mutableStateOf<String?>(null)
    var connectionStatus by mutableStateOf(""); private set
    var approval by mutableStateOf<JSONObject?>(null); private set
    var questions by mutableStateOf<List<JSONObject>>(emptyList()); private set
    private var questionRevision = 0L
    var browserURL by mutableStateOf<String?>(null)
    var workspaces by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var workspace by mutableStateOf("")
    private var live: ChatThread? = null
    private var remote: Remote? = null
    private var history: History? = null
    private var liveRows = emptyList<ThreadRow>()
    private var refresh: Job? = null
    private var eventJob: Job? = null
    private var loginJob: Job? = null
    private var generation = 0L
    private var authGeneration = 0L
    private var openGeneration = 0L
    init { try { account = vault.read("session")?.let(AccountSession::decode) } catch (e: Exception) { error = e.message } }
    fun perform(block: suspend () -> Unit) {
        val stamp = generation
        viewModelScope.launch { try { block() } catch (_: CancellationException) {} catch (e: Exception) { if (stamp == generation) report(e) } }
    }
    private fun report(e: Exception) {
        error = e.message ?: "请求失败"
        if (e is HttpFailure && e.status == 401) logout()
    }
    private fun check(stamp: Long) { if (generation != stamp) throw CancellationException() }
    private suspend fun accept(session: AccountSession, stamp: Long) {
        if (stamp != authGeneration) throw CancellationException()
        vault.write("session", session.encode()); vault.delete("github")
        account = session; browserURL = null; loadDevices()
    }
    fun login(server: String, username: String, password: String, register: Boolean = false) {
        if (busy) return
        loginJob?.cancel(); val stamp = ++authGeneration; busy = true
        loginJob = viewModelScope.launch {
            try {
                val api = AccountAPI(server); val user = username.trim().lowercase()
                accept(api.login(user, password, vault.identity(api.origin.toString(), user), Build.MODEL, register), stamp)
            } catch (_: CancellationException) {} catch (e: Exception) { if (authGeneration == stamp) report(e) }
            finally { if (authGeneration == stamp) busy = false }
        }
    }
    suspend fun revoke(device: JSONObject) {
        val session = account ?: return
        val pub = device.getString("pub")
        require(pub.unb64().size == 32) { "Invalid device identity" }
        AccountAPI(session.server).request("/devices/$pub", session.token, method = "DELETE")
        if (account != session) return
        if (pub == session.pub) logout() else loadDevices()
    }
    fun acknowledgeRecovery() {
        val session = account?.copy(recovery = "") ?: return
        try { vault.write("session", session.encode()); account = session }
        catch (e: Exception) { report(e) }
    }
    fun github(server: String) {
        if (busy) return
        loginJob?.cancel(); val stamp = ++authGeneration; busy = true
        loginJob = viewModelScope.launch {
            try {
                val pending = AccountAPI(server).startGitHub()
                if (authGeneration != stamp) throw CancellationException()
                vault.write("github", pending); browserURL = pending.getString("authorize_url")
                pollGitHub(pending, stamp)
            } catch (_: CancellationException) {} catch (e: Exception) { if (authGeneration == stamp) report(e) }
            finally { if (authGeneration == stamp) busy = false }
        }
    }
    private suspend fun pollGitHub(pending: JSONObject, stamp: Long) {
        val api = AccountAPI(pending.getString("server"))
        while (System.currentTimeMillis() < pending.getLong("expires")) {
            val result = api.pollGitHub(pending)
            if (stamp != authGeneration) throw CancellationException()
            when (result.getString("status")) {
                "authorized" -> {
                    val user = result.getString("username")
                    accept(api.completeGitHub(pending, user, vault.identity(api.origin.toString(), user), Build.MODEL), stamp); return
                }
                "pending" -> delay(2000)
                else -> error("GitHub 登录未完成，请重试")
            }
        }
        vault.delete("github"); error("登录已过期，请重试")
    }
    suspend fun loadDevices() {
        val session = account ?: return
        val result = AccountAPI(session.server).request("/devices", session.token)
        if (account != session) return
        devices = result.getJSONArray("devices").objects()
        val selected = host?.optString("pub")
        if (selected != null && devices.none { it.optString("pub") == selected && it.optString("role") == "host" }) leaveHost()
    }
    fun selectHost(device: JSONObject) = perform {
        leaveHost(); val session = account ?: return@perform
        require(device.optString("role") == "host" && devices.any { it.optString("pub") == device.optString("pub") })
        host = device
        val store = History(session, device.getString("pub"), directory); history = store
        val stamp = generation; store.restore(); check(stamp); showHistory(); foreground()
    }
    private fun showHistory() {
        val snapshot = history?.snapshot
        historyEnabled = snapshot?.enabled == true
        rows = (liveRows + (snapshot?.entries?.map { ThreadRow.from(it, true) } ?: emptyList()))
            .distinctBy { it.id }.sortedWith(compareByDescending<ThreadRow> { it.pinned }.thenByDescending { it.updated })
        if (live == null) messages = savedMessages(activeID?.let { snapshot?.cached(it) })
    }
    fun foreground() {
        if (account == null) {
            if (loginJob?.isActive != true) {
                val pending = runCatching { vault.read("github") }.getOrNull()
                if (pending != null) {
                    val stamp = ++authGeneration; busy = true
                    loginJob = viewModelScope.launch {
                        try { pollGitHub(pending, stamp) } catch (_: CancellationException) {} catch (e: Exception) { report(e) }
                        finally { if (stamp == authGeneration) busy = false }
                    }
                }
            }
            return
        }
        if (refresh?.isActive == true) return
        val stamp = generation
        refresh = viewModelScope.launch {
            while (generation == stamp) {
                try {
                    loadDevices(); check(stamp)
                    history?.sync(); check(stamp); showHistory()
                } catch (_: CancellationException) { return@launch }
                catch (e: Exception) { if (e is HttpFailure && e.status == 401) { report(e); return@launch }; connectionStatus = e.message ?: "历史同步失败" }
                if (host != null && !connected && !connecting) {
                    try { connect(stamp) } catch (_: CancellationException) { return@launch }
                    catch (e: Exception) { connectionStatus = e.message ?: "电脑未连接" }
                }
                delay(10_000)
            }
        }
    }
    fun background() {
        generation++; refresh?.cancel(); refresh = null; eventJob?.cancel(); eventJob = null
        remote?.close(); remote = null; connected = false; connecting = false; approval = null
        questions = emptyList(); questionRevision++
    }
    private suspend fun connect(stamp: Long) {
        val session = account ?: return; val selected = host ?: return
        connecting = true
        val transport = Remote(session, selected.getString("pub")); remote = transport
        try {
            eventJob = viewModelScope.launch {
                for (event in transport.events) {
                    if (generation != stamp || remote !== transport) break
                    when (event) {
                        is Remote.Event.Snapshot -> if (event.tag == openGeneration) {
                            val result = event.result
                            live = ChatThread(result.getJSONObject("thread"), (result.optJSONArray("pending_user_messages")?.objects() ?: emptyList()) + (result.optJSONArray("held_user_messages")?.objects() ?: emptyList()))
                            showLive()
                        }
                        is Remote.Event.Disconnected -> { connected = false; approval = null; connectionStatus = event.reason }
                        is Remote.Event.Approval -> { approval = event.line }
                        is Remote.Event.Notification -> {
                            if (event.method == "user-question/requested") {
                                questionRevision++
                                event.params.optJSONObject("request")?.let { request ->
                                    questions = questions.filterNot { it.optString("request_id") == request.optString("request_id") } + request
                                }
                            } else if (event.method == "user-question/resolved") {
                                questionRevision++
                                questions = questions.filterNot { it.optString("request_id") == event.params.optString("request_id") }
                            }
                            live?.apply(event.method, event.params); showLive()
                            if (event.method.startsWith("thread/")) perform { loadThreads() }
                        }
                    }
                }
            }
            transport.connect(); check(stamp)
            transport.call("initialize"); check(stamp)
            connected = true; connectionStatus = "已连接"
            loadQuestions(); check(stamp)
            val result = transport.call("workspace/list"); check(stamp)
            workspaces = result.optJSONArray("workspaces")?.objects() ?: emptyList()
            if (workspace.isBlank()) workspace = result.optString("current").ifBlank { workspaces.firstOrNull()?.optString("path") ?: "" }
            loadThreads(); check(stamp)
            activeID?.let { open(it) }
        } catch (e: Exception) { transport.close(); if (remote === transport) { remote = null; connected = false }; throw e }
        finally { if (generation == stamp) connecting = false }
    }
    suspend fun loadThreads() {
        val transport = remote ?: return; val stamp = generation; val selectedWorkspace = workspace
        val params = json("summary_only" to true); if (selectedWorkspace.isNotBlank()) params.put("cwd", selectedWorkspace)
        val result = transport.call("thread/list", params); check(stamp)
        if (workspace != selectedWorkspace) return
        liveRows = result.getJSONArray("threads").objects().map { ThreadRow.from(it) }; showHistory()
    }
    suspend fun open(id: String) {
        val stamp = generation; val opening = ++openGeneration
        activeID = id; live = null; messages = savedMessages(history?.snapshot?.cached(id)); running = false; readOnly = true
        title = rows.firstOrNull { it.id == id }?.title ?: "会话"
        if (connected) {
            checkNotNull(remote).call("thread/resume", json("session_id" to id, "response_only" to true), snapshotTag = opening)
        } else {
            val thread = history?.thread(id); check(stamp); if (opening != openGeneration) return
            messages = savedMessages(thread)
        }
    }
    private fun showLive() { live?.let { messages = it.messages; title = it.title; running = it.running; readOnly = it.readOnly } }
    suspend fun newThread() {
        check(connected) { "请先连接电脑" }
        val stamp = generation; val params = JSONObject(); if (workspace.isNotBlank()) params.put("cwd", workspace)
        val opening = ++openGeneration
        val result = checkNotNull(remote).call("thread/start", params); check(stamp)
        if (opening != openGeneration) return
        live = ChatThread(result.getJSONObject("thread")); activeID = live?.id; showLive(); loadThreads()
    }
    suspend fun send(text: String): Boolean {
        check(connected && !sending && !readOnly && text.isNotBlank()) { "当前无法发送" }
        val id = activeID ?: error("请先创建会话")
        val stamp = generation; sending = true
        try {
            val method = if (running) "turn/queue" else "turn/start"
            checkNotNull(remote).call(method, json("thread_id" to id, "prompt" to text, "images" to org.json.JSONArray(), "files" to org.json.JSONArray(), "client_id" to UUID.randomUUID().toString()))
            check(stamp); return true
        } finally { sending = false }
    }
    suspend fun stop() { val id = activeID ?: return; checkNotNull(remote).call("turn/interrupt", json("thread_id" to id)) }
    suspend fun pin(row: ThreadRow) { checkNotNull(remote).call("thread/pin", json("thread_id" to row.id, "pinned" to !row.pinned)); loadThreads() }
    suspend fun archive(row: ThreadRow) {
        val stamp = generation
        checkNotNull(remote).call("thread/archive", json("thread_id" to row.id, "archived" to true)); check(stamp)
        if (activeID == row.id) { openGeneration++; activeID = null; live = null; messages = emptyList() }; loadThreads()
    }
    suspend fun setHistory(enabled: Boolean) { val stamp = generation; history?.setEnabled(enabled); history?.sync(); check(stamp); showHistory() }
    fun rejectApproval() { approval?.let { remote?.reject(it) }; approval = null }
    private suspend fun loadQuestions() {
        val stamp = generation; val revision = questionRevision
        val result = checkNotNull(remote).call("user-question/list"); check(stamp)
        if (revision == questionRevision) questions = result.optJSONArray("questions")?.objects() ?: emptyList()
    }
    suspend fun answerQuestion(request: JSONObject, answers: org.json.JSONArray?) {
        check(connected) { "请先连接电脑" }
        val stamp = generation; val id = request.getString("request_id")
        val params = json("request_id" to id)
        if (answers != null) params.put("answer", json("answers" to answers))
        checkNotNull(remote).call(if (answers == null) "user-question/cancel" else "user-question/respond", params)
        check(stamp); questions = questions.filterNot { it.optString("request_id") == id }
    }
    suspend fun leaveHost() {
        val oldHistory = history
        background(); history = null; host = null; activeID = null; live = null; openGeneration++
        liveRows = emptyList(); rows = emptyList(); messages = emptyList(); workspace = ""; workspaces = emptyList(); historyEnabled = false
        withContext(NonCancellable) { oldHistory?.invalidate() }
    }
    fun logout() {
        authGeneration++; loginJob?.cancel(); loginJob = null; busy = false
        account = null; vault.delete("session"); vault.delete("github")
        perform { leaveHost(); directory.deleteRecursively(); devices = emptyList() }
    }
    override fun onCleared() { remote?.close(); super.onCleared() }
}
