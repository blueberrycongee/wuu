import Foundation
import CryptoKit

/// A local retry record grants no remote authority. Only an explicit retry adopts it after scope/agent validation.
public struct PersonalStoredDraft: Sendable {
    public let agentID: String
    public let requestID: String
    public let prompt: String
    public let attachments: [InputAttachment]
    static func validate(_ prompt: String, _ attachments: [InputAttachment]) throws {
        try InputAttachment.validate(attachments, text: prompt)
        guard prompt.utf8.count <= 64_000 else { throw NativeError.invalid("Personal 消息不能超过 64,000 字节") }
        guard !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty else { throw PersonalClientError.emptyMessage }
        for file in attachments {
            guard !file.filename.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, file.filename.utf8.count <= 255,
                  !file.filename.contains("/"), !file.filename.contains("\\"),
                  !file.filename.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) }) else { throw NativeError.invalid("Personal 附件名称不能包含控制字符或超过 255 字节") }
        }
    }
}

/// Durable, bounded app-private retry storage, separate from the OS-purgeable display cache.
public actor PersonalDraftStore {
    private let file: URL
    private let scope: String
    private var valid = true
    private static let maxBytes = 5 * 1024 * 1024
    public init(directory: URL, identity: [String]) {
        scope = SHA256.hash(data: (try? JSONEncoder().encode(identity)) ?? Data()).map { String(format: "%02x", $0) }.joined()
        file = directory.appendingPathComponent(scope + ".json")
    }
    public func save(_ draft: PersonalMessageDraft) throws {
        guard valid else { throw CancellationError() }
        try PersonalStoredDraft.validate(draft.prompt, draft.attachments)
        let value: JSONValue = ["version": 1, "scope": .string(scope), "agent_id": .string(draft.agentID), "request_id": .string(draft.requestID), "prompt": .string(draft.prompt), "attachments": .array(draft.attachments.map { file in
            ["filename": .string(file.filename), "media_type": .string(file.mediaType), "data": .string(file.data.base64EncodedString()), "sha256": .string(SHA256.hash(data: file.data).map { String(format: "%02x", $0) }.joined())]
        })]
        let bytes = try JSONEncoder().encode(value)
        guard bytes.count <= Self.maxBytes else { throw PersonalClientError.invalidResponse }
        var directory = file.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        var attributes = URLResourceValues(); attributes.isExcludedFromBackup = true; try directory.setResourceValues(attributes)
        #if os(iOS)
        try bytes.write(to: file, options: [.atomic, .completeFileProtection])
        #else
        try bytes.write(to: file, options: [.atomic])
        #endif
    }
    public func load() throws -> PersonalStoredDraft? {
        guard valid else { throw CancellationError() }
        guard FileManager.default.fileExists(atPath: file.path) else { return nil }
        guard (try file.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? Self.maxBytes + 1) <= Self.maxBytes else { throw PersonalClientError.invalidResponse }
        let value = try JSONDecoder().decode(JSONValue.self, from: Data(contentsOf: file))
        guard value["version"].number == 1, value["scope"].string == scope,
              let agentID = value["agent_id"].string, agentID.range(of: "^[A-Za-z0-9_.:-]{1,256}$", options: .regularExpression) != nil,
              let requestID = value["request_id"].string, UUID(uuidString: requestID) != nil, let prompt = value["prompt"].string,
              case .array(let files) = value["attachments"], files.count <= 4 else { throw PersonalClientError.invalidResponse }
        let attachments = try files.map { file -> InputAttachment in
            guard let filename = file["filename"].string, let media = file["media_type"].string, let encoded = file["data"].string,
                  let bytes = Data(base64Encoded: encoded), SHA256.hash(data: bytes).map({ String(format: "%02x", $0) }).joined() == file["sha256"].string else { throw PersonalClientError.invalidResponse }
            let attachment = try InputAttachment(filename: filename, mediaType: media, data: bytes)
            guard attachment.filename == filename else { throw PersonalClientError.invalidResponse }
            return attachment
        }
        try PersonalStoredDraft.validate(prompt, attachments)
        return PersonalStoredDraft(agentID: agentID, requestID: requestID, prompt: prompt, attachments: attachments)
    }
    public func clear() throws {
        guard valid else { throw CancellationError() }
        if FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
    }
    public func invalidate(remove: Bool) throws {
        valid = false
        if remove, FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
    }
}
