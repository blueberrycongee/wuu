import Foundation
import CryptoKit

/// A navigation bookmark carries no authority; restore only within its original login and device directory.
public struct NavigationLocation: Codable, Equatable {
    private let login: String
    public let host: String
    public let workspace: String
    public let thread: String?
    public let room: String?
    public let mode: String

    public init(account: AccountSession, host: String, workspace: String = "", thread: String? = nil,
                room: String? = nil, mode: String = "collaboration") throws {
        login = try Self.key(account)
        self.host = host; self.workspace = workspace; self.thread = thread; self.room = room; self.mode = mode
    }
    private static func key(_ account: AccountSession) throws -> String {
        Data(SHA256.hash(data: try JSONEncoder().encode([account.server, account.username, account.pub, account.token]))).base64URL
    }
    public func restore(account: AccountSession, devices: [AccountDevice]) throws -> NavigationLocation? {
        guard login == (try Self.key(account)), devices.contains(where: { $0.pub == host && $0.role == "host" }) else { return nil }
        return self
    }
}
