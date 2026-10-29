import XCTest
@testable import WuuCore
final class PersonalShareInboxTests: XCTestCase {
 func testAccountIsolationExpiryAndAck() throws {
  let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
  defer { try? FileManager.default.removeItem(at: root) }
  let box = PersonalShareInbox(directory: root)
  let binding = PersonalShareInbox.binding(["account-a", "host-a"])
  try box.enable(binding: binding, now: Date(timeIntervalSince1970: 100))
  let lease = try box.begin(now: Date(timeIntervalSince1970: 101))
  let item = try box.append(text: "quoted text", attachments: [], expectedLease: lease, now: Date(timeIntervalSince1970: 101))
  XCTAssertEqual(try box.first(binding: binding)?.id, item.id)
  try box.enable(binding: PersonalShareInbox.binding(["account-b"]), now: Date(timeIntervalSince1970: 102))
  XCTAssertThrowsError(try box.append(text: "raced share", attachments: [], expectedLease: lease, now: Date(timeIntervalSince1970: 103)))
  XCTAssertNil(try box.first(binding: PersonalShareInbox.binding(["other"])))
  XCTAssertThrowsError(try box.append(text: "expired", attachments: [], expectedLease: lease, now: Date(timeIntervalSince1970: 1000)))
  try box.remove(id: item.id, binding: binding)
  XCTAssertNil(try box.first(binding: binding))
 }
 func testNoPathsNoEmptyAndNoOversize() throws {
  let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
  defer { try? FileManager.default.removeItem(at: root) }
  let box = PersonalShareInbox(directory: root), binding = PersonalShareInbox.binding(["a"])
  try box.enable(binding: binding)
  let lease = try box.begin()
  XCTAssertThrowsError(try box.append(text: "", attachments: [], expectedLease: lease))
  XCTAssertThrowsError(try box.append(text: String(repeating: "x", count: 64001), attachments: [], expectedLease: lease))
  XCTAssertThrowsError(try box.remove(id: "../../outside", binding: binding))
 }
}
