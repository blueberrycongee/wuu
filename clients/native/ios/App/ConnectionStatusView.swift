import SwiftUI

struct ConnectionStatusView: View {
    let connecting: Bool
    let offlineMessage: String
    var detail = ""
    var reconnect: () -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.scenePhase) private var phase
    @State private var visible = false
    @State private var started = Date.now
    private let connectingMessage = "正在连接电脑…"
    private var animating: Bool { connecting && visible && phase == .active && !reduceMotion }

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 30, paused: !animating)) { context in
            let elapsed = max(0, context.date.timeIntervalSince(started))
            HStack(spacing: 12) {
                if connecting {
                    WuuConnectionMark(elapsed: elapsed, animated: animating)
                        .accessibilityHidden(true)
                    // Reserve the full label's footprint while it types, including wrapped lines.
                    ZStack(alignment: .topLeading) {
                        Text(connectingMessage).hidden().accessibilityHidden(true)
                        Text(animating ? String(connectingMessage.prefix(Int(elapsed / 0.08) + 1)) : connectingMessage)
                            .accessibilityLabel(connectingMessage)
                    }
                } else {
                    Text(offlineMessage)
                        .accessibilityHint(detail)
                }
                Spacer(minLength: 0)
                if !connecting {
                    Button("重连", action: reconnect)
                        .foregroundStyle(.primary).frame(minWidth: 44, minHeight: 44)
                }
            }
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .frame(minHeight: 44)
            .padding(.horizontal, 20).padding(.vertical, 8)
        }
        .onAppear { visible = true }
        .onDisappear { visible = false }
        .onChange(of: animating) { _, active in if active { started = .now } }
    }
}

private struct WuuConnectionMark: View {
    let elapsed: TimeInterval
    let animated: Bool
    @Environment(\.colorScheme) private var scheme
    @ScaledMetric(relativeTo: .body) private var size: CGFloat = 24
    private static let curve = UnitCurve.bezier(startControlPoint: .init(x: 0.2, y: 0), endControlPoint: .init(x: 0, y: 1))
    private var green: Color {
        scheme == .dark ? Color(red: 76 / 255, green: 195 / 255, blue: 138 / 255)
            : Color(red: 31 / 255, green: 157 / 255, blue: 85 / 255)
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: size * 0.075) {
            ForEach(0..<3) { index in
                let motion = letterMotion(index: index)
                Text(index == 0 ? "w" : "u")
                    .offset(y: motion.y * size)
                    .opacity(motion.opacity)
            }
            RoundedRectangle(cornerRadius: size * 0.04)
                .frame(width: size * 0.2, height: size * 0.8)
                .offset(y: size * 0.1)
                .padding(.leading, size * 0.15)
                .opacity(!animated || elapsed.truncatingRemainder(dividingBy: 0.9) < 0.414 ? 1 : 0)
        }
        .font(.system(size: size, weight: .bold))
        .foregroundStyle(green)
        .fixedSize()
        .padding(.top, size * 0.2)
    }

    private func letterMotion(index: Int) -> (y: CGFloat, opacity: Double) {
        guard animated else { return (0, 1) }
        // Match desktop turns.css: wuu-mark-rise keyframes, easing, and stagger.
        let delayed = elapsed - Double(index) * 0.1
        guard delayed >= 0 else { return (0, 1) }
        let progress = delayed.truncatingRemainder(dividingBy: 1.4) / 1.4
        if progress < 0.38 {
            let eased = Self.curve.value(at: progress / 0.38)
            return (-0.2 * eased, 0.64 + 0.36 * eased)
        }
        if progress < 0.7 {
            let eased = Self.curve.value(at: (progress - 0.38) / 0.32)
            return (-0.2 * (1 - eased), 1 - 0.14 * eased)
        }
        let eased = Self.curve.value(at: (progress - 0.7) / 0.3)
        return (0, 0.86 - 0.22 * eased)
    }
}

#Preview("Connecting") {
    ConnectionStatusView(connecting: true, offlineMessage: "电脑离线 · 消息只读") {}
}

#Preview("Offline") {
    ConnectionStatusView(connecting: false, offlineMessage: "电脑离线 · 消息只读") {}
}
