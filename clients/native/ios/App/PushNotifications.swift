import SwiftUI
import UIKit
import UserNotifications
import WuuCore

@MainActor final class PushDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate, ObservableObject {
    @Published var openedHost: String?
    private var pending: CheckedContinuation<String, Error>?
    private var deadline: Task<Void, Never>?
    static weak var shared: PushDelegate?

    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        Self.shared = self
        UNUserNotificationCenter.current().delegate = self
        return true
    }
    func token() async throws -> String {
        guard pending == nil else { throw NativeError.invalid("通知注册正在进行") }
        return try await withCheckedThrowingContinuation { continuation in
            pending = continuation
            deadline = Task {
                do { try await Task.sleep(for: .seconds(20)) } catch { return }
                finish(.failure(NativeError.invalid("Apple 通知注册超时，请重试")))
            }
            UIApplication.shared.registerForRemoteNotifications()
        }
    }
    private func finish(_ result: Result<String, Error>) {
        deadline?.cancel(); deadline = nil
        let continuation = pending; pending = nil
        continuation?.resume(with: result)
    }
    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        finish(.success(deviceToken.map { String(format: "%02x", $0) }.joined()))
    }
    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) { finish(.failure(error)) }
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        let host = response.notification.request.content.userInfo["host"] as? String
        Task { @MainActor in self.openedHost = host }
        completionHandler()
    }
}

@MainActor @Observable final class PushNotifications {
    private(set) var enabled: Bool?
    private(set) var supported = false
    private(set) var busy = false
    var error: String?
    private var session: AccountSession?
    private let vault = KeychainStore()
    var configured: Bool { (Bundle.main.object(forInfoDictionaryKey: "WuuPushEnabled") as? NSString)?.boolValue == true || Bundle.main.object(forInfoDictionaryKey: "WuuPushEnabled") as? Bool == true }

    func bind(_ value: AccountSession?) {
        guard session?.token != value?.token || session?.server != value?.server else { session = value; return }
        session = value; enabled = nil; supported = false; error = nil
        if value == nil {
            UIApplication.shared.unregisterForRemoteNotifications()
            UNUserNotificationCenter.current().removeAllDeliveredNotifications()
            UNUserNotificationCenter.current().removeAllPendingNotificationRequests()
            try? vault.delete("push-consent")
        }
    }
    func mayOpenNotification() -> Bool {
        guard let session, let consent = try? vault.load("push-consent", as: PushConsent.self) else { return false }
        return consent.matches(session) && consent.enabled
    }
    // Serialize mutations, including foreground retries after an uncertain DELETE.
    func refresh() async {
        guard !busy, let session else { return }
        busy = true; error = nil; defer { busy = false }
        do {
            let api = try AccountAPI(server: session.server)
            let config: AccountConfiguration = try await api.request("/config")
            let status = try await api.pushStatus(token: session.token)
            guard self.session == session else { return }
            supported = config.push_platforms?.contains("ios") == true
            enabled = status.enabled
            if let consent = try vault.load("push-consent", as: PushConsent.self), consent.matches(session) {
                if !consent.enabled {
                    if status.enabled { try await api.setPush(token: session.token, platform: "ios", deviceToken: nil) }
                    if self.session == session { enabled = false }
                } else if configured && supported {
                    try await register(session, ask: false)
                }
            }
        } catch { if self.session == session { self.error = error.localizedDescription } }
    }
    func setEnabled(_ value: Bool) async {
        guard !busy, let session else { return }
        busy = true; error = nil; defer { busy = false }
        do {
            if value {
                guard configured && supported else { throw NativeError.invalid("当前 App 或服务器未配置通知") }
                try vault.save(PushConsent(session: session, enabled: true), key: "push-consent")
                try await register(session, ask: true)
            } else {
                try vault.save(PushConsent(session: session, enabled: false), key: "push-consent")
                UIApplication.shared.unregisterForRemoteNotifications()
                UNUserNotificationCenter.current().removeAllDeliveredNotifications()
                try await AccountAPI(server: session.server).setPush(token: session.token, platform: "ios", deviceToken: nil)
                if self.session == session { enabled = false }
            }
        } catch { if self.session == session { self.error = error.localizedDescription } }
    }
    private func register(_ session: AccountSession, ask: Bool) async throws {
        let center = UNUserNotificationCenter.current()
        if ask { _ = try await center.requestAuthorization(options: [.alert, .sound, .badge]) }
        let settings = await center.notificationSettings()
        guard self.session == session else { return }
        guard settings.authorizationStatus == .authorized || settings.authorizationStatus == .provisional else {
            throw NativeError.invalid("系统通知未允许，请在系统设置中开启")
        }
        guard let delegate = PushDelegate.shared else { throw NativeError.invalid("通知服务尚未就绪") }
        let token = try await delegate.token()
        guard self.session == session else { return }
        try await AccountAPI(server: session.server).setPush(token: session.token, platform: "ios", deviceToken: token)
        if self.session == session { enabled = true }
    }
}

struct PushSettingsView: View {
    @Bindable var push: PushNotifications
    @Environment(\.openURL) private var openURL
    var body: some View {
        Form {
            Section {
                Text(push.enabled.map { $0 ? "服务器通知已开启" : "服务器通知未开启" } ?? "通知状态尚未确认")
                if !push.configured { Text("此构建未配置 Apple 推送。") }
                else if !push.supported { Text("此账号服务器未配置 Apple 推送。") }
                Button("开启或重新注册") { Task { await push.setEnabled(true) } }.disabled(push.busy || !push.configured || !push.supported)
                Button("关闭通知", role: .destructive) { Task { await push.setEnabled(false) } }.disabled(push.busy)
            } footer: { Text("任务完成或需要输入时提醒。通知只包含通用提示和电脑标识，不包含对话内容。关闭失败时会在下次打开 App 重试。") }
            if push.busy { ProgressView() }
            if let error = push.error { Text(error).foregroundStyle(.red) }
            Button("系统通知设置") { if let url = URL(string: UIApplication.openNotificationSettingsURLString) { openURL(url) } }
        }.navigationTitle("通知").task { await push.refresh() }
    }
}
