import Foundation
import CryptoKit

/// A pinned computer and this phone's identity. Store only in the device keychain.
public struct PairedComputer: Codable, Identifiable, Equatable, Sendable {
    public let hostPub: String
    public let hostName: String
    public let relayURL: String
    public let deviceSeed: String
    public var id: String { hostPub }

    public static func pair(_ input: String, deviceName: String) async throws -> PairedComputer {
        let link = try PairLink(input)
        let identity = try DeviceIdentity()
        let ephemeral = Curve25519.KeyAgreement.PrivateKey()
        let phoneEph = ephemeral.publicKey.rawRepresentation
        let shared = try ephemeral.sharedSecretFromKeyAgreement(with: Curve25519.KeyAgreement.PublicKey(rawRepresentation: link.ephemeral))
        let transcript = Data(SHA256.hash(data: Data.utf8("wuu/pair/v1") + link.ephemeral + phoneEph + Data.utf8(link.id)))
        let offerKey = shared.hkdfDerivedSymmetricKey(using: SHA256.self, salt: transcript,
            sharedInfo: Data.utf8("wuu pair offer v1"), outputByteCount: 32)
        let answerKey = shared.hkdfDerivedSymmetricKey(using: SHA256.self, salt: transcript,
            sharedInfo: Data.utf8("wuu pair answer v1"), outputByteCount: 32)
        let offer = try JSONEncoder().encode(["device_pub": identity.publicKey.base64URL, "name": deviceName, "platform": "ios"])
        let sealed = try AES.GCM.seal(offer, using: offerKey, authenticating: transcript)
        let payload = phoneEph + sealed.nonce.withUnsafeBytes { Data($0) } + sealed.ciphertext + sealed.tag
        let session = URLSession(configuration: .ephemeral, delegate: NoRedirect(), delegateQueue: nil)
        let socket = session.webSocketTask(with: link.relay)
        socket.maximumMessageSize = 64 * 1024
        defer {
            socket.cancel(with: .goingAway, reason: nil)
            session.invalidateAndCancel()
        }
        socket.resume()
        return try await withThrowingTaskGroup(of: PairedComputer.self) { group in
            group.addTask {
                try await withTaskCancellationHandler {
                    let message: JSONValue = ["type": "pair_offer", "pairing_id": .string(link.id), "payload": .string(payload.base64URL)]
                    try await socket.send(.string(String(decoding: JSONEncoder().encode(message), as: UTF8.self)))
                    while true {
                        let data: Data
                        switch try await socket.receive() {
                        case .data(let bytes): data = bytes
                        case .string(let text): data = Data(text.utf8)
                        @unknown default: throw NativeError.invalid("Unsupported pairing response")
                        }
                        let response = try JSONDecoder().decode(JSONValue.self, from: data)
                        switch response["type"].string {
                        case "pair_answer":
                            let bytes = try Data(base64URL: response["payload"].string ?? "")
                            let box = try AES.GCM.SealedBox(combined: bytes)
                            let plain = try AES.GCM.open(box, using: answerKey, authenticating: transcript)
                            let answer = try JSONDecoder().decode(PairAnswer.self, from: plain)
                            let host = try Data(base64URL: answer.host_pub)
                            guard host == link.host else { throw NativeError.invalid("电脑身份与配对码不一致，请在电脑上重新生成配对码") }
                            let key = try Curve25519.Signing.PublicKey(rawRepresentation: host)
                            guard key.isValidSignature(try Data(base64URL: answer.sig),
                                for: Data.signing("wuu/pair/confirm/v1", [transcript, identity.publicKey])) else {
                                throw NativeError.invalid("无法确认电脑身份，请在电脑上重新生成配对码")
                            }
                            return PairedComputer(hostPub: host.base64URL, hostName: answer.host_name,
                                relayURL: link.relay.absoluteString, deviceSeed: identity.seed.base64URL)
                        case "pair_err", "error":
                            throw NativeError.invalid("配对失败（\(response["code"].string ?? "rejected")），请在电脑上重新生成配对码后重试。")
                        default: break
                        }
                    }
                } onCancel: { socket.cancel(with: .goingAway, reason: nil) }
            }
            group.addTask {
                try await Task.sleep(for: .seconds(60))
                throw NativeError.invalid("配对超时，请在电脑上重新生成配对码后重试。")
            }
            defer { group.cancelAll() }
            return try await group.next()!
        }
    }
}

/// Relay traffic is encrypted and authenticated using the identity pinned in the pairing code.
func validateRelay(_ value: String) throws -> URL {
    guard let url = URLComponents(string: value), ["ws", "wss"].contains(url.scheme),
          let host = url.host, !host.isEmpty, url.user == nil, url.password == nil,
          url.fragment == nil, let result = url.url else {
        throw NativeError.invalid("配对码中的连接地址无效，请在电脑上重新生成")
    }
    return result
}

private struct PairAnswer: Decodable {
    let host_pub: String
    let host_name: String
    let sig: String
}

private struct PairLink {
    let id: String
    let relay: URL
    let ephemeral: Data
    let host: Data

    init(_ input: String) throws {
        var text = input.trimmingCharacters(in: .whitespacesAndNewlines)
        // Older desktop QR codes wrap the same URI in a web-client fragment.
        if let wrapper = URLComponents(string: text), ["https", "http"].contains(wrapper.scheme),
           let fragment = wrapper.percentEncodedFragment,
           let pair = URLComponents(string: "wuu://pair?" + fragment)?.queryItems?.first(where: { $0.name == "pair" })?.value {
            text = pair
        }
        guard let url = URLComponents(string: text), url.scheme == "wuu", url.host == "pair",
              url.user == nil, url.password == nil, url.fragment == nil else {
            throw NativeError.invalid("请粘贴或扫描电脑上 Wuu 显示的配对码")
        }
        let items = url.queryItems ?? []
        func field(_ name: String) throws -> String {
            let matches = items.filter { $0.name == name }
            guard matches.count == 1, let value = matches[0].value, !value.isEmpty else {
                throw NativeError.invalid("配对码不完整，请在电脑上重新生成。")
            }
            return value
        }
        guard try field("v") == "1" else { throw NativeError.invalid("请更新 Wuu 后再使用这个配对码") }
        id = try field("p")
        relay = try validateRelay(field("r"))
        ephemeral = try Data(base64URL: field("k"))
        host = try Data(base64URL: field("h"))
        guard ephemeral.count == 32, host.count == 32 else { throw NativeError.invalid("配对码无效，请在电脑上重新生成") }
    }
}
