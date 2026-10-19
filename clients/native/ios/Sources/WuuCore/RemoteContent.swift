import Foundation
import CryptoKit

public struct LoadedAttachment: Identifiable, Sendable {
    public let id = UUID()
    public let mediaType: String
    public let filename: String
    public let data: Data
}

extension RemoteConnection {
    /// Fetch an explicitly requested large message in bounded chunks, verifying its immutable reference.
    public func readContent(_ reference: String, threadID: String) async throws -> JSONValue {
        guard reference.hasPrefix("content:") else { throw NativeError.invalid("无效的消息引用") }
        let parts = try JSONDecoder().decode([String].self, from: Data(base64URL: String(reference.dropFirst(8))))
        guard parts.count == 4, parts[0] == threadID else { throw NativeError.invalid("消息不属于当前会话") }
        let encoded = try await readEncoded("thread/content/read", params: [
            "thread_id": .string(parts[0]), "turn_id": .string(parts[1]), "item_id": .string(parts[2]),
            "sha256": .string(parts[3])], mediaType: "application/json")
        guard let data = Data(base64Encoded: encoded),
              SHA256.hash(data: data).map({ String(format: "%02x", $0) }).joined() == parts[3] else {
            throw NativeError.invalid("消息内容校验失败")
        }
        let item = try JSONDecoder().decode(JSONValue.self, from: data)
        guard item["id"].string == parts[2] else { throw NativeError.invalid("消息引用不匹配") }
        return item
    }

    public func readAttachment(_ attachment: JSONValue, threadID: String, messageID: String, preview: Bool = false) async throws -> LoadedAttachment {
        try await readMessageAttachment(attachment, scopeID: threadID, messageID: messageID, preview: preview) { method, params in
            try await self.call(method, params: params)
        }
    }

    private func readEncoded(_ method: String, params: JSONValue, mediaType: String) async throws -> String {
        try await readAttachmentChunks(method, params: params, mediaType: mediaType) { method, params in
            try await self.call(method, params: params)
        }
    }
}

public func readMessageAttachment(_ attachment: JSONValue, scopeID: String, messageID: String, preview: Bool = false,
    call: @escaping @Sendable (String, JSONValue) async throws -> JSONValue) async throws -> LoadedAttachment {
    let media = attachment["media_type"].string ?? ""
    guard ["image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf"].contains(media) else { throw NativeError.invalid("手机暂不支持此附件格式") }
    var encoded = attachment["data"].string ?? ""
    let ref = attachment["remote_ref"].string ?? ""
    let managed = encoded.isEmpty && ref.isEmpty && attachment["uri"].string?.hasPrefix("wuu-artifact:") == true
    if managed {
        guard let turn = attachment["turn_id"].string, let item = attachment["item_id"].string,
              turn + ":" + item == messageID,
              let index = attachment["content_index"].number, index >= 0, index.rounded() == index,
              let digest = attachment["artifact"]["sha256"].string, digest.count == 64 else {
            throw NativeError.invalid("成果不属于当前消息")
        }
        let params: JSONValue = ["thread_id": .string(scopeID), "turn_id": .string(turn), "item_id": .string(item),
            "kind": "artifact", "index": .number(index), "sha256": .string(digest), "preview": .bool(preview)]
        encoded = try await readAttachmentChunks("thread/attachment/read", params: params,
            mediaType: preview ? "image/jpeg" : media, limit: preview ? 128 * 1024 : 16 * 1024 * 1024, call: call)
        if !preview {
            guard let data = Data(base64Encoded: encoded),
                  SHA256.hash(data: data).map({ String(format: "%02x", $0) }).joined() == digest else {
                throw NativeError.invalid("成果内容校验失败")
            }
        }
    } else if ref.hasPrefix("markdown:") {
        guard ref.utf8.count < 8192 else { throw NativeError.invalid("无效的图片引用") }
        let parts = try JSONDecoder().decode([String].self, from: Data(base64URL: String(ref.dropFirst(9))))
        guard parts.count == 5, parts[0] == "thread", parts[1] == scopeID, parts[2] + ":" + parts[3] == messageID else {
            throw NativeError.invalid("图片不属于当前消息")
        }
        let params: JSONValue = ["kind": .string(parts[0]), "scope_id": .string(scopeID), "turn_id": .string(parts[2]),
            "message_id": .string(parts[3]), "source": .string(parts[4]), "preview": .bool(preview)]
        encoded = try await readAttachmentChunks("message/image/read", params: params, mediaType: preview ? "image/jpeg" : media,
            limit: preview ? 128 * 1024 : 16 * 1024 * 1024, verifyDigest: !preview, call: call)
    } else if !ref.isEmpty {
        guard ref.utf8.count < 8192, ref.hasPrefix("thread:") else { throw NativeError.invalid("无效的附件引用") }
        let parts = try JSONDecoder().decode(JSONValue.self, from: Data(base64URL: String(ref.dropFirst(7)))).array
        guard (5...6).contains(parts.count), parts[0].string == scopeID,
              let index = parts[3].number, index >= 0, index.rounded() == index,
              let digest = parts[4].string, digest.count == 64,
              let turn = parts[1].string, let item = parts[2].string, turn + ":" + item == messageID,
              parts.count == 5 || parts[5].string == "result" else { throw NativeError.invalid("附件不属于当前消息") }
        let params: JSONValue = ["thread_id": .string(scopeID), "turn_id": .string(turn), "item_id": .string(item),
            "kind": .string(parts.count == 6 ? "result" : ""), "index": .number(index), "sha256": .string(digest), "preview": .bool(preview)]
        encoded = try await readAttachmentChunks("thread/attachment/read", params: params,
            mediaType: preview ? "image/jpeg" : media, limit: preview ? 128 * 1024 : 16 * 1024 * 1024, call: call)
        if !preview {
            guard SHA256.hash(data: Data((media + "\0" + encoded).utf8)).map({ String(format: "%02x", $0) }).joined() == digest else { throw NativeError.invalid("附件内容校验失败") }
        }
    }
    let descriptor: JSONValue = preview && (managed || !ref.isEmpty) ? ["media_type": "image/jpeg", "filename": attachment["filename"]] : attachment
    return try decodeInlineAttachment(descriptor, encoded: encoded)
}

private func readAttachmentChunks(_ method: String, params: JSONValue, mediaType: String, limit: Int = 16 * 1024 * 1024,
    verifyDigest: Bool = false, call: @escaping @Sendable (String, JSONValue) async throws -> JSONValue) async throws -> String {
    let read: @Sendable (Int, Int?, String?) async throws -> (data: String, total: Int, digest: String?) = { offset, total, digest in
        try Task.checkCancellation()
        guard case .object(var request) = params else { throw NativeError.invalid("无效的读取参数") }
        request["offset"] = .number(Double(offset))
        if let digest { request["sha256"] = .string(digest) }
        let result = try await call(method, .object(request))
        guard let count = result["total"].number, count > 0, count <= Double(limit), count.rounded() == count,
              result["content_type"].string == mediaType, result["offset"].number == Double(offset),
              let chunk = result["data"].string, !chunk.isEmpty, chunk.utf8.count <= 128 * 1024,
              offset + chunk.utf8.count <= Int(count), total == nil || total == Int(count) else {
            throw NativeError.invalid("消息过大或读取不完整，请在电脑上查看")
        }
        let next = result["sha256"].string
        if verifyDigest {
            guard let next, next.count == 64, digest == nil || digest == next else { throw NativeError.invalid("图片读取时发生变化") }
        }
        return (chunk, Int(count), verifyDigest ? next : nil)
    }
    let first = try await read(0, nil, nil)
    var encoded = first.data
    encoded.reserveCapacity(first.total)
    let chunkSize = first.data.utf8.count
    // Bound in-flight data while overlapping independent network round trips.
    // The first chunk fixes the size and immutable version for the remaining reads.
    while encoded.utf8.count < first.total {
        let start = encoded.utf8.count
        let end = min(first.total, start + 4 * chunkSize)
        let chunks = try await withThrowingTaskGroup(of: (Int, String).self) { group in
            for offset in stride(from: start, to: end, by: chunkSize) {
                group.addTask {
                    let result = try await read(offset, first.total, first.digest)
                    guard result.data.utf8.count == min(chunkSize, first.total - offset) else {
                        throw NativeError.invalid("消息过大或读取不完整，请在电脑上查看")
                    }
                    return (offset, result.data)
                }
            }
            var chunks: [(Int, String)] = []
            for try await chunk in group { chunks.append(chunk) }
            return chunks.sorted { $0.0 < $1.0 }
        }
        for (_, chunk) in chunks { encoded += chunk }
    }
    if verifyDigest, SHA256.hash(data: Data((mediaType + "\0" + encoded).utf8)).map({ String(format: "%02x", $0) }).joined() != first.digest { throw NativeError.invalid("图片内容校验失败") }
    return encoded
}

func decodeInlineAttachment(_ attachment: JSONValue, encoded: String? = nil) throws -> LoadedAttachment {
    let media = attachment["media_type"].string ?? ""
    guard ["image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf"].contains(media) else { throw NativeError.invalid("手机暂不支持此附件格式") }
    let encoded = encoded ?? attachment["data"].string ?? ""
    guard !encoded.isEmpty, encoded.utf8.count <= 16 * 1024 * 1024, let data = Data(base64Encoded: encoded), !data.isEmpty else {
        throw NativeError.invalid("附件过大或内容无效，请在电脑上查看")
    }
    if media == "application/pdf", !data.starts(with: Data("%PDF-".utf8)) { throw NativeError.invalid("文件不是有效的 PDF") }
    return LoadedAttachment(mediaType: media, filename: attachment["filename"].string ?? "图片", data: data)
}
