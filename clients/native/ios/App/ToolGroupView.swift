import SwiftUI
import WuuCore

/// One compact line per run of tool calls. The shared desktop rules still produce every
/// sentence; the phone shows the current step or a count and keeps the full account in a sheet.
struct ToolGroupView: View {
    let messages: [ChatMessage]
    let settings: ThreadSettings?
    let active: Bool
    @State private var summary: ToolSummary?
    @State private var current: ToolSummary?
    @State private var unavailable = false
    @State private var request: Task<Void, Never>?
    @State private var details = false
    fileprivate static let engine = ToolSummaryEngine(script: {
        guard let url = Bundle.main.url(forResource: "process", withExtension: "js", subdirectory: "NativeUI") else { return "" }
        return (try? String(contentsOf: url, encoding: .utf8)) ?? ""
    }())
    private var tools: [ToolActivity] { messages.compactMap(\.tool) }
    private var failures: Int { tools.filter { $0.status == "failed" }.count }
    private var interrupted: Bool { !active && tools.contains { $0.status == "ended" } }
    /// A single operation is already short; several collapse to a count instead of a joined sentence.
    private var title: String {
        if active { return current?.text ?? summary?.text ?? "正在处理…" }
        if tools.count == 1, let text = summary?.text { return text }
        if unavailable && tools.count <= 1 { return "动作摘要暂不可用" }
        return "已执行 \(tools.count) 项操作"
    }
    private var status: String? {
        var parts: [String] = []
        if active && tools.count > 1 { parts.append("\(tools.count) 项") }
        if failures > 0 { parts.append("\(failures) 项失败") }
        else if interrupted { parts.append("已中断") }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
    var body: some View {
        Button { details = true } label: {
            HStack(spacing: 8) {
                if active {
                    ConversationActivityMark(activity: current?.activity ?? summary?.activity ?? "tool", settings: settings)
                } else {
                    Image(systemName: failures > 0 ? "exclamationmark.circle" : "checklist")
                        .font(.system(size: 15)).frame(width: 20)
                        .foregroundStyle(failures > 0 ? Color.red : Color.secondary)
                        .accessibilityHidden(true)
                }
                Text(title).lineLimit(1).truncationMode(.tail).layoutPriority(1)
                if let status {
                    Text(status).foregroundStyle(failures > 0 ? Color.red : Color.secondary)
                        .lineLimit(1).fixedSize()
                }
                Spacer(minLength: 0)
                Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(.tertiary).accessibilityHidden(true)
            }
            .foregroundStyle(.secondary)
            .frame(minHeight: 44).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(status.map { title + "，" + $0 } ?? title)
        .accessibilityHint("显示操作详情")
        .accessibilityIdentifier("tool-group")
        .sheet(isPresented: $details) {
            ToolGroupDetails(tools: tools, summary: summary?.text, active: active)
                .presentationDetents([.medium, .large])
        }
        .onAppear { refresh() }
        .onChange(of: messages) { _, _ in refresh() }
        .onChange(of: active) { _, _ in refresh() }
        .onDisappear { request?.cancel() }
    }
    private func refresh() {
        request?.cancel()
        let tools = self.tools, active = self.active
        request = Task {
            let next = try? await Self.engine.summarize(tools.map(\.presentation))
            // The step in progress, described by the same rules as a one-item group.
            var now: ToolSummary?
            if active, let step = tools.last(where: { $0.status == "in_progress" }) ?? tools.last {
                now = try? await Self.engine.summarize([step.presentation])
            }
            guard !Task.isCancelled else { return }
            summary = next; current = now; unavailable = next == nil
        }
    }
}

/// Every operation in the group as one shared-rule line each, with its error when it failed.
private struct ToolGroupDetails: View {
    let tools: [ToolActivity]
    let summary: String?
    let active: Bool
    @Environment(\.dismiss) private var dismiss
    @State private var lines: [String] = []
    var body: some View {
        NavigationStack {
            List {
                if let summary {
                    Section { Text(summary).foregroundStyle(.secondary).textSelection(.enabled) }
                }
                Section {
                    ForEach(Array(tools.enumerated()), id: \.offset) { index, tool in
                        HStack(alignment: .firstTextBaseline, spacing: 10) {
                            symbol(tool.status).font(.system(size: 13)).frame(width: 18).accessibilityHidden(true)
                            VStack(alignment: .leading, spacing: 4) {
                                Text(lines.indices.contains(index) && !lines[index].isEmpty ? lines[index] : tool.name)
                                    .lineLimit(3).truncationMode(.middle)
                                if tool.status == "failed" && !tool.error.isEmpty {
                                    Text(tool.error).font(.footnote).foregroundStyle(.red)
                                        .lineLimit(4).textSelection(.enabled)
                                }
                            }
                        }
                        .accessibilityElement(children: .combine)
                        .accessibilityValue(tool.statusLabel)
                    }
                }
            }
            .navigationTitle(active ? "正在执行" : "\(tools.count) 项操作").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } } }
            .task(id: tools.map(\.presentation)) {
                var result: [String] = []
                for tool in tools {
                    let line = try? await ToolGroupView.engine.summarize([tool.presentation])
                    guard !Task.isCancelled else { return }
                    result.append(line?.text ?? "")
                }
                lines = result
            }
        }
    }
    @ViewBuilder private func symbol(_ status: String) -> some View {
        switch status {
        case "failed": Image(systemName: "xmark.circle.fill").foregroundStyle(.red)
        case "in_progress": ProgressView().controlSize(.mini)
        case "completed": Image(systemName: "checkmark").foregroundStyle(.secondary)
        default: Image(systemName: "minus").foregroundStyle(.secondary)
        }
    }
}
