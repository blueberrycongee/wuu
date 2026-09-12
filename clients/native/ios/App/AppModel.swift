import SwiftUI
import WuuCore
import CryptoKit

@MainActor @Observable final class AppModel {
    var account: AccountSession?
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
    var historyEnabled = false
    var connectionStatus = ""
    var error: String?
    var workspaces: [JSONValue] = []
    var workspace = ""
    var pendingApproval: JSONValue?
    var questions: [JSONValue] = []
    private var questionRevision = 0
    var githubURL: URL?
    private var remote: RemoteConnection?
    private var history: ConversationHistory?
    private var events: Task<Void, Never>?
    private var refresh: Task<Void, Never>?
    private var loginTask: Task<Void, Never>?
    private var epoch = UUID()
    private var authEpoch = UUID()
    private var opening = UUID()
    private let vault = KeychainStore()
    private var cacheDirectory: URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("WuuHistory")
    }
    var messages: [ChatMessage] {
        live?.messages ?? saved?.messages.map { ChatMessage(id: $0.stableID, role: $0.role, text: $0.text) } ?? []
    }
    init() {
        do { account = try vault.load("session", as: AccountSession.self) }
        catch { self.error = error.localizedDescription }
    }
    func perform(_ operation: @escaping @MainActor () async throws -> Void) {
        let stamp = epoch
        Task { do { try await operation() } catch is CancellationError {} catch { if epoch == stamp { self.report(error) } } }
    }
    private func report(_ failure: Error) {
        error = failure.localizedDescription
        if case NativeError.http(401, _) = failure { perform { try await self.logout() } }
    }
    private func identity(server: String, username: String) throws -> DeviceIdentity {
        let key = "identity-" + Data(SHA256.hash(data: Data((server + "\n" + username).utf8))).base64URL
        if let seed = try vault.load(key, as: String.self) { return try DeviceIdentity(seed: Data(base64URL: seed)) }
        let identity = try DeviceIdentity()
        try vault.save(identity.seed.base64URL, key: key)
        return identity
    }
    private func accept(_ session: AccountSession) async throws {
        try vault.save(session, key: "session")
        try vault.delete("github")
        account = session
        githubURL = nil
        try await loadDevices()
    }
    func revoke(_ device: AccountDevice) async throws {
        guard let session = account else { return }
        guard try Data(base64URL: device.pub).count == 32 else { throw NativeError.invalid("Invalid device identity") }
        let _: JSONValue = try await AccountAPI(server: session.server).request("/devices/" + device.pub, token: session.token, method: "DELETE")
        guard account == session else { return }
        if device.pub == session.pub { try await logout() }
        else { try await loadDevices() }
    }
    func acknowledgeRecovery() throws {
        guard var session = account else { return }
        session.recovery = nil
        try vault.save(session, key: "session")
        account = session
    }
    func login(server: String, username: String, password: String, register: Bool = false) async throws {
        guard !busy else { return }
        busy = true; defer { busy = false }
        loginTask?.cancel()
        authEpoch = UUID(); let stamp = authEpoch
        let api = try AccountAPI(server: server)
        let user = username.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let result = try await api.login(username: user, password: password, name: UIDevice.current.name,
                                        identity: identity(server: api.origin.absoluteString, username: user), register: register)
        guard authEpoch == stamp else { throw CancellationError() }
        try await accept(result)
    }
    func github(server: String) async throws {
        guard !busy else { return }
        busy = true; defer { busy = false }
        authEpoch = UUID(); let stamp = authEpoch
        let pending = try await AccountAPI(server: server).startGitHub()
        guard authEpoch == stamp else { throw CancellationError() }
        try vault.save(pending, key: "github")
        githubURL = pending.authorizationURL
        await UIApplication.shared.open(pending.authorizationURL)
        pollGitHub(pending)
    }
    private func pollGitHub(_ pending: GitHubPending) {
        loginTask?.cancel()
        loginTask = Task {
            do {
                let api = try AccountAPI(server: pending.server)
                while !Task.isCancelled {
                    let result = try await api.pollGitHub(pending)
                    try Task.checkCancellation()
                    if result.status == "authorized", let user = result.username {
                        let session = try await api.completeGitHub(pending, username: user, name: UIDevice.current.name,
                            identity: identity(server: pending.server, username: user))
                        try Task.checkCancellation()
                        try await accept(session)
                        return
                    }
                    guard result.status == "pending" else { throw NativeError.invalid("GitHub 登录未完成，请重新登录") }
                    try await Task.sleep(for: .seconds(2))
                }
            } catch is CancellationError {} catch { report(error) }
        }
    }
    func loadDevices() async throws {
        guard let account else { return }
        let directory: DeviceDirectory = try await AccountAPI(server: account.server).request("/devices", token: account.token)
        guard self.account == account else { return }
        devices = directory.devices
        if let host, !devices.contains(where: { $0.pub == host.pub && $0.role == "host" }) { await leaveHost() }
    }
    func selectHost(_ device: AccountDevice) async throws {
        await leaveHost()
        host = device
        guard let account else { return }
        let store = try ConversationHistory(account: account, host: device.pub, directory: cacheDirectory)
        history = store
        show(await store.current())
        foreground()
    }
    private func show(_ snapshot: HistorySnapshot) {
        entries = snapshot.sortedEntries
        historyEnabled = snapshot.enabled
        if let activeID, let body = snapshot.bodies[activeID] { saved = body.thread }
        else if !snapshot.enabled || !entries.contains(where: { $0.id == activeID }) { saved = nil }
    }
    func foreground() {
        if account == nil {
            if let pending = try? vault.load("github", as: GitHubPending.self), pending.expires > Date() {
                githubURL = pending.authorizationURL
                pollGitHub(pending)
            }
            return
        }
        perform { try await self.loadDevices() }
        guard host != nil else { return }
        refresh?.cancel()
        let stamp = epoch
        refresh = Task {
            while !Task.isCancelled, epoch == stamp {
                do { try await syncHistory() } catch is CancellationError { return } catch {
                    if case NativeError.http(401, _) = error { report(error); return }
                    connectionStatus = error.localizedDescription
                }
                guard !Task.isCancelled, epoch == stamp else { return }
                if !connected && !connecting { await connect() }
                do { try await Task.sleep(for: .seconds(10)) } catch { return }
            }
        }
    }
    func background() async {
        epoch = UUID()
        refresh?.cancel(); refresh = nil
        loginTask?.cancel()
        events?.cancel(); events = nil
        await remote?.disconnect(); remote = nil
        connected = false; connecting = false; pendingApproval = nil
        questions = []; questionRevision += 1
    }
    func leaveHost() async {
        epoch = UUID()
        await background()
        try? await history?.invalidate(removeCache: false)
        history = nil; host = nil; entries = []; threads = []; activeID = nil; live = nil; saved = nil
        workspace = ""; workspaces = []; historyEnabled = false; opening = UUID()
    }
    func logout() async throws {
        authEpoch = UUID(); loginTask?.cancel(); account = nil
        await leaveHost()
        try vault.delete("session")
        try vault.delete("github")
        account = nil; devices = []
        if FileManager.default.fileExists(atPath: cacheDirectory.path) { try FileManager.default.removeItem(at: cacheDirectory) }
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
        try await history?.setEnabled(enabled)
        try await syncHistory()
    }
    func connect() async {
        guard !connecting, let account, let host else { return }
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
                            live = ChatThread(result["thread"], pending: result["pending_user_messages"].array + result["held_user_messages"].array)
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
            if let id = activeID { try await open(id) }
        } catch {
            if epoch == stamp {
                await remote?.disconnect(); remote = nil; connected = false
                if !(error is CancellationError) { connectionStatus = error.localizedDescription }
            }
        }
    }
    func loadThreads() async throws {
        guard connected, let remote else { return }
        let stamp = epoch
        let selected = workspace
        let result = try await remote.call("thread/list", params: ["cwd": .string(selected), "summary_only": true])
        guard epoch == stamp, workspace == selected else { return }
        threads = result["threads"].array.map { ChatThread($0) }
    }
    func open(_ id: String) async throws {
        opening = UUID(); let selection = opening
        activeID = id; live = nil; saved = nil
        let stamp = epoch
        if let history, entries.contains(where: { $0.id == id }) {
            do {
                let body = try await history.thread(id)
                if epoch == stamp, opening == selection { saved = body }
            } catch { if !connected { throw error } }
        }
        if connected, let remote {
            _ = try await remote.call("thread/resume", params: ["session_id": .string(id), "response_only": true], snapshotTag: selection.uuidString)
        }
    }
    func startThread() async throws {
        guard let remote, connected else { throw NativeError.invalid("请先连接电脑") }
        let stamp = epoch; opening = UUID(); let selection = opening
        var params: JSONValue = [:]
        if !workspace.isEmpty { params = ["cwd": .string(workspace)] }
        let result = try await remote.call("thread/start", params: params)
        guard epoch == stamp, opening == selection else { return }
        let thread = ChatThread(result["thread"])
        activeID = thread.id; live = thread; saved = nil
        try await loadThreads()
    }
    func send(_ text: String) async throws {
        guard !busy, let remote, let live, connected, !live.readOnly, !text.isEmpty else { throw NativeError.invalid("当前无法发送") }
        let stamp = epoch
        busy = true; defer { busy = false }
        _ = try await remote.call(live.running ? "turn/queue" : "turn/start", params: [
            "thread_id": .string(live.id), "prompt": .string(text), "images": [], "files": [],
            "client_id": .string(UUID().uuidString)])
        guard epoch == stamp else { throw CancellationError() }
    }
    func stop() async throws {
        guard let remote, let activeID else { return }
        _ = try await remote.call("turn/interrupt", params: ["thread_id": .string(activeID)])
    }
    func pin(_ thread: ChatThread) async throws {
        _ = try await remote?.call("thread/pin", params: ["thread_id": .string(thread.id), "pinned": .bool(!thread.pinned)])
        try await loadThreads()
    }
    func archive(_ thread: ChatThread) async throws {
        _ = try await remote?.call("thread/archive", params: ["thread_id": .string(thread.id), "archived": true])
        if activeID == thread.id { activeID = nil; live = nil; saved = nil }
        try await loadThreads()
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
