import SwiftUI
import WuuCore

/// Edits a past user message and resends from it. The model owns validation, trimming and
/// resending; this sheet keeps the draft and reports failures without losing text.
struct HistoryMessageEditView: View {
    let model: AppModel
    @Bindable var edit: HistoryMessageEdit
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var sending = false
    @FocusState private var focused: Bool
    private var busy: Bool { sending || edit.submitting }
    private var empty: Bool { edit.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && edit.attachmentCount == 0 }
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    TextEditor(text: $edit.text)
                        .focused($focused)
                        .scrollContentBackground(.hidden)
                        .frame(height: 180)
                        .padding(8)
                        .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12))
                        .disabled(busy)
                        .accessibilityLabel("消息内容")
                        .accessibilityIdentifier("history-edit-text")
                    if edit.attachmentCount > 0 {
                        Label("保留原消息的 \(edit.attachmentCount) 个附件", systemImage: "paperclip")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                    Text(edit.prepared ? "之后的对话已被替换，这条消息还没有发送。" : "发送后将从这条消息重新开始，之后的对话会被替换。")
                        .font(.footnote).foregroundStyle(.secondary)
                    if let error = edit.error {
                        HStack(alignment: .firstTextBaseline, spacing: 4) {
                            Label(error, systemImage: "exclamationmark.triangle").lineLimit(3)
                            if error.count > 120 || error.contains("\n") { DetailButton(detail: error, label: "错误详情") }
                        }
                        .font(.footnote).foregroundStyle(.red)
                        .transition(.opacity)
                    }
                }
                .padding(20)
                .animation(chromeAnimation(reduceMotion), value: edit.error)
            }
            .scrollDismissesKeyboard(.interactively)
            .navigationTitle("编辑消息").navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    // Once history was trimmed the draft is the only copy, so leaving keeps it.
                    Button(edit.prepared ? "稍后" : "取消") { close() }.disabled(busy)
                }
                ToolbarItem(placement: .confirmationAction) {
                    if busy { ProgressView() }
                    else {
                        Button("发送", action: submit).fontWeight(.semibold)
                            .disabled(empty || model.historyActionBusy || !model.connected || model.live?.running == true || model.live?.pending.isEmpty == false || model.live?.readOnly == true || model.live?.archived == true)
                            .accessibilityIdentifier("history-edit-send")
                    }
                }
            }
            .onAppear { if !edit.prepared { focused = true } }
        }
        .interactiveDismissDisabled(busy)
    }
    private func close() {
        model.showingHistoryEdit = false
        if !edit.prepared { model.historyEdit = nil }
    }
    private func submit() {
        guard !busy, !empty else { return }
        sending = true; edit.error = nil; focused = false
        Haptics.tap()
        Task {
            defer { sending = false }
            do {
                try await model.submitHistoryEdit(edit)
            } catch is CancellationError {
            } catch {
                edit.error = error.localizedDescription
                Haptics.failure()
            }
        }
    }
}

/// Shown in the conversation when an edit trimmed history but was not sent yet.
struct HistoryEditResumeBanner: View {
    let model: AppModel
    let edit: HistoryMessageEdit
    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "pencil.circle").font(.system(size: 19)).foregroundStyle(.secondary).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text("编辑的消息还没有发送")
                if let error = edit.error { Text(error).font(.footnote).foregroundStyle(.red).lineLimit(1) }
            }
            Spacer(minLength: 8)
            Button("继续编辑") { model.showingHistoryEdit = true }
                .buttonStyle(.bordered).buttonBorderShape(.capsule)
                .frame(minHeight: 44)
                .accessibilityIdentifier("history-edit-resume")
        }
        .padding(.horizontal, 14).padding(.vertical, 6)
        .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 16))
        .accessibilityElement(children: .contain)
    }
}
