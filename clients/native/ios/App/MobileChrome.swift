import SwiftUI
import WuuCore

/// Last component of a host path. The computer may use POSIX or Windows separators.
func folderName(_ path: String) -> String {
    path.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? path
}

/// Haptics for deliberate actions and their outcomes. Not for streaming, polling or reconnects,
/// and not for system controls that already give their own feedback.
@MainActor enum Haptics {
    static func tap() { UIImpactFeedbackGenerator(style: .light).impactOccurred() }
    static func success() { UINotificationFeedbackGenerator().notificationOccurred(.success) }
    static func failure() { UINotificationFeedbackGenerator().notificationOccurred(.error) }
}

/// Restrained motion for chrome that appears or leaves; none when Reduce Motion is on.
func chromeAnimation(_ reduceMotion: Bool) -> Animation? { reduceMotion ? nil : .easeOut(duration: 0.2) }

extension View {
    /// The app uses a monochrome tint; prominent labels need the opposite surface color.
    func mobilePrimaryAction() -> some View {
        buttonStyle(.borderedProminent).foregroundStyle(Color(uiColor: .systemBackground))
    }
    /// A presented sheet covers the root alert, so sheets attach their own.
    func modelErrorAlert(_ model: AppModel) -> some View {
        alert("提示", isPresented: Binding(get: { model.error != nil }, set: { if !$0 { model.error = nil } })) {
            Button("知道了", role: .cancel) { model.error = nil }
        } message: { Text(model.error ?? "") }
    }
}

struct MobileCircleButton: View {
    let symbol: String
    let label: String
    var action: () -> Void
    var body: some View {
        Button(action: action) {
            Image(systemName: symbol).font(.system(size: 19, weight: .regular))
                .frame(width: 44, height: 44)
                .background(Color(uiColor: .systemBackground), in: Circle())
                .overlay(Circle().stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
        }.buttonStyle(.plain).accessibilityLabel(label)
    }
}

struct MobileComposer: View {
    @Environment(\.mobileTextSize) private var textSize
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Binding var text: String
    @Binding var attachments: [InputAttachment]
    let model: AppModel
    var enabled = true
    var sending = false
    var placeholder = "发送消息"
    var identifier = "native-composer"
    var stop: (() -> Void)? = nil
    var send: () -> Void
    private var canSend: Bool { enabled && !sending && (!text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty) }
    var body: some View {
        VStack(spacing: 8) {
            if !attachments.isEmpty {
                ScrollView(.horizontal) {
                    HStack(spacing: 8) {
                        ForEach(attachments) { file in
                            if file.isImage {
                                DraftImage(attachment: file).overlay(alignment: .topTrailing) {
                                    Button { attachments.removeAll { $0.id == file.id } } label: {
                                        Image(systemName: "xmark").font(.system(size: 10, weight: .bold)).padding(5)
                                            .background(.regularMaterial, in: Circle()).frame(width: 32, height: 32)
                                    }.accessibilityLabel("移除附件 " + file.filename).disabled(sending)
                                }
                            } else {
                            Button { attachments.removeAll { $0.id == file.id } } label: {
                                HStack(spacing: 6) {
                                    Image(systemName: file.isImage ? "photo" : "doc")
                                    Text(file.filename).lineLimit(1).frame(maxWidth: 150)
                                    Image(systemName: "xmark")
                                }.font(.caption).padding(10).background(Color(uiColor: .secondarySystemBackground), in: Capsule())
                            }.accessibilityLabel("移除附件 " + file.filename).disabled(sending)
                            }
                        }
                    }.padding(.leading, 52)
                }.scrollIndicators(.hidden)
            }
            HStack(alignment: .bottom, spacing: 8) {
                // Drafts stay editable while the computer reconnects; only sending waits.
                AttachmentPicker(attachments: $attachments, model: model).disabled(sending)
                HStack(alignment: .bottom, spacing: 4) {
                    TextField(placeholder, text: $text, axis: .vertical)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .font(.system(size: textSize)).lineLimit(1...6)
                        .padding(.leading, 15).padding(.vertical, 12)
                        .accessibilityIdentifier(identifier)
                    if let stop {
                        Button(action: stop) {
                            Image(systemName: "stop.fill").font(.system(size: 13))
                                .frame(width: 28, height: 28)
                                .foregroundStyle(Color(uiColor: .systemBackground))
                                .background(Color.primary, in: Circle())
                                .frame(width: 44, height: 44)
                        }.disabled(!enabled).accessibilityLabel("停止生成")
                            .transition(.opacity)
                    }
                    if stop == nil || canSend || sending {
                    Button(action: send) {
                        Group {
                            if sending { ProgressView().tint(Color(uiColor: .systemBackground)).scaleEffect(0.7) }
                            else { Image(systemName: "arrow.up").font(.system(size: 15, weight: .semibold)) }
                        }.frame(width: 28, height: 28)
                            .foregroundStyle(canSend || sending ? Color(uiColor: .systemBackground) : .secondary)
                            .background(canSend || sending ? Color.primary : Color.primary.opacity(0.08), in: Circle())
                            .frame(width: 44, height: 44)
                    }.disabled(!canSend).accessibilityLabel(sending ? "正在发送" : "发送")
                        .transition(.opacity)
                    }
                }.background(Color(uiColor: .secondarySystemBackground).opacity(0.6), in: RoundedRectangle(cornerRadius: 24))
                    .overlay(RoundedRectangle(cornerRadius: 24).stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
                    .animation(chromeAnimation(reduceMotion), value: stop != nil)
                    .animation(chromeAnimation(reduceMotion), value: canSend)
            }
        }.padding(.horizontal, 12).padding(.vertical, 8)
            .animation(chromeAnimation(reduceMotion), value: attachments.count)
    }
}
