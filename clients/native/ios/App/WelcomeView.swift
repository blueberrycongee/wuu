import SwiftUI
import WuuCore

/// First run: pair a computer directly, or sign in to an account that already lists computers.
struct WelcomeView: View {
    @Bindable var model: AppModel
    var openPairing: () -> Void
    @State private var signingIn = false
    var body: some View {
        GeometryReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    Text("wuu").font(.system(size: 64, weight: .black, design: .rounded))
                        .padding(.bottom, 12).accessibilityHidden(true)
                    Text("在手机上继续电脑上的工作").font(.title2).accessibilityAddTraits(.isHeader)
                    Text("任务在你的电脑上运行。连接后，可以在手机上新建会话、发送消息和跟进进度。")
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 28).padding(.vertical, 24)
                .frame(maxWidth: .infinity, minHeight: proxy.size.height, alignment: .bottomLeading)
            }.scrollBounceBehavior(.basedOnSize)
        }
        .safeAreaInset(edge: .bottom) {
            VStack(spacing: 8) {
                Button(action: openPairing) {
                    Text("连接电脑").frame(maxWidth: .infinity, minHeight: 44)
                }.mobilePrimaryAction().controlSize(.large)
                Button("使用账号登录") { signingIn = true }
                    .frame(maxWidth: .infinity, minHeight: 44)
            }.padding(.horizontal, 28).padding(.top, 12).padding(.bottom, 8)
                .background(Color(uiColor: .systemBackground))
        }
        .sheet(isPresented: $signingIn) { AccountSignInView(model: model) }
        // A GitHub login resumed after relaunch continues in the sign-in sheet.
        .onChange(of: model.githubURL != nil, initial: true) { _, pending in if pending { signingIn = true } }
    }
}

/// Account server, GitHub, password, registration and recovery sign-in.
struct AccountSignInView: View {
    @Bindable var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @AppStorage("server") private var server = ""
    @State private var serverDraft = ""
    @State private var username = ""
    @State private var password = ""
    @State private var register = false
    @State private var passwordVisible = false
    @State private var loadingConfiguration = false
    @State private var failure: String?
    private var serverPending: Bool { server.isEmpty || serverDraft != server }
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("https://你的服务器", text: $serverDraft)
                        .keyboardType(.URL).textContentType(.URL)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .submitLabel(.continue).onSubmit(useServer)
                        .disabled(model.busy || model.githubURL != nil)
                        .accessibilityLabel("账号服务器")
                    if serverPending {
                        Button("继续", action: useServer)
                            .disabled(serverDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.busy)
                    }
                } header: { Text("账号服务器") } footer: { Text("使用与你的电脑相同的 Wuu 服务器。") }
                if !serverPending { methods }
                if let failure {
                    Section { Text(failure).foregroundStyle(.red).textSelection(.enabled) }
                }
            }
            .navigationTitle(register ? "注册账号" : "使用账号登录").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("取消") { dismiss() }.disabled(model.resettingPassword)
                }
            }
            .onAppear { if serverDraft.isEmpty { serverDraft = server } }
            .task(id: server) {
                guard !server.isEmpty else { return }
                loadingConfiguration = true
                await model.loadConfiguration(server: server)
                loadingConfiguration = false
            }
            .onChange(of: model.account != nil) { _, signedIn in if signedIn { dismiss() } }
            .modelErrorAlert(model)
        }
        .interactiveDismissDisabled(model.resettingPassword)
        // Leaving the sheet abandons a pending GitHub login, as the old login sheet did.
        .onDisappear { if model.account == nil { model.perform { try model.cancelLogin() } } }
    }
    @ViewBuilder private var methods: some View {
        if model.githubURL != nil {
            Section {
                HStack(spacing: 12) { ProgressView(); Text("在浏览器完成 GitHub 登录后返回") }
                Button("重新打开浏览器") { if let url = model.githubURL { UIApplication.shared.open(url) } }
                Button("取消登录", role: .destructive) { model.perform { try model.cancelLogin() } }
            }
        } else if let configuration = model.configuration {
            if configuration.github && !register {
                Section {
                    Button { attempt { try await model.github(server: server) } } label: {
                        Text("使用 GitHub 继续").frame(maxWidth: .infinity, minHeight: 44)
                    }.mobilePrimaryAction().disabled(model.busy)
                        .listRowInsets(EdgeInsets()).listRowBackground(Color.clear)
                    if !passwordVisible {
                        Button("使用密码登录") { passwordVisible = true }
                            .frame(maxWidth: .infinity, minHeight: 44).listRowBackground(Color.clear)
                    }
                }
            }
            if passwordVisible || register || !configuration.github {
                Section {
                    TextField("用户名", text: $username).textContentType(.username)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    SecureField("密码", text: $password).textContentType(register ? .newPassword : .password)
                        .submitLabel(.go).onSubmit(login)
                    Button(register ? "注册并登录" : "登录", action: login)
                        .disabled(model.busy || username.isEmpty || password.isEmpty)
                }
                Section {
                    if configuration.registration {
                        Button(register ? "已有账号，登录" : "注册账号") { register.toggle(); failure = nil }
                    }
                    if !register {
                        NavigationLink("忘记密码？使用恢复密钥") { PasswordResetView(model: model, server: server, changing: false) }
                    }
                }
            }
        } else if loadingConfiguration {
            Section { HStack(spacing: 12) { ProgressView(); Text("正在读取服务器设置…").foregroundStyle(.secondary) } }
        } else {
            Section { Button("重新读取服务器设置") { Task { await model.loadConfiguration(server: server) } } }
        }
    }
    private func useServer() {
        do {
            let origin = try AccountAPI.validateOrigin(serverDraft.trimmingCharacters(in: .whitespacesAndNewlines)).absoluteString
            if origin != server { try model.cancelLogin(); register = false; server = origin }
            serverDraft = origin; failure = nil
        } catch { failure = error.localizedDescription }
    }
    private func login() {
        guard !model.busy, !username.isEmpty, !password.isEmpty else { return }
        let user = username, secret = password, registering = register
        attempt {
            try await model.login(server: server, username: user, password: secret, register: registering)
            password = ""
        }
    }
    private func attempt(_ operation: @escaping @MainActor () async throws -> Void) {
        failure = nil
        Task {
            do { try await operation() }
            catch is CancellationError {}
            catch { failure = error.localizedDescription }
        }
    }
}
