import SwiftUI
import WuuCore
import CryptoKit

@MainActor @Observable final class AppModel: CollaborationConnection {
    var account: AccountSession?
    let collaboration = CollaborationModel()
    let push = PushNotifications()
    var devices: [AccountDevice] = []
    var host: AccountDevice?
    var entries: [HistoryEntry] = []
    var threads: [ChatThread] = []
    var activeID: String?
    var live: ChatThread?
    var saved: HistoryThread?
    var connected = false
    var connecting = false
    var busy = false
    var sending = false
    var loadingHistory = false
    var loadingContent: Set<String> = []
    var attachmentPreview: LoadedAttachment?
    var loadingAttachment = false
    var archivedList = false
    var search = ""
    var historyEnabled = false
    var connectionStatus = ""
    var error: String?
    var workspaces: [JSONValue] = []
    var workspace = ""
    var pendingApproval: JSONValue?
    var questions: [JSONValue] = []
    private var questionRevision = 0
    private var listRevision = UUID()
    var githubURL: URL?
    var configuration: AccountConfiguration?
    var authMethod = ""
    var directoryCached = false
    var resetRecovery: String?
    var resettingPassword = false
    var recovery: String? { account?.recovery ?? resetRecovery }
    private var configEpoch = UUID()
    private var remote: RemoteConnection?
    private var history: ConversationHistory?
    private var events: Task<Void, Never>?
    private var refresh: Task<Void, Never>?
    private var loginTask: Task<Void, Never>?
    private var epoch = UUID()
    private var authEpoch = UUID()
    private var opening = UUID()
    private var isForeground = false
    private let vault = KeychainStore()
    private var cacheDirectory: URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("WuuHistory")
    }
    var messages: [ChatMessage] {
        live?.messages ?? saved?.messages.map { ChatMessage(id: $0.stableID, role: $0.role, text: $0.text) } ?? []
    }
    init() {
        clearAbandonedAttachmentPreviews()
        do {
            account = try vault.load("session", as: AccountSession.self)
            resetRecovery = try vault.load("recovery", as: String.self)
            if let account, let directory = try vault.load("directory", as: RememberedDirectory.self)?.restore(account: account) {
                devices = directory.devices; authMethod = directory.auth_method; directoryCached = true
            }
        }
        catch { self.error = error.localizedDescription }
        push.bind(account)
    }
    func perform(_ operation: @escaping @MainActor () async throws -> Void) {
        let stamp = epoch
        Task { do { try await operation() } catch is CancellationError {} catch { if epoch == stamp { self.report(error) } } }
    }
    private func report(_ failure: Error) {
        error = failure.localizedDescription
        // A host-scoped 401 can mean that only that computer was removed.
        if case NativeError.http(401, _) = failure, account != nil { perform { try await self.loadDevices() } }
    }
    private func identity(server: String, username: String) throws -> DeviceIdentity {
        let key = "identity-" + Data(SHA256.hash(data: Data((server + "\n" + username).utf8))).base64URL
        if let seed = try vault.load(key, as: String.self) { return try DeviceIdentity(seed: Data(base64URL: seed)) }
        let identity = try DeviceIdentity()
        try vault.save(identity.seed.base64URL, key: key)
        return identity
    }
    private func accept(_ session: AccountSession) async throws {
        try vault.delete("directory")
        try vault.save(session, key: "session")
        try vault.delete("github")
        account = session
        githubURL = nil
        push.bind(session)
        try await loadDevices()
        foreground()
    }
    func revoke(_ device: AccountDevice) async throws {
        guard let session = account else { return }
        guard try Data(base64URL: device.pub).count == 32 else { throw NativeError.invalid("Invalid device identity") }
        let _: JSONValue = try await AccountAPI(server: session.server).request("/devices/" + device.pub, token: session.token, method: "DELETE")
        guard account == session else { return }
        if device.pub == session.pub { try await clearSession() }
        else { try await loadDevices() }
    }
    func acknowledgeRecovery() throws {
        try vault.delete("recovery"); resetRecovery = nil
        guard var session = account else { return }
        session.recovery = nil
        try vault.save(session, key: "session")
        account = session
    }
    func loadConfiguration(server: String) async {
        configEpoch = UUID(); let stamp = configEpoch
        configuration = nil
        guard !server.isEmpty else { return }
        do {
            let result: AccountConfiguration = try await AccountAPI(server: server).request("/config")
            if configEpoch == stamp { configuration = result }
        } catch is CancellationError {} catch { if configEpoch == stamp { self.error = error.localizedDescription } }
    }
    func cancelLogin() throws {
        guard !resettingPassword else { return }
        let pending = try vault.load("github", as: GitHubPending.self)
        authEpoch = UUID(); loginTask?.cancel(); loginTask = nil
        busy = false; githubURL = nil
        try vault.delete("github")
        if let pending { Task { try? await AccountAPI(server: pending.server).cancelGitHub(pending) } }
    }
    func resetPassword(server: String, username: String, secret: String, password: String, changing: Bool) async throws {
        guard !busy else { return }
        busy = true; resettingPassword = true
        defer { busy = false; resettingPassword = false }
        authEpoch = UUID(); let stamp = authEpoch
        let session = account
        if changing, session == nil { throw NativeError.invalid("请先登录") }
        let key = try await AccountAPI(server: changing ? session?.server ?? server : server).resetPassword(
            username: changing ? session?.username ?? username : username, secret: secret, password: password, token: changing ? session?.token : nil)
        guard authEpoch == stamp else { throw CancellationError() }
        try vault.save(key, key: "recovery"); resetRecovery = key
        try await clearSession()
    }
    func login(server: String, username: String, password: String, register: Bool = false) async throws {
        guard !busy else { return }
        loginTask?.cancel()
        authEpoch = UUID(); let stamp = authEpoch
        busy = true; defer { if authEpoch == stamp { busy = false } }
        try vault.delete("github"); githubURL = nil
        let api = try AccountAPI(server: server)
        let user = username.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let result = try await api.login(username: user, password: password, name: UIDevice.current.name,
                                        identity: identity(server: api.origin.absoluteString, username: user), register: register)
        guard authEpoch == stamp else { throw CancellationError() }
        try await accept(result)
    }
    func github(server: String) async throws {
        guard !busy else { return }
        authEpoch = UUID(); let stamp = authEpoch
        busy = true; defer { if authEpoch == stamp { busy = false } }
        let pending = try await AccountAPI(server: server).startGitHub()
        guard authEpoch == stamp else { throw CancellationError() }
        try vault.save(pending, key: "github")
        githubURL = pending.authorizationURL
        await UIApplication.shared.open(pending.authorizationURL)
        guard authEpoch == stamp, isForeground else { return }
        pollGitHub(pending)
    }
    private func pollGitHub(_ pending: GitHubPending) {
        loginTask?.cancel()
        let stamp = authEpoch
        loginTask = Task {
            do {
                let api = try AccountAPI(server: pending.server)
                while !Task.isCancelled {
                    let result = try await api.pollGitHub(pending)
                    try Task.checkCancellation()
                    guard authEpoch == stamp else { return }
                    if result.status == "authorized", let user = result.username {
                        let session = try await api.completeGitHub(pending, username: user, name: UIDevice.current.name,
                            identity: identity(server: pending.server, username: user))
                        try Task.checkCancellation()
                        guard authEpoch == stamp else { return }
                        try await accept(session)
                        return
                    }
                    guard result.status == "pending" else { throw NativeError.invalid("GitHub 登录未完成，请重新登录") }
                    try await Task.sleep(for: .seconds(2))
                }
            } catch is CancellationError {} catch {
                guard authEpoch == stamp else { return }
                try? cancelLogin(); report(error)
            }
        }
    }
    func loadDevices() async throws {
        guard let account else { return }
        let directory: DeviceDirectory
        do { directory = try await AccountAPI(server: account.server).request("/devices", token: account.token) }
        catch NativeError.http(401, _) {
            if self.account == account { try await clearSession() }
            return
        }
        guard self.account == account else { return }
        try vault.save(RememberedDirectory(account: account, directory: directory), key: "directory")
        devices = directory.devices
        authMethod = directory.auth_method
        directoryCached = false
        if let host, !devices.contains(where: { $0.pub == host.pub && $0.role == "host" }) {
            await leaveHost(removeCache: true)
            guard self.account == account else { return }
            foreground()
        }
        try ConversationHistory.prune(account: account, hosts: devices.filter { $0.role == "host" }.map(\.pub), directory: cacheDirectory)
    }
    func selectHost(_ device: AccountDevice) async throws {
        let session = account
        let selection = await leaveHost()
        let stamp = epoch
        guard opening == selection, let account, account == session,
              devices.contains(where: { $0.pub == device.pub && $0.role == "host" }) else { return }
        host = device
        let store = try ConversationHistory(account: account, host: device.pub, directory: cacheDirectory)
        history = store
        let cached = await store.current()
        guard epoch == stamp else { return }
        show(cached)
        foreground()
    }
    private func show(_ snapshot: HistorySnapshot) {
        entries = snapshot.sortedEntries
        historyEnabled = snapshot.enabled
        if let activeID, let body = snapshot.bodies[activeID] { saved = body.thread }
        else if !snapshot.enabled || !entries.contains(where: { $0.id == activeID }) { saved = nil }
    }
    func foreground() {
        isForeground = true
        if account == nil {
            if let pending = try? vault.load("github", as: GitHubPending.self), pending.expires > Date() {
                githubURL = pending.authorizationURL
                pollGitHub(pending)
            } else if githubURL != nil {
                try? cancelLogin()
            }
            return
        }
        guard refresh == nil else { return }
        Task { await push.refresh() }
        let stamp = epoch
        refresh = Task {
            while !Task.isCancelled, epoch == stamp {
                do {
                    try await loadDevices()
                    guard epoch == stamp else { return }
                    try await syncHistory()
                } catch is CancellationError { return } catch {
                    guard epoch == stamp else { return }
                    if case NativeError.http(401, _) = error { report(error) }
                    connectionStatus = error.localizedDescription
                }
                guard !Task.isCancelled, epoch == stamp else { return }
                if !connected && !connecting { await connect() }
                do { try await Task.sleep(for: .seconds(10)) } catch { return }
            }
        }
    }
    func background() async {
        isForeground = false
        epoch = UUID()
        refresh?.cancel(); refresh = nil
        loginTask?.cancel()
        events?.cancel(); events = nil
        let oldRemote = remote; remote = nil
        connected = false; connecting = false; pendingApproval = nil; sending = false
        loadingHistory = false; loadingContent = []; attachmentPreview = nil; loadingAttachment = false
        questions = []; questionRevision += 1
        await oldRemote?.disconnect()
    }
    @discardableResult func leaveHost(removeCache: Bool = false) async -> UUID {
        collaboration.reset()
        let oldHistory = history
        history = nil; host = nil; entries = []; threads = []; activeID = nil; live = nil; saved = nil
        workspace = ""; workspaces = []; historyEnabled = false; opening = UUID(); search = ""; archivedList = false
        let selection = opening
        await background()
        try? await oldHistory?.invalidate(removeCache: removeCache)
        return selection
    }
    func logout() async throws {
        guard !busy, let session = account else { return }
        busy = true; defer { busy = false }
        // Clear locally even offline, but never imply that remote revocation succeeded.
        var warning: String?
        do { try await AccountAPI(server: session.server).logout(token: session.token) }
        catch { warning = "已退出本机，但服务器撤销失败。请在其他设备移除此手机：" + error.localizedDescription }
        guard account == session else { return }
        try await clearSession()
        if let warning { error = warning }
    }
    private func clearSession() async throws {
        busy = true; defer { busy = false }
        authEpoch = UUID(); loginTask?.cancel(); account = nil
        push.bind(nil)
        try vault.delete("session")
        try vault.delete("github")
        try vault.delete("directory")
        githubURL = nil; devices = []; authMethod = ""; directoryCached = false
        await leaveHost()
        if FileManager.default.fileExists(atPath: cacheDirectory.path) { try FileManager.default.removeItem(at: cacheDirectory) }
    }
    func openPushHost(_ pub: String) async throws {
        guard let session = account, push.mayOpenNotification(), (try? Data(base64URL: pub).count) == 32 else { return }
        try await loadDevices()
        guard account == session, push.mayOpenNotification(), !directoryCached,
              let device = devices.first(where: { $0.pub == pub && $0.role == "host" }) else { return }
        try await selectHost(device)
    }
    func syncHistory() async throws {
        guard let history else { return }
        let stamp = epoch
        let snapshot = try await history.sync()
        guard epoch == stamp else { return }
        show(snapshot)
        if let id = activeID, snapshot.entries[id] != nil {
            let body = try await history.thread(id)
            if epoch == stamp, activeID == id { saved = body }
        }
    }
    func channelCall(_ method: String, _ params: JSONValue = [:]) async throws -> JSONValue {
        try Task.checkCancellation()
        guard connected, let remote else { throw NativeError.invalid("请先连接电脑") }
        let stamp = epoch
        let result = try await remote.call(method, params: params)
        guard stamp == epoch, self.remote === remote else { throw CancellationError() }
        return result
    }
    func setHistory(_ enabled: Bool) async throws {
        let stamp = epoch
        try await history?.setEnabled(enabled)
        guard epoch == stamp else { return }
        try await syncHistory()
    }
    func connect() async {
        guard !connecting, !connected, let account, let host else { return }
        connecting = true
        let stamp = epoch
        defer { if epoch == stamp { connecting = false } }
        do {
            let connection = try RemoteConnection(account: account, host: host.pub)
            remote = connection
            events?.cancel()
            events = Task {
                for await event in connection.events {
                    guard !Task.isCancelled, epoch == stamp else { return }
                    switch event {
                    case .snapshot(let tag, let result):
                        if opening.uuidString == tag {
                            live = ChatThread(result["thread"], pending: result["pending_user_messages"].array, held: result["held_user_messages"].array)
                        }
                    case .disconnected(let reason): connected = false; pendingApproval = nil; connectionStatus = reason
                    case .notification(let method, let params):
                        if method == "user-question/requested" {
                            questionRevision += 1
                            let request = params["request"]
                            questions.removeAll { $0["request_id"] == request["request_id"] }
                            questions.append(request)
                        } else if method == "user-question/resolved" {
                            questionRevision += 1
                            questions.removeAll { $0["request_id"] == params["request_id"] }
                        }
                        live?.apply(method, params)
                        if ["thread/started", "thread/updated", "thread/archived", "turn/completed"].contains(method) {
                            perform { try await self.loadThreads() }
                        }
                    case .request(let request): pendingApproval = request
                    default: break
                    }
                }
            }
            try await connection.connect()
            _ = try await connection.call("initialize")
            guard epoch == stamp else { await connection.disconnect(); return }
            connected = true
            try await loadQuestions()
            let result = try await connection.call("workspace/list")
            guard epoch == stamp else { return }
            workspaces = result["workspaces"].array
            if workspace.isEmpty { workspace = result["current"].string ?? workspaces.first?["path"].string ?? "" }
            try await loadThreads()
            guard epoch == stamp else { return }
            if let id = activeID { try await open(id) }
        } catch {
            if epoch == stamp {
                let old = remote; remote = nil; connected = false; events?.cancel(); events = nil
                if !(error is CancellationError) { connectionStatus = error.localizedDescription }
                await old?.disconnect()
            }
        }
    }
    func loadThreads() async throws {
        guard connected, let remote else { return }
        let stamp = epoch
        let selected = workspace
        let query = search.trimmingCharacters(in: .whitespacesAndNewlines), archived = archivedList
        listRevision = UUID(); let revision = listRevision
        let method = !query.isEmpty ? "thread/search" : archived ? "thread/listArchived" : selected.isEmpty ? "thread/listAll" : "thread/list"
        let params: JSONValue = query.isEmpty ? ["cwd": .string(selected), "summary_only": true] : ["query": .string(query), "limit": 100]
        let result = try await remote.call(method, params: params)
        guard epoch == stamp, self.remote === remote, listRevision == revision, workspace == selected,
              search.trimmingCharacters(in: .whitespacesAndNewlines) == query, archivedList == archived else { return }
        threads = (query.isEmpty ? result["threads"].array : result["results"].array.map { $0["thread"] }).map { ChatThread($0) }
    }
    func open(_ id: String) async throws {
        opening = UUID(); let selection = opening
        activeID = id; live = nil; saved = nil; loadingHistory = false; loadingContent = []; attachmentPreview = nil; loadingAttachment = false
        let stamp = epoch
        if connected, let remote {
            _ = try await remote.call("thread/resume", params: ["session_id": .string(id), "response_only": true, "history_page": true], snapshotTag: selection.uuidString)
        } else if let history, entries.contains(where: { $0.id == id }) {
            let body = try await history.thread(id)
            guard epoch == stamp, opening == selection else { throw CancellationError() }
            saved = body
        }
    }
    func loadModelChoices(threadID: String) async throws -> [RemoteProvider] {
        guard connected, let remote, live?.id == threadID else { throw CancellationError() }
        let stamp = epoch, selection = opening
        let result = try await remote.call("config/read")
        guard epoch == stamp, opening == selection, self.remote === remote else { throw CancellationError() }
        return result["providers"].array.map(RemoteProvider.init)
    }
    func updateSettings(_ settings: ThreadSettings, threadID: String) async throws {
        guard connected, let remote, let live, live.id == threadID, !live.running, !live.readOnly, !live.archived,
              live.engine.isEmpty || live.engine == "wuu" else { throw NativeError.invalid("请在会话空闲且电脑在线时修改设置") }
        let stamp = epoch, selection = opening
        // The response describes global defaults. The ordered thread/updated event owns this selection.
        _ = try await remote.call("config/model/update", params: settings.updateParams(threadID: threadID))
        guard epoch == stamp, opening == selection, self.remote === remote else { throw CancellationError() }
    }
    func loadOlder() async throws {
        guard connected, let remote, let live, !live.historyCursor.isEmpty, !loadingHistory else { return }
        let stamp = epoch, selection = opening
        loadingHistory = true
        defer { if epoch == stamp, opening == selection { loadingHistory = false } }
        let page = try await remote.call("thread/history/read", params: ["thread_id": .string(live.id), "cursor": .string(live.historyCursor)])
        guard epoch == stamp, opening == selection, self.remote === remote else { return }
        self.live?.prependHistory(page)
    }
    func expand(_ message: ChatMessage) async throws {
        guard connected, let remote, let live, !message.contentRef.isEmpty, !loadingContent.contains(message.id) else { return }
        let stamp = epoch, selection = opening
        loadingContent.insert(message.id)
        defer { if epoch == stamp, opening == selection { loadingContent.remove(message.id) } }
        let item = try await remote.readContent(message.contentRef, threadID: live.id)
        guard epoch == stamp, opening == selection, self.remote === remote else { return }
        self.live?.expandContent(message.contentRef, item: item)
    }
    func startThread() async throws {
        guard let remote, connected else { throw NativeError.invalid("请先连接电脑") }
        let stamp = epoch; opening = UUID(); let selection = opening
        var params: JSONValue = [:]
        if !workspace.isEmpty { params = ["cwd": .string(workspace)] }
        let result = try await remote.call("thread/start", params: params)
        guard epoch == stamp, opening == selection else { return }
        let thread = ChatThread(result["thread"])
        activeID = thread.id; live = thread; saved = nil; loadingHistory = false; loadingContent = []; attachmentPreview = nil; loadingAttachment = false
        try await loadThreads()
    }
    func send(_ text: String, attachments: [InputAttachment] = []) async throws {
        guard !sending, let remote, let live, connected, !live.readOnly, !live.archived, !text.isEmpty || !attachments.isEmpty else { throw NativeError.invalid("当前无法发送") }
        let input = try ChatInput(text: text, attachments: attachments)
        let stamp = epoch
        sending = true; defer { if epoch == stamp { sending = false } }
        _ = try await remote.call(live.running ? "turn/queue" : "turn/start", params: input.params(threadID: live.id, queued: live.running))
        guard epoch == stamp else { throw CancellationError() }
    }
    func previewAttachment(_ message: ChatMessage, index: Int) async throws {
        guard connected, let remote, let live, !loadingAttachment, message.attachments.indices.contains(index),
              live.messages.contains(where: { $0 == message }) else { return }
        let stamp = epoch, selection = opening
        loadingAttachment = true
        defer { if epoch == stamp, opening == selection { loadingAttachment = false } }
        let result = try await remote.readAttachment(message.attachments[index], threadID: live.id, messageID: message.id)
        guard epoch == stamp, opening == selection, self.remote === remote,
              self.live?.messages.contains(where: { $0 == message }) == true else { return }
        attachmentPreview = result
    }
    func stop() async throws {
        guard let remote, let activeID else { return }
        _ = try await remote.call("turn/interrupt", params: ["thread_id": .string(activeID)])
    }
    func pin(_ thread: ChatThread) async throws {
        guard connected, let remote else { throw NativeError.invalid("请先连接电脑") }
        let stamp = epoch
        _ = try await remote.call("thread/pin", params: ["thread_id": .string(thread.id), "pinned": .bool(!thread.pinned)])
        guard epoch == stamp, self.remote === remote else { return }
        try await loadThreads()
    }
    func archive(_ thread: ChatThread) async throws {
        guard connected, let remote else { throw NativeError.invalid("请先连接电脑") }
        let stamp = epoch
        _ = try await remote.call("thread/archive", params: ["thread_id": .string(thread.id), "archived": .bool(!thread.archived)])
        guard epoch == stamp, self.remote === remote else { return }
        if activeID == thread.id { opening = UUID(); activeID = nil; live = nil; saved = nil; attachmentPreview = nil; loadingAttachment = false }
        try await loadThreads()
    }
    func rename(_ id: String, title: String) async throws {
        guard connected, let remote else { throw NativeError.invalid("请先连接电脑") }
        let stamp = epoch
        _ = try await remote.call("thread/rename", params: ["thread_id": .string(id), "title": .string(title)])
        guard epoch == stamp, self.remote === remote else { return }
        if live?.id == id { live?.title = title }
        try await loadThreads()
    }
    func pendingAction(_ message: PendingMessage, resume: Bool) async throws {
        guard connected, let remote, let live, live.id == message.value["thread_id"].string, !live.readOnly,
              live.pending.contains(where: { $0.id == message.id }) else { throw NativeError.invalid("消息状态已改变，请重新打开会话") }
        if resume {
            guard message.held, !live.running else { throw NativeError.invalid("请等待当前处理结束") }
            _ = try await remote.call("turn/steer", params: message.resumeParams)
        } else {
            _ = try await remote.call(message.origin == "steer" ? "turn/unsteer" : "turn/dequeue", params:
                ["thread_id": .string(live.id), message.origin == "steer" ? "steer_id" : "queue_id": .string(message.id)])
        }
    }
    func rejectRequest() async throws {
        guard let pendingApproval, let remote else { return }
        try await remote.reject(id: pendingApproval["id"], message: "请在电脑上处理此请求")
        self.pendingApproval = nil
    }
    func loadQuestions() async throws {
        guard let remote, connected else { return }
        let stamp = epoch; let revision = questionRevision
        let result = try await remote.call("user-question/list")
        if stamp == epoch, revision == questionRevision { questions = result["questions"].array }
    }
    func answerQuestion(_ request: JSONValue, answers: [JSONValue]?) async throws {
        guard let remote, connected else { throw NativeError.invalid("请先连接电脑") }
        let stamp = epoch
        var params: JSONValue = ["request_id": request["request_id"]]
        if let answers { params = ["request_id": request["request_id"], "answer": ["answers": .array(answers)]] }
        _ = try await remote.call(answers == nil ? "user-question/cancel" : "user-question/respond", params: params)
        if stamp == epoch { questions.removeAll { $0["request_id"] == request["request_id"] } }
    }
}
