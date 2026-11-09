import Foundation

// AppKit and ScreenCaptureKit may deliver completion handlers through the main
// run loop. A plain semaphore wait on the helper's main thread prevents those
// handlers from running, so alternate short waits with run-loop servicing.
func waitForAsyncSignal(_ semaphore: DispatchSemaphore, timeout: TimeInterval) -> Bool {
    let deadline = Date(timeIntervalSinceNow: timeout)
    while Date() < deadline {
        if semaphore.wait(timeout: .now() + 0.01) == .success { return true }
        let remaining = deadline.timeIntervalSinceNow
        if remaining <= 0 { break }
        CFRunLoopRunInMode(.defaultMode, min(0.02, remaining), true)
    }
    return semaphore.wait(timeout: .now()) == .success
}
