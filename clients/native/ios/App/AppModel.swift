import SwiftUI
import WuuCore
import CryptoKit

private struct PairedLocation: Codable {
    let host: String
    let workspace: String
    let thread: String?
}

@MainActor @Observable final class HistoryMessageEdit: Identifiable {
    let id = UUID()
    let threadID: String
    let hostID: String
    let message: ChatMessage
    let original: JSONValue
    let attachments: [InputAttachment]
    var text: String
    var prepared = false
    var submitting = false
    var error: String?
    var attachmentCount: Int { attachments.count }
    init(threadID: String, hostID: String, message: ChatMessage, original: JSONValue, attachments: [InputAttachment]) {
        self.threadID = threadID; self.hostID = hostID; self.message = message
        self.original = original; self.attachments = attachments
        text = original["input_text"].string ?? original["text"].string ?? ""
    }
}

@MainActor @Observable final class AppModel {
    var account: AccountSession?
    var pairedComputers: [PairedComputer] = []
    var selectedPair: PairedComputer?
    var hasSavedConnections: Bool { account != nil || !pairedComputers.isEmpty }
    var pairingBusy = false
    var receivedPairingLink: String?
    let push = PushNotifications()
    var devices: [AccountDevice] = []
    var host: AccountDevice?
    var entries: [HistoryEntry] = []
    var threads: [ChatThread] = []
    var activeID: String? { didSet { rememberLocation() } }
    var live: ChatThread?
    var saved: HistoryThread?
    var connected = false
    var connecting = false
    var busy = false
    var sending = false
    var historyActionBusy = false
    var showingHistoryEdit = false
    private var historyEdits: [String: HistoryMessageEdit] = [:]
    private var historyEditKey: String { (host?.pub ?? "") + ":" + (activeID ?? "") }
    var historyEdit: HistoryMessageEdit? {
        get { historyEdits[historyEditKey] }
        set { historyEdits[historyEditKey] = newValue }
    }
    var loadingHistory = false
    var loadingContent: Set<String> = []
    let imagePreviews = ImagePreviewLoader()
    var attachmentPreview: LoadedAttachment?
    var loadingAttachment = false
    var archivedList = false
    var search = ""
    var historyEnabled = false
    var connectionStatus = ""
    var error: String?
    var workspaces: [JSONValue] = []
    var workspace = "" { didSet { rememberLocation() } }
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
    private var reconnectTask: Task<Void, Never>?
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
    var conversationRows: [ConversationRow] { live?.rows ?? ConversationRow.grouped(messages) }
    init() {
        #if DEBUG
        if NativeUIFixture.enabled {
            live = NativeUIFixture.thread(); activeID = live?.id; connected = true
            return
        }
        #endif
        clearAbandonedAttachmentPreviews()
        do {
            account = try vault.load("session", as: AccountSession.self)
            pairedComputers = try vault.load("paired-computers", as: [PairedComputer].self) ?? []
            if let location = try vault.load("paired-location", as: PairedLocation.self),
               let pair = pairedComputers.first(where: { $0.id == location.host }) {
                selectedPair = pair
                host = AccountDevice(pub: pair.hostPub, name: pair.hostName, role: "host", online: false)
                activeID = location.thread; workspace = location.workspace
            }
            resetRecovery = try vault.load("recovery", as: String.self)
            if let account, let directory = try vault.load("directory", as: RememberedDirectory.self)?.restore(account: account) {
                devices = directory.devices; authMethod = directory.auth_method; directoryCached = true
                if selectedPair == nil, let location = try vault.load("location", as: NavigationLocation.self)?.restore(account: account, devices: devices) {
                    host = devices.first { $0.pub == location.host }
                    activeID = location.thread; workspace = location.workspace
                    history = try ConversationHistory(account: account, host: location.host, directory: cacheDirectory)
                }
            }
        }
        catch { self.error = error.localizedDescription }
        push.bind(account)
    }
    func rememberLocation() {
        guard let host else { return }
        do {
            if let selectedPair {
                try vault.save(PairedLocation(host: selectedPair.id, workspace: workspace, thread: activeID), key: "paired-location")
            } else if let account {
                try vault.save(NavigationLocation(account: account, host: host.pub, workspace: workspace, thread: activeID), key: "location")
            }
        } catch { self.error = error.localizedDescription }
    }
    func pairComputer(_ input: String) async throws {
        guard !pairingBusy else { throw CancellationError() }
        pairingBusy = true
        defer { pairingBusy = false }
        let pair = try await PairedComputer.pair(input, deviceName: UIDevice.current.name)
        try Task.checkCancellation()
        var computers = pairedComputers.filter { $0.id != pair.id }
        computers.append(pair)
        try vault.save(computers, key: "paired-computers")
        pairedComputers = computers
        receivedPairingLink = nil
        try await selectComputer(pair)
    }
    func selectComputer(_ pair: PairedComputer) async throws {
        let selection = await leaveHost()
        guard opening == selection, pairedComputers.contains(pair) else { return }
        selectedPair = pair
        host = AccountDevice(pub: pair.hostPub, name: pair.hostName, role: "host", online: false)
        rememberLocation()
        foreground()
    }
    func forgetComputer(_ pair: PairedComputer) async throws {
        let computers = pairedComputers.filter { $0.id != pair.id }
        try vault.save(computers, key: "paired-computers")
        pairedComputers = computers
        if selectedPair?.id == pair.id { await leaveHost(); foreground() }
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
        if selectedPair == nil, let host, !devices.contains(where: { $0.pub == host.pub && $0.role == "host" }) {
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
        rememberLocation()
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
        #if DEBUG
        if NativeUIFixture.enabled { return }
        #endif
        isForeground = true
        if account == nil && selectedPair == nil {
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
        // History HTTP requests must never hold up the encrypted execution channel.
        reconnectTask = Task {
            while !Task.isCancelled, epoch == stamp {
                if !connected && !connecting { await connect() }
                do { try await Task.sleep(for: .seconds(3)) } catch { return }
            }
        }
        refresh = Task {
            if let history {
                let cached = await history.current()
                guard epoch == stamp else { return }
                show(cached)
            }
            while !Task.isCancelled, epoch == stamp {
                do {
                    try await loadDevices()
                    guard epoch == stamp else { return }
                    try await syncHistory()
                } catch is CancellationError { return } catch {
                    guard epoch == stamp else { return }
                    if case NativeError.http(401, _) = error { report(error) }
                    if !connected { connectionStatus = error.localizedDescription }
                }
                guard !Task.isCancelled, epoch == stamp else { return }
                do { try await Task.sleep(for: .seconds(10)) } catch { return }
            }
        }
    }
    func background() async {
        isForeground = false
        epoch = UUID()
        refresh?.cancel(); refresh = nil
        reconnectTask?.cancel(); reconnectTask = nil
        loginTask?.cancel()
        events?.cancel(); events = nil
        let oldRemote = remote; remote = nil
        connected = false; connecting = false; pendingApproval = nil; sending = false; historyActionBusy = false
        imagePreviews.clear(); loadingHistory = false; loadingContent = []; attachmentPreview = nil; loadingAttachment = false
        questions = []; questionRevision += 1
        await oldRemote?.disconnect()
    }
    @discardableResult func leaveHost(removeCache: Bool = false) async -> UUID {
        try? vault.delete("location")
        try? vault.delete("paired-location")
        selectedPair = nil
        host = nil
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
        if selectedPair == nil { await leaveHost() }
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
    func setHistory(_ enabled: Bool) async throws {
        let stamp = epoch
        try await history?.setEnabled(enabled)
        guard epoch == stamp else { return }
        try await syncHistory()
    }
    func connect() async {
        guard !connecting, !connected, let host, account != nil || selectedPair != nil else { return }
        connecting = true
        let stamp = epoch
        defer { if epoch == stamp { connecting = false } }
        do {
            let connection: RemoteConnection
            if let selectedPair { connection = try RemoteConnection(computer: selectedPair) }
            else if let account { connection = try RemoteConnection(account: account, host: host.pub) }
            else { return }
            remote = connection
            events?.cancel()
            events = Task {
                for await event in connection.events {
                    guard !Task.isCancelled, epoch == stamp, remote === connection else { return }
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
            connectionStatus = ""
        } catch {
            if epoch == stamp {
                let old = remote; remote = nil; connected = false; events?.cancel(); events = nil
                if !(error is CancellationError) { connectionStatus = error.localizedDescription }
                await old?.disconnect()
            }
            return
        }
        // A missing conversation or failed directory read is not a transport failure.
        guard let connection = remote, epoch == stamp else { return }
        do {
            async let pendingQuestions: Void = loadQuestions()
            let result = try await connection.call("workspace/list")
            guard epoch == stamp else { return }
            workspaces = result["workspaces"].array
            if workspace.isEmpty { workspace = result["current"].string ?? workspaces.first?["path"].string ?? "" }
            async let pendingThreads: Void = loadThreads()
            if let id = activeID { try await open(id, preservingContent: true) }
            try await pendingThreads
            try await pendingQuestions
        } catch is CancellationError {} catch {
            if epoch == stamp, remote === connection { report(error) }
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
    func open(_ id: String, preservingContent: Bool = false) async throws {
        if activeID != id { showingHistoryEdit = false }
        opening = UUID(); let selection = opening
        if activeID != id || (!preservingContent && connected) { live = nil; saved = nil }
        activeID = id; imagePreviews.clear(); loadingHistory = false; loadingContent = []; attachmentPreview = nil; loadingAttachment = false
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
    func loadOlder(beforePrepend: () -> Void) async throws {
        guard connected, let remote, let live, !live.historyCursor.isEmpty, !loadingHistory else { return }
        let stamp = epoch, selection = opening
        loadingHistory = true
        defer { if epoch == stamp, opening == selection { loadingHistory = false } }
        let page = try await remote.call("thread/history/read", params: ["thread_id": .string(live.id), "cursor": .string(live.historyCursor)])
        guard epoch == stamp, opening == selection, self.remote === remote else { return }
        beforePrepend()
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
    func startThread(workdir: String? = nil) async throws {
        guard let remote, connected else { throw NativeError.invalid("请先连接电脑") }
        let directory = (workdir ?? workspace).trimmingCharacters(in: .whitespacesAndNewlines)
        let stamp = epoch; opening = UUID(); let selection = opening
        var params: [String: JSONValue] = [:]
        if !directory.isEmpty { params["cwd"] = .string(directory) }
        if let id = workspaces.first(where: { $0["path"].string == directory })?["id"].string {
            params["workspace_id"] = .string(id)
        }
        let result = try await remote.call("thread/start", params: .object(params))
        guard epoch == stamp, opening == selection else { throw CancellationError() }
        let thread = ChatThread(result["thread"])
        workspace = thread.cwd
        activeID = thread.id; live = thread; saved = nil; imagePreviews.clear(); loadingHistory = false; loadingContent = []; attachmentPreview = nil; loadingAttachment = false
        perform { try await self.loadThreads() }
    }
    func send(_ text: String, attachments: [InputAttachment] = []) async throws {
        guard !sending, let remote, let live, connected, !live.readOnly, !live.archived, !text.isEmpty || !attachments.isEmpty else { throw NativeError.invalid("当前无法发送") }
        let input = try ChatInput(text: text, attachments: attachments)
        let stamp = epoch
        sending = true; defer { if epoch == stamp { sending = false } }
        _ = try await remote.call(live.running ? "turn/queue" : "turn/start", params: input.params(threadID: live.id, queued: live.running))
        guard epoch == stamp else { throw CancellationError() }
    }
    func canFork(_ message: ChatMessage) -> Bool {
        guard connected, let live, !live.readOnly, !live.archived,
              ["user", "assistant"].contains(message.role), let item = live.item(for: message) else { return false }
        return item["status"].string != "in_progress" && !item["read_only"].bool
    }
    func canEdit(_ message: ChatMessage) -> Bool {
        guard canFork(message), let live, !live.running, live.pending.isEmpty, message.role == "user",
              let item = live.item(for: message) else { return false }
        return !["host", "plugin"].contains(item["origin"].string ?? "") && message.sourceSessionID.isEmpty
    }
    func copyMessage(_ message: ChatMessage) async throws {
        if message.contentRef.isEmpty {
            UIPasteboard.general.string = message.text
        } else {
            guard connected, let remote, let live, live.item(for: message) != nil else { throw NativeError.invalid("请连接电脑以复制完整消息") }
            let stamp = epoch, selection = opening
            let item = try await remote.readContent(message.contentRef, threadID: live.id)
            guard epoch == stamp, opening == selection else { throw CancellationError() }
            UIPasteboard.general.string = item["text"].string ?? item["error"].string ?? ""
        }
        Haptics.tap()
    }
    func forkMessage(_ message: ChatMessage) async throws {
        guard !historyActionBusy, canFork(message), let remote, let live, let item = live.item(for: message) else {
            throw NativeError.invalid("当前无法从这条消息分叉")
        }
        let stamp = epoch, selection = opening
        historyActionBusy = true
        defer { if epoch == stamp { historyActionBusy = false } }
        let result = try await remote.call("thread/fork", params: ["thread_id": .string(live.id),
            "turn_id": .string(message.turnID), "item_id": .string(message.itemID), "mode": "local",
            "target": ["seq": item["seq"], "source_id": item["source_id"], "type": item["type"]]])
        guard epoch == stamp, self.remote === remote else { return }
        perform { try await self.loadThreads() }
        guard opening == selection else { return }
        let fork = ChatThread(result["thread"])
        guard !fork.id.isEmpty else { throw NativeError.invalid("电脑未返回分叉会话") }
        workspace = fork.cwd
        try await open(fork.id)
        Haptics.tap()
    }
    func beginHistoryEdit(_ message: ChatMessage) async throws {
        guard !historyActionBusy, canEdit(message), let remote, let live, let host, var item = live.item(for: message) else {
            throw NativeError.invalid("请在会话空闲、待发消息处理完毕且电脑在线时编辑")
        }
        if let edit = historyEdit {
            if edit.message.id == message.id || edit.prepared { showingHistoryEdit = true; return }
        }
        let stamp = epoch, selection = opening
        historyActionBusy = true
        defer { if epoch == stamp { historyActionBusy = false } }
        if !message.contentRef.isEmpty { item = try await remote.readContent(message.contentRef, threadID: live.id) }
        // Resolve every original attachment before allowing a destructive history edit.
        var attachments: [InputAttachment] = []
        for attachment in item["images"].array + item["files"].array {
            let loaded = try await remote.readAttachment(attachment, threadID: live.id, messageID: message.id)
            attachments.append(try InputAttachment(filename: loaded.filename, mediaType: loaded.mediaType, data: loaded.data))
        }
        try InputAttachment.validate(attachments, text: item["input_text"].string ?? item["text"].string ?? "")
        guard epoch == stamp, opening == selection, self.remote === remote, canEdit(message) else { throw CancellationError() }
        historyEdit = HistoryMessageEdit(threadID: live.id, hostID: host.pub, message: message, original: item, attachments: attachments)
        showingHistoryEdit = true
    }
    func submitHistoryEdit(_ edit: HistoryMessageEdit) async throws {
        guard !edit.submitting, !sending, !historyActionBusy, connected, let remote, let live,
              live.id == edit.threadID, host?.pub == edit.hostID, !live.readOnly, !live.archived else {
            throw NativeError.invalid("请连接原来的电脑和会话后重试")
        }
        // A lost reply may follow an accepted send. Its durable input identity wins over retry.
        if live.turns.flatMap({ $0["items"].array }).contains(where: { $0["source_id"].string == edit.id.uuidString }) {
            historyEdit = nil; showingHistoryEdit = false; return
        }
        guard !live.running, live.pending.isEmpty else { throw NativeError.invalid("请先等待当前回复结束并处理待发消息") }
        let text = edit.text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty || !edit.attachments.isEmpty else { throw NativeError.invalid("消息不能为空") }
        let input = try ChatInput(text: text, attachments: edit.attachments)
        guard case .object(var params) = input.params(threadID: edit.threadID, queued: false) else { return }
        params["client_id"] = .string(edit.id.uuidString)
        let originalImages = edit.original["images"].array
        params["images"] = .array((params["images"]?.array ?? []).enumerated().map { index, image in
            guard originalImages.indices.contains(index), originalImages[index]["original"].bool,
                  case .object(var value) = image else { return image }
            value["original"] = true; return .object(value)
        })
        let stamp = epoch, selection = opening
        edit.submitting = true; edit.error = nil; historyActionBusy = true; sending = true
        defer {
            edit.submitting = false
            if epoch == stamp { historyActionBusy = false; sending = false }
        }
        if !edit.prepared {
            guard canEdit(edit.message), let current = live.item(for: edit.message),
                  current["seq"] == edit.original["seq"], current["source_id"] == edit.original["source_id"] else {
                throw NativeError.invalid("原消息已改变，请重新打开消息后编辑；本次修改仍保留在这里")
            }
            _ = try await remote.call("thread/edit-message", params: ["thread_id": .string(edit.threadID),
                "turn_id": .string(edit.message.turnID), "item_id": .string(edit.message.itemID)], snapshotTag: selection.uuidString)
            edit.prepared = true
        }
        guard epoch == stamp, opening == selection, self.remote === remote else { throw CancellationError() }
        _ = try await remote.call("turn/start", params: .object(params))
        historyEdits[edit.hostID + ":" + edit.threadID] = nil
        if epoch == stamp, opening == selection { showingHistoryEdit = false; Haptics.tap() }
    }
    func attachmentThumbnail(_ message: ChatMessage, index: Int) async throws -> LoadedAttachment {
        #if DEBUG
        if NativeUIFixture.enabled { return try await fixtureAttachment(message, index: index) }
        #endif
        guard connected, let remote, let live, message.attachments.indices.contains(index),
              live.messages.contains(where: { $0.id == message.id }) else { throw CancellationError() }
        let stamp = epoch, selected = opening
        let result = try await remote.readAttachment(message.attachments[index], threadID: live.id, messageID: message.id, preview: true)
        guard epoch == stamp, opening == selected else { throw CancellationError() }
        return result
    }
    func previewAttachment(_ message: ChatMessage, index: Int) async throws {
        #if DEBUG
        if NativeUIFixture.enabled { attachmentPreview = try await fixtureAttachment(message, index: index); return }
        #endif
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
    #if DEBUG
    private func fixtureAttachment(_ message: ChatMessage, index: Int) async throws -> LoadedAttachment {
        try await readMessageAttachment(message.attachments[index], scopeID: "local-ui-fixture", messageID: message.id) { _, _ in
            throw NativeError.invalid("Fixture must not access the network")
        }
    }
    #endif
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
    enum PendingAction { case remove, resume, steer, requeue }
    func pendingAction(_ message: PendingMessage, action: PendingAction) async throws {
        guard connected, let remote, let live, live.id == message.value["thread_id"].string, !live.readOnly,
              live.pending.contains(where: { $0.id == message.id }) else { throw NativeError.invalid("消息状态已改变，请重新打开会话") }
        switch action {
        case .resume:
            guard message.held, !live.running else { throw NativeError.invalid("请等待当前处理结束") }
            _ = try await remote.call("turn/steer", params: message.resumeParams)
        case .steer:
            guard !message.held, message.origin == "queue", live.running,
                  let turnID = live.turns.last(where: { $0["status"].string == "in_progress" })?["id"].string,
                  case .object(var params) = message.resumeParams else { throw NativeError.invalid("当前回复已结束") }
            // The host moves this same message atomically; never remove then resend it.
            params["expected_turn_id"] = .string(turnID)
            _ = try await remote.call("turn/steer", params: .object(params))
        case .requeue:
            guard !message.held, message.origin == "steer" else { throw NativeError.invalid("消息状态已改变") }
            _ = try await remote.call("turn/requeue", params: ["thread_id": .string(live.id), "steer_id": .string(message.id)])
        case .remove:
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
