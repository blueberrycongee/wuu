import SwiftUI
import WuuCore

/// The commentary and tool calls that led to an answer, folded to one line once the answer
/// arrives (desktop's outer process fold, not its per-tool summary).
struct ProcessGroupView: View {
    let messages: [ChatMessage]
    let settings: ThreadSettings?
    let active: Bool
    var inspect: () -> Void
    private var toolCount: Int { messages.filter { $0.tool != nil }.count }
    private var failures: Int { messages.filter { $0.tool?.status == "failed" }.count }
    private var detail: String? {
        var parts: [String] = []
        if toolCount > 0 { parts.append("\(toolCount) 项操作") }
        if failures > 0 { parts.append("\(failures) 项失败") }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
    var body: some View {
        Button(action: inspect) {
            HStack(spacing: 8) {
                if active {
                    ConversationActivityMark(activity: messages.last?.tool != nil ? "tool" : "thinking", settings: settings)
                } else {
                    Image(systemName: failures > 0 ? "exclamationmark.circle" : "text.alignleft")
                        .font(.system(size: 15)).frame(width: 20)
                        .foregroundStyle(failures > 0 ? Color.red : Color.secondary)
                        .accessibilityHidden(true)
                }
                Text(active ? "正在处理" : "处理过程").lineLimit(1)
                if let detail {
                    Text(detail).foregroundStyle(failures > 0 ? Color.red : Color.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
                Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(.tertiary).accessibilityHidden(true)
            }
            .foregroundStyle(.secondary)
            .frame(minHeight: 44).contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(detail.map { (active ? "正在处理，" : "处理过程，") + $0 } ?? (active ? "正在处理" : "处理过程"))
        .accessibilityHint("显示完整处理过程")
        .accessibilityIdentifier("process-group")
    }
}

/// Everything the group folded, in order: commentary in full and each tool run with its details.
struct ProcessGroupDetails: View {
    let model: AppModel
    let messages: [ChatMessage]
    let settings: ThreadSettings?
    let active: Bool
    @Environment(\.dismiss) private var dismiss
    /// Consecutive tool calls form one run; each piece of commentary stands alone.
    private var segments: [[ChatMessage]] {
        var result: [[ChatMessage]] = []
        for message in messages {
            if message.tool != nil, result.last?.last?.tool != nil { result[result.count - 1].append(message) }
            else { result.append([message]) }
        }
        return result
    }
    var body: some View {
        NavigationStack {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    ForEach(segments, id: \.first?.id) { segment in
                        if segment[0].tool != nil {
                            ToolGroupView(messages: segment, settings: settings,
                                          active: active && segment.last?.id == messages.last?.id)
                        } else if !segment[0].text.isEmpty {
                            MessageText(text: segment[0].text, markdown: segment[0].role == "assistant")
                                .foregroundStyle(segment[0].role == "error" ? Color.red : Color.primary)
                            if !segment[0].contentRef.isEmpty {
                                Button("加载完整消息") { model.perform { try await model.expand(segment[0]) } }
                                    .disabled(!model.connected || model.loadingContent.contains(segment[0].id))
                            }
                        }
                    }
                }
                .padding(20).frame(maxWidth: .infinity, alignment: .leading)
            }
            .navigationTitle("处理过程").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } } }
        }
    }
}
