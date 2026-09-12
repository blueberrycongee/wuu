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

    /// Only inline bytes or a reference to this message are accepted; attachment URLs are never fetched.
    public func readAttachment(_ attachment: JSONValue, threadID: String, messageID: String) async throws -> LoadedAttachment {
        let media = attachment["media_type"].string ?? ""
        guard ["image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf"].contains(media) else {
            throw NativeError.invalid("手机暂不支持此附件格式")
        }
        var encoded = attachment["data"].string ?? ""
        if let ref = attachment["remote_ref"].string, !ref.isEmpty {
            guard ref.hasPrefix("thread:"), ref.utf8.count < 8192 else { throw NativeError.invalid("无效的附件引用") }
            let parts = try JSONDecoder().decode(JSONValue.self, from: Data(base64URL: String(ref.dropFirst(7)))).array
            guard parts.count == 5, parts[0].string == threadID,
                  let turn = parts[1].string, let item = parts[2].string, turn + ":" + item == messageID,
                  let index = parts[3].number, index >= 0, index.rounded() == index,
                  let digest = parts[4].string, digest.count == 64 else { throw NativeError.invalid("附件不属于当前消息") }
            encoded = try await readEncoded("thread/attachment/read", params: [
                "thread_id": .string(threadID), "turn_id": .string(turn), "item_id": .string(item),
                "index": .number(index), "sha256": .string(digest)], mediaType: media)
            guard SHA256.hash(data: Data((media + "\0" + encoded).utf8)).map({ String(format: "%02x", $0) }).joined() == digest else {
                throw NativeError.invalid("附件内容校验失败")
            }
        }
        guard !encoded.isEmpty, encoded.utf8.count <= 16 * 1024 * 1024, let data = Data(base64Encoded: encoded), !data.isEmpty else {
            throw NativeError.invalid("附件过大或内容无效，请在电脑上查看")
        }
        if media == "application/pdf", !data.starts(with: Data("%PDF-".utf8)) { throw NativeError.invalid("文件不是有效的 PDF") }
        return LoadedAttachment(mediaType: media, filename: attachment["filename"].string ?? "图片", data: data)
    }

    private func readEncoded(_ method: String, params: JSONValue, mediaType: String) async throws -> String {
        var encoded = "", total: Int?
        repeat {
            try Task.checkCancellation()
            guard case .object(var request) = params else { throw NativeError.invalid("无效的读取参数") }
            request["offset"] = .number(Double(encoded.utf8.count))
            let result = try await call(method, params: .object(request))
            guard let count = result["total"].number, count > 0, count <= 16 * 1024 * 1024, count.rounded() == count,
                  result["content_type"].string == mediaType,
                  result["offset"].number == Double(encoded.utf8.count),
                  let chunk = result["data"].string, !chunk.isEmpty, chunk.utf8.count <= 128 * 1024,
                  encoded.utf8.count + chunk.utf8.count <= Int(count), total == nil || total == Int(count) else {
                throw NativeError.invalid("消息过大或读取不完整，请在电脑上查看")
            }
            total = Int(count); encoded += chunk
        } while encoded.utf8.count < (total ?? 0)
        return encoded
    }
}
