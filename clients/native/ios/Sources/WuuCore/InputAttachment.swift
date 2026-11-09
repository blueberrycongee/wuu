import Foundation

public struct InputAttachment: Identifiable, Sendable, Equatable {
    public static let maxBytes = 3 * 1024 * 1024
    public let id: UUID
    public let filename: String
    public let mediaType: String
    public let data: Data
    public var isImage: Bool { mediaType.hasPrefix("image/") }
    public init(filename: String, mediaType: String, data: Data) throws {
        guard !data.isEmpty, data.count <= Self.maxBytes else { throw NativeError.invalid("附件需介于 1 字节和 3 MB 之间") }
        guard ["image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf"].contains(mediaType) else { throw NativeError.invalid("支持图片和 PDF 附件") }
        self.id = UUID(); self.filename = String(URL(fileURLWithPath: filename).lastPathComponent.prefix(200))
        self.mediaType = mediaType; self.data = data
    }
    public var value: JSONValue {
        var value: [String: JSONValue] = ["media_type": .string(mediaType), "data": .string(data.base64EncodedString())]
        if !isImage { value["filename"] = .string(filename) }
        return .object(value)
    }
    public static func validate(_ attachments: [InputAttachment], text: String) throws {
        // Leave room for JSON and the relay's second base64 envelope under the phone's 8 MB frame cap.
        guard attachments.count <= 4, attachments.reduce(0, { $0 + $1.data.count }) <= maxBytes else { throw NativeError.invalid("每条消息最多 4 个附件，合计不超过 3 MB") }
        guard text.utf8.count <= 128 * 1024 else { throw NativeError.invalid("文字消息不能超过 128 KB") }
    }
}

public struct ChatInput: Sendable {
    public let text: String
    public let attachments: [InputAttachment]
    public init(text: String, attachments: [InputAttachment] = []) throws {
        try InputAttachment.validate(attachments, text: text)
        self.text = text; self.attachments = attachments
    }
    public func params(threadID: String, queued: Bool) -> JSONValue {
        var params: [String: JSONValue] = ["thread_id": .string(threadID), "prompt": .string(text),
            "images": .array(attachments.filter(\.isImage).map(\.value)), "files": .array(attachments.filter { !$0.isImage }.map(\.value))]
        // The start RPC rejects unknown fields; client_id belongs only to queued/steered inputs.
        if queued { params["client_id"] = .string(UUID().uuidString) }
        return .object(params)
    }
}
