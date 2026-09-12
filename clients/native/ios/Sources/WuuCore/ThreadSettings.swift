import Foundation

public struct ThreadSettings: Sendable, Equatable {
    public var provider: String
    public var model: String
    public var variant: String
    public var permission: String
    public init(_ thread: JSONValue) {
        provider = thread["model_provider"].string ?? ""
        model = thread["model"].string ?? ""
        variant = thread["model_variant"].string ?? ""
        if variant.isEmpty { variant = thread["model_effort"].string ?? "" }
        permission = thread["permission_mode"].string ?? "standard"
        if permission.isEmpty { permission = "standard" }
    }
    /// A missing thread ID would change the computer's defaults instead of this conversation.
    public func updateParams(threadID: String) throws -> JSONValue {
        guard !threadID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !provider.isEmpty, !model.isEmpty,
              ["standard", "read_only", "unconfined"].contains(permission) else {
            throw NativeError.invalid("请选择当前会话的模型和权限")
        }
        return ["thread_id": .string(threadID), "provider": .string(provider), "model": .string(model),
                "variant": .string(variant), "permission_mode": .string(permission)]
    }
}

public struct RemoteModel: Identifiable, Sendable {
    public let id: String
    public let name: String
    public let variants: [String]
    init(_ value: JSONValue) {
        id = value["id"].string ?? ""
        name = value["display_name"].string.flatMap { $0.isEmpty ? nil : $0 } ?? id
        let declared = value["variants"].array.compactMap { $0["id"].string }.filter { !$0.isEmpty }
        variants = declared.isEmpty ? value["supported_efforts"].array.compactMap(\.string) : declared
    }
}

public struct RemoteProvider: Identifiable, Sendable {
    public let id: String
    public let models: [RemoteModel]
    public init(_ value: JSONValue) {
        id = value["name"].string ?? ""
        var models = value["models"].array.map(RemoteModel.init).filter { !$0.id.isEmpty }
        if models.isEmpty, let current = value["model"].string, !current.isEmpty {
            models = [RemoteModel(["id": .string(current)])]
        }
        self.models = models
    }
}
