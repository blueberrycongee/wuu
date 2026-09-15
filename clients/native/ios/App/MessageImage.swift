import SwiftUI
import WuuCore
import ImageIO

@MainActor final class ImagePreviewLoader {
    private let previews = PreviewCache<UIImage>()
    func clear() { previews.clear() }
    func load(_ key: String, read: @escaping @MainActor () async throws -> LoadedAttachment) async throws -> UIImage {
        try await previews.value(for: key) {
            let data = try await read().data
            try Task.checkCancellation()
            return try await Task.detached { try thumbnailImage(data, size: 288) }.value
        }
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
    static let width: CGFloat = 96
    static let height: CGFloat = 72
    let key: String
    let connected: Bool
    let loader: ImagePreviewLoader
    let read: @MainActor () async throws -> LoadedAttachment
    let open: () -> Void
    @State private var image: UIImage?
    @State private var loadedKey = ""
    @State private var failed = false
    @State private var attempt = 0
    var body: some View {
        Button { if failed { attempt += 1 } else { open() } } label: {
            ZStack {
                Color(uiColor: .tertiarySystemFill)
                if let image { Image(uiImage: image).resizable().scaledToFill() }
                else if failed { Image(systemName: "arrow.clockwise").foregroundStyle(.secondary) }
                else if connected { ProgressView() }
                else { Image(systemName: "photo").foregroundStyle(.secondary) }
            }.frame(width: Self.width, height: Self.height).clipShape(RoundedRectangle(cornerRadius: 10))
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
        AttachmentGallery(items: message.attachments.enumerated().map { AttachmentTile(field: "attachments", index: $0.offset, value: $0.element) },
            scope: "\(model.activeID ?? ""):\(message.id)", own: message.role == "user", model: model,
            read: { try await model.attachmentThumbnail(message, index: $0.index) },
            open: { tile in model.perform { try await model.previewAttachment(message, index: tile.index) } })
    }
}

struct AttachmentTile: Identifiable {
    let field: String
    let index: Int
    let value: JSONValue
    var id: String { "\(field):\(index)" }
    var isImage: Bool { value["media_type"].string?.hasPrefix("image/") == true }
}

/// Thumbnails have a stable footprint before and after loading; decoding never moves the timeline.
struct AttachmentGallery: View {
    let items: [AttachmentTile]
    let scope: String
    let own: Bool
    let model: AppModel
    let read: (AttachmentTile) async throws -> LoadedAttachment
    let open: (AttachmentTile) -> Void
    var body: some View {
        if items.contains(where: \.isImage) {
            ThumbnailLayout(own: own) {
                ForEach(items.filter(\.isImage)) { tile in
                    MessageImage(key: "\(scope):\(tile.id):\(tile.value["remote_ref"].string ?? "")",
                        connected: model.connected, loader: model.imagePreviews,
                        read: { try await read(tile) }, open: { open(tile) })
                }
            }
        }
        ForEach(items.filter { !$0.isImage }) { tile in
            Button { open(tile) } label: { Label(tile.value["filename"].string ?? "查看文件", systemImage: "doc") }
                .disabled(!model.connected || model.loadingAttachment)
        }
    }
}

private struct ThumbnailLayout: Layout {
    let own: Bool
    private let gap: CGFloat = 8
    private func columns(width: CGFloat, count: Int) -> Int {
        let available = width.isFinite ? width : CGFloat(count) * (MessageImage.width + gap)
        return max(1, min(count, Int((available + gap) / (MessageImage.width + gap))))
    }
    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard !subviews.isEmpty else { return .zero }
        let columns = columns(width: proposal.width ?? MessageImage.width, count: subviews.count)
        let rows = (subviews.count + columns - 1) / columns
        return CGSize(width: CGFloat(columns) * (MessageImage.width + gap) - gap,
            height: CGFloat(rows) * (MessageImage.height + gap) - gap)
    }
    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let columns = columns(width: bounds.width, count: subviews.count)
        for index in subviews.indices {
            let row = index / columns, column = index % columns
            let rowCount = min(columns, subviews.count - row * columns)
            let rowWidth = CGFloat(rowCount) * (MessageImage.width + gap) - gap
            subviews[index].place(at: CGPoint(x: bounds.minX + (own ? bounds.width - rowWidth : 0) + CGFloat(column) * (MessageImage.width + gap),
                y: bounds.minY + CGFloat(row) * (MessageImage.height + gap)), proposal: ProposedViewSize(width: MessageImage.width, height: MessageImage.height))
        }
    }
}
