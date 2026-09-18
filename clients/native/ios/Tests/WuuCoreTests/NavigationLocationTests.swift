import XCTest
@testable import WuuCore

final class NavigationLocationTests: XCTestCase {
    func testColdBookmarkRestoresBothModesOnlyForAuthorizedHostAndLogin() throws {
        let account = AccountSession(server: "https://example.test", token: "token", username: "alice", pub: "phone", deviceSeed: "seed")
        let devices = [AccountDevice(pub: "host", name: "Computer", role: "host", online: false)]
        for mode in ["collaboration", "harness"] {
            let location = try NavigationLocation(account: account, host: "host", workspace: "/work", thread: "thread", room: "room", mode: mode)
            let disk = try JSONDecoder().decode(NavigationLocation.self, from: JSONEncoder().encode(location))
            XCTAssertEqual(try disk.restore(account: account, devices: devices), location)
            XCTAssertNil(try disk.restore(account: account, devices: []))
            XCTAssertNil(try disk.restore(account: account, devices: [AccountDevice(pub: "host", name: "Phone", role: "phone", online: true)]))
            for other in [
                AccountSession(server: "https://other.test", token: account.token, username: account.username, pub: account.pub, deviceSeed: "seed"),
                AccountSession(server: account.server, token: "new-login", username: account.username, pub: account.pub, deviceSeed: "seed"),
                AccountSession(server: account.server, token: account.token, username: "bob", pub: account.pub, deviceSeed: "seed")
            ] { XCTAssertNil(try disk.restore(account: other, devices: devices)) }
        }
    }
}
