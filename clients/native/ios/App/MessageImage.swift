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
            }.frame(width: Self.width, height: Self.height)
                .contentShape(RoundedRectangle(cornerRadius: 10))
                .clipShape(RoundedRectangle(cornerRadius: 10))
                // Light images, such as screenshots, would otherwise blend into the background.
                .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color.primary.opacity(0.12), lineWidth: 0.5))
        }.buttonStyle(.plain).disabled(!connected)
            .accessibilityElement(children: .ignore)
            .accessibilityAddTraits(.isButton)
            .accessibilityLabel(failed ? "图片加载失败，点击重试" : "图片，点击查看原图")
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

/// One attachment and the message that owns it; reads and previews need both.
private struct AttachmentTile: Identifiable {
    let message: ChatMessage
    let index: Int
    let value: JSONValue
    var id: String { message.id + ":\(index)" }
    var isImage: Bool { value["media_type"].string?.hasPrefix("image/") == true }
}

/// Thumbnails have a stable footprint before and after loading; decoding never moves the timeline.
/// Images from consecutive messages, such as several tool results, share one wrapping grid.
struct MessageAttachments: View {
    let model: AppModel
    let messages: [ChatMessage]
    var own = false
    let inspectionOnly: Bool
    @Binding private var preview: LoadedAttachment?
    init(model: AppModel, message: ChatMessage) {
        self.init(model: model, messages: [message])
        own = message.role == "user"
    }
    init(model: AppModel, messages: [ChatMessage], inspectionOnly: Bool = false, preview: Binding<LoadedAttachment?>? = nil) {
        self.model = model; self.messages = messages; self.inspectionOnly = inspectionOnly
        _preview = preview ?? Binding(get: { model.attachmentPreview }, set: { model.attachmentPreview = $0 })
    }
    private var tiles: [AttachmentTile] {
        messages.flatMap { message in
            // Filter after enumeration so reads keep the host attachment address.
            message.attachments.enumerated().filter { message.isInspectionImage($0.element) == inspectionOnly }
                .map { AttachmentTile(message: message, index: $0.offset, value: $0.element) }
        }
    }
    var body: some View {
        let tiles = self.tiles
        if tiles.contains(where: \.isImage) {
            ThumbnailLayout(own: own) {
                ForEach(tiles.filter(\.isImage)) { tile in
                    MessageImage(key: "\(model.activeID ?? ""):\(tile.message.id):\(tile.index):\(tile.value["remote_ref"].string ?? "")",
                        connected: model.connected, loader: model.imagePreviews,
                        read: { try await model.attachmentThumbnail(tile.message, index: tile.index) }, open: { open(tile) })
                }
            }
        }
        ForEach(tiles.filter { !$0.isImage }) { tile in
            Button { open(tile) } label: { Label(tile.value["filename"].string ?? "查看文件", systemImage: "doc") }
                .frame(minHeight: 44)
                .disabled(!model.connected || model.loadingAttachment)
        }
    }
    private func open(_ tile: AttachmentTile) {
        model.perform {
            if let attachment = try await model.loadAttachmentPreview(tile.message, index: tile.index) { preview = attachment }
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
