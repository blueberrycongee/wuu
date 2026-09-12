import Foundation
import CryptoKit

public struct AccountSession: Codable, Equatable, Sendable {
    public let server: String
    public let token: String
    public let username: String
    public let pub: String
    public let deviceSeed: String
    public var recovery: String? = nil
}

public struct AccountDevice: Codable, Identifiable, Sendable {
    public let pub: String
    public let name: String
    public let role: String
    public let online: Bool
    public var id: String { pub }
}

public struct DeviceDirectory: Codable, Sendable {
    public let devices: [AccountDevice]
    public let auth_method: String
}

/// Last authorized directory, usable only by the same login. Presence is never restored as live.
public struct RememberedDirectory: Codable {
    private let login: String
    private let directory: DeviceDirectory
    private static func key(_ account: AccountSession) throws -> String {
        let data = try JSONEncoder().encode([account.server, account.username, account.pub, account.token])
        return Data(SHA256.hash(data: data)).base64URL
    }
    public init(account: AccountSession, directory: DeviceDirectory) throws {
        login = try Self.key(account); self.directory = directory
    }
    public func restore(account: AccountSession) throws -> DeviceDirectory? {
        guard login == (try Self.key(account)) else { return nil }
        return DeviceDirectory(devices: directory.devices.map {
            AccountDevice(pub: $0.pub, name: $0.name, role: $0.role, online: false)
        }, auth_method: directory.auth_method)
    }
}

public struct AccountConfiguration: Decodable, Sendable {
    public let registration: Bool
    public let github: Bool
    public let push_platforms: [String]?
}

final class NoRedirect: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) {
        completionHandler(nil)
    }
}

public final class AccountAPI: @unchecked Sendable {
    public let origin: URL
    private let transport: URLSession

    public static func validateOrigin(_ value: String) throws -> URL {
        guard let c = URLComponents(string: value.trimmingCharacters(in: .whitespacesAndNewlines)),
              let host = c.host, !host.isEmpty, c.user == nil, c.password == nil,
              c.query == nil, c.fragment == nil, c.path.isEmpty || c.path == "/",
              c.scheme == "https" || (c.scheme == "http" && ["localhost", "127.0.0.1", "[::1]", "::1"].contains(host)),
              let url = c.url else { throw NativeError.invalid("Enter an HTTPS server origin without a path") }
        var canonical = URLComponents(url: url, resolvingAgainstBaseURL: false)!
        canonical.path = ""
        return canonical.url!
    }
    public init(server: String) throws {
        origin = try Self.validateOrigin(server)
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 15
        config.timeoutIntervalForResource = 30
        transport = URLSession(configuration: config, delegate: NoRedirect(), delegateQueue: nil)
    }
    deinit { transport.invalidateAndCancel() }

    public func request<T: Decodable>(_ path: String, token: String? = nil,
                                      query: [String: String] = [:], body: [String: Any]? = nil, method: String? = nil) async throws -> T {
        var components = URLComponents(url: origin.appendingPathComponent("v1/account" + path), resolvingAgainstBaseURL: false)!
        if !query.isEmpty { components.queryItems = query.map { URLQueryItem(name: $0.key, value: $0.value) } }
        var request = URLRequest(url: components.url!)
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token { request.setValue("Bearer " + token, forHTTPHeaderField: "Authorization") }
        if let body {
            request.httpMethod = "POST"
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        if let method { request.httpMethod = method }
        let (data, response) = try await transport.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw NativeError.invalid("Invalid server response") }
        guard (200..<300).contains(http.statusCode) else {
            let message = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String ?? "Request failed"
            throw NativeError.http(http.statusCode, message)
        }
        return try JSONDecoder().decode(T.self, from: data)
    }

    public func login(username: String, password: String, name: String, identity: DeviceIdentity, register: Bool = false) async throws -> AccountSession {
        let user = username.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        struct Result: Decodable { let token: String; let username: String; let pub: String; let recovery: String? }
        let result: Result = try await request(register ? "/register" : "/login", body: ["username": user, "password": password,
            "name": name, "role": "phone", "pub": identity.publicKey.base64URL, "proof": identity.enrollmentProof(username: user)])
        guard result.pub == identity.publicKey.base64URL, result.username == user else { throw NativeError.invalid("Server returned a different device identity") }
        return AccountSession(server: origin.absoluteString, token: result.token, username: result.username,
                              pub: result.pub, deviceSeed: identity.seed.base64URL, recovery: result.recovery)
    }

    /// Changes the password and rotates the recovery key. The server revokes every device.
    public func resetPassword(username: String, secret: String, password: String, token: String? = nil) async throws -> String {
        struct Result: Decodable { let recovery: String }
        let result: Result = try await request(token == nil ? "/recover" : "/password", token: token,
            body: ["username": username.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(), "secret": secret, "password": password])
        return result.recovery
    }

    public func logout(token: String) async throws {
        do { let _: JSONValue = try await request("/logout", token: token, body: [:]) }
        catch NativeError.http(401, _) { /* Already revoked. */ }
    }
}
