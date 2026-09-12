import SwiftUI
import WuuCore

struct ThreadSettingsView: View {
    let model: AppModel
    let thread: ChatThread
    @Environment(\.dismiss) private var dismiss
    @State private var selection: ThreadSettings
    @State private var providers: [RemoteProvider] = []
    @State private var loading = true
    @State private var saving = false
    @State private var error: String?
    @State private var confirmUnconfined = false
    init(model: AppModel, thread: ChatThread) {
        self.model = model; self.thread = thread; _selection = State(initialValue: thread.settings)
    }
    private var models: [RemoteModel] { providers.first { $0.id == selection.provider }?.models ?? [] }
    private var variants: [String] { models.first { $0.id == selection.model }?.variants ?? [] }
    private var editable: Bool {
        model.connected && model.live?.id == thread.id && model.live?.running == false &&
        model.live?.readOnly == false && model.live?.archived == false && ["", "wuu"].contains(thread.engine)
    }
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("提供商", selection: $selection.provider) {
                        if !providers.contains(where: { $0.id == selection.provider }) { Text(selection.provider).tag(selection.provider) }
                        ForEach(providers) { Text($0.id).tag($0.id) }
                    }.onChange(of: selection.provider) { _, _ in selection.model = models.first?.id ?? ""; selection.variant = "" }
                    Picker("模型", selection: $selection.model) {
                        if !models.contains(where: { $0.id == selection.model }) { Text(selection.model).tag(selection.model) }
                        ForEach(models) { Text($0.name).tag($0.id) }
                    }.onChange(of: selection.model) { _, _ in selection.variant = "" }
                    if !variants.isEmpty || !selection.variant.isEmpty {
                        Picker("推理配置", selection: $selection.variant) {
                            Text("模型默认").tag("")
                            if !selection.variant.isEmpty && !variants.contains(selection.variant) { Text(selection.variant).tag(selection.variant) }
                            ForEach(variants, id: \.self) { Text($0).tag($0) }
                        }
                    }
                } footer: { Text("仅影响当前会话。模型连接和凭据在电脑上管理。") }
                Section {
                    Picker("执行权限", selection: $selection.permission) {
                        Text("标准").tag("standard")
                        Text("只读").tag("read_only")
                        Text("不受限").tag("unconfined")
                    }
                } footer: {
                    Text(selection.permission == "unconfined" ? "允许任务访问电脑上当前用户可访问的文件和网络。" :
                        selection.permission == "read_only" ? "限制为读取，不允许修改文件。" : "使用电脑上的工作区和网络访问边界。")
                }
                if loading { ProgressView() }
                if !editable { Text("连接电脑并等待任务结束后可修改。其他执行引擎的设置请在电脑上管理。").foregroundStyle(.secondary) }
                if let error { Text(error).foregroundStyle(.red).textSelection(.enabled) }
            }.disabled(saving)
                .navigationTitle("会话设置").navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() }.disabled(saving) }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("保存") {
                            if selection.permission == "unconfined" && thread.settings.permission != "unconfined" { confirmUnconfined = true }
                            else { save() }
                        }.disabled(!editable || saving || loading || selection.provider.isEmpty || selection.model.isEmpty || selection == thread.settings)
                    }
                }
                .confirmationDialog("允许此会话不受限地访问电脑？", isPresented: $confirmUnconfined, titleVisibility: .visible) {
                    Button("允许不受限访问", role: .destructive) { save() }
                } message: { Text("任务将不再受工作区文件和网络边界限制。仅对信任的任务开启。") }
                .task {
                    defer { loading = false }
                    do { providers = try await model.loadModelChoices(threadID: thread.id) }
                    catch is CancellationError {} catch { self.error = error.localizedDescription }
                }
        }
    }
    private func save() {
        saving = true; error = nil
        Task {
            defer { saving = false }
            do { try await model.updateSettings(selection, threadID: thread.id); dismiss() }
            catch is CancellationError { dismiss() }
            catch { self.error = error.localizedDescription }
        }
    }
}
