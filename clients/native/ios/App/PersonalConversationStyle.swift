import SwiftUI
import WuuCore

let personalAccent = Color(red: 53 / 255, green: 102 / 255, blue: 240 / 255)
let personalSectionNames = ["对话", "事项", "今天", "记忆", "设置", "日历", "查找历史"]
let personalSectionIcons = ["bubble.left", "checklist", "sun.max", "brain", "slider.horizontal.3", "calendar", "magnifyingglass"]

struct PersonalAgentOption: Identifiable {
    let id: String
    let name: String
}

struct PersonalConversationHeader: View {
    let agents: [PersonalAgentOption]
    let name: String
    let status: String
    let activity: String
    let activityCount: Int
    let chooseAgent: (String) -> Void
    let showComputers: () -> Void
    let showActivity: () -> Void
    @ScaledMetric(relativeTo: .body) private var avatarMetric: CGFloat = 48

    private var avatarSize: CGFloat { min(avatarMetric, 62) }
    var body: some View {
        ZStack(alignment: .top) {
            HStack {
                headerButton("电脑列表", systemImage: "chevron.left", action: showComputers)
                Spacer()
                headerButton("活动与资料", systemImage: "rectangle.stack", action: showActivity)
                    .overlay(alignment: .topTrailing) {
                        if activityCount > 0 {
                            Text(activityCount, format: .number)
                                .font(.caption2.weight(.semibold)).foregroundStyle(.white)
                                .frame(minWidth: 18, minHeight: 18).padding(1)
                                .background(personalAccent, in: Capsule())
                                .accessibilityHidden(true)
                        }
                    }
            }.padding(.horizontal, 10).padding(.top, 4)

            ZStack {
                identity
                if !agents.isEmpty {
                    Menu {
                        ForEach(agents) { agent in
                            Button(agent.name) { chooseAgent(agent.id) }
                        }
                    } label: {
                        Color.clear
                            .frame(width: 180, height: avatarSize + 32)
                            .contentShape(Rectangle())
                    }
                }
            }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel("\(name)，\(status)")
            .accessibilityHint(agents.count > 1 ? "轻点切换助理" : "")
        }
        .frame(minHeight: avatarSize + 34)
        .padding(.bottom, 6)
        .background(Color(uiColor: .systemBackground))
    }

    private var identity: some View {
        VStack(spacing: 3) {
            PersonalOrbAvatar(activity: activity, size: avatarSize)
            HStack(spacing: 4) {
                Circle().fill(activity == "idle" ? Color.secondary : personalAccent).frame(width: 5, height: 5)
                Text(name).font(.subheadline).lineLimit(1)
                Text("· " + status).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .padding(.horizontal, 10).padding(.vertical, 3)
            .background(Color(uiColor: .secondarySystemBackground), in: Capsule())
        }.foregroundStyle(.primary)
    }

    private func headerButton(_ label: String, systemImage: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemImage).font(.body.weight(.medium)).frame(width: 44, height: 44)
        }
        .foregroundStyle(.primary)
        .contentShape(Rectangle())
        .accessibilityLabel(label)
    }
}

private struct PersonalOrbAvatar: View {
    let activity: String
    let size: CGFloat
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var pulsing = false

    var body: some View {
        ZStack {
            Circle().fill(Color.primary)
            HStack(spacing: size * 0.16) {
                Capsule().fill(Color(uiColor: .systemBackground)).frame(width: size * 0.11, height: size * 0.34)
                Capsule().fill(Color(uiColor: .systemBackground)).frame(width: size * 0.11, height: size * 0.34)
            }
        }
        .overlay {
            if activity != "idle" {
                Circle().stroke(personalAccent.opacity(pulsing ? 0.95 : 0.45), lineWidth: max(2, size * 0.06))
                    .padding(-3)
            }
        }
        .scaleEffect(activity != "idle" && pulsing ? 1.04 : 1)
        .frame(width: size, height: size)
        .shadow(color: Color.black.opacity(0.08), radius: 3, y: 1)
        .task(id: activity) {
            pulsing = false
            guard activity != "idle", !reduceMotion else { return }
            pulsing = true
        }
        .animation(activity != "idle" && !reduceMotion ? .easeInOut(duration: 0.9).repeatForever(autoreverses: true) : .none,
                   value: pulsing)
        .accessibilityHidden(true)
    }
}

struct PersonalNotice: View {
    let text: String
    let error: Bool
    var body: some View {
        Text(text)
            .font(.footnote).foregroundStyle(error ? Color.red : Color.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 16).padding(.vertical, 8)
            .background(error ? Color.red.opacity(0.07) : Color.secondary.opacity(0.07))
            .accessibilityLabel(error ? "错误：" + text : text)
    }
}

struct PersonalMessageRow: View {
    let record: JSONValue
    let agentName: String
    let attachmentsEnabled: Bool
    let openAttachment: (JSONValue) -> Void

    private var user: Bool { record["role"].string == "user" }
    private var content: String { record["display_content"].string ?? record["content"].string ?? "" }
    private var alignment: HorizontalAlignment { user ? .trailing : .leading }
    var body: some View {
        HStack(alignment: .bottom, spacing: 0) {
            if user { Spacer(minLength: 52) }
            VStack(alignment: alignment, spacing: 7) {
                ForEach(Array(record["attachments"].array.enumerated()), id: \.offset) { _, attachment in
                    Button { openAttachment(attachment) } label: {
                        HStack(spacing: 7) {
                            Image(systemName: attachment["kind"].string == "image" ? "photo" : "doc")
                            Text(attachment["name"].string ?? "查看附件").lineLimit(1)
                            Image(systemName: "chevron.right").font(.caption2)
                        }
                        .font(.footnote).padding(.horizontal, 11).frame(minHeight: 38)
                        .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: 12, style: .continuous).stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
                    }
                    .buttonStyle(.plain)
                    .disabled(!attachmentsEnabled || attachment["name"].string == nil)
                }
                if !content.isEmpty {
                    MessageText(text: content, markdown: !user)
                        .foregroundStyle(user ? Color.white : Color.primary)
                        .tint(user ? Color.white : personalAccent)
                        .padding(.horizontal, user ? 16 : 0)
                        .padding(.vertical, user ? 10 : 0)
                        .background(user ? personalAccent : Color.clear,
                                    in: RoundedRectangle(cornerRadius: 22, style: .continuous))
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityLabel(user ? "你" : agentName)
            if !user { Spacer(minLength: 0) }
        }.frame(maxWidth: .infinity)
    }
}

struct PersonalEmptyConversation: View {
    var body: some View {
        VStack(spacing: 7) {
            Text("今天，想一起处理什么？").font(.headline.weight(.medium))
            Text("安排一件事，记住一个想法，或者随便聊聊。")
                .font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.horizontal, 32).padding(.top, 72)
    }
}

struct PersonalComposer: View {
    @Binding var text: String
    @Binding var attachments: [InputAttachment]
    let model: AppModel
    let composerID: UUID
    let available: Bool
    let enabled: Bool
    let sending: Bool
    let pending: Bool
    let onSend: () -> Void
    let onDiscard: () -> Void

    private var canSend: Bool {
        enabled && !sending && (pending || !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty)
    }
    var body: some View {
        VStack(spacing: 8) {
            if !attachments.isEmpty {
                ScrollView(.horizontal) {
                    HStack(spacing: 8) {
                        ForEach(attachments) { file in
                            Button { attachments.removeAll { $0.id == file.id } } label: {
                                HStack(spacing: 6) {
                                    Image(systemName: file.isImage ? "photo" : "doc")
                                    Text(file.filename).lineLimit(1)
                                    Image(systemName: "xmark").font(.caption2)
                                }
                                .font(.caption).padding(.horizontal, 10).frame(minHeight: 36)
                                .background(Color(uiColor: .secondarySystemBackground), in: Capsule())
                            }
                            .buttonStyle(.plain).disabled(sending)
                            .accessibilityLabel("移除附件 " + file.filename)
                        }
                    }.padding(.leading, 52)
                }.scrollIndicators(.hidden)
            }
            HStack(alignment: .bottom, spacing: 8) {
                AttachmentPicker(attachments: $attachments, model: model)
                    .id(composerID).disabled(!enabled || pending)
                HStack(alignment: .bottom, spacing: 4) {
                    TextField(available ? "给 twin 发消息" : "连接电脑后发送", text: $text, axis: .vertical)
                        .lineLimit(1...6).disabled(pending)
                        .padding(.leading, 15).padding(.vertical, 12).frame(minHeight: 48)
                        .accessibilityLabel("消息")
                    Button(action: onSend) {
                        Group {
                            if sending { ProgressView().controlSize(.small) }
                            else { Image(systemName: "arrow.up").font(.body.weight(.semibold)) }
                        }
                        .frame(width: 32, height: 32)
                        .foregroundStyle(canSend ? Color(uiColor: .systemBackground) : Color.secondary)
                        .background(canSend ? Color.primary : Color.primary.opacity(0.08), in: Circle())
                        .frame(width: 44, height: 48)
                    }
                    .disabled(!canSend)
                    .accessibilityLabel(pending ? "重试发送" : "发送")
                }
                .background(Color(uiColor: .secondarySystemBackground), in: RoundedRectangle(cornerRadius: 26, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 26, style: .continuous).stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
            }
            if pending {
                Button("放下本机重试", action: onDiscard).font(.footnote).disabled(sending)
            }
        }
        .padding(.horizontal, 12).padding(.top, 8).padding(.bottom, 6)
        .background(.bar)
    }
}

#if DEBUG
/// A local, account-free acceptance surface for simulator screenshots.
struct PersonalUIFixtureView: View {
    @State private var text = ""
    @State private var keyboardText = ""
    @State private var attachments: [InputAttachment] = []
    @State private var model = AppModel()
    @FocusState private var keyboardPresented: Bool
    private let messages: [JSONValue] = [
        ["seq": 1, "role": "user", "content": "帮我把今天剩下的事情理清楚，先从最重要的一件开始。"],
        ["seq": 2, "role": "assistant", "content": "可以。先把**必须今天完成**的事情发给我，我会替你排出顺序，也会把可以推迟的部分单独放好。"],
        ["seq": 3, "role": "user", "content": "先准备明早的产品评审，再提醒我晚上给家里回电话。"]
    ]
    var body: some View {
        VStack(spacing: 0) {
            PersonalConversationHeader(agents: [.init(id: "twin", name: "twin")], name: "twin", status: "已就绪",
                activity: "idle", activityCount: 2, chooseAgent: { _ in }, showComputers: {}, showActivity: {})
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 18) {
                    ForEach(messages, id: \.fixtureIdentity) { record in
                        PersonalMessageRow(record: record, agentName: "twin", attachmentsEnabled: true, openAttachment: { _ in })
                    }
                }.padding(.horizontal, 16).padding(.vertical, 20)
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            PersonalComposer(text: $text, attachments: $attachments, model: model, composerID: UUID(), available: true,
                enabled: true, sending: false, pending: false, onSend: {}, onDiscard: {})
        }
        .overlay {
            TextField("", text: $keyboardText)
                .frame(width: 1, height: 1).opacity(0.01)
                .focused($keyboardPresented)
                .accessibilityHidden(true)
        }
        .task {
            if ProcessInfo.processInfo.arguments.contains("--native-keyboard-fixture") { keyboardPresented = true }
        }
    }
}

private extension JSONValue {
    var fixtureIdentity: Double { self["seq"].number ?? 0 }
}

#Preview("Twin conversation") {
    PersonalUIFixtureView().modifier(MobileTypography())
}
#endif
