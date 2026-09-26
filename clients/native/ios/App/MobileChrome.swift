import SwiftUI
import WuuCore

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
                AttachmentPicker(attachments: $attachments, model: model).disabled(!enabled || sending)
                HStack(alignment: .bottom, spacing: 4) {
                    TextField(enabled ? placeholder : "连接电脑后发送", text: $text, axis: .vertical)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                        .font(.system(size: textSize)).lineLimit(1...6)
                        .padding(.leading, 15).padding(.vertical, 12).disabled(!enabled)
                        .accessibilityIdentifier(identifier)
                    if let stop {
                        Button(action: stop) {
                            Image(systemName: "stop.fill").font(.system(size: 13))
                                .frame(width: 28, height: 28)
                                .foregroundStyle(Color(uiColor: .systemBackground))
                                .background(Color.primary, in: Circle())
                                .frame(width: 44, height: 44)
                        }.disabled(!enabled).accessibilityLabel("停止生成")
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
                    }
                }.background(Color(uiColor: .secondarySystemBackground).opacity(0.6), in: RoundedRectangle(cornerRadius: 24))
                    .overlay(RoundedRectangle(cornerRadius: 24).stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
            }
        }.padding(.horizontal, 12).padding(.vertical, 8)
    }
}
