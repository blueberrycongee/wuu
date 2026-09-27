@testable import CUAMacCore
import XCTest

final class CompositeLayoutTests: XCTestCase {
    func testScaleMultipliesOriginAndSizeUniformly() {
        let frames = [
            CGRect(x: 100, y: 50, width: 800, height: 600),
            CGRect(x: 1000, y: 50, width: 400, height: 300),
        ]
        let layout = compositeLayout(frames: frames, scale: 2)
        // Union frame stays in points (global coordinates); only pixel placements scale.
        XCTAssertEqual(layout.union, CGRect(x: 100, y: 50, width: 1300, height: 600))
        XCTAssertEqual(layout.placements[0], CGRect(x: 0, y: 0, width: 1600, height: 1200))
        XCTAssertEqual(layout.placements[1], CGRect(x: 1800, y: 0, width: 800, height: 600))
    }

    func testVerticalStackAndNegativeOriginsProducePixelOffsets() {
        // Windows on an upper display sit at negative global y; the union origin still
        // anchors placements at zero so no window lands at a negative pixel offset.
        let frames = [
            CGRect(x: 0, y: -900, width: 1440, height: 900),
            CGRect(x: 0, y: 0, width: 1440, height: 900),
        ]
        let layout = compositeLayout(frames: frames, scale: 2)
        XCTAssertEqual(layout.union, CGRect(x: 0, y: -900, width: 1440, height: 1800))
        XCTAssertEqual(layout.placements[0], CGRect(x: 0, y: 0, width: 2880, height: 1800))
        XCTAssertEqual(layout.placements[1], CGRect(x: 0, y: 1800, width: 2880, height: 1800))
    }

    func testNonPositiveScaleFallsBackToUnitScale() {
        let frames = [CGRect(x: 10, y: 10, width: 100, height: 100)]
        let layout = compositeLayout(frames: frames, scale: 0)
        XCTAssertEqual(layout.placements[0], CGRect(x: 0, y: 0, width: 100, height: 100))
    }

    func testEmptyFramesYieldZeroUnionAndNoPlacements() {
        let layout = compositeLayout(frames: [], scale: 2)
        XCTAssertEqual(layout.union, .zero)
        XCTAssertTrue(layout.placements.isEmpty)
    }
}
