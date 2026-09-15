import SwiftUI
import WebKit
import WuuCore

struct ConversationActivityMark: View {
    var activity = "thinking"
    var settings: ThreadSettings? = nil
    var body: some View {
        SharedAvatar(value: ["conversation": true, "activity": .string(activity),
            "provider": .string(settings?.provider ?? ""), "model": .string(settings?.model ?? "")], size: 28)
    }
}

private enum AvatarDocument {
    static let html: String = {
        guard let url = Bundle.main.url(forResource: "mascot", withExtension: "html", subdirectory: "NativeUI") else { return "" }
        return (try? String(contentsOf: url, encoding: .utf8)) ?? ""
    }()
    static let dataStore = WKWebsiteDataStore.nonPersistent()
}

struct SharedAvatar: View {
    let value: JSONValue
    let size: CGFloat
    var width: CGFloat? = nil
    var accessible = false
    @Environment(\.colorScheme) private var scheme
    @Environment(\.scenePhase) private var phase
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    var body: some View {
        AvatarSurface(payload: payload, accessible: accessible).frame(width: width ?? size, height: size)
            .allowsHitTesting(accessible).accessibilityHidden(!accessible)
    }
    private var payload: String {
        guard case .object(var fields) = value else { return "{}" }
        fields["size"] = .number(Double(size))
        fields["width"] = .number(Double(width ?? size))
        fields["dark"] = .bool(scheme == .dark); fields["paused"] = .bool(phase != .active); fields["reducedMotion"] = .bool(reduceMotion)
        return (try? String(decoding: JSONEncoder().encode(JSONValue.object(fields)), as: UTF8.self)) ?? "{}"
    }
}

private struct AvatarSurface: UIViewRepresentable {
    let payload: String
    let accessible: Bool
    func makeCoordinator() -> Coordinator { Coordinator() }
    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = AvatarDocument.dataStore
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.isOpaque = false; view.backgroundColor = .clear
        view.scrollView.backgroundColor = .clear; view.scrollView.isScrollEnabled = false
        view.scrollView.contentInsetAdjustmentBehavior = .never
        view.isUserInteractionEnabled = accessible; view.isAccessibilityElement = false; view.accessibilityElementsHidden = !accessible
        view.navigationDelegate = context.coordinator
        view.loadHTMLString(AvatarDocument.html, baseURL: nil)
        return view
    }
    func updateUIView(_ view: WKWebView, context: Context) { context.coordinator.update(payload, view: view) }
    static func dismantleUIView(_ view: WKWebView, coordinator: Coordinator) {
        view.evaluateJavaScript("document.documentElement.setAttribute('data-renderer-hidden','')")
        view.stopLoading(); view.navigationDelegate = nil
    }
    final class Coordinator: NSObject, WKNavigationDelegate {
        private var loaded = false
        private var payload = "{}"
        func update(_ value: String, view: WKWebView) {
            guard payload != value else { return }
            payload = value; render(view)
        }
        private func render(_ view: WKWebView) { if loaded { view.evaluateJavaScript("window.renderWuuAvatar(\(payload))") } }
        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { loaded = true; render(webView) }
        func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            decisionHandler(navigationAction.request.url?.absoluteString == "about:blank" ? .allow : .cancel)
        }
        func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
            loaded = false; webView.loadHTMLString(AvatarDocument.html, baseURL: nil)
        }
    }
}

func avatarRecord(_ agent: CollaborationAgent?) -> JSONValue {
    ["id": .string(agent?.id ?? "wuu"), "avatar_key": .string(agent?.value["avatar_key"].string ?? "abstract-1"),
     "avatar_image": .string(agent?.value["avatar_image"].string ?? ""), "activity_status": .string(agent?.value["activity_status"].string ?? "idle")]
}
