import ApplicationServices
import Foundation

private final class AXChangeWaiter: @unchecked Sendable {
    private let lock = NSLock()
    private var didChange = false

    func signal() {
        lock.lock()
        didChange = true
        lock.unlock()
    }

    func changed() -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return didChange
    }
}

private let axChangeCallback: AXObserverCallback = { _, _, _, refcon in
    guard let refcon else { return }
    Unmanaged<AXChangeWaiter>.fromOpaque(refcon).takeUnretainedValue().signal()
}

private let axSettleNotifications = [
    kAXFocusedWindowChangedNotification,
    kAXFocusedUIElementChangedNotification,
    kAXWindowCreatedNotification,
    kAXValueChangedNotification,
    kAXSelectedTextChangedNotification,
    kAXUIElementDestroyedNotification,
    kAXRowCountChangedNotification,
    kAXLayoutChangedNotification,
]

private final class AXSettleWaiter: @unchecked Sendable {
    private let lock = NSLock()
    private var lastActivity = Date.distantPast
    private var sawActivity = false

    func signal() {
        lock.lock()
        lastActivity = Date()
        sawActivity = true
        lock.unlock()
    }

    // Quiet once no notification has arrived for `interval`. Before the first
    // notification the reference point is the caller-supplied start time.
    func quiet(for interval: TimeInterval, since start: Date) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        let reference = sawActivity ? lastActivity : start
        return Date().timeIntervalSince(reference) >= interval
    }
}

private let axSettleCallback: AXObserverCallback = { _, _, _, refcon in
    guard let refcon else { return }
    Unmanaged<AXSettleWaiter>.fromOpaque(refcon).takeUnretainedValue().signal()
}

// Wait for the target app's Accessibility tree to stop changing. Every AX
// notification resets a debounce window; once the app has been quiet for
// `debounce`, or the total `timeout` elapses, the UI is treated as settled. This
// is event-driven rather than fixed-interval polling, so it returns as soon as a
// fast app is done and still bounds the wait for a slow one.
func waitForAccessibilitySettle(application: AXUIElement, processID: pid_t, debounce: TimeInterval, timeout: TimeInterval) {
    let start = Date()
    var observer: AXObserver?
    guard AXObserverCreate(processID, axSettleCallback, &observer) == .success, let observer else {
        RunLoop.current.run(until: Date(timeIntervalSinceNow: debounce))
        return
    }
    let waiter = AXSettleWaiter()
    let refcon = Unmanaged.passUnretained(waiter).toOpaque()
    for notification in axSettleNotifications {
        _ = AXObserverAddNotification(observer, application, notification as CFString, refcon)
    }
    let source = AXObserverGetRunLoopSource(observer)
    let runLoop = CFRunLoopGetCurrent()
    CFRunLoopAddSource(runLoop, source, .defaultMode)
    defer {
        CFRunLoopRemoveSource(runLoop, source, .defaultMode)
        for notification in axSettleNotifications {
            _ = AXObserverRemoveNotification(observer, application, notification as CFString)
        }
    }
    let deadline = start.addingTimeInterval(timeout)
    while Date() < deadline {
        if waiter.quiet(for: debounce, since: start) { return }
        let slice = min(debounce, max(0.01, deadline.timeIntervalSinceNow))
        CFRunLoopRunInMode(.defaultMode, slice, true)
    }
}

func waitForAccessibilityChange(application: AXUIElement, processID: pid_t, timeout: TimeInterval) throws -> Bool {
    var observer: AXObserver?
    let createError = AXObserverCreate(processID, axChangeCallback, &observer)
    guard createError == .success, let observer else {
        throw ComputerError.operationFailed("create AXObserver failed with error \(createError.rawValue)")
    }
    let waiter = AXChangeWaiter()
    let refcon = Unmanaged.passUnretained(waiter).toOpaque()
    let notifications = [
        kAXFocusedWindowChangedNotification,
        kAXFocusedUIElementChangedNotification,
        kAXWindowCreatedNotification,
        kAXValueChangedNotification,
        kAXSelectedTextChangedNotification,
        kAXUIElementDestroyedNotification,
    ]
    for notification in notifications {
        _ = AXObserverAddNotification(observer, application, notification as CFString, refcon)
    }
    let source = AXObserverGetRunLoopSource(observer)
    let runLoop = CFRunLoopGetCurrent()
    CFRunLoopAddSource(runLoop, source, .defaultMode)
    defer {
        CFRunLoopRemoveSource(runLoop, source, .defaultMode)
        for notification in notifications {
            _ = AXObserverRemoveNotification(observer, application, notification as CFString)
        }
    }
    let deadline = Date(timeIntervalSinceNow: timeout)
    while !waiter.changed(), Date() < deadline {
        try ComputerExecution.checkpoint()
        CFRunLoopRunInMode(.defaultMode, min(0.1, deadline.timeIntervalSinceNow), true)
    }
    return waiter.changed()
}
