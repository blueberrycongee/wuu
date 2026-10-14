import SwiftUI
import WebKit

private enum MermaidDocument {
    static let html: String = {
        guard let url = Bundle.main.url(forResource: "mermaid", withExtension: "html", subdirectory: "NativeUI"),
              let html = try? String(contentsOf: url, encoding: .utf8) else {
            return "<p>无法加载图表渲染器</p>"
        }
        return html
    }()
    static let dataStore = WKWebsiteDataStore.nonPersistent()
}

struct MermaidDiagram: View {
    let code: String
    let live: Bool
    @Environment(\.colorScheme) private var scheme
    @Environment(\.mobileTextSize) private var textSize
    @State private var height: CGFloat = 96
    @State private var showingSource = false
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Button(showingSource ? "显示图表" : "查看源码") { showingSource.toggle() }
                Spacer()
                Button("复制代码") { UIPasteboard.general.string = code; Haptics.tap() }
            }.font(.caption).frame(minHeight: 44)
            if showingSource {
                ScrollView(.horizontal) {
                    Text(code).font(.system(size: textSize * MobileTypography.baseCodeSize / MobileTypography.baseTextSize, design: .monospaced))
                        .textSelection(.enabled).fixedSize(horizontal: true, vertical: false)
                }
            } else {
                MermaidSurface(code: code, live: live, dark: scheme == .dark, fontSize: textSize, height: $height)
                    .frame(height: height)
            }
        }
    }
}

private struct MermaidSurface: UIViewRepresentable {
    let code: String
    let live: Bool
    let dark: Bool
    let fontSize: CGFloat
    @Binding var height: CGFloat
    func makeCoordinator() -> Coordinator { Coordinator(height: $height) }
    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = MermaidDocument.dataStore
        configuration.userContentController.add(context.coordinator, name: "diagramHeight")
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.isOpaque = false; view.backgroundColor = .clear
        view.scrollView.backgroundColor = .clear
        view.scrollView.bounces = false
        view.scrollView.contentInsetAdjustmentBehavior = .never
        view.navigationDelegate = context.coordinator
        view.loadHTMLString(MermaidDocument.html, baseURL: nil)
        return view
    }
    func updateUIView(_ view: WKWebView, context: Context) {
        context.coordinator.height = $height
        context.coordinator.update(["code": code, "live": live, "dark": dark, "fontSize": fontSize], view: view)
    }
    static func dismantleUIView(_ view: WKWebView, coordinator: Coordinator) {
        view.stopLoading(); view.navigationDelegate = nil
        view.configuration.userContentController.removeScriptMessageHandler(forName: "diagramHeight")
    }
    final class Coordinator: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
        var height: Binding<CGFloat>
        private var loaded = false
        private var payload: [String: Any] = [:]
        private var lastPayload: NSDictionary = [:]
        init(height: Binding<CGFloat>) { self.height = height }
        func update(_ value: [String: Any], view: WKWebView) {
            guard !lastPayload.isEqual(to: value) else { return }
            payload = value; lastPayload = value as NSDictionary; render(view)
        }
        private func render(_ view: WKWebView) {
            guard loaded else { return }
            // Arguments are serialized by WebKit, never interpolated into source.
            view.callAsyncJavaScript("window.renderWuuMermaid(input)", arguments: ["input": payload], in: nil, in: .page)
        }
        func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            guard message.frameInfo.isMainFrame, let number = message.body as? NSNumber else { return }
            let value = CGFloat(number.doubleValue)
            guard value.isFinite else { return }
            let measured = min(600, max(64, value))
            if abs(height.wrappedValue - measured) > 0.5 { height.wrappedValue = measured }
        }
        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) { loaded = true; render(webView) }
        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            decisionHandler(action.request.url?.absoluteString == "about:blank" ? .allow : .cancel)
        }
        func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
            loaded = false; webView.loadHTMLString(MermaidDocument.html, baseURL: nil)
        }
    }
}
