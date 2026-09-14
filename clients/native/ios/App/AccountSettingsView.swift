import SwiftUI
import WuuCore

struct AccountSettingsView: View {
    @Bindable var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var revokeTarget: AccountDevice?
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    VStack(alignment: .leading, spacing: 5) {
                        Text(model.account?.username ?? "").font(.headline)
                        Text(model.account?.server ?? "").font(.footnote).foregroundStyle(.secondary)
                    }.padding(.vertical, 4)
                }
                if model.host != nil {
                    Section {
                        Button { dismiss(); Task { await model.leaveHost(); model.foreground() } } label: { Label("切换电脑", systemImage: "desktopcomputer") }
                    }
                }
                Section {
                    NavigationLink { PushSettingsView(push: model.push) } label: { Label("通知", systemImage: "bell") }
                    if model.authMethod == "password" {
                        NavigationLink { PasswordResetView(model: model, server: model.account?.server ?? "", changing: true) } label: { Label("修改密码", systemImage: "lock") }
                    }
                    NavigationLink { LicensesView() } label: { Label("开源许可", systemImage: "doc.text") }
                }
                Section("设备") {
                    ForEach(model.devices) { device in
                        HStack {
                            Label(device.name, systemImage: device.role == "host" ? "desktopcomputer" : "iphone")
                            Spacer()
                            Button { revokeTarget = device } label: { Image(systemName: "minus.circle") }
                                .buttonStyle(.borderless).accessibilityLabel("移除 " + device.name)
                        }.font(.subheadline)
                    }
                }
                Section {
                    Button("退出登录", role: .destructive) { model.perform { try await model.logout(); dismiss() } }.disabled(model.busy)
                }
            }.navigationTitle("账号设置").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .cancellationAction) { Button { dismiss() } label: { Image(systemName: "xmark") }.accessibilityLabel("关闭账号设置") } }
                .confirmationDialog("移除这台设备的账号访问权限？", isPresented: Binding(get: { revokeTarget != nil }, set: { if !$0 { revokeTarget = nil } }), titleVisibility: .visible) {
                    Button("移除设备", role: .destructive) {
                        if let device = revokeTarget { model.perform { try await model.revoke(device) } }
                        revokeTarget = nil
                    }
                }
        }
    }
}
