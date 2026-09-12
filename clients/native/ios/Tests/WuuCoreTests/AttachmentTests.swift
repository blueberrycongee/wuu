import Foundation
import XCTest
@testable import WuuCore

final class AttachmentTests: XCTestCase {
    func testCombinedPayloadBudgetAndUTF8TextLimit() throws {
        let large = try InputAttachment(filename: "document.pdf", mediaType: "application/pdf", data: Data(repeating: 1, count: InputAttachment.maxBytes))
        let small = try InputAttachment(filename: "small.pdf", mediaType: "application/pdf", data: Data([1]))
        XCTAssertNoThrow(try InputAttachment.validate([large], text: ""))
        XCTAssertThrowsError(try InputAttachment.validate([large, small], text: ""))
        XCTAssertThrowsError(try InputAttachment.validate(Array(repeating: small, count: 5), text: ""))
        XCTAssertThrowsError(try InputAttachment.validate([], text: String(repeating: "🌱", count: 40_000)))
        XCTAssertThrowsError(try InputAttachment(filename: "empty.pdf", mediaType: "application/pdf", data: Data()))
    }
}
