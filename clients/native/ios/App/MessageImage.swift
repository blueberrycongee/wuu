import SwiftUI
import WuuCore
import ImageIO

@MainActor final class ImagePreviewLoader {
    private var cache: [String: UIImage] = [:]
    private var order: [String] = []
    private var active = 0
    private var waiting: [CheckedContinuation<Void, Never>] = []
    private var generation = UUID()
    func clear() { generation = UUID(); cache = [:]; order = [] }
    func load(_ key: String, read: () async throws -> LoadedAttachment) async throws -> UIImage {
        let stamp = generation
        if active >= 2 { await withCheckedContinuation { waiting.append($0) } } else { active += 1 }
        defer {
            if waiting.isEmpty { active -= 1 } else { waiting.removeFirst().resume() }
        }
        try Task.checkCancellation()
        guard generation == stamp else { throw CancellationError() }
        if let image = cache[key] { order.removeAll { $0 == key }; order.append(key); return image }
        let data = try await read().data
        let image = try await Task.detached { try thumbnailImage(data, size: 384) }.value
        try Task.checkCancellation()
        guard generation == stamp else { throw CancellationError() }
        cache[key] = image; order.removeAll { $0 == key }; order.append(key)
        if order.count > 48 { cache[order.removeFirst()] = nil }
        return image
    }
}

private func thumbnailImage(_ data: Data, size: Int) throws -> UIImage {
    guard let source = CGImageSourceCreateWithData(data as CFData, nil),
          let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
          let width = properties[kCGImagePropertyPixelWidth] as? Int, let height = properties[kCGImagePropertyPixelHeight] as? Int,
          width > 0, height > 0, Int64(width) * Int64(height) <= 40_000_000,
          let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true, kCGImageSourceThumbnailMaxPixelSize: size] as CFDictionary) else {
        throw NativeError.invalid("无法读取图片")
    }
    return UIImage(cgImage: image)
}

struct MessageImage: View {
    let key: String
    let connected: Bool
    let loader: ImagePreviewLoader
    let read: () async throws -> LoadedAttachment
    let open: () -> Void
    @State private var image: UIImage?
    @State private var loadedKey = ""
    @State private var failed = false
    @State private var attempt = 0
    private var ratio: CGFloat { image.map { min(1.8, max(0.65, $0.size.width / $0.size.height)) } ?? 1.25 }
    var body: some View {
        Button { if failed { attempt += 1 } else { open() } } label: {
            ZStack {
                Color(uiColor: .tertiarySystemFill)
                if let image { Image(uiImage: image).resizable().scaledToFit() }
                else if failed { Label("点击重试", systemImage: "arrow.clockwise").font(.caption).foregroundStyle(.secondary) }
                else if connected { ProgressView() }
                else { Image(systemName: "photo").foregroundStyle(.secondary) }
            }.frame(width: 232, height: 232 / ratio).clipShape(RoundedRectangle(cornerRadius: 12))
        }.buttonStyle(.plain).disabled(!connected).accessibilityLabel(failed ? "图片加载失败，点击重试" : "图片，点击查看原图")
            .task(id: key + ":\(connected):\(attempt)") {
                if loadedKey != key { image = nil; loadedKey = key }
                guard connected, image == nil else { return }
                failed = false
                do { image = try await loader.load(key, read: read) }
                catch is CancellationError { }
                catch { failed = true }
            }
    }
}

struct DraftImage: View {
    let attachment: InputAttachment
    @State private var image: UIImage?
    var body: some View {
        Group {
            if let image { Image(uiImage: image).resizable().scaledToFill() }
            else { Color(uiColor: .secondarySystemFill).overlay { ProgressView() } }
        }.frame(width: 64, height: 64).clipShape(RoundedRectangle(cornerRadius: 10))
            .task(id: attachment.id) { image = try? await Task.detached { try thumbnailImage(attachment.data, size: 192) }.value }
    }
}

struct MessageAttachments: View {
    let model: AppModel
    let message: ChatMessage
    var body: some View {
                ForEach(Array(message.attachments.enumerated()), id: \.offset) { index, attachment in
                    if attachment["media_type"].string?.hasPrefix("image/") == true {
                        MessageImage(key: "\(model.activeID ?? ""):\(message.id):\(index):\(attachment["remote_ref"].string ?? "")",
                            connected: model.connected, loader: model.imagePreviews,
                            read: { try await model.attachmentThumbnail(message, index: index) },
                            open: { model.perform { try await model.previewAttachment(message, index: index) } })
                    } else {
                    Button { model.perform { try await model.previewAttachment(message, index: index) } } label: {
                        Label(attachment["filename"].string ?? "查看图片", systemImage: attachment["media_type"].string?.hasPrefix("image/") == true ? "photo" : "doc")
                    }.disabled(!model.connected || model.loadingAttachment)
                    }
                }
    }
}
