import SwiftUI
import MarkdownUI

struct MessageText: View {
    let text: String
    let markdown: Bool
    @Environment(\.mobileTextSize) private var textSize
    @State private var content = MarkdownContent("")
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
                    .task(id: text) { content = MarkdownContent(text) }
            } else {
                Text(text).font(.system(size: textSize)).lineSpacing(textSize * 0.2)
            }
        }.textSelection(.enabled)
            .contextMenu { Button("复制消息") { UIPasteboard.general.string = text } }
    }
}

// Remote Markdown must not fetch tracking images just because a conversation was opened.
private struct ExternalImageLink: ImageProvider, InlineImageProvider {
    func makeImage(url: URL?) -> some View {
        if let url, ["https", "http"].contains(url.scheme ?? "") { Link("在浏览器查看图片", destination: url).font(.caption) }
    }
    func image(with url: URL, label: String) async throws -> Image { Image(systemName: "photo") }
}
