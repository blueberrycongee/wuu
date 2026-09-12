import Foundation
import CryptoKit

public struct GitHubPending: Codable, Sendable {
    public let server: String
    public let requestID: String
    public let verifier: String
    public let authorizationURL: URL
    public let expires: Date
}
public struct GitHubPoll: Decodable {
    public let status: String
    public let username: String?
}

extension AccountAPI {
    public func startGitHub() async throws -> GitHubPending {
        let verifier = Data((0..<32).map { _ in UInt8.random(in: 0...255) }).base64URL
        let challenge = Data(SHA256.hash(data: Data(verifier.utf8))).base64URL
        struct Started: Decodable { let request_id: String; let authorize_url: String; let expires_in: Double }
        let started: Started = try await request("/github/start", body: ["challenge": challenge, "native": true])
        guard let c = URLComponents(string: started.authorize_url), let url = c.url,
              c.scheme == origin.scheme, c.host == origin.host, c.port == origin.port,
              c.user == nil, c.password == nil, c.fragment == nil,
              c.path == "/v1/account/github/authorize",
              started.request_id.range(of: "^[A-Za-z0-9_-]{43}$", options: .regularExpression) != nil,
              c.queryItems?.filter({ $0.name == "state" }).map(\.value) == [started.request_id],
              started.expires_in > 0 else { throw NativeError.invalid("Invalid authorization URL") }
        return GitHubPending(server: origin.absoluteString, requestID: started.request_id, verifier: verifier,
                             authorizationURL: url, expires: Date().addingTimeInterval(min(started.expires_in, 600)))
    }
    public func pollGitHub(_ pending: GitHubPending) async throws -> GitHubPoll {
        try validate(pending)
        return try await request("/github/poll", body: ["request_id": pending.requestID, "verifier": pending.verifier])
    }
    public func cancelGitHub(_ pending: GitHubPending) async throws {
        guard pending.server == origin.absoluteString else { throw NativeError.invalid("Wrong login server") }
        do { let _: JSONValue = try await request("/github/cancel", body: ["request_id": pending.requestID, "verifier": pending.verifier]) }
        catch NativeError.http(401, _) {}
    }
    public func completeGitHub(_ pending: GitHubPending, username: String, name: String,
                               identity: DeviceIdentity) async throws -> AccountSession {
        try validate(pending)
        struct Result: Decodable { let username: String; let pub: String; let token: String }
        let result: Result = try await request("/github/complete", body: ["request_id": pending.requestID,
            "verifier": pending.verifier, "name": name, "pub": identity.publicKey.base64URL,
            "role": "phone", "proof": identity.enrollmentProof(username: username)])
        guard result.username == username, result.pub == identity.publicKey.base64URL else {
            throw NativeError.invalid("Invalid enrollment response")
        }
        return AccountSession(server: pending.server, token: result.token, username: username,
                              pub: result.pub, deviceSeed: identity.seed.base64URL)
    }
    private func validate(_ pending: GitHubPending) throws {
        guard pending.server == origin.absoluteString, pending.expires > Date() else {
            throw NativeError.invalid("GitHub login expired; start again")
        }
    }
}
