import SwiftUI
import MarkdownUI

struct MessageText: View {
    let text: String
    let markdown: Bool
    @Environment(\.mobileTextSize) private var textSize
    @State private var content = MarkdownContent("")
    @State private var renderedText: String?
    var body: some View {
        Group {
            if markdown, text.utf8.count <= 128 * 1024 {
                Markdown(content)
                    // MarkdownUI scales its base font internally; pass unscaled points here.
                    .markdownTextStyle { FontSize(MobileTypography.baseTextSize) }
                    .markdownTextStyle(\.code) {
                        FontFamilyVariant(.monospaced)
                        FontSize(.rem(MobileTypography.baseCodeSize / MobileTypography.baseTextSize))
                    }
                    .markdownBlockStyle(\.paragraph) { block in
                        block.label.fixedSize(horizontal: false, vertical: true)
                            .lineSpacing(textSize * 0.2).markdownMargin(top: .zero, bottom: .em(0.8))
                    }
                    .markdownImageProvider(ExternalImageLink())
                    .markdownInlineImageProvider(ExternalImageLink())
                    .markdownBlockStyle(\.codeBlock) { block in
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Text(block.language ?? "代码").font(.caption).foregroundStyle(.secondary)
                                Spacer()
                                Button("复制代码") { UIPasteboard.general.string = block.content }.font(.caption)
                            }
                            ScrollView(.horizontal) {
                                block.label.markdownTextStyle { FontSize(.rem(MobileTypography.baseCodeSize / MobileTypography.baseTextSize)) }
                                    .fixedSize(horizontal: true, vertical: false)
                            }
                        }.padding(12).background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
                    }
                    .environment(\.openURL, OpenURLAction { url in
                        guard ["https", "http"].contains(url.scheme?.lowercased() ?? ""), url.host != nil else { return .discarded }
                        return .systemAction
                    })
                    .task(id: text) {
                        guard renderedText != text else { return }
                        // Coalesce token bursts before parsing; never parse a long reply on the UI actor.
                        do { try await Task.sleep(for: .milliseconds(40)) } catch { return }
                        let value = await Task.detached(priority: .userInitiated) { ParsedMarkdown(text) }.value
                        guard !Task.isCancelled else { return }
                        content = value.content; renderedText = text
                    }
            } else {
                Text(text).font(.system(size: textSize)).lineSpacing(textSize * 0.2)
            }
        }.textSelection(.enabled)
            .contextMenu { Button("复制消息") { UIPasteboard.general.string = text } }
    }
}

// MarkdownUI 2.4.1 predates Sendable. Its parsed content is an immutable tree of
// value-type blocks, inline nodes and strings, with no retained parser pointers.
private struct ParsedMarkdown: @unchecked Sendable {
    let content: MarkdownContent
    init(_ text: String) { content = MarkdownContent(text) }
}

// Remote Markdown must not fetch tracking images just because a conversation was opened.
private struct ExternalImageLink: ImageProvider, InlineImageProvider {
    func makeImage(url: URL?) -> some View {
        if let url, ["https", "http"].contains(url.scheme ?? "") { Link("在浏览器查看图片", destination: url).font(.caption) }
    }
    func image(with url: URL, label: String) async throws -> Image { Image(systemName: "photo") }
}
