import SwiftUI
import PhotosUI
import UniformTypeIdentifiers
import ImageIO
import AVFoundation
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
    @State private var camera = false
    @State private var photos = false
    @State private var files = false
    @State private var photo: PhotosPickerItem?
    @State private var reading = false
    var body: some View {
        Menu {
            if UIImagePickerController.isSourceTypeAvailable(.camera) {
                Button("拍照", systemImage: "camera") {
                    Task {
                        if await AVCaptureDevice.requestAccess(for: .video) { camera = true }
                        else { model.error = "请在系统设置中允许 Wuu 使用相机。" }
                    }
                }
            }
            Button("照片", systemImage: "photo") { photos = true }
            Button("文件", systemImage: "doc") { files = true }
            Text("最多 4 个图片或 PDF，合计 3 MB")
        } label: {
            Image(systemName: reading ? "hourglass" : "plus").font(.system(size: 20)).foregroundStyle(.primary)
                .frame(width: 44, height: 44).background(Color(uiColor: .systemBackground), in: Circle())
                .overlay(Circle().stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
        }
            .accessibilityLabel("添加附件").disabled(reading || attachments.count >= 4)
            .fullScreenCover(isPresented: $camera) {
                CameraCapture { image in
                    camera = false
                    guard let image else { return }
                    do {
                        let width = image.size.width * image.scale, height = image.size.height * image.scale
                        guard width > 0, height > 0, width * height <= 40_000_000 else { throw NativeError.invalid("图片超过 4000 万像素") }
                        let scale = min(1, 1600 / max(width, height))
                        let format = UIGraphicsImageRendererFormat(); format.scale = 1
                        let size = CGSize(width: width * scale, height: height * scale)
                        let resized = UIGraphicsImageRenderer(size: size, format: format).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }
                        guard let data = resized.jpegData(compressionQuality: 0.85) else { throw NativeError.invalid("无法处理照片") }
                        try append(InputAttachment(filename: "photo.jpg", mediaType: "image/jpeg", data: data))
                    } catch { model.error = error.localizedDescription }
                }.ignoresSafeArea()
            }
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

private struct CameraCapture: UIViewControllerRepresentable {
    var completion: (UIImage?) -> Void
    func makeCoordinator() -> Coordinator { Coordinator(completion) }
    func makeUIViewController(context: Context) -> UIImagePickerController {
        let picker = UIImagePickerController()
        picker.sourceType = .camera; picker.cameraCaptureMode = .photo; picker.delegate = context.coordinator
        return picker
    }
    func updateUIViewController(_ controller: UIImagePickerController, context: Context) {}
    final class Coordinator: NSObject, UINavigationControllerDelegate, UIImagePickerControllerDelegate {
        let completion: (UIImage?) -> Void
        init(_ completion: @escaping (UIImage?) -> Void) { self.completion = completion }
        func imagePickerControllerDidCancel(_ picker: UIImagePickerController) { completion(nil) }
        func imagePickerController(_ picker: UIImagePickerController, didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]) {
            completion(info[.originalImage] as? UIImage)
        }
    }
}
