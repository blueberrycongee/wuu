import Foundation
import CryptoKit

/// Consent belongs to one login, including its bearer-token lifetime.
public struct PushConsent: Codable, Sendable {
    private let login: String
    public let enabled: Bool
    private static func key(_ session: AccountSession) -> String {
        Data(SHA256.hash(data: Data([session.server, session.username, session.pub, session.token].joined(separator: "\n").utf8))).base64URL
    }
    public init(session: AccountSession, enabled: Bool) { login = Self.key(session); self.enabled = enabled }
    public func matches(_ session: AccountSession) -> Bool { login == Self.key(session) }
}

public struct AccountPushStatus: Decodable, Sendable {
    public let enabled: Bool
    public let platform: String
}

extension AccountAPI {
    public func pushStatus(token: String) async throws -> AccountPushStatus { try await request("/push", token: token) }
    public func setPush(token: String, platform: String, deviceToken: String?) async throws {
        if let deviceToken {
            guard !deviceToken.isEmpty, ["ios", "android"].contains(platform) else { throw NativeError.invalid("Invalid push registration") }
            let _: JSONValue = try await request("/push", token: token, body: ["platform": platform, "token": deviceToken])
        } else {
            let _: JSONValue = try await request("/push", token: token, method: "DELETE")
        }
    }
}
