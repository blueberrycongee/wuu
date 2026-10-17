import ApplicationServices
import Foundation

private let activityNotifications = [
    kAXFocusedWindowChangedNotification, kAXFocusedUIElementChangedNotification,
    kAXWindowCreatedNotification, kAXValueChangedNotification,
    kAXSelectedTextChangedNotification, kAXUIElementDestroyedNotification,
    kAXRowCountChangedNotification, kAXLayoutChangedNotification,
    kAXMenuOpenedNotification, kAXMenuClosedNotification,
]

// Arm before sending input so synchronous changes and menu transitions are not
// lost. The observer and its callback context share one lifetime.
final class AXActivityMonitor {
    private var observer: AXObserver?
    private var watched: [AXUIElement] = []
    private let runLoop = CFRunLoopGetCurrent()
    private var lastActivity = ProcessInfo.processInfo.systemUptime
    private(set) var changed = false

    init(application: AXUIElement, processID: pid_t) {
        let callback: AXObserverCallback = { _, _, _, refcon in
            guard let refcon else { return }
            let monitor = Unmanaged<AXActivityMonitor>.fromOpaque(refcon).takeUnretainedValue()
            monitor.changed = true
            monitor.lastActivity = ProcessInfo.processInfo.systemUptime
        }
        guard AXObserverCreate(processID, callback, &observer) == .success, let observer else { return }
        watched = [application] + axElements(application, attribute: kAXWindowsAttribute as String)
        if let focused = axValue(application, kAXFocusedUIElementAttribute as String),
           CFGetTypeID(focused) == AXUIElementGetTypeID() {
            watched.append(focused as! AXUIElement)
        }
        let refcon = Unmanaged.passUnretained(self).toOpaque()
        for element in watched {
            for notification in activityNotifications {
                _ = AXObserverAddNotification(observer, element, notification as CFString, refcon)
            }
        }
        CFRunLoopAddSource(runLoop, AXObserverGetRunLoopSource(observer), .defaultMode)
    }

    deinit {
        guard let observer else { return }
        CFRunLoopRemoveSource(runLoop, AXObserverGetRunLoopSource(observer), .defaultMode)
        for element in watched {
            for notification in activityNotifications {
                _ = AXObserverRemoveNotification(observer, element, notification as CFString)
            }
        }
    }

    // Sample AX and/or pixels as well as notifications: canvas and some web
    // applications update without emitting Accessibility events. Quiet means an
    // observation boundary, not a verified application postcondition.
    func settle(debounce: TimeInterval = 0.2, timeout: TimeInterval = 1.5,
                sample: () -> String?) throws -> String {
        let start = ProcessInfo.processInfo.systemUptime
        var previous = sample()
        lastActivity = max(lastActivity, ProcessInfo.processInfo.systemUptime)
        let deadline = start + timeout
        repeat {
            try ComputerExecution.checkpoint()
            CFRunLoopRunInMode(.defaultMode, 0.05, false)
            let current = sample()
            let now = ProcessInfo.processInfo.systemUptime
            if current != previous { previous = current; lastActivity = now }
            if now >= deadline { return "timed_out" }
            if now - lastActivity >= debounce { return current == nil && observer == nil ? "unavailable" : "quiet" }
        } while true
    }

    func waitForChange(timeout: TimeInterval) throws -> Bool {
        guard observer != nil else { throw ComputerError.operationFailed("could not create AXObserver") }
        let deadline = ProcessInfo.processInfo.systemUptime + timeout
        while !changed, ProcessInfo.processInfo.systemUptime < deadline {
            try ComputerExecution.checkpoint()
            CFRunLoopRunInMode(.defaultMode, 0.05, true)
        }
        return changed
    }
}

func waitForAccessibilityChange(application: AXUIElement, processID: pid_t, timeout: TimeInterval) throws -> Bool {
    try AXActivityMonitor(application: application, processID: processID).waitForChange(timeout: timeout)
}
