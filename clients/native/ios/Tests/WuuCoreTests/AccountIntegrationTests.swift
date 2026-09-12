import Foundation
import XCTest
@testable import WuuCore

private final class AccountFixture {
    let process = Process(), input = Pipe(), output = Pipe()
    private let exited = DispatchSemaphore(value: 0)
    var seed: JSONValue = .null
    init(github: Bool = false) throws {
        guard let executable = ProcessInfo.processInfo.environment["WUU_NATIVE_TESTACCOUNT"] else {
            throw XCTSkip("Set WUU_NATIVE_TESTACCOUNT and use clients/native/with-postgres.sh")
        }
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = github ? ["-github"] : []
        process.standardInput = input; process.standardOutput = output
        let exited = self.exited
        process.terminationHandler = { _ in exited.signal() }
        try process.run()
        try output.fileHandleForWriting.close()
        try input.fileHandleForReading.close()
        seed = try JSONDecoder().decode(JSONValue.self, from: Data(readLine().utf8))
    }
    func readLine() throws -> String {
        var bytes = Data()
        while let byte = try output.fileHandleForReading.read(upToCount: 1), !byte.isEmpty {
            if byte == Data([10]) { return String(decoding: bytes, as: UTF8.self) }
            bytes.append(byte)
        }
        throw NativeError.invalid("Account fixture exited")
    }
    func command(_ command: String) throws { try input.fileHandleForWriting.write(contentsOf: Data((command + "\n").utf8)) }
    func close() {
        try? command("quit")
        // waitUntilExit uses the calling thread's run loop; async tests may resume
        // on another worker after Process.run, leaving that wait stuck after exit.
        if exited.wait(timeout: .now() + 10) != .success {
            if process.isRunning { process.terminate() }
            XCTFail("Account fixture did not exit")
        }
    }
    var api: AccountAPI { get throws { try AccountAPI(server: seed["server"].string!) } }
    func login() async throws -> AccountSession {
        try await api.login(username: seed["username"].string!, password: seed["password"].string!, name: "iOS test", identity: DeviceIdentity())
    }
}

final class AccountIntegrationTests: XCTestCase {
    private func unauthorized(_ operation: () async throws -> Void) async throws {
        do { try await operation(); XCTFail("Revoked or incorrect credentials were accepted") }
        catch NativeError.http(401, _) {}
    }
    func testGitHubBrowserReturnRestorationCancellationAndEnrollment() async throws {
        let fixture = try AccountFixture(github: true); defer { fixture.close() }
        let api = try fixture.api
        let config: AccountConfiguration = try await api.request("/config"); XCTAssertTrue(config.github)
        let cancelled = try await api.startGitHub()
        try await api.cancelGitHub(cancelled)
        try await unauthorized { _ = try await api.pollGitHub(cancelled) }
        let denied = try await api.startGitHub()
        try fixture.command("deny " + denied.requestID); XCTAssertEqual(try fixture.readLine(), "browser-returned")
        do { _ = try await api.pollGitHub(denied); XCTFail("Browser denial was ignored") } catch NativeError.http(400, _) {}
        try await api.cancelGitHub(denied)

        let started = try await api.startGitHub()
        let pending = try JSONDecoder().decode(GitHubPending.self, from: JSONEncoder().encode(started))
        let poll = try await api.pollGitHub(pending); XCTAssertEqual(poll.status, "pending")
        try await unauthorized {
            let _: GitHubPoll = try await api.request("/github/poll", body: ["request_id": pending.requestID, "verifier": Data(repeating: 1, count: 32).base64URL])
        }
        try fixture.command("authorize " + pending.requestID); XCTAssertEqual(try fixture.readLine(), "browser-returned")
        let authorized = try await api.pollGitHub(pending)
        let user = try XCTUnwrap(authorized.username), identity = try DeviceIdentity()
        XCTAssertEqual(authorized.status, "authorized")
        let session = try await api.completeGitHub(pending, username: user, name: "test", identity: identity)
        let retried = try await api.completeGitHub(pending, username: user, name: "test", identity: identity)
        XCTAssertEqual(session, retried)
        let directory: DeviceDirectory = try await api.request("/devices", token: session.token)
        XCTAssertEqual(directory.auth_method, "github"); XCTAssertEqual(directory.devices.first?.pub, session.pub)
        try await unauthorized { _ = try await api.completeGitHub(pending, username: user, name: "other", identity: DeviceIdentity()) }
        try await api.logout(token: session.token)
        try await unauthorized { _ = try await api.completeGitHub(pending, username: user, name: "test", identity: identity) }
    }
    func testRegisterPasswordRecoveryAndServerLogout() async throws {
        let fixture = try AccountFixture(); defer { fixture.close() }
        let api = try fixture.api
        let config: AccountConfiguration = try await api.request("/config")
        XCTAssertTrue(config.registration); XCTAssertFalse(config.github)
        let identity = try DeviceIdentity()
        let session = try await api.login(username: " ALICE ", password: "initial-password", name: "test", identity: identity, register: true)
        let other = try await api.login(username: "alice", password: "initial-password", name: "other", identity: DeviceIdentity())
        try await unauthorized { _ = try await api.resetPassword(username: "alice", secret: "wrong-password", password: "changed-password", token: session.token) }
        let directory: DeviceDirectory = try await api.request("/devices", token: session.token)
        XCTAssertEqual(directory.devices.count, 2)
        let recovery = try await api.resetPassword(username: "alice", secret: "initial-password", password: "changed-password", token: session.token)
        XCTAssertNotEqual(recovery, session.recovery)
        for token in [session.token, other.token] {
            try await unauthorized { let _: DeviceDirectory = try await api.request("/devices", token: token) }
        }
        try await unauthorized { _ = try await api.resetPassword(username: "alice", secret: session.recovery!, password: "recovered-password") }
        let next = try await api.resetPassword(username: "alice", secret: recovery, password: "recovered-password")
        XCTAssertNotEqual(next, recovery)
        try await unauthorized { _ = try await api.login(username: "alice", password: "changed-password", name: "test", identity: identity) }
        let restored = try await api.login(username: "alice", password: "recovered-password", name: "test", identity: identity)
        try await api.logout(token: restored.token)
        try await unauthorized { let _: DeviceDirectory = try await api.request("/devices", token: restored.token) }
        try await api.logout(token: restored.token)
    }

    func testPushRegistrationRotationDisableAndLoginIsolation() async throws {
        let fixture = try AccountFixture(); defer { fixture.close() }
        let api = try fixture.api, session = try await fixture.login()
        let config: AccountConfiguration = try await api.request("/config")
        XCTAssertTrue(config.push_platforms?.contains("ios") == true)
        let consent = try JSONDecoder().decode(PushConsent.self, from: JSONEncoder().encode(PushConsent(session: session, enabled: true)))
        XCTAssertTrue(consent.matches(session))
        try await api.setPush(token: session.token, platform: "ios", deviceToken: "simulated-apns-token")
        let first = try await api.pushStatus(token: session.token)
        XCTAssertTrue(first.enabled); XCTAssertEqual(first.platform, "ios")
        let other = try await api.login(username: session.username, password: "native-test-password", name: "other", identity: DeviceIdentity())
        XCTAssertFalse(consent.matches(other))
        let untouched = try await api.pushStatus(token: other.token); XCTAssertFalse(untouched.enabled)
        try await api.setPush(token: session.token, platform: "ios", deviceToken: "rotated-apns-token")
        try await api.setPush(token: session.token, platform: "ios", deviceToken: nil)
        let disabled = try await api.pushStatus(token: session.token); XCTAssertFalse(disabled.enabled)
        try await api.setPush(token: session.token, platform: "ios", deviceToken: "simulated-apns-token")
        try await api.logout(token: session.token)
        try await unauthorized { try await api.setPush(token: session.token, platform: "ios", deviceToken: "late-registration") }
        let relogin = try await api.login(username: session.username, password: "native-test-password", name: "test", identity: DeviceIdentity(seed: Data(base64URL: session.deviceSeed)))
        XCTAssertFalse(consent.matches(relogin))
        let revoked = try await api.pushStatus(token: relogin.token); XCTAssertFalse(revoked.enabled)
    }

    func testHistoryDiskRestorePaginationDeletionAndLateResponse() async throws {
        let fixture = try AccountFixture(); defer { fixture.close() }
        let api = try fixture.api, session = try await fixture.login()
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let host = fixture.seed["host"].string!, token = fixture.seed["hostToken"].string!
        let store = try ConversationHistory(account: session, host: host, directory: directory)
        let initial = try await store.sync(); XCTAssertFalse(initial.enabled)
        try await store.setEnabled(true)
        let enabled = try await store.sync()
        func upload(_ id: String, _ text: String, revision: String = "0", deleted: Bool = false) async throws -> HistoryEntry {
            try await api.request("/history/thread", token: token, body: ["generation": enabled.generation, "expected": revision, "deleted": deleted,
                "thread": ["id": id, "title": id, "updated_at": "2026-09-11T00:00:00Z", "messages": [["id": "m", "turn_id": "t", "role": "user", "text": text]]]])
        }
        let entry = try await upload("chosen", "offline text")
        // A full page plus one item exercises the server's actual continuation boundary.
        for index in 0..<100 { _ = try await upload("page-\(index)", "text") }
        async let firstPage = store.sync()
        async let secondPage = store.sync()
        let pages = try await [firstPage, secondPage]
        for page in pages { XCTAssertEqual(page.entries.count, 101) }
        let body = try await store.thread("chosen"); XCTAssertEqual(body.messages.first?.text, "offline text")
        try await store.invalidate(removeCache: false)
        let restored = try ConversationHistory(account: session, host: host, directory: directory)
        let cached = await restored.current(); XCTAssertEqual(cached.bodies["chosen"]?.thread.messages.first?.text, "offline text")
        let changed = try await upload("chosen", "edited text", revision: entry.revision)
        _ = try await restored.sync()
        try fixture.command("pause-history"); XCTAssertEqual(try fixture.readLine(), "paused")
        let delayed = Task { try await restored.thread("chosen") }
        XCTAssertEqual(try fixture.readLine(), "blocked")
        _ = try await upload("chosen", "edited text", revision: changed.revision, deleted: true)
        let deleted = try await restored.sync(); XCTAssertNil(deleted.entries["chosen"])
        try await restored.invalidate(removeCache: true)
        try fixture.command("release-history")
        do { _ = try await delayed.value; XCTFail("Late history body restored removed cache") } catch is CancellationError {}
        XCTAssertTrue(try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil).isEmpty)
        let current = try ConversationHistory(account: session, host: host, directory: directory)
        _ = try await current.sync()
        try await current.setEnabled(false)
        let disabled = try await current.sync(); XCTAssertFalse(disabled.enabled); XCTAssertTrue(disabled.entries.isEmpty)
        try await current.invalidate(removeCache: false)
        try ConversationHistory.prune(account: session, hosts: [], directory: directory)
        XCTAssertTrue(try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil).isEmpty)
        let stranger = try await api.login(username: "stranger", password: "stranger-password", name: "test", identity: DeviceIdentity(), register: true)
        try await unauthorized { let _: HistoryPage = try await api.request("/history", token: stranger.token, query: ["host": host]) }
        let _: JSONValue = try await api.request("/devices/" + host, token: session.token, method: "DELETE")
        try await unauthorized { let _: HistoryPage = try await api.request("/history", token: session.token, query: ["host": host]) }
    }
}
