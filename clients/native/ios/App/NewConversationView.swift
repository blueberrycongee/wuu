import SwiftUI
import WuuCore

/// Chooses the computer's working folder before the conversation exists, then starts it there.
struct NewConversationView: View {
    @Bindable var model: AppModel
    var started: () -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var selection: String
    @State private var query = ""
    @State private var starting = false
    @State private var failure: String?
    private let rememberKey: String
    private let remembered: String
    init(model: AppModel, started: @escaping () -> Void) {
        self.model = model
        self.started = started
        rememberKey = "new-conversation-folder:" + (model.host?.pub ?? "")
        remembered = UserDefaults.standard.string(forKey: rememberKey) ?? ""
        let initial = !model.workspace.isEmpty ? model.workspace : !remembered.isEmpty ? remembered : model.workspaces.first?["path"].string ?? ""
        _selection = State(initialValue: initial)
    }
    private var folders: [String] {
        var paths = model.workspaces.compactMap { $0["path"].string }.filter { !$0.isEmpty }
        // A previously used custom folder stays selectable even if the computer does not list it.
        for path in [remembered, selection] where !path.isEmpty && !paths.contains(path) { paths.insert(path, at: 0) }
        return paths
    }
    private var trimmedQuery: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var matches: [String] {
        trimmedQuery.isEmpty ? folders : folders.filter { $0.localizedCaseInsensitiveContains(trimmedQuery) }
    }
    private var customPath: String? {
        let path = trimmedQuery
        let absolute = path.hasPrefix("/") || path.hasPrefix("\\\\") || path.range(of: #"^[A-Za-z]:[\\/]"#, options: .regularExpression) != nil
        return absolute && !folders.contains(path) ? path : nil
    }
    var body: some View {
        NavigationStack {
            List {
                if let customPath {
                    Section {
                        Button { selection = customPath; query = "" } label: {
                            FolderRow(title: "使用此路径", detail: customPath, path: customPath, symbol: "folder.badge.plus", selected: false)
                        }
                    }
                }
                Section {
                    ForEach(matches, id: \.self) { path in
                        Button { selection = path } label: {
                            // The name is the title; the parent locates it without repeating it.
                            FolderRow(title: folderName(path), detail: parentPath(path), path: path, symbol: "folder", selected: path == selection)
                        }
                        .contextMenu {
                            Button("复制路径", systemImage: "doc.on.doc") { UIPasteboard.general.string = path; Haptics.tap() }
                        }
                    }
                }
            }
            .listStyle(.insetGrouped)
            .sensoryFeedback(.selection, trigger: selection)
            .overlay {
                if matches.isEmpty && customPath == nil {
                    if trimmedQuery.isEmpty {
                        ContentUnavailableView("没有最近的文件夹", systemImage: "folder",
                            description: Text(model.connected ? "输入完整路径，或直接开始使用默认文件夹。" : "电脑连接后显示文件夹。"))
                    } else {
                        ContentUnavailableView("没有匹配的文件夹", systemImage: "magnifyingglass",
                            description: Text("输入完整路径可以使用其他文件夹。"))
                    }
                }
            }
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "搜索文件夹或输入完整路径")
            .textInputAutocapitalization(.never).autocorrectionDisabled()
            .onSubmit(of: .search) { if let customPath { selection = customPath; query = "" } }
            .navigationTitle("新会话").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("取消") { dismiss() }.disabled(starting) }
            }
            .safeAreaInset(edge: .bottom) { startBar }
        }
        .interactiveDismissDisabled(starting)
    }
    private var startBar: some View {
        VStack(spacing: 10) {
            Group {
                if let failure {
                    HStack(spacing: 4) {
                        Text(failure).foregroundStyle(.red).lineLimit(2)
                        if failure.count > 80 || failure.contains("\n") { DetailButton(detail: failure, label: "错误详情") }
                    }
                }
                else if !model.connected { Text(model.connecting ? "正在连接电脑…" : "电脑未连接，连接后才能开始。").foregroundStyle(.secondary) }
                else {
                    Label(selection.isEmpty ? "使用电脑的默认文件夹" : folderName(selection), systemImage: "folder")
                        .foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                        .accessibilityLabel(selection.isEmpty ? "使用电脑的默认文件夹" : "文件夹：" + selection)
                }
            }.font(.footnote).frame(maxWidth: .infinity)
                .animation(chromeAnimation(reduceMotion), value: failure)
            Button(action: start) {
                HStack(spacing: 8) {
                    if starting { ProgressView() }
                    Text(starting ? "正在开始…" : "开始会话")
                }.frame(maxWidth: .infinity, minHeight: 44)
            }.mobilePrimaryAction().controlSize(.large)
                .disabled(starting || !model.connected)
        }
        .padding(.horizontal, 20).padding(.top, 12).padding(.bottom, 8)
        .background(.bar)
    }
    private func start() {
        guard !starting, model.connected else { return }
        starting = true; failure = nil
        let path = selection
        Task {
            defer { starting = false }
            do {
                try await model.startThread(workdir: path.isEmpty ? nil : path)
                if !path.isEmpty { UserDefaults.standard.set(path, forKey: rememberKey) }
                Haptics.tap()
                started()
                dismiss()
            } catch is CancellationError {} catch { failure = error.localizedDescription; Haptics.failure() }
        }
    }
}

/// The containing folder of a host path, keeping the root itself ("/", "C:\").
private func parentPath(_ path: String) -> String {
    guard let cut = path.dropLast().lastIndex(where: { $0 == "/" || $0 == "\\" }) else { return path }
    return String(path[...cut])
}

private struct FolderRow: View {
    let title: String
    let detail: String
    let path: String
    let symbol: String
    let selected: Bool
    @Environment(\.dynamicTypeSize) private var typeSize
    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: symbol).font(.system(size: 20)).foregroundStyle(.secondary).frame(width: 24).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).foregroundStyle(.primary).lineLimit(2)
                Text(detail).font(.footnote).foregroundStyle(.secondary)
                    .lineLimit(typeSize.isAccessibilitySize ? 2 : 1).truncationMode(.middle)
            }
            Spacer(minLength: 8)
            if selected {
                Image(systemName: "checkmark").font(.system(size: 17, weight: .semibold)).foregroundStyle(.tint).accessibilityHidden(true)
            }
        }
        .padding(.vertical, 2).contentShape(Rectangle())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue(path)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}
