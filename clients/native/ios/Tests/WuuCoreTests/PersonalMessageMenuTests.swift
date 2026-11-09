import XCTest
@testable import WuuCore

final class PersonalMessageMenuTests: XCTestCase {
    func testBottomMessageMovesWithoutChangingWrappingWidth() {
        let layout = PersonalMessageMenuLayout(source: CGRect(x: 16, y: 405, width: 244, height: 287), viewport: CGRect(x: 0, y: 24, width: 360, height: 760), toolsHeight: 224)
        XCTAssertEqual(layout.message.width, 244)
        XCTAssertEqual(layout.tools.minX, layout.message.minX)
        XCTAssertEqual(layout.tools.width, 200)
        XCTAssertLessThan(layout.message.minY, 405)
        XCTAssertLessThanOrEqual(layout.tools.maxY, 772)
        XCTAssertEqual(layout.message.maxY + 24, layout.tools.minY)
    }
    func testTopMessageStaysInPlaceWhenItFits() {
        let source = CGRect(x: 16, y: 80, width: 180, height: 45)
        let layout = PersonalMessageMenuLayout(source: source, viewport: CGRect(x: 0, y: 24, width: 360, height: 760), toolsHeight: 224)
        XCTAssertEqual(layout.message, source)
    }
    func testLongMessageScrollsInsideKeyboardViewport() {
        let layout = PersonalMessageMenuLayout(source: CGRect(x: 16, y: -250, width: 244, height: 2000), viewport: CGRect(x: 0, y: 24, width: 360, height: 370), toolsHeight: 224)
        XCTAssertEqual(layout.message.width, 244)
        XCTAssertGreaterThan(layout.message.height, 0)
        XCTAssertLessThan(layout.message.height, 2000)
        XCTAssertGreaterThanOrEqual(layout.message.minY, 36)
        XCTAssertLessThanOrEqual(layout.tools.maxY, 382)
    }
    func testNarrowViewportKeepsActionsReachable() {
        let layout = PersonalMessageMenuLayout(source: CGRect(x: 16, y: 300, width: 180, height: 150), viewport: CGRect(x: 0, y: 0, width: 240, height: 280), toolsHeight: 224)
        XCTAssertLessThanOrEqual(layout.tools.maxX, 228)
        XCTAssertLessThanOrEqual(layout.tools.maxY, 268)
    }
}
