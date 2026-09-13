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
    val push = PushNotifications(application)
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
    var hasOlder by mutableStateOf(false); private set
    var loadingHistory by mutableStateOf(false); private set
    var loadingContent by mutableStateOf<Set<String>>(emptySet()); private set
    var attachmentPreview by mutableStateOf<LoadedAttachment?>(null)
    var loadingAttachment by mutableStateOf(false); private set
    var running by mutableStateOf(false); private set
    var readOnly by mutableStateOf(false); private set
    var error by mutableStateOf<String?>(null)
    var connectionStatus by mutableStateOf(""); private set
    var approval by mutableStateOf<JSONObject?>(null); private set
    var questions by mutableStateOf<List<JSONObject>>(emptyList()); private set
    private var questionRevision = 0L
    var browserURL by mutableStateOf<String?>(null)
    var githubURL by mutableStateOf<String?>(null); private set
    var configuration by mutableStateOf<JSONObject?>(null); private set
    var authMethod by mutableStateOf(""); private set
    var directoryCached by mutableStateOf(false); private set
    var resetRecovery by mutableStateOf<String?>(null); private set
    val recovery get() = account?.recovery?.takeIf { it.isNotEmpty() } ?: resetRecovery
    private var configGeneration = 0L
    var workspaces by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var workspace by mutableStateOf("")
    var archivedList by mutableStateOf(false)
    var search by mutableStateOf("")
    var pending by mutableStateOf<List<PendingMessage>>(emptyList()); private set
    private var listRevision = 0L
    private var live: ChatThread? = null
    var threadSettings by mutableStateOf<ThreadSettings?>(null); private set
    var threadEngine by mutableStateOf(""); private set
    private var remote: Remote? = null
    val collaboration = Collaboration { method, params ->
        val transport = checkNotNull(remote) { "电脑未连接" }; val stamp = generation
        check(connected) { "电脑未连接" }
        val result = transport.call(method, params)
        check(stamp)
        if (remote !== transport) throw CancellationException()
        result
    }
    val conversationDrafts = androidx.compose.runtime.mutableStateMapOf<String, String>()
    val conversationAttachments = androidx.compose.runtime.mutableStateMapOf<String, List<InputAttachment>>()
    private var history: History? = null
    private var liveRows = emptyList<ThreadRow>()
    private var refresh: Job? = null
    private var eventJob: Job? = null
    private var loginJob: Job? = null
    private var generation = 0L
    private var authGeneration = 0L
    private var openGeneration = 0L
    init { try {
        account = vault.read("session")?.let(AccountSession::decode)
        resetRecovery = vault.read("recovery")?.getString("key")
        account?.let { session -> vault.read("directory")?.let { RememberedDirectory.restore(session, it) } }?.let {
            devices = it.getJSONArray("devices").objects(); authMethod = it.optString("auth_method"); directoryCached = true
        }
    } catch (e: Exception) { error = e.message }
        push.bind(account)
    }
    fun perform(block: suspend () -> Unit) {
        val stamp = generation
        viewModelScope.launch { try { block() } catch (_: CancellationException) {} catch (e: Exception) { if (stamp == generation) report(e) } }
    }
    private fun report(e: Exception) {
        error = e.message ?: "请求失败"
        if (e is HttpFailure && e.status == 401 && account != null) perform { loadDevices() }
    }
    private fun check(stamp: Long) { if (generation != stamp) throw CancellationException() }
    private suspend fun accept(session: AccountSession, stamp: Long) {
        if (stamp != authGeneration) throw CancellationException()
        vault.delete("directory"); vault.write("session", session.encode()); vault.delete("github")
        account = session; push.bind(session); browserURL = null; githubURL = null; loadDevices(); foreground()
    }
    fun login(server: String, username: String, password: String, register: Boolean = false) {
        if (busy) return
        vault.delete("github"); githubURL = null; browserURL = null
        loginJob?.cancel(); val stamp = ++authGeneration; busy = true
        loginJob = viewModelScope.launch {
            try {
                val api = AccountAPI(server); val user = username.trim().lowercase(java.util.Locale.ROOT)
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
        if (pub == session.pub) clearSession() else loadDevices()
    }
    fun acknowledgeRecovery() {
        try {
            vault.delete("recovery"); resetRecovery = null
            val session = account?.copy(recovery = "") ?: return
            vault.write("session", session.encode()); account = session
        }
        catch (e: Exception) { report(e) }
    }
    suspend fun loadConfiguration(server: String) {
        val stamp = ++configGeneration; configuration = null
        if (server.isBlank()) return
        try {
            val result = AccountAPI(server).request("/config")
            if (configGeneration == stamp) configuration = result
        } catch (_: CancellationException) {} catch (e: Exception) { if (configGeneration == stamp) error = e.message }
    }
    fun cancelLogin() {
        val pending = vault.read("github")
        authGeneration++; loginJob?.cancel(); loginJob = null
        busy = false; githubURL = null; browserURL = null; vault.delete("github")
        if (pending != null) viewModelScope.launch { runCatching { AccountAPI(pending.getString("server")).cancelGitHub(pending) } }
    }
    fun resetPassword(server: String, username: String, secret: String, password: String, changing: Boolean) {
        if (busy) return
        loginJob?.cancel(); val stamp = ++authGeneration; busy = true
        loginJob = viewModelScope.launch {
            try {
                val session = if (changing) checkNotNull(account) else null
                val key = AccountAPI(session?.server ?: server).resetPassword(session?.username ?: username, secret, password, session?.token)
                if (authGeneration != stamp) throw CancellationException()
                vault.write("recovery", json("key" to key)); resetRecovery = key
                loginJob = null; clearSession()
            } catch (_: CancellationException) {} catch (e: Exception) { if (authGeneration == stamp) report(e) }
            finally { if (authGeneration == stamp) busy = false }
        }
    }
    fun github(server: String) {
        if (busy) return
        loginJob?.cancel(); val stamp = ++authGeneration; busy = true
        loginJob = viewModelScope.launch {
            try {
                val pending = AccountAPI(server).startGitHub()
                if (authGeneration != stamp) throw CancellationException()
                vault.write("github", pending); githubURL = pending.getString("authorize_url"); browserURL = githubURL
                pollGitHub(pending, stamp)
            } catch (_: CancellationException) {} catch (e: Exception) {
                if (authGeneration == stamp) { cancelLogin(); report(e) }
            }
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
        val result = try { AccountAPI(session.server).request("/devices", session.token) }
        catch (e: HttpFailure) {
            if (e.status != 401) throw e
            if (account == session) clearSession()
            return
        }
        if (account != session) return
        vault.write("directory", RememberedDirectory.encode(session, result))
        devices = result.getJSONArray("devices").objects()
        authMethod = result.optString("auth_method")
        directoryCached = false
        val selected = host?.optString("pub")
        if (selected != null && devices.none { it.optString("pub") == selected && it.optString("role") == "host" }) {
            // This refresh job owns the connection being stopped. Finish cleanup
            // and start its replacement before returning to the cancelled job.
            withContext(NonCancellable) {
                leaveHost(removeCache = true)
                if (account == session) {
                    History.prune(session, devices.filter { it.optString("role") == "host" }.map { it.getString("pub") }, directory)
                    foreground()
                }
            }
            return
        }
        History.prune(session, devices.filter { it.optString("role") == "host" }.map { it.getString("pub") }, directory)
    }
    fun selectHost(device: JSONObject) = selectHostFor(device, account)
    private fun selectHostFor(device: JSONObject, previous: AccountSession?) = perform {
        if (account != previous) return@perform
        val selection = leaveHost(); val session = account ?: return@perform
        if (selection != openGeneration || session != previous) return@perform
        require(device.optString("role") == "host" && devices.any { it.optString("pub") == device.optString("pub") })
        host = device
        val store = History(session, device.getString("pub"), directory); history = store
        val stamp = generation; store.restore(); check(stamp); showHistory(); foreground()
    }
    fun openPushHost(pub: String) = perform {
        val session = account ?: return@perform
        if (!push.mayOpenNotification() || runCatching { pub.unb64().size }.getOrNull() != 32) return@perform
        loadDevices()
        if (account != session || !push.mayOpenNotification() || directoryCached) return@perform
        devices.firstOrNull { it.optString("pub") == pub && it.optString("role") == "host" }?.let { selectHostFor(it, session) }
    }
    private fun showHistory() {
        val snapshot = history?.snapshot
        historyEnabled = snapshot?.enabled == true
        // The text archive has no workspace/archived metadata. Use it as the offline directory.
        rows = (if (connected) liveRows else (snapshot?.entries?.map { ThreadRow.from(it, true) } ?: emptyList()).filter { it.title.contains(search, ignoreCase = true) })
            .distinctBy { it.id }.sortedWith(compareByDescending<ThreadRow> { it.pinned }.thenByDescending { it.updated })
        if (live == null) messages = savedMessages(activeID?.let { snapshot?.cached(it) })
    }
    fun foreground() {
        if (account == null) {
            if (loginJob?.isActive != true) {
                val pending = runCatching { vault.read("github") }.getOrNull()
                if (pending != null) {
                    githubURL = pending.getString("authorize_url")
                    val stamp = ++authGeneration; busy = true
                    loginJob = viewModelScope.launch {
                        try { pollGitHub(pending, stamp) } catch (_: CancellationException) {} catch (e: Exception) {
                            if (stamp == authGeneration) { cancelLogin(); report(e) }
                        }
                        finally { if (stamp == authGeneration) busy = false }
                    }
                }
            }
            return
        }
        if (refresh?.isActive == true) return
        viewModelScope.launch { push.refresh() }
        val stamp = generation
        refresh = viewModelScope.launch {
            while (generation == stamp) {
                try {
                    loadDevices(); check(stamp)
                    history?.sync(); check(stamp); showHistory()
                } catch (_: CancellationException) { return@launch }
                catch (e: Exception) { check(stamp); if (e is HttpFailure && e.status == 401) report(e); connectionStatus = e.message ?: "历史同步失败" }
                if (host != null && !connected && !connecting) {
                    try { connect(stamp) } catch (_: CancellationException) { return@launch }
                    catch (e: Exception) { connectionStatus = e.message ?: "电脑未连接" }
                }
                delay(10_000)
            }
        }
    }
    fun background() {
        collaboration.invalidate()
        generation++; refresh?.cancel(); refresh = null; eventJob?.cancel(); eventJob = null
        remote?.close(); remote = null; connected = false; connecting = false; approval = null; sending = false
        loadingHistory = false; loadingContent = emptySet(); attachmentPreview = null; loadingAttachment = false
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
                            live = ChatThread(result.getJSONObject("thread"), result.optJSONArray("pending_user_messages")?.objects() ?: emptyList(), result.optJSONArray("held_user_messages")?.objects() ?: emptyList())
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
                            if (event.method.startsWith("thread/") || event.method == "turn/completed") perform { loadThreads() }
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
        if (!connected) { showHistory(); return }
        val transport = remote ?: return; val stamp = generation; val selectedWorkspace = workspace
        val query = search.trim(); val archived = archivedList; val revision = ++listRevision
        val method = if (query.isNotEmpty()) "thread/search" else if (archived) "thread/listArchived" else if (selectedWorkspace.isBlank()) "thread/listAll" else "thread/list"
        val params = if (query.isNotEmpty()) json("query" to query, "limit" to 100) else json("summary_only" to true, "cwd" to selectedWorkspace)
        val result = transport.call(method, params); check(stamp)
        if (remote !== transport || workspace != selectedWorkspace || search.trim() != query || archivedList != archived || revision != listRevision) return
        liveRows = (if (query.isEmpty()) result.getJSONArray("threads").objects() else result.getJSONArray("results").objects().map { it.getJSONObject("thread") }).map { ThreadRow.from(it) }; showHistory()
    }
    suspend fun open(id: String) {
        val stamp = generation; val opening = ++openGeneration
        activeID = id; live = null; threadSettings = null; pending = emptyList(); messages = savedMessages(history?.snapshot?.cached(id)); running = false; readOnly = true
        hasOlder = false; loadingHistory = false; loadingContent = emptySet(); attachmentPreview = null; loadingAttachment = false
        title = rows.firstOrNull { it.id == id }?.title ?: "会话"
        if (connected) {
            checkNotNull(remote).call("thread/resume", json("session_id" to id, "response_only" to true, "history_page" to true), snapshotTag = opening)
        } else {
            val thread = history?.thread(id); check(stamp); if (opening != openGeneration) return
            messages = savedMessages(thread)
        }
    }
    private fun showLive() { live?.let { messages = it.messages; pending = it.pending; title = it.title; running = it.running; readOnly = it.readOnly || it.value.optBoolean("archived"); hasOlder = it.historyCursor.isNotEmpty(); threadSettings = it.settings; threadEngine = it.engine } }
    suspend fun loadModelChoices(threadID: String): List<RemoteProvider> {
        check(connected && live?.id == threadID) { "请先连接电脑" }
        val transport = checkNotNull(remote); val stamp = generation; val selection = openGeneration
        val result = transport.call("config/read"); check(stamp)
        if (selection != openGeneration || remote !== transport) throw CancellationException()
        return result.optJSONArray("providers")?.objects()?.map(RemoteProvider::from).orEmpty()
    }
    suspend fun updateSettings(settings: ThreadSettings, threadID: String) {
        check(connected && live?.id == threadID && !running && !readOnly && threadEngine in listOf("", "wuu")) { "请在会话空闲且电脑在线时修改设置" }
        val transport = checkNotNull(remote); val stamp = generation; val selection = openGeneration
        // The response is global; the ordered thread/updated notification supplies thread settings.
        transport.call("config/model/update", settings.updateParams(threadID)); check(stamp)
        if (selection != openGeneration || remote !== transport) throw CancellationException()
    }
    suspend fun loadOlder() {
        val thread = live ?: return; val transport = remote ?: return
        if (!connected || thread.historyCursor.isEmpty() || loadingHistory) return
        val stamp = generation; val selection = openGeneration; loadingHistory = true
        try {
            val page = transport.call("thread/history/read", json("thread_id" to thread.id, "cursor" to thread.historyCursor)); check(stamp)
            if (selection == openGeneration && remote === transport) { live?.prependHistory(page); showLive() }
        } finally { if (stamp == generation && selection == openGeneration) loadingHistory = false }
    }
    suspend fun expand(message: ChatMessage) {
        val thread = live ?: return; val transport = remote ?: return
        if (!connected || message.contentRef.isEmpty() || message.id in loadingContent) return
        val stamp = generation; val selection = openGeneration; loadingContent += message.id
        try {
            val item = transport.readContent(message.contentRef, thread.id); check(stamp)
            if (selection == openGeneration && remote === transport) { live?.expandContent(message.contentRef, item); showLive() }
        } finally { if (stamp == generation && selection == openGeneration) loadingContent -= message.id }
    }
    suspend fun newThread() {
        check(connected) { "请先连接电脑" }
        val stamp = generation; val params = JSONObject(); if (workspace.isNotBlank()) params.put("cwd", workspace)
        val opening = ++openGeneration
        val result = checkNotNull(remote).call("thread/start", params); check(stamp)
        if (opening != openGeneration) return
        live = ChatThread(result.getJSONObject("thread")); activeID = live?.id; loadingHistory = false; loadingContent = emptySet(); attachmentPreview = null; loadingAttachment = false; showLive(); loadThreads()
    }
    suspend fun export(context: android.content.Context) {
        val session = account; val selection = openGeneration; val stamp = generation
        exportConversation(context, messages) { account == session && openGeneration == selection && generation == stamp }
    }
    suspend fun previewAttachment(message: ChatMessage, index: Int) {
        val thread = live ?: return; val transport = remote ?: return
        if (!connected || loadingAttachment || index !in message.attachments.indices || message !in thread.messages) return
        val stamp = generation; val selection = openGeneration; loadingAttachment = true
        try {
            val result = transport.readAttachment(JSONObject(message.attachments[index]), thread.id, message.id); check(stamp)
            if (selection == openGeneration && remote === transport && live?.messages?.contains(message) == true) attachmentPreview = result
        } finally { if (stamp == generation && selection == openGeneration) loadingAttachment = false }
    }
    suspend fun exportAttachment(context: android.content.Context, attachment: LoadedAttachment, view: Boolean) {
        val stamp = generation; val selection = openGeneration
        shareAttachment(context, attachment, view) { stamp == generation && selection == openGeneration && attachmentPreview === attachment }
    }
    suspend fun send(text: String, attachments: List<InputAttachment> = emptyList()): Boolean {
        check(connected && !sending && !readOnly && (text.isNotBlank() || attachments.isNotEmpty())) { "当前无法发送" }
        val input = ChatInput(text, attachments)
        val id = activeID ?: error("请先创建会话")
        val stamp = generation; sending = true
        try {
            val method = if (running) "turn/queue" else "turn/start"
            checkNotNull(remote).call(method, input.params(id, queued = running))
            check(stamp); return true
        } finally { if (generation == stamp) sending = false }
    }
    suspend fun stop() { val id = activeID ?: return; checkNotNull(remote).call("turn/interrupt", json("thread_id" to id)) }
    suspend fun pin(row: ThreadRow) {
        check(connected); val stamp = generation
        checkNotNull(remote).call("thread/pin", json("thread_id" to row.id, "pinned" to !row.pinned)); check(stamp); loadThreads()
    }
    suspend fun archive(row: ThreadRow) {
        val stamp = generation
        check(connected)
        checkNotNull(remote).call("thread/archive", json("thread_id" to row.id, "archived" to !row.archived)); check(stamp)
        if (activeID == row.id) { openGeneration++; activeID = null; live = null; pending = emptyList(); messages = emptyList(); running = false; readOnly = true; attachmentPreview = null; loadingAttachment = false }; loadThreads()
    }
    suspend fun rename(id: String, title: String) {
        check(connected); val stamp = generation
        checkNotNull(remote).call("thread/rename", json("thread_id" to id, "title" to title)); check(stamp)
        if (live?.id == id) { live?.value?.put("title", title); showLive() }; loadThreads()
    }
    suspend fun pendingAction(message: PendingMessage, resume: Boolean) {
        check(connected && !readOnly && activeID == message.value.getString("thread_id") && pending.any { it.id == message.id }) { "消息状态已改变，请重新打开会话" }
        val transport = checkNotNull(remote)
        if (resume) {
            check(message.held && !running) { "请等待当前处理结束" }
            transport.call("turn/steer", message.resumeParams())
        } else transport.call(if (message.origin == "steer") "turn/unsteer" else "turn/dequeue",
            json("thread_id" to activeID, (if (message.origin == "steer") "steer_id" else "queue_id") to message.id))
    }
    suspend fun setHistory(enabled: Boolean) {
        val store = history ?: return; val stamp = generation
        store.setEnabled(enabled); check(stamp)
        store.sync(); check(stamp); showHistory()
    }
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
    suspend fun leaveHost(removeCache: Boolean = false): Long {
        val oldHistory = history
        collaboration.clear(); conversationDrafts.clear(); conversationAttachments.clear()
        background(); history = null; host = null; activeID = null; live = null; openGeneration++
        liveRows = emptyList(); rows = emptyList(); messages = emptyList(); workspace = ""; workspaces = emptyList(); historyEnabled = false; threadSettings = null; threadEngine = ""
        running = false; sending = false; readOnly = true; pending = emptyList(); search = ""; archivedList = false
        val selection = openGeneration
        withContext(NonCancellable) { oldHistory?.invalidate(removeCache) }
        return selection
    }
    fun logout() {
        if (busy) return
        val session = account ?: return
        busy = true
        viewModelScope.launch {
            var warning: String? = null
            try { AccountAPI(session.server).logout(session.token) }
            catch (e: Exception) { warning = "已退出本机，但服务器撤销失败。请在其他设备移除此手机：${e.message}" }
            try {
                if (account == session) { clearSession(); warning?.let { error = it } }
            } catch (e: Exception) { report(e) }
            finally { busy = false }
        }
    }
    private suspend fun clearSession() = withContext(NonCancellable) {
        busy = true
        try {
            authGeneration++; loginJob?.cancel(); loginJob = null
            account = null; devices = emptyList(); authMethod = ""; directoryCached = false; githubURL = null; browserURL = null
            push.bind(null)
            vault.delete("session"); vault.delete("github"); vault.delete("directory")
            leaveHost()
            check(directory.deleteRecursively()) { "无法清除会话缓存" }
            check(File(getApplication<Application>().cacheDir, "exports").deleteRecursively()) { "无法清除导出缓存" }
        } finally { busy = false }
    }
    override fun onCleared() { remote?.close(); super.onCleared() }
}
