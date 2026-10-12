import SwiftUI
import WuuCore

struct ThreadSettingsView: View {
    let model: AppModel
    let thread: ChatThread
    @Environment(\.dismiss) private var dismiss
    @State private var selection: ThreadSettings
    @State private var originalSettings: ThreadSettings
    @State private var engine: String
    @State private var originalEngine: String
    @State private var defaults = ThreadSettings([:])
    @State private var providers: [RemoteProvider] = []
    @State private var engines: [RemoteEngine] = []
    @State private var loading = true
    @State private var loaded = false
    @State private var saving = false
    @State private var error: String?
    @State private var confirmUnconfined = false
    init(model: AppModel, thread: ChatThread) {
        self.model = model; self.thread = thread
        let settings = model.settingsSelection(for: thread)
        _selection = State(initialValue: settings); _originalSettings = State(initialValue: settings)
        let engine = thread.engine.isEmpty ? "wuu" : thread.engine
        _engine = State(initialValue: engine); _originalEngine = State(initialValue: engine)
    }
    private var engineInfo: RemoteEngine? { engines.first { $0.id == engine } }
    private var models: [RemoteModel] {
        engine == "wuu" ? providers.first { $0.id == selection.provider }?.models ?? [] : engineInfo?.models ?? []
    }
    private var variants: [String] { models.first { $0.id == selection.model }?.variants ?? [] }
    private var permissions: [String] {
        engine == "wuu" ? ["standard", "read_only", "unconfined"] : engineInfo?.permissions ?? []
    }
    private var isDraft: Bool { model.draftWorkspace != nil && model.live?.id == thread.id }
    private var editable: Bool {
        model.connected && !model.refreshing && !model.sending && model.live?.id == thread.id && model.live?.running == false &&
        model.live?.readOnly == false && model.live?.archived == false
    }
    private var canSave: Bool {
        editable && loaded && !saving && !loading &&
        (!isDraft || engineInfo?.available == true) &&
        (engine != "wuu" || (!selection.provider.isEmpty && !selection.model.isEmpty)) &&
        (selection != originalSettings || engine != originalEngine)
    }
    var body: some View {
        NavigationStack {
            Form {
                Section {
                    if isDraft {
                        Picker("引擎", selection: Binding(get: { engine }, set: { value in
                            engine = value
                            selection = value == originalEngine ? originalSettings : draftSelection(engine: value)
                        })) {
                            if !engines.contains(where: { $0.available && $0.id == engine }) { Text(engineInfo?.name ?? engine).tag(engine) }
                            ForEach(engines.filter(\.available)) { Text($0.name).tag($0.id) }
                        }
                    } else {
                        LabeledContent("引擎", value: engineInfo?.name ?? (engine == "wuu" ? "Wuu" : engine))
                    }
                } footer: {
                    Text(isDraft ? "使用电脑上已启用的引擎。首次发送后无法更换。" : "此会话已绑定引擎。使用其他引擎请新建会话。")
                }
                Section {
                    if engine == "wuu" {
                        Picker("提供商", selection: Binding(get: { selection.provider }, set: {
                            selection.provider = $0; selection.model = models.first?.id ?? ""; selection.variant = ""
                        })) {
                            if !providers.contains(where: { $0.id == selection.provider }) { Text(selection.provider).tag(selection.provider) }
                            ForEach(providers) { Text($0.id).tag($0.id) }
                        }
                    }
                    Picker("模型", selection: Binding(get: { selection.model }, set: {
                        selection.model = $0; selection.variant = ""
                    })) {
                        if engine != "wuu" { Text("引擎默认").tag("") }
                        if !models.contains(where: { $0.id == selection.model }) && (engine == "wuu" || !selection.model.isEmpty) {
                            Text(selection.model).tag(selection.model)
                        }
                        ForEach(models) { Text($0.name).tag($0.id) }
                    }
                    if !variants.isEmpty || !selection.variant.isEmpty {
                        Picker("推理配置", selection: $selection.variant) {
                            Text("模型默认").tag("")
                            if !selection.variant.isEmpty && !variants.contains(selection.variant) { Text(selection.variant).tag(selection.variant) }
                            ForEach(variants, id: \.self) { Text($0).tag($0) }
                        }
                    }
                } footer: { Text("模型与推理配置会用于此电脑上相同引擎的新会话。模型连接和凭据在电脑上管理。") }
                Section {
                    Picker("执行权限", selection: $selection.permission) {
                        ForEach(["standard", "read_only", "unconfined"].filter { permissions.contains($0) || $0 == selection.permission }, id: \.self) { mode in
                            Text(mode == "standard" ? "标准" : mode == "read_only" ? "只读" : "不受限").tag(mode)
                        }
                    }.disabled(permissions.isEmpty)
                } footer: {
                    Text(selection.permission == "unconfined" ? "允许任务访问电脑上当前用户可访问的文件和网络。" :
                        selection.permission == "read_only" ? "限制为读取，不允许修改文件。" : "使用电脑上的工作区和网络访问边界。")
                }
                if loading { ProgressView() }
                if !editable { Text("连接电脑并等待任务结束后可修改。").foregroundStyle(.secondary) }
                if let error { Text(error).foregroundStyle(.red).textSelection(.enabled) }
                if !loading && !loaded { Button("重新加载") { Task { await load() } }.disabled(!model.connected) }
            }.disabled(saving || loading || !editable)
                .navigationTitle("会话设置").navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() }.disabled(saving) }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("保存") {
                            if selection.permission == "unconfined" && originalSettings.permission != "unconfined" { confirmUnconfined = true }
                            else { save() }
                        }.disabled(!canSave)
                    }
                }
                .confirmationDialog("允许此会话不受限地访问电脑？", isPresented: $confirmUnconfined, titleVisibility: .visible) {
                    Button("允许不受限访问", role: .destructive) { save() }
                } message: { Text("任务将不再受工作区文件和网络边界限制。仅对信任的任务开启。") }
                .task { await load() }
        }
    }
    private func draftSelection(engine: String) -> ThreadSettings {
        model.preferredDraftSettings(engine: engine,
            defaults: engine == "wuu" ? defaults : ThreadSettings(["permission_mode": "unconfined"]),
            providers: providers, engines: engines)
    }
    private func load() async {
        loading = true; error = nil
        defer { loading = false }
        do {
            let choices = try await model.loadSettingsChoices(threadID: thread.id)
            try Task.checkCancellation()
            providers = choices.providers; engines = choices.engines; defaults = choices.defaults
            if let current = model.live, current.id == thread.id {
                engine = current.engine.isEmpty ? (isDraft ? choices.defaultEngine : "wuu") : current.engine
                selection = isDraft && current.engine.isEmpty ? draftSelection(engine: engine) : model.settingsSelection(for: current)
                originalEngine = engine; originalSettings = selection
            }
            loaded = true
        } catch is CancellationError {} catch { self.error = error.localizedDescription }
    }
    private func save() {
        saving = true; error = nil
        Task {
            defer { saving = false }
            do { try await model.updateSettings(selection, threadID: thread.id, engine: engine); dismiss() }
            catch is CancellationError { dismiss() }
            catch { self.error = error.localizedDescription }
        }
    }
}
