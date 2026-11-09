import SwiftUI
import WuuCore

/// Computers this phone can control: direct pairs stored on the phone and the account's hosts.
struct ComputersView: View {
    @Bindable var model: AppModel
    var addComputer: () -> Void
    @State private var accountSettings = false
    @State private var signingIn = false
    @State private var forgetTarget: PairedComputer?
    private var accountHosts: [AccountDevice] {
        model.devices.filter { $0.role == "host" }.sorted {
            $0.online != $1.online ? $0.online : $0.name.localizedStandardCompare($1.name) == .orderedAscending
        }
    }
    var body: some View {
        NavigationStack {
            Group {
                if model.pairedComputers.isEmpty && accountHosts.isEmpty {
                    ContentUnavailableView {
                        Label("还没有电脑", systemImage: "desktopcomputer")
                    } description: {
                        Text(model.account == nil ? "在电脑上的 Wuu 生成配对链接，或使用账号登录。" : "在电脑上的 Wuu 生成配对链接，或用同一账号登录电脑。")
                    } actions: {
                        Button("连接电脑", action: addComputer).mobilePrimaryAction()
                        if model.account == nil { Button("使用账号登录") { signingIn = true } }
                    }
                } else { computers }
            }
            .navigationTitle("电脑")
            .toolbar {
                if model.account != nil {
                    ToolbarItem(placement: .topBarLeading) {
                        Button { accountSettings = true } label: { Image(systemName: "person.crop.circle").font(.system(size: 19)) }
                            .accessibilityLabel("账号设置")
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button(action: addComputer) { Image(systemName: "plus").font(.system(size: 19)) }.accessibilityLabel("连接电脑")
                }
            }
            .confirmationDialog("从这台手机移除「\(forgetTarget?.hostName ?? "")」？",
                                isPresented: Binding(get: { forgetTarget != nil }, set: { if !$0 { forgetTarget = nil } }),
                                titleVisibility: .visible, presenting: forgetTarget) { computer in
                Button("移除", role: .destructive) { model.perform { try await model.forgetComputer(computer) } }
            } message: { _ in
                Text("手机将不再保存这台电脑的配对。电脑上的授权需在电脑的 Wuu 设置中撤销。")
            }
            .sheet(isPresented: $accountSettings) { AccountSettingsView(model: model).modelErrorAlert(model) }
            .sheet(isPresented: $signingIn) { AccountSignInView(model: model) }
            // A GitHub login resumed after relaunch continues in the sign-in sheet.
            .onChange(of: model.githubURL != nil, initial: true) { _, pending in if pending { signingIn = true } }
        }
    }
    private var computers: some View {
        List {
            if !model.pairedComputers.isEmpty {
                Section {
                    ForEach(model.pairedComputers) { computer in
                        Button { model.perform { try await model.selectComputer(computer) } } label: {
                            ComputerRow(name: computer.hostName, detail: "直接配对")
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            Button("移除", systemImage: "trash", role: .destructive) { forgetTarget = computer }
                        }
                        .contextMenu {
                            Button("从这台手机移除", systemImage: "trash", role: .destructive) { forgetTarget = computer }
                        }
                    }
                } header: { if model.account != nil { Text("直接配对") } }
            }
            if let account = model.account, !accountHosts.isEmpty {
                Section {
                    ForEach(accountHosts) { device in
                        Button { model.perform { try await model.selectHost(device) } } label: {
                            // A cached directory never restores presence as live.
                            ComputerRow(name: device.name, detail: model.directoryCached ? "状态未知" : device.online ? "在线" : "离线",
                                        online: !model.directoryCached && device.online)
                        }
                    }
                } header: { if !model.pairedComputers.isEmpty { Text(account.username) } }
            }
            if model.account == nil {
                Section { Button("使用账号登录") { signingIn = true } }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            guard model.account != nil else { return }
            do { try await model.loadDevices() } catch is CancellationError {} catch { model.error = error.localizedDescription }
        }
    }
}

private struct ComputerRow: View {
    let name: String
    let detail: String
    var online = false
    var body: some View {
        HStack(spacing: 14) {
            Image(systemName: "desktopcomputer").font(.system(size: 22)).foregroundStyle(.secondary)
                .frame(width: 28).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(name.isEmpty ? "电脑" : name).foregroundStyle(.primary).lineLimit(2)
                HStack(spacing: 6) {
                    if online { Circle().fill(Color.green).frame(width: 6, height: 6).accessibilityHidden(true) }
                    Text(detail)
                }.font(.footnote).foregroundStyle(.secondary)
            }
            Spacer(minLength: 8)
            Image(systemName: "chevron.right").font(.footnote.weight(.semibold)).foregroundStyle(.tertiary)
                .accessibilityHidden(true)
        }
        .padding(.vertical, 6).contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}
