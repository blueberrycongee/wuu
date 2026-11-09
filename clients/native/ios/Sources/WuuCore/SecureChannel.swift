import Foundation
import CryptoKit

public enum NativeError: Error, LocalizedError {
    case invalid(String)
    case http(Int, String)
    public var errorDescription: String? {
        switch self {
        case .invalid(let message): return message
        case .http(let code, let message): return "HTTP \(code): \(message)"
        }
    }
}

extension Data {
    public var base64URL: String {
        base64EncodedString().replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
    }
    public init(base64URL: String) throws {
        let text = base64URL.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        guard let data = Data(base64Encoded: text + String(repeating: "=", count: (4 - text.count % 4) % 4)) else {
            throw NativeError.invalid("Invalid base64url")
        }
        self = data
    }
    static func utf8(_ text: String) -> Data { Data(text.utf8) }
    static func framed(_ parts: [Data]) -> Data {
        var result = Data()
        for part in parts {
            var length = UInt32(part.count).bigEndian
            Swift.withUnsafeBytes(of: &length) { result.append(contentsOf: $0) }
            result.append(part)
        }
        return result
    }
    static func signing(_ label: String, _ parts: [Data]) -> Data {
        Data.utf8(label) + Data([0]) + Data.framed(parts)
    }
}

public struct DeviceIdentity {
    private let key: Curve25519.Signing.PrivateKey
    public init(seed: Data? = nil) throws {
        key = try seed.map { try Curve25519.Signing.PrivateKey(rawRepresentation: $0) } ?? Curve25519.Signing.PrivateKey()
    }
    public var seed: Data { key.rawRepresentation }
    public var publicKey: Data { key.publicKey.rawRepresentation }
    public func sign(_ data: Data) throws -> Data { try key.signature(for: data) }
    public func relayProof(nonce: Data) throws -> String {
        try sign(.signing("wuu/relay/auth/v1", [nonce, publicKey, .utf8("phone")])).base64URL
    }
    public func enrollmentProof(username: String) throws -> String {
        try relayProof(nonce: .utf8("wuu/account/enroll/v1:" + username))
    }
}

public struct HandshakeReply: Codable {
    public let eph: String
    public let nonce: String
    public let sig: String
}

public struct PhoneHandshake {
    private let identity: DeviceIdentity
    private let host: Data
    private let ephemeral: Curve25519.KeyAgreement.PrivateKey
    private let nonce: Data

    public init(identity: DeviceIdentity, host: Data) throws {
        guard host.count == 32 else { throw NativeError.invalid("Invalid host key") }
        self.identity = identity
        self.host = host
        ephemeral = Curve25519.KeyAgreement.PrivateKey()
        nonce = Data((0..<16).map { _ in UInt8.random(in: 0...255) })
    }
    public func offer() throws -> [String: String] {
        let eph = ephemeral.publicKey.rawRepresentation
        let signature = try identity.sign(.signing("wuu/hs1/v1", [host, identity.publicKey, eph, nonce]))
        return ["t": "hs1", "device_pub": identity.publicKey.base64URL, "eph": eph.base64URL,
                "nonce": nonce.base64URL, "sig": signature.base64URL]
    }
    public func finish(_ reply: HandshakeReply) throws -> SecureChannel {
        let hostEph = try Data(base64URL: reply.eph)
        let hostNonce = try Data(base64URL: reply.nonce)
        guard hostEph.count == 32, hostNonce.count == 16 else { throw NativeError.invalid("Invalid handshake") }
        let parts = [host, identity.publicKey, ephemeral.publicKey.rawRepresentation, nonce, hostEph, hostNonce]
        let hostKey = try Curve25519.Signing.PublicKey(rawRepresentation: host)
        guard hostKey.isValidSignature(try Data(base64URL: reply.sig), for: Data.signing("wuu/hs2/v1", parts)) else {
            throw NativeError.invalid("Host authentication failed")
        }
        let secret = try ephemeral.sharedSecretFromKeyAgreement(with: Curve25519.KeyAgreement.PublicKey(rawRepresentation: hostEph))
        let transcript = Data(SHA256.hash(data: Data.utf8("wuu/session/v1") + Data.framed(parts)))
        return SecureChannel(shared: secret.withUnsafeBytes { Data($0) }, transcript: transcript)
    }
}

/// Confine each channel to one transport actor. Counters must never be reused.
public final class SecureChannel {
    private let sendKey: SymmetricKey
    private let receiveKey: SymmetricKey
    private let transcript: Data
    private var sent: UInt64 = 0
    private var received: UInt64 = 0

    init(shared: Data, transcript: Data, phoneSide: Bool = true) {
        func key(_ direction: String) -> SymmetricKey {
            HKDF<SHA256>.deriveKey(inputKeyMaterial: SymmetricKey(data: shared), salt: transcript,
                                  info: Data.utf8("wuu e2e \(direction) v1"), outputByteCount: 32)
        }
        sendKey = key(phoneSide ? "phone->host" : "host->phone")
        receiveKey = key(phoneSide ? "host->phone" : "phone->host")
        self.transcript = transcript
    }
    public func seal(_ plain: Data) throws -> Data {
        guard sent < UInt64.max else { throw NativeError.invalid("Channel counter exhausted") }
        sent += 1
        var counter = sent.bigEndian
        let nonceData = Data(repeating: 0, count: 4) + withUnsafeBytes(of: &counter) { Data($0) }
        let box = try AES.GCM.seal(plain, using: sendKey, nonce: AES.GCM.Nonce(data: nonceData), authenticating: transcript)
        return nonceData + box.ciphertext + box.tag
    }
    public func open(_ frame: Data) throws -> Data {
        guard frame.count >= 28 else { throw NativeError.invalid("Truncated sealed frame") }
        let counter = frame.dropFirst(4).prefix(8).reduce(UInt64(0)) { ($0 << 8) | UInt64($1) }
        guard counter > received else { throw NativeError.invalid("Replayed sealed frame") }
        let box = try AES.GCM.SealedBox(nonce: AES.GCM.Nonce(data: frame.prefix(12)),
                                        ciphertext: frame.dropFirst(12).dropLast(16), tag: frame.suffix(16))
        let plain = try AES.GCM.open(box, using: receiveKey, authenticating: transcript)
        received = counter
        return plain
    }
}
