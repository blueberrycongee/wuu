import SwiftUI

struct RecoveryView: View {
    @Bindable var model: AppModel
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    if model.resetRecovery != nil {
                        Text("密码已更新，所有设备已退出。旧恢复密钥已失效，请保存新密钥，然后重新登录。")
                    }
                    Text("请将下面的密钥保存到密码管理器。它可用于重置密码，切勿分享。确认后此页面不再展示。")
                    Text(model.recovery ?? "").font(.body.monospaced()).textSelection(.enabled)
                    Button("我已安全保存") { model.perform { try model.acknowledgeRecovery() } }.buttonStyle(.borderedProminent)
                }.padding()
            }.navigationTitle("保存账号恢复密钥")
        }
    }
}

struct PasswordResetView: View {
    @Bindable var model: AppModel
    let server: String
    let changing: Bool
    @State private var username = ""
    @State private var secret = ""
    @State private var password = ""
    @State private var confirmation = ""
    @State private var confirm = false
    var body: some View {
        Form {
            if !changing {
                TextField("用户名", text: $username).textInputAutocapitalization(.never).autocorrectionDisabled().textContentType(.username)
            }
            SecureField(changing ? "当前密码" : "恢复密钥", text: $secret).textContentType(changing ? .password : nil)
            SecureField("新密码（至少 12 字节）", text: $password).textContentType(.newPassword)
            SecureField("再次输入新密码", text: $confirmation).textContentType(.newPassword)
            Text("更新密码会撤销所有电脑和手机的登录，并生成新的恢复密钥。设备需要重新登录才能继续使用。")
            Button("更新密码", role: .destructive) { confirm = true }
                .disabled(model.busy || secret.isEmpty || (!changing && username.isEmpty) || password.utf8.count < 12 || password.utf8.count > 1024 || password != confirmation)
        }.navigationTitle(changing ? "修改密码" : "找回账号")
            .confirmationDialog("更新密码并退出所有设备？", isPresented: $confirm, titleVisibility: .visible) {
                Button("确认更新", role: .destructive) {
                    model.perform { try await model.resetPassword(server: server, username: username, secret: secret, password: password, changing: changing) }
                }
            }
    }
}
