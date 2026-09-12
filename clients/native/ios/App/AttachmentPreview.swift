import SwiftUI
import QuickLook
import ImageIO
import UniformTypeIdentifiers
import WuuCore

private struct AttachmentDocument: FileDocument {
    static var readableContentTypes: [UTType] { [.data] }
    let data: Data
    init(data: Data) { self.data = data }
    init(configuration: ReadConfiguration) throws { data = configuration.file.regularFileContents ?? Data() }
    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper { FileWrapper(regularFileWithContents: data) }
}

struct AttachmentPreview: View {
    let attachment: LoadedAttachment
    @Environment(\.dismiss) private var dismiss
    @State private var url: URL?
    @State private var error: String?
    @State private var exporting = false
    private var contentType: UTType { UTType(mimeType: attachment.mediaType) ?? .data }
    var body: some View {
        NavigationStack {
            Group {
                if let url { NativePreview(url: url) }
                else if let error { Text(error).padding() }
                else { ProgressView("正在读取附件…") }
            }.navigationTitle(attachment.filename).navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("关闭") { dismiss() } }
                    ToolbarItem(placement: .primaryAction) { Button("保存原文件") { exporting = true } }
                }
                .fileExporter(isPresented: $exporting, document: AttachmentDocument(data: attachment.data), contentType: contentType,
                    defaultFilename: "attachment." + (contentType.preferredFilenameExtension ?? "bin")) { result in
                    if case .failure(let failure) = result { error = failure.localizedDescription }
                }
        }.task(id: attachment.id) {
            do {
                let file = try await Task.detached(priority: .userInitiated) { try previewFile(attachment) }.value
                guard !Task.isCancelled else { try? FileManager.default.removeItem(at: file); return }
                url = file
            } catch { self.error = error.localizedDescription }
        }.onDisappear { if let url { try? FileManager.default.removeItem(at: url) }; url = nil }
    }
}

private func previewFile(_ attachment: LoadedAttachment) throws -> URL {
    var data = attachment.data
    let pdf = attachment.mediaType == "application/pdf"
    if !pdf {
        guard let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
              let image = UIImage(cgImage: try downsampleImage(source)).jpegData(compressionQuality: 0.9) else { throw NativeError.invalid("无法读取图片") }
        data = image
    }
    // The filename comes from us, not message data. Quick Look only sees a bounded static image or PDF.
    let file = FileManager.default.temporaryDirectory.appendingPathComponent("wuu-preview-" + UUID().uuidString + (pdf ? ".pdf" : ".jpg"))
    try data.write(to: file, options: [.atomic, .completeFileProtection])
    return file
}

func clearAbandonedAttachmentPreviews() {
    let directory = FileManager.default.temporaryDirectory
    for url in (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)) ?? [] {
        let name = url.deletingPathExtension().lastPathComponent
        if name.hasPrefix("wuu-preview-"), UUID(uuidString: String(name.dropFirst(12))) != nil,
           ["pdf", "jpg"].contains(url.pathExtension) { try? FileManager.default.removeItem(at: url) }
    }
}

private struct NativePreview: UIViewControllerRepresentable {
    let url: URL
    func makeCoordinator() -> Coordinator { Coordinator(url: url) }
    func makeUIViewController(context: Context) -> QLPreviewController {
        let view = QLPreviewController(); view.dataSource = context.coordinator; return view
    }
    func updateUIViewController(_ controller: QLPreviewController, context: Context) {}
    final class Coordinator: NSObject, QLPreviewControllerDataSource {
        let url: URL
        init(url: URL) { self.url = url }
        func numberOfPreviewItems(in controller: QLPreviewController) -> Int { 1 }
        func previewController(_ controller: QLPreviewController, previewItemAt index: Int) -> any QLPreviewItem { url as NSURL }
    }
}
