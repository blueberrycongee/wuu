import AppKit
import CoreGraphics
import Darwin
import Foundation

// Level 2 "background directed input": synthesize keyboard and mouse events and
// deliver them straight to a target process with CGEvent.postToPid, so the app
// receives them without being activated. The user's frontmost app keeps focus
// and the real pointer never moves (postToPid does not warp the hardware
// cursor). Apps that only honor genuine foreground input are handled by letting
// the model escalate to level 3 (activate + global CGEvent) via foreground_policy.

private func directedWindowID(_ pid: pid_t, at point: CGPoint) -> CGWindowID? {
    windowFrames(processID: pid).filter { $0.value.contains(point) }
        .min { $0.value.width * $0.value.height < $1.value.width * $1.value.height }?.key
}

private typealias SetWindowLocation = @convention(c) (CGEvent, Double, Double) -> Void

private func directedPointerPoster(_ pid: pid_t, at point: CGPoint?) throws -> (CGEvent) -> Void {
    // Public CGEvent.location does not populate the separate location used by
    // AppKit for PID-directed hit testing. Resolve this SPI before any button is
    // held, so cleanup never needs to load a symbol or throw.
    guard let symbol = dlsym(UnsafeMutableRawPointer(bitPattern: -2), "CGEventSetWindowLocation") else {
        throw ComputerError.requiresForeground("background pointer window-location support is unavailable")
    }
    let setWindowLocation = unsafeBitCast(symbol, to: SetWindowLocation.self)
    let windowID = point.flatMap { directedWindowID(pid, at: $0) }
    return { postPointerEvent($0, to: pid, windowID: windowID, setWindowLocation: setWindowLocation) }
}

private func postPointerEvent(_ event: CGEvent, to pid: pid_t, windowID: CGWindowID?, setWindowLocation: SetWindowLocation) {
    // postToPid bypasses WindowServer hit testing. Supply both the AppKit window
    // identity and Quartz hit-test fields; coordinates alone arrive at window 0.
    guard let windowID, let frame = windowFrames(processID: pid)[windowID],
          let type = NSEvent.EventType(rawValue: UInt(event.type.rawValue)) else {
        event.postToPid(pid)
        return
    }
    let point = event.location
    let native = NSEvent.mouseEvent(with: type == .scrollWheel ? .mouseMoved : type,
        location: CGPoint(x: point.x - frame.minX, y: frame.maxY - point.y),
        modifierFlags: NSEvent.ModifierFlags(rawValue: UInt(event.flags.rawValue)),
        timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: Int(windowID), context: nil,
        eventNumber: Int(event.getIntegerValueField(.mouseEventNumber)),
        clickCount: Int(event.getIntegerValueField(.mouseEventClickState)),
        pressure: Float(event.getDoubleValueField(.mouseEventPressure)))
    let routed = markSynthetic(native?.cgEvent) ?? event
    routed.type = event.type
    routed.location = point
    // The NSEvent bridge plus public PID transport expects a window-relative,
    // top-left point here. AppKit flips it to locationInWindow on receipt.
    setWindowLocation(routed, point.x - frame.minX, point.y - frame.minY)
    routed.flags = event.flags
    routed.setIntegerValueField(.mouseEventWindowUnderMousePointer, value: Int64(windowID))
    routed.setIntegerValueField(.mouseEventWindowUnderMousePointerThatCanHandleThisEvent, value: Int64(windowID))
    if type == .scrollWheel {
        for field: CGEventField in [.scrollWheelEventDeltaAxis1, .scrollWheelEventDeltaAxis2,
                                   .scrollWheelEventFixedPtDeltaAxis1, .scrollWheelEventFixedPtDeltaAxis2,
                                   .scrollWheelEventPointDeltaAxis1, .scrollWheelEventPointDeltaAxis2,
                                   .scrollWheelEventIsContinuous] {
            routed.setIntegerValueField(field, value: event.getIntegerValueField(field))
        }
    }
    routed.postToPid(pid)
}

// A single CGEventKeyboardSetUnicodeString payload is delivered as one event, and
// the HID system drops long strings, so type in small chunks split only on code
// point boundaries (never inside a UTF-16 surrogate pair).
private let unicodeChunkUnits = 16

func unicodeChunks(_ text: String) -> [[UniChar]] {
    var chunks: [[UniChar]] = []
    var current: [UniChar] = []
    for scalar in text.unicodeScalars {
        let units = Array(String(scalar).utf16)
        if current.count + units.count > unicodeChunkUnits, !current.isEmpty {
            chunks.append(current)
            current = []
        }
        current.append(contentsOf: units)
    }
    if !current.isEmpty { chunks.append(current) }
    return chunks
}

func mouseTypes(for name: String?) -> (button: CGMouseButton, down: CGEventType, up: CGEventType) {
    switch name?.lowercased() ?? "left" {
    case "right": return (.right, .rightMouseDown, .rightMouseUp)
    case "middle": return (.center, .otherMouseDown, .otherMouseUp)
    default: return (.left, .leftMouseDown, .leftMouseUp)
    }
}

func postKeyChordToPid(_ pid: pid_t, chord: KeyChord, duration: TimeInterval = 0) throws {
    try withHeldKey(chord, duration: duration, post: { $0.postToPid(pid) }) {}
}

func postClickToPid(_ pid: pid_t, point: CGPoint, button name: String?, count: Int, modifiers: KeyModifiers = [], duration: TimeInterval = 0, heldKey: KeyChord? = nil) throws {
    let post = try directedPointerPoster(pid, at: point)
    try withHeldKey(heldKey, post: { $0.postToPid(pid) }) {
        try postClicks(point: point, button: name, count: count, modifiers: modifiers, duration: duration, post: post)
    }
}

func postScrollToPid(_ pid: pid_t, vertical: Int32, horizontal: Int32, steps: Int, at point: CGPoint?) throws {
    let source = syntheticEventSource()
    let post = try directedPointerPoster(pid, at: point)
    for _ in 0..<max(1, steps) {
        guard let event = markSynthetic(CGEvent(scrollWheelEvent2Source: source, units: .line, wheelCount: 2, wheel1: vertical, wheel2: horizontal, wheel3: 0)) else {
            throw ComputerError.operationFailed("could not create scroll event")
        }
        // postToPid does not move the cursor, so pin the scroll to the target point;
        // otherwise the app hit-tests it at the global origin.
        if let point { event.location = point }
        try ComputerExecution.input()
        post(event)
        usleep(16_000)
    }
}

func postDragToPid(_ pid: pid_t, from start: CGPoint, to end: CGPoint) throws {
    let source = syntheticEventSource()
    let post = try directedPointerPoster(pid, at: start)
    guard let down = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseDown, mouseCursorPosition: start, mouseButton: .left)),
          let up = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseUp, mouseCursorPosition: end, mouseButton: .left)) else {
        throw ComputerError.operationFailed("could not create drag event")
    }
    try ComputerExecution.input()
    post(down)
    var lastPoint = start
    defer { up.location = lastPoint; post(up) }
    for step in 1...12 {
        try ComputerExecution.checkpoint()
        let progress = Double(step) / 12
        let point = CGPoint(
            x: start.x + (end.x - start.x) * progress,
            y: start.y + (end.y - start.y) * progress
        )
        if let dragged = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseDragged, mouseCursorPosition: point, mouseButton: .left)) {
            post(dragged)
            lastPoint = point
        }
        usleep(8_000)
    }
}
