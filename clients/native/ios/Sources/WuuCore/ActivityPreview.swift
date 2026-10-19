import Foundation

/// Runtime observation only. These descriptors and frames never enter conversation history.
public struct PreviewActivity: Identifiable, Sendable, Equatable {
    public let id: String
    public let threadID: String
    public let executionID: String
    public let workdir: String
    public let kind: String
    public let state: String
    public let controller: String
    public let target: String
    public let processID: Int
    public let updatedAt: String
    /// Host timestamps are UTC RFC 3339 with variable fractional precision.
    public var orderingTimestamp: String {
        let parts = updatedAt.replacingOccurrences(of: "Z", with: "").split(separator: ".", maxSplits: 1)
        return String(parts.first ?? "") + "." + String(((parts.count > 1 ? String(parts[1]) : "") + "000000000").prefix(9))
    }
    public var observable: Bool {
        state != "stopped" && state != "error" && controller != "none" && !executionID.isEmpty && !target.isEmpty
            && (kind != "cua" || processID > 0)
    }
    public init?(_ value: JSONValue) {
        guard let id = value["id"].string, !id.isEmpty,
              let thread = value["thread_id"].string, !thread.isEmpty,
              let kind = value["kind"].string, kind == "browser" || kind == "cua",
              let state = value["state"].string, let updated = value["updated_at"].string else { return nil }
        self.id = id; threadID = thread; executionID = value["execution_id"].string ?? ""
        workdir = value["workdir"].string ?? ""
        self.kind = kind; self.state = state; updatedAt = updated
        controller = value["controller"].string ?? "none"
        target = (value["target"].string ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let pid = value["process_id"].number ?? 0
        processID = pid > 0 && pid < Double(Int32.max) ? Int(pid) : 0
    }
    public var params: JSONValue {
        ["thread_id": .string(threadID), "activity_id": .string(id), "execution_id": .string(executionID)]
    }
    public func accepts(_ value: JSONValue) -> Bool {
        value["thread_id"].string == threadID && value["activity_id"].string == id && value["execution_id"].string == executionID
    }
}

public struct PreviewFrame: Sendable {
    public let streamID: String
    public let sequence: Int
    public let generation: Int
    public let configuration: Int
    public let codec: String
    public let keyframe: Bool
    public let timestamp: Int64
    public let width: Int
    public let height: Int
    public let description: Data?
    public let data: Data
    public init(_ value: JSONValue) throws {
        func integer(_ key: String, minimum: Int = 1, maximum: Int = Int.max) throws -> Int {
            guard let number = value[key].number, number.isFinite, number.rounded() == number,
                  number >= Double(minimum), number < Double(Int.max), number <= Double(maximum) else {
                throw NativeError.invalid("Invalid preview \(key)")
            }
            return Int(number)
        }
        guard let id = value["stream_id"].string, !id.isEmpty,
              let codec = value["codec"].string, codec == "jpeg" || codec.range(of: "^avc1\\.[0-9a-fA-F]{6}$", options: .regularExpression) != nil,
              let encoded = value["data"].string, encoded.utf8.count <= 174_764,
              let data = Data(base64Encoded: encoded), !data.isEmpty, data.count <= 128 * 1024 else {
            throw NativeError.invalid("Invalid preview frame")
        }
        streamID = id; self.codec = codec; self.data = data
        sequence = try integer("sequence"); generation = try integer("generation"); configuration = try integer("configuration")
        timestamp = Int64(try integer("timestamp", minimum: 0))
        width = try integer("width", maximum: 1280); height = try integer("height", maximum: 1280)
        keyframe = value["keyframe"].bool
        if value["description"] != .null {
            let bytes = value["description"].array
            guard !bytes.isEmpty, bytes.count <= 4096, bytes.allSatisfy({
                guard let byte = $0.number else { return false }; return byte >= 0 && byte <= 255 && byte.rounded() == byte
            }) else { throw NativeError.invalid("Invalid preview video configuration") }
            description = Data(bytes.map { UInt8($0.number!) })
        } else { description = nil }
        if codec != "jpeg", keyframe, description == nil { throw NativeError.invalid("Preview keyframe has no configuration") }
    }
}

public enum PreviewEvent: Sendable {
    case frame(PreviewFrame)
    case state(String, String)
}

public struct PreviewSubscription: Sendable {
    public let id: String
    public let events: AsyncThrowingStream<PreviewEvent, Error>
}
