import XCTest
@testable import WuuCore

final class DirectoryTests: XCTestCase {
    func testOfflineDirectoryDoesNotRestorePresenceOrCrossLogins() throws {
        let session = AccountSession(server: "https://example.test", token: "first", username: "alice", pub: "phone", deviceSeed: "seed")
        let directory = DeviceDirectory(devices: [AccountDevice(pub: "host", name: "Computer", role: "host", online: true)], auth_method: "password")
        let bytes = try JSONEncoder().encode(RememberedDirectory(account: session, directory: directory))
        let disk = try JSONDecoder().decode(RememberedDirectory.self, from: bytes)
        let offline = try XCTUnwrap(disk.restore(account: session))
        XCTAssertEqual(offline.devices.first?.pub, "host"); XCTAssertFalse(offline.devices.first!.online)
        for other in [
            AccountSession(server: session.server, token: "second", username: session.username, pub: session.pub, deviceSeed: "seed"),
            AccountSession(server: "https://other.test", token: session.token, username: session.username, pub: session.pub, deviceSeed: "seed"),
            AccountSession(server: session.server, token: session.token, username: "bob", pub: session.pub, deviceSeed: "seed"),
            AccountSession(server: session.server, token: session.token, username: session.username, pub: "other-phone", deviceSeed: "seed")
        ] { XCTAssertNil(try disk.restore(account: other)) }
    }
}
