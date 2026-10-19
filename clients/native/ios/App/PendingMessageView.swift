import SwiftUI
import WuuCore

/// Keeps pending work within reach without letting the queue fill the conversation.
struct PendingMessagesView: View {
    let model: AppModel
    var body: some View {
        PendingMessagesContent(model: model, pending: model.live?.pending ?? [],
            editable: model.connected && !model.refreshing && model.live?.readOnly != true,
            running: model.live?.running == true)
            .equatable()
    }
}

// Keep presentation state outside live observation so transcript deltas cannot update an open sheet or menu.
private struct PendingMessagesContent: View, Equatable {
    let model: AppModel
    let pending: [PendingMessage]
    let editable: Bool
    let running: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var showingAll = false
    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.model === rhs.model && lhs.editable == rhs.editable && lhs.running == rhs.running
            && lhs.pending.elementsEqual(rhs.pending) { $0.held == $1.held && $0.value == $1.value }
    }
    var body: some View {
        VStack(spacing: 4) {
            if let first = pending.first {
                PendingMessageView(model: model, message: first, editable: editable, running: running)
                    .id(first.id).transition(.opacity)
                if pending.count > 1 {
                    Button("查看全部 \(pending.count) 条待处理消息") { showingAll = true }
                        .font(.footnote).foregroundStyle(.secondary).frame(minHeight: 44)
                }
            }
        }
        .padding(.horizontal, 12)
        .animation(chromeAnimation(reduceMotion), value: pending.map(\.id))
        .sheet(isPresented: $showingAll) {
            NavigationStack {
                ScrollView {
                    LazyVStack(spacing: 8) {
                        ForEach(pending) { PendingMessageView(model: model, message: $0, editable: editable, running: running) }
                    }.padding(16)
                }
                .navigationTitle("待处理消息").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完成") { showingAll = false } } }
            }.presentationDetents([.medium, .large])
        }
        .onChange(of: pending.isEmpty) { _, empty in if empty { showingAll = false } }
    }
}

/// Where a waiting message stands. Origins come from the host: "steer" joins the current
/// reply, "queue" waits for the next turn, and held messages stay put after an interruption.
private struct PendingStatus {
    let title: String
    let explanation: String
    let symbol: String
    init(_ message: PendingMessage) {
        if message.held {
            title = "已暂停"; symbol = "pause.circle"
            explanation = "这条消息已暂停，不会自动处理。选择继续后发给电脑。"
        } else if message.origin == "steer" {
            title = "加入当前回复"; symbol = "arrow.turn.down.right"
            explanation = "电脑会在当前回复的下一步读取这条消息。"
        } else {
            title = "排队中"; symbol = "clock"
            explanation = "当前回复结束后处理。也可以选择立即引导，让电脑在当前回复的下一步读取。"
        }
    }
}

/// A waiting message as one compact row. Its full text, attachments and actions open in a
/// sheet, so long prompts never pile up above the composer.
struct PendingMessageView: View {
    let model: AppModel
    let message: PendingMessage
    let editable: Bool
    let running: Bool
    @Environment(\.dynamicTypeSize) private var typeSize
    @Environment(\.verticalSizeClass) private var verticalSizeClass
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var rowWidth: CGFloat = 0
    @State private var inspecting = false
    @State private var working = false
    private var status: PendingStatus { PendingStatus(message) }
    private var attachmentCount: Int { message.value["images"].array.count + message.value["files"].array.count }
    private var preview: String {
        let text = message.text.split(whereSeparator: \.isNewline).joined(separator: " ").trimmingCharacters(in: .whitespaces)
        if !text.isEmpty { return text.count > 120 ? String(text.prefix(120)) + "…" : text }
        return attachmentCount > 0 ? "\(attachmentCount) 个附件" : "空消息"
    }
    var body: some View {
        HStack(spacing: 8) {
            Button { inspecting = true } label: {
                summary.contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityElement(children: .combine)
            .accessibilityHint("显示完整消息和操作")
            if !message.held && message.origin == "queue" {
                Button { act(.steer) } label: {
                    Image(systemName: "arrow.turn.down.right").font(.system(size: 17)).frame(width: 44, height: 44)
                }
                .buttonStyle(.plain).accessibilityLabel("立即引导")
                .disabled(working || !editable || !running)
            }
            if message.held {
                ZStack {
                    if working { ProgressView().transition(.opacity) }
                    else {
                        Button("继续") { act(.resume) }
                            .buttonStyle(.bordered).buttonBorderShape(.capsule).controlSize(.small)
                            .disabled(!editable || running)
                            .transition(.opacity)
                    }
                }
                .frame(minWidth: 44, minHeight: 44)
                .animation(chromeAnimation(reduceMotion), value: working)
            }
        }
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 14))
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { rowWidth = $0 }
        .contextMenu {
            if !message.text.isEmpty {
                Button("复制", systemImage: "doc.on.doc") { UIPasteboard.general.string = message.text; Haptics.tap() }
            }
            Group {
                if message.held {
                    Button("继续", systemImage: "play") { act(.resume) }.disabled(running)
                }
                if !message.held {
                    Button(message.origin == "steer" ? "改为排队" : "立即引导", systemImage: "arrow.turn.down.right") {
                        act(message.origin == "steer" ? .requeue : .steer)
                    }.disabled(!running)
                }
                Button("移除", systemImage: "trash", role: .destructive) { act(.remove) }
            }.disabled(working || !editable)
        } preview: {
            summary
                .padding(.horizontal, 12).padding(.vertical, 8)
                // Compact-height menus sit beside the preview; keep both within the viewport.
                .frame(width: min(rowWidth, verticalSizeClass == .compact ? 250 : 360))
                // The lifted preview overlaps the transcript and must not let its text show through.
                .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 14))
        }
        .accessibilityAction(named: "移除") { if editable && !working { act(.remove) } }
        .sheet(isPresented: $inspecting) {
            PendingMessageDetail(model: model, message: message, status: status)
                .presentationDetents([.medium, .large])
        }
    }
    private var summary: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: status.symbol).font(.system(size: 14)).frame(width: 18)
                .foregroundStyle(.secondary).accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 8) {
                    Text(status.title)
                    if attachmentCount > 0 {
                        HStack(spacing: 2) {
                            Image(systemName: "paperclip").font(.system(size: 11))
                            Text("\(attachmentCount)")
                        }.accessibilityLabel("\(attachmentCount) 个附件")
                    }
                }.font(.footnote).foregroundStyle(.secondary)
                Text(preview).foregroundStyle(.primary)
                    .lineLimit(typeSize.isAccessibilitySize ? 3 : 2)
            }
            Spacer(minLength: 0)
        }
    }
    private func act(_ action: AppModel.PendingAction) {
        working = true
        Haptics.tap()
        model.perform { defer { working = false }; try await model.pendingAction(message, action: action) }
    }
}

private struct PendingMessageDetail: View {
    let model: AppModel
    let message: PendingMessage
    let status: PendingStatus
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var working = false
    @State private var failure: String?
    @State private var attachmentPreview: LoadedAttachment?
    private var images: [JSONValue] { message.value["images"].array }
    private var files: [JSONValue] { message.value["files"].array }
    private var editable: Bool { model.connected && !model.refreshing && model.live?.readOnly != true }
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Text(status.explanation).font(.footnote).foregroundStyle(.secondary)
                    if !images.isEmpty {
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: MessageImage.width), spacing: 8, alignment: .leading)],
                                  alignment: .leading, spacing: 8) {
                            ForEach(Array(images.enumerated()), id: \.offset) { index, attachment in
                                MessageImage(key: "pending:\(message.id):\(index)", connected: true, loader: model.imagePreviews,
                                    read: { try await read(attachment) },
                                    open: { model.perform { attachmentPreview = try await read(attachment) } })
                            }
                        }
                    }
                    ForEach(Array(files.enumerated()), id: \.offset) { _, attachment in
                        Button { model.perform { attachmentPreview = try await read(attachment) } } label: {
                            Label(attachment["filename"].string ?? "文件", systemImage: "doc").lineLimit(1).truncationMode(.middle)
                        }.frame(minHeight: 44)
                    }
                    if !message.text.isEmpty { Text(message.text).textSelection(.enabled) }
                    if let failure {
                        Label(failure, systemImage: "exclamationmark.triangle").font(.footnote)
                            .foregroundStyle(.red).textSelection(.enabled).transition(.opacity)
                    }
                }
                .padding(20).frame(maxWidth: .infinity, alignment: .leading)
                .animation(chromeAnimation(reduceMotion), value: failure)
            }
            .safeAreaInset(edge: .bottom) { actions }
            .navigationTitle(status.title).navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("完成") { dismiss() } }
                if !message.text.isEmpty {
                    ToolbarItem(placement: .primaryAction) {
                        Button { UIPasteboard.general.string = message.text; Haptics.tap() } label: {
                            Image(systemName: "doc.on.doc").font(.system(size: 19))
                        }.accessibilityLabel("复制")
                    }
                }
            }
            .sheet(item: $attachmentPreview) { attachment in
                AttachmentPreview(attachment: attachment)
            }
        }
    }
    private var actions: some View {
        HStack(spacing: 12) {
            Button("移除", role: .destructive) { act(.remove) }
                .frame(minHeight: 44)
            Button { act(message.held ? .resume : message.origin == "steer" ? .requeue : .steer) } label: {
                Text(message.held ? "继续" : message.origin == "steer" ? "改为排队" : "立即引导")
                    .frame(maxWidth: .infinity, minHeight: 44)
            }
            .mobilePrimaryAction().controlSize(.large)
            .disabled(message.held ? model.live?.running == true : model.live?.running != true)
        }
        .disabled(working || !editable)
        .padding(.horizontal, 20).padding(.vertical, 12)
        .background(.bar)
    }
    private func read(_ attachment: JSONValue) async throws -> LoadedAttachment {
        try await readMessageAttachment(attachment, scopeID: message.value["thread_id"].string ?? "", messageID: message.id) { _, _ in
            throw NativeError.invalid("排队附件内容不可用")
        }
    }
    private func act(_ action: AppModel.PendingAction) {
        working = true; failure = nil
        Haptics.tap()
        Task {
            defer { working = false }
            do { try await model.pendingAction(message, action: action); dismiss() }
            catch is CancellationError {}
            catch { failure = error.localizedDescription; Haptics.failure() }
        }
    }
}
