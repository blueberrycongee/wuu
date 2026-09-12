import SwiftUI
import PhotosUI
import UniformTypeIdentifiers
import ImageIO
import WuuCore

struct PickedImage: Transferable {
    let attachment: InputAttachment
    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .image) { file in
            PickedImage(attachment: try prepareAttachment(file.file))
        }
    }
}

func prepareAttachment(_ url: URL) throws -> InputAttachment {
    let access = url.startAccessingSecurityScopedResource()
    defer { if access { url.stopAccessingSecurityScopedResource() } }
    let values = try url.resourceValues(forKeys: [.fileSizeKey, .contentTypeKey])
    guard let size = values.fileSize, size > 0, size <= 32 * 1024 * 1024 else { throw NativeError.invalid("源文件不能超过 32 MB") }
    if values.contentType?.conforms(to: .pdf) == true || url.pathExtension.lowercased() == "pdf" {
        let file = try FileHandle(forReadingFrom: url); defer { try? file.close() }
        let data = try file.read(upToCount: InputAttachment.maxBytes + 1) ?? Data()
        guard data.starts(with: Data("%PDF-".utf8)) else { throw NativeError.invalid("文件不是有效的 PDF") }
        return try InputAttachment(filename: url.lastPathComponent, mediaType: "application/pdf", data: data)
    }
    guard let source = CGImageSourceCreateWithURL(url as CFURL, [kCGImageSourceShouldCache: false] as CFDictionary) else { throw NativeError.invalid("无法读取图片") }
    guard let data = UIImage(cgImage: try downsampleImage(source)).jpegData(compressionQuality: 0.85) else { throw NativeError.invalid("无法处理图片") }
    return try InputAttachment(filename: url.deletingPathExtension().lastPathComponent + ".jpg", mediaType: "image/jpeg", data: data)
}

func downsampleImage(_ source: CGImageSource) throws -> CGImage {
    guard let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
          let width = properties[kCGImagePropertyPixelWidth] as? NSNumber,
          let height = properties[kCGImagePropertyPixelHeight] as? NSNumber,
          width.doubleValue > 0, height.doubleValue > 0, width.doubleValue * height.doubleValue <= 40_000_000,
          let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [
            kCGImageSourceCreateThumbnailFromImageAlways: true, kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: 1600, kCGImageSourceShouldCacheImmediately: true] as CFDictionary) else {
        throw NativeError.invalid("无法读取图片，或图片超过 4000 万像素")
    }
    return image
}

struct AttachmentPicker: View {
    @Binding var attachments: [InputAttachment]
    let model: AppModel
    @State private var photos = false
    @State private var files = false
    @State private var photo: PhotosPickerItem?
    @State private var reading = false
    var body: some View {
        Menu {
            Button("选择照片") { photos = true }
            Button("选择图片或 PDF") { files = true }
            Text("图片缩至 1600 像素；附件合计最多 3 MB")
        } label: { Image(systemName: reading ? "hourglass" : "paperclip").font(.title3) }
            .accessibilityLabel("添加附件").disabled(reading || attachments.count >= 4)
            .photosPicker(isPresented: $photos, selection: $photo, matching: .images)
            .onChange(of: photo) { _, item in
                guard let item else { return }
                reading = true
                model.perform {
                    defer { reading = false; photo = nil }
                    if let result = try await item.loadTransferable(type: PickedImage.self) { try append(result.attachment) }
                }
            }
            .fileImporter(isPresented: $files, allowedContentTypes: [.pdf, .image]) { result in
                guard case .success(let url) = result else {
                    if case .failure(let error) = result { model.error = error.localizedDescription }; return
                }
                reading = true
                model.perform {
                    defer { reading = false }
                    let attachment = try await Task.detached { try prepareAttachment(url) }.value
                    try append(attachment)
                }
            }
    }
    private func append(_ attachment: InputAttachment) throws {
        try InputAttachment.validate(attachments + [attachment], text: "")
        attachments.append(attachment)
    }
}
