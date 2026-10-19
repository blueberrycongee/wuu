@testable import CUAMacCore
import XCTest

final class StreamRecoveryTests: XCTestCase {
    func testIdleCallbacksKeepAStaticStreamHealthy() {
        var freshness = CaptureFreshness()
        freshness.recordCallback(at: 1, displayedFrame: true, captureUnavailable: false)
        freshness.recordCallback(at: 9, displayedFrame: false, captureUnavailable: false)

        XCTAssertFalse(freshness.requiresRecovery(
            at: 11,
            streamStartedAt: 0,
            startupTimeout: 5,
            callbackSilenceTimeout: 3,
            unavailableStatusTimeout: 3
        ))
        XCTAssertEqual(freshness.lastHealthyCallbackUptime, 9)
    }

    func testSilentStreamRecoversAfterItPreviouslyDisplayedAFrame() {
        var freshness = CaptureFreshness()
        freshness.recordCallback(at: 1, displayedFrame: true, captureUnavailable: false)

        XCTAssertTrue(freshness.requiresRecovery(
            at: 4,
            streamStartedAt: 0,
            startupTimeout: 5,
            callbackSilenceTimeout: 3,
            unavailableStatusTimeout: 3
        ))
    }

    func testAStreamThatNeverStartsStillRequiresRecovery() {
        let freshness = CaptureFreshness()

        XCTAssertTrue(freshness.requiresRecovery(
            at: 5,
            streamStartedAt: 0,
            startupTimeout: 5,
            callbackSilenceTimeout: 3,
            unavailableStatusTimeout: 3
        ))
    }

    func testPersistentUnavailableStatusRequiresRecovery() {
        var freshness = CaptureFreshness()
        freshness.recordCallback(at: 1, displayedFrame: true, captureUnavailable: false)
        freshness.recordCallback(at: 2, displayedFrame: false, captureUnavailable: true)
        freshness.recordCallback(at: 4, displayedFrame: false, captureUnavailable: true)

        XCTAssertTrue(freshness.requiresRecovery(
            at: 5,
            streamStartedAt: 0,
            startupTimeout: 5,
            callbackSilenceTimeout: 3,
            unavailableStatusTimeout: 3
        ))
        XCTAssertEqual(freshness.unavailableSinceUptime, 2)
    }

    func testHealthyCallbackClearsUnavailableStatus() {
        var freshness = CaptureFreshness()
        freshness.recordCallback(at: 1, displayedFrame: true, captureUnavailable: false)
        freshness.recordCallback(at: 2, displayedFrame: false, captureUnavailable: true)
        freshness.recordCallback(at: 4, displayedFrame: false, captureUnavailable: false)

        XCTAssertNil(freshness.unavailableSinceUptime)
        XCTAssertFalse(freshness.requiresRecovery(
            at: 6,
            streamStartedAt: 0,
            startupTimeout: 5,
            callbackSilenceTimeout: 3,
            unavailableStatusTimeout: 3
        ))
    }
}
