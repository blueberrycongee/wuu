import SwiftUI
import MarkdownUI

struct MessageText: View {
    private static let markdownByteLimit = 128 * 1024
    let text: String
    let markdown: Bool
    var contextMenuEnabled = true
    var isLive = false
    @Environment(\.mobileTextSize) private var textSize
    @StateObject private var renderState: MessageMarkdown
    private var input: MessageMarkdown.Input { .init(text: text, live: isLive) }
    @State private var selecting = false
    init(text: String, markdown: Bool, contextMenuEnabled: Bool = true, isLive: Bool = false) {
        self.text = text
        self.markdown = markdown
        self.contextMenuEnabled = contextMenuEnabled
        self.isLive = isLive
        // StateObject initializes once per row identity, before the lazy stack measures it.
        _renderState = StateObject(wrappedValue: MessageMarkdown(.init(
            text: markdown && text.utf8.count <= Self.markdownByteLimit ? text : "", live: isLive)))
    }
    var body: some View {
        if contextMenuEnabled {
            rendered.textSelection(.enabled)
                .contextMenu {
                    Button("复制", systemImage: "doc.on.doc") { UIPasteboard.general.string = text; Haptics.tap() }
                    Button("选择文本", systemImage: "selection.pin.in.out") { selecting = true }
                    ShareLink(item: text) { Label("分享", systemImage: "square.and.arrow.up") }
                }
                .sheet(isPresented: $selecting) { TextSelectionSheet(text: text) }
        } else {
            rendered
        }
    }
    private var rendered: some View {
        Group {
            if markdown, text.utf8.count <= Self.markdownByteLimit {
                MessageMarkdownView(rendered: renderState)
                    .task(id: input) { renderState.submit(input) }
                    .onDisappear { renderState.stop() }
            } else {
                Text(text).font(.system(size: textSize)).lineSpacing(textSize * 0.2)
            }
        }
    }
}

/// Long-press selection in a bubble selects the whole message; this sheet allows any range.
struct TextSelectionSheet: View {
    let text: String
    @Environment(\.dismiss) private var dismiss
    @Environment(\.mobileTextSize) private var textSize
    var body: some View {
        NavigationStack {
            SelectableText(text: text, size: textSize)
                .ignoresSafeArea(edges: .bottom)
                .navigationTitle("选择文本").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("完成") { dismiss() } } }
        }
    }
}

private struct SelectableText: UIViewRepresentable {
    let text: String
    let size: CGFloat
    func makeUIView(context: Context) -> UITextView {
        let view = UITextView()
        view.accessibilityIdentifier = "message-selection-text"
        view.isEditable = false; view.isSelectable = true
        view.adjustsFontForContentSizeCategory = false
        view.backgroundColor = .clear
        view.textContainerInset = UIEdgeInsets(top: 16, left: 16, bottom: 32, right: 16)
        return view
    }
    func updateUIView(_ view: UITextView, context: Context) {
        if view.text != text { view.text = text }
        view.font = .systemFont(ofSize: size)
        view.textColor = .label
    }
}

// Only committed snapshots invalidate the Markdown tree; incoming tokens update the owner.
private struct MessageMarkdownView: View {
    @ObservedObject var rendered: MessageMarkdown
    @Environment(\.mobileTextSize) private var textSize
    var body: some View {
        Markdown(rendered.content)
            // MarkdownUI scales its base font internally; pass unscaled points here.
            .markdownTextStyle { FontSize(MobileTypography.baseTextSize) }
            .markdownTextStyle(\.code) {
                FontFamilyVariant(.monospaced)
                FontSize(.rem(MobileTypography.baseCodeSize / MobileTypography.baseTextSize))
            }
            .markdownBlockStyle(\.paragraph) { block in
                StreamingTextReveal(text: block.content.renderPlainText(), reveal: rendered.reveal) {
                    block.label.fixedSize(horizontal: false, vertical: true)
                        .lineSpacing(textSize * 0.2)
                }.markdownMargin(top: .zero, bottom: .em(0.8))
            }
            .markdownImageProvider(ExternalImageLink())
            .markdownInlineImageProvider(ExternalImageLink())
            .markdownBlockStyle(\.codeBlock) { block in
                VStack(alignment: .leading, spacing: 8) {
                    if block.language?.split(whereSeparator: { $0.isWhitespace }).first?.lowercased() == "mermaid" {
                        MermaidDiagram(code: block.content, live: rendered.live)
                    } else {
                        HStack {
                            Text(block.language ?? "代码").font(.caption).foregroundStyle(.secondary)
                            Spacer()
                            Button("复制代码") { UIPasteboard.general.string = block.content; Haptics.tap() }.font(.caption)
                                .frame(minHeight: 44)
                        }
                        ScrollView(.horizontal) {
                            StreamingTextReveal(text: block.content, reveal: rendered.reveal) {
                                block.label.markdownTextStyle { FontSize(.rem(MobileTypography.baseCodeSize / MobileTypography.baseTextSize)) }
                                    .fixedSize(horizontal: true, vertical: false)
                            }
                        }
                    }
                }.padding(12).background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
            }
            .environment(\.openURL, OpenURLAction { url in
                guard ["https", "http"].contains(url.scheme?.lowercased() ?? ""), url.host != nil else { return .discarded }
                return .systemAction
            })
            .markdownTheme(revealTheme)
    }

    private var revealTheme: Theme {
        var theme = Theme.basic
        let headings: [(WritableKeyPath<Theme, BlockStyle<BlockConfiguration>>, Double)] = [
            (\.heading1, 2), (\.heading2, 1.5), (\.heading3, 1.17),
            (\.heading4, 1), (\.heading5, 0.83), (\.heading6, 0.67)
        ]
        for (key, size) in headings {
            theme[keyPath: key] = BlockStyle { block in
                StreamingTextReveal(text: block.content.renderPlainText(), reveal: rendered.reveal) {
                    block.label.markdownTextStyle { FontWeight(.semibold); FontSize(.em(size)) }
                }.markdownMargin(top: .rem(1.5), bottom: .rem(1))
            }
        }
        theme.tableCell = BlockStyle { block in
            StreamingTextReveal(text: block.content.renderPlainText(), reveal: rendered.reveal) {
                block.label.markdownTextStyle { if block.row == 0 { FontWeight(.semibold) } }
                    .fixedSize(horizontal: false, vertical: true)
                    .relativeLineSpacing(.em(0.15))
            }
            .relativePadding(.horizontal, length: .em(0.72))
            .relativePadding(.vertical, length: .em(0.35))
        }
        return theme
    }
}

@MainActor private final class MessageMarkdown: ObservableObject {
    struct Input: Equatable { let text: String; let live: Bool }
    @Published var content: MarkdownContent
    @Published var live: Bool
    @Published var reveal = StreamingTextRevealState()
    private var desired: Input
    private var rendered: Input
    private var work: Task<Void, Never>?
    private var generation = 0
    private var lastParse = ContinuousClock.now

    init(_ input: Input) {
        let value = ParsedMarkdown(input.text)
        content = value.content
        live = input.live
        desired = input
        rendered = input
        reveal.update(value.plainText, animated: false)
    }

    func submit(_ input: Input) {
        desired = input
        guard work == nil, rendered != input else { return }
        let stamp = generation
        work = Task {
            while rendered != desired {
                if rendered.text == desired.text { rendered = desired; live = desired.live; break }
                // A fixed cadence cannot be starved by continuously arriving tokens.
                if desired.live {
                    let deadline = lastParse + .milliseconds(100)
                    do { try await Task.sleep(until: deadline, clock: .continuous) } catch { return }
                }
                guard !Task.isCancelled, generation == stamp else { return }
                let snapshot = desired
                let value = await Task.detached(priority: .userInitiated) { ParsedMarkdown(snapshot.text) }.value
                guard !Task.isCancelled, generation == stamp else { return }
                // A replacement owns the next frame; an older append snapshot is still useful.
                if desired.text.hasPrefix(snapshot.text) {
                    reveal.update(value.plainText, animated: snapshot.live || rendered.live)
                    content = value.content
                    live = snapshot.live
                    rendered = snapshot
                }
                lastParse = .now
            }
            work = nil
        }
    }
    func stop() {
        generation += 1
        work?.cancel(); work = nil
        reveal.chunks.removeAll()
    }
}

// MarkdownUI 2.4.1 predates Sendable. Its parsed content is an immutable tree of
// value-type blocks, inline nodes and strings, with no retained parser pointers.
private struct ParsedMarkdown: @unchecked Sendable {
    let content: MarkdownContent
    let plainText: String
    init(_ text: String) { content = MarkdownContent(text); plainText = content.renderPlainText() }
}

// Remote Markdown must not fetch tracking images just because a conversation was opened.
private struct ExternalImageLink: ImageProvider, InlineImageProvider {
    func makeImage(url: URL?) -> some View {
        if let url, ["https", "http"].contains(url.scheme ?? "") { Link("在浏览器查看图片", destination: url).font(.caption) }
    }
    func image(with url: URL, label: String) async throws -> Image { Image(systemName: "photo") }
}
