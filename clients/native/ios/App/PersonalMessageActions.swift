import SwiftUI
import WuuCore

struct PersonalMessageSelection: Identifiable {
    let id: Int
    let text: String
    let user: Bool
    let frame: CGRect
}

@MainActor @Observable final class PersonalMessageActions {
    var selection: PersonalMessageSelection?
    var selecting: PersonalMessageSelection?
    var presented = false
    var closing = false

    func open(_ value: PersonalMessageSelection) {
        guard selection == nil, value.frame.width > 0, !value.text.isEmpty else { return }
        selection = value
        #if DEBUG
        PersonalMenuFixtureAudit.record(["event": "open", "id": value.id])
        #endif
        Haptics.tap()
    }
    func reset() {
        #if DEBUG
        PersonalMenuFixtureAudit.record(["event": "reset", "hadSelection": selection != nil])
        #endif
        selection = nil; selecting = nil; presented = false; closing = false
    }
}

struct PersonalMessageBody: View {
    let text: String
    let user: Bool
    var body: some View {
        MessageText(text: text, markdown: !user, contextMenuEnabled: false)
            .foregroundStyle(user ? Color.white : Color.primary)
            .tint(user ? Color.white : personalAccent)
            .padding(.horizontal, user ? 16 : 0)
            .padding(.vertical, user ? 10 : 0)
            .background(user ? personalAccent : Color.clear,
                        in: RoundedRectangle(cornerRadius: 22, style: .continuous))
    }
}

struct PersonalMessageInteraction: ViewModifier {
    let id: Int
    let text: String
    let user: Bool
    @Environment(PersonalMessageActions.self) private var actions
    @State private var frame = CGRect.zero
    func body(content: Content) -> some View {
        content
            .onGeometryChange(for: CGRect.self) { proxy in proxy.frame(in: .global) } action: { frame = $0 }
            .opacity(actions.selection?.id == id ? 0 : 1)
            .contentShape(Rectangle())
            .onLongPressGesture(minimumDuration: 0.45, maximumDistance: 12) { open() }
            .accessibilityAction(named: "消息操作") { open() }
            .task(id: frame) {
                #if DEBUG
                guard id == 2, ProcessInfo.processInfo.arguments.contains("--native-menu-fixture") else { return }
                do { try await Task.sleep(for: .milliseconds(900)) } catch { return }
                open()
                if ProcessInfo.processInfo.arguments.contains("--native-menu-repeat") {
                    do { try await Task.sleep(for: .milliseconds(100)) } catch { return }
                    open()
                    do { try await Task.sleep(for: .milliseconds(1300)) } catch { return }
                    open()
                }
                #endif
            }
    }
    private func open() { actions.open(.init(id: id, text: text, user: user, frame: frame)) }
}

struct PersonalMessageActionsHost: ViewModifier {
    let context: String
    let reply: ((String) -> Void)?
    @State private var actions = PersonalMessageActions()
    @Environment(\.scenePhase) private var phase
    @Environment(\.colorScheme) private var colorScheme
    private var overlaySurface: Color { Color(uiColor: colorScheme == .dark ? .secondarySystemBackground : .systemBackground) }
    @Environment(\.accessibilityReduceMotion) private var systemReduceMotion
    private var reduceMotion: Bool {
        #if DEBUG
        systemReduceMotion || ProcessInfo.processInfo.arguments.contains("--native-menu-reduced")
        #else
        systemReduceMotion
        #endif
    }
    @AccessibilityFocusState private var menuFocused: Bool
    @ScaledMetric(relativeTo: .body) private var rowHeight: CGFloat = 48
    @ScaledMetric(relativeTo: .body) private var menuFont: CGFloat = 15

    func body(content: Content) -> some View {
        content
            .environment(actions)
            .accessibilityHidden(actions.selection != nil)
            .overlay {
                GeometryReader { proxy in
                    if let selected = actions.selection {
                        overlay(selected, proxy: proxy)
                            .id(selected.id)
                    }
                }
            }
            .sheet(item: $actions.selecting) { selected in TextSelectionSheet(text: selected.text) }
            .onChange(of: context) { _, _ in actions.reset() }
            .onChange(of: phase) { _, value in if value != .active { actions.reset() } }
            .onDisappear { actions.reset() }
    }

    private func overlay(_ selected: PersonalMessageSelection, proxy: GeometryProxy) -> some View {
        let origin = proxy.frame(in: .global).origin
        let source = selected.frame.offsetBy(dx: -origin.x, dy: -origin.y)
        let toolHeight = rowHeight * (reply == nil ? 2 : 3) + 16
        let layout = PersonalMessageMenuLayout(source: source, viewport: CGRect(origin: .zero, size: proxy.size), toolsHeight: toolHeight, toolsWidth: max(200, rowHeight * 4.2), alignTrailing: selected.user)
        let expanded = actions.presented || reduceMotion
        return ZStack(alignment: .topLeading) {
            Color.black.opacity(actions.presented ? 0.51 : 0)
                .ignoresSafeArea()
                .contentShape(Rectangle())
                .onTapGesture { close() }
                .accessibilityLabel("关闭消息操作")
                .accessibilityAddTraits(.isButton)
                .animation(.easeOut(duration: 0.14), value: actions.presented)

            ScrollView {
                PersonalMessageBody(text: selected.text, user: selected.user)
                    .frame(width: layout.message.width, alignment: .leading)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .scrollDisabled(source.height <= layout.message.height)
            .frame(width: layout.message.width, height: layout.message.height)
            .background {
                RoundedRectangle(cornerRadius: 22).fill(overlaySurface)
                    .padding(selected.user ? 0 : -12)
            }
            .offset(x: layout.message.minX, y: expanded ? layout.message.minY : source.minY)
            .animation(movement, value: actions.presented)
            .accessibilityLabel(selected.text)

            ScrollView {
                VStack(spacing: 0) {
                    action("复制", icon: .copy) { UIPasteboard.general.string = selected.text; close() }
                    action("选择文本", icon: .select) { close { actions.selecting = selected } }
                    if let reply {
                        action("引用文本", icon: .reply) { close { reply(selected.text) } }
                    }
                }
                .padding(.vertical, 8)
                .frame(width: layout.tools.width)
            }
            .frame(width: layout.tools.width, height: layout.tools.height)
            .background(overlaySurface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
            .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
            .offset(x: layout.tools.minX, y: layout.tools.minY + (expanded ? 0 : 30))
            .opacity(actions.presented ? 1 : 0)
            .animation(movement, value: actions.presented)
            .accessibilityFocused($menuFocused)
        }
        .accessibilityAddTraits(.isModal)
        .accessibilityAction(.escape) { close() }
        .onAppear {
            actions.presented = true; menuFocused = true
            #if DEBUG
            PersonalMenuFixtureAudit.record([
                "event": "layout", "reduceMotion": reduceMotion,
                "source": PersonalMenuFixtureAudit.rect(source),
                "message": PersonalMenuFixtureAudit.rect(layout.message),
                "tools": PersonalMenuFixtureAudit.rect(layout.tools),
                "viewport": [proxy.size.width, proxy.size.height],
                "globalOrigin": [origin.x, origin.y],
                "safeArea": [proxy.safeAreaInsets.top, proxy.safeAreaInsets.bottom],
                "window": [UIScreen.main.bounds.width, UIScreen.main.bounds.height]
            ])
            #endif
        }
        .task {
            #if DEBUG
            let cancelOpening = ProcessInfo.processInfo.arguments.contains("--native-menu-cancel-opening")
            guard cancelOpening || ProcessInfo.processInfo.arguments.contains("--native-menu-cycle") else { return }
            do { try await Task.sleep(for: .milliseconds(cancelOpening ? 50 : 850)) } catch { return }
            close()
            #endif
        }
        .onChange(of: proxy.size) { _, _ in actions.reset() }
    }

    private var movement: Animation {
        if reduceMotion { return .easeOut(duration: 0.14) }
        return actions.closing ? .easeInOut(duration: 0.2) : Animation(PersonalLiftAnimation())
    }
    private func action(_ title: String, icon: PersonalMessageActionIcon.Kind, perform: @escaping () -> Void) -> some View {
        Button(action: perform) {
            HStack(spacing: 14) {
                PersonalMessageActionIcon(kind: icon).frame(width: 18, height: 18)
                Text(title).font(.system(size: menuFont)).foregroundStyle(.primary)
                Spacer(minLength: 0)
            }
            .frame(minHeight: rowHeight).padding(.horizontal, 18)
            .contentShape(Rectangle())
        }.buttonStyle(.plain).disabled(actions.closing)
    }
    private func close(then: @escaping () -> Void = {}) {
        guard actions.selection != nil, !actions.closing else { return }
        actions.closing = true
        withAnimation(reduceMotion ? .easeOut(duration: 0.14) : .easeInOut(duration: 0.2), completionCriteria: .removed) {
            actions.presented = false
        } completion: {
            guard actions.closing else { return }
            actions.reset(); then()
        }
    }
}

private struct PersonalLiftAnimation: CustomAnimation {
    func animate<V: VectorArithmetic>(value: V, time: TimeInterval, context: inout AnimationContext<V>) -> V? {
        guard time < 0.48 else { return nil }
        let progress = 1 - (1 + 20 * time) * exp(-20 * time)
        return value.scaled(by: progress)
    }
}

#if DEBUG
/// Exports only synthetic fixture geometry, never account or message contents.
private enum PersonalMenuFixtureAudit {
    static func rect(_ value: CGRect) -> [CGFloat] { [value.minX, value.minY, value.width, value.height] }
    static func record(_ value: [String: Any]) {
        let arguments = ProcessInfo.processInfo.arguments
        guard arguments.contains("--native-personal-fixture"), arguments.contains("--native-menu-audit") else { return }
        let path = FileManager.default.temporaryDirectory.appendingPathComponent("PersonalMenuAudit-\(ProcessInfo.processInfo.processIdentifier).jsonl")
        do {
            var data = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
            data.append(10)
            if FileManager.default.fileExists(atPath: path.path) {
                let file = try FileHandle(forWritingTo: path)
                defer { try? file.close() }
                try file.seekToEnd(); try file.write(contentsOf: data)
            } else { try data.write(to: path, options: .atomic) }
        } catch { NSLog("Personal menu fixture audit failed: %@", String(describing: error)) }
    }
}
#endif

private struct PersonalMessageActionIcon: View {
    enum Kind { case copy, select, reply }
    let kind: Kind
    var body: some View {
        Canvas { context, size in
            let scale = min(size.width, size.height) / 20
            context.scaleBy(x: scale, y: scale)
            var path = Path()
            switch kind {
            case .copy:
                path.addRoundedRect(in: CGRect(x: 3, y: 7, width: 10, height: 11), cornerSize: CGSize(width: 2, height: 2))
                path.move(to: CGPoint(x: 7, y: 7)); path.addLine(to: CGPoint(x: 7, y: 4))
                path.addQuadCurve(to: CGPoint(x: 9, y: 2), control: CGPoint(x: 7, y: 2))
                path.addLine(to: CGPoint(x: 16, y: 2)); path.addQuadCurve(to: CGPoint(x: 18, y: 4), control: CGPoint(x: 18, y: 2))
                path.addLine(to: CGPoint(x: 18, y: 12)); path.addQuadCurve(to: CGPoint(x: 16, y: 14), control: CGPoint(x: 18, y: 14))
                path.addLine(to: CGPoint(x: 13, y: 14))
            case .select:
                path.addRoundedRect(in: CGRect(x: 4, y: 2, width: 12, height: 16), cornerSize: CGSize(width: 2, height: 2))
                path.move(to: CGPoint(x: 7, y: 7)); path.addLine(to: CGPoint(x: 13, y: 7))
                path.move(to: CGPoint(x: 7, y: 11)); path.addLine(to: CGPoint(x: 11, y: 11))
            case .reply:
                path.move(to: CGPoint(x: 3, y: 5)); path.addLine(to: CGPoint(x: 3, y: 10))
                path.addQuadCurve(to: CGPoint(x: 6, y: 13), control: CGPoint(x: 3, y: 13))
                path.addLine(to: CGPoint(x: 17, y: 13))
                path.move(to: CGPoint(x: 13, y: 9)); path.addLine(to: CGPoint(x: 17, y: 13)); path.addLine(to: CGPoint(x: 13, y: 17))
            }
            context.stroke(path, with: .foreground, style: StrokeStyle(lineWidth: 1.5, lineCap: .round, lineJoin: .round))
        }.accessibilityHidden(true)
    }
}
