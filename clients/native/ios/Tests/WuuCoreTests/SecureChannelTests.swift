import XCTest
import CryptoKit
@testable import WuuCore

final class SecureChannelTests: XCTestCase {
    // Golden frames produced by the Go host, not by this implementation.
    func testGoWireCompatibilityAndReplayRejection() throws {
        let shared = try Data(base64URL: "7m04smrnw6Ze4LRwsN4zMQXTX47eCxLNlBh_xQb3KDo")
        let transcript = try Data(base64URL: "qE1-R4qo148jV39azZgImQcYgU6oCIMl93ZGQtwip8c")
        let phone = SecureChannel(shared: shared, transcript: transcript)
        let host = SecureChannel(shared: shared, transcript: transcript, phoneSide: false)
        let plain = try Data(base64URL: "eyJpZCI6IjEiLCJtZXRob2QiOiJpbml0aWFsaXplIiwicGFyYW1zIjp7fX0K")
        let frame = try phone.seal(plain)
        XCTAssertEqual(frame.base64URL, "AAAAAAAAAAAAAAABNPgt9aU7lsVM27S0hKYHSGglgVNHg5pTDRukVqL5hcxpwj20i7G_aSR16KuWSrh4Be1ENDnK5BTTR8Ot7A")
        var tampered = frame
        tampered[tampered.count - 1] ^= 1
        XCTAssertThrowsError(try host.open(tampered))
        XCTAssertEqual(try host.open(frame), plain)
        XCTAssertThrowsError(try host.open(frame))
        XCTAssertEqual(try phone.seal(Data()).base64URL, "AAAAAAAAAAAAAAACMFFBfnrSyNAuNXtJts4oxw")
    }
    func testGoEnrollmentIdentityAndRelaySignature() throws {
        let identity = try DeviceIdentity(seed: Data(base64URL: "4Yxwscu9AXnFoXJkd-VQ3-CESwiDldRP4YXkla7kOXI"))
        XCTAssertEqual(identity.publicKey.base64URL, "-yy5BmoxzRhqVEmIv1bynQUcrRHPo66aRq6vUXfZ6vo")
        XCTAssertEqual(Data.signing("wuu/relay/auth/v1", [try Data(base64URL: "7SlatUzvq_mKJiLsN8a0IZztjWIvwg7AgjhRx12kRgY"), identity.publicKey, Data.utf8("phone")]).base64URL,
                       "d3V1L3JlbGF5L2F1dGgvdjEAAAAAIO0pWrVM76v5iiYi7DfGtCGc7Y1iL8IOwII4UcddpEYGAAAAIPssuQZqMc0YalRJiL9W8p0FHK0Rz6Oumkaur1F32er6AAAABXBob25l")
        let buffer = Data.signing("wuu/relay/auth/v1", [try Data(base64URL: "7SlatUzvq_mKJiLsN8a0IZztjWIvwg7AgjhRx12kRgY"), identity.publicKey, Data.utf8("phone")])
        let publicKey = try Curve25519.Signing.PublicKey(rawRepresentation: identity.publicKey)
        XCTAssertTrue(publicKey.isValidSignature(try Data(base64URL: "D3zcAOFooDUQJEEi3dPZ8bLqw3khpjNy-KaXxsQ8MfUrs866EH3-syKe-GxQYrAGecHiseM39heS64y07hRUAw"), for: buffer))
        // CryptoKit may randomize Ed25519 signatures; verify the contract rather than byte equality.
        XCTAssertTrue(publicKey.isValidSignature(try Data(base64URL: identity.relayProof(nonce: Data(base64URL: "7SlatUzvq_mKJiLsN8a0IZztjWIvwg7AgjhRx12kRgY"))), for: buffer))
    }
    func testOriginRejectsCredentialLeakAndCleartext() throws {
        for value in ["http://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?token=x", "https://example.com#x", "file:///tmp"] {
            XCTAssertThrowsError(try AccountAPI.validateOrigin(value), value)
        }
        XCTAssertEqual(try AccountAPI.validateOrigin("https://example.com").host, "example.com")
        XCTAssertNoThrow(try AccountAPI.validateOrigin("http://127.0.0.1:8787"))
    }
}
