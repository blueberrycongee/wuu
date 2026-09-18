import CoreGraphics
import Darwin
import Foundation

// Level 2 "background directed input": synthesize keyboard and mouse events and
// deliver them straight to a target process with CGEvent.postToPid, so the app
// receives them without being activated. The user's frontmost app keeps focus
// and the real pointer never moves (postToPid does not warp the hardware
// cursor). Apps that only honor genuine foreground input are handled by letting
// the model escalate to level 3 (activate + global CGEvent) via foreground_policy.

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

func postKeyChordToPid(_ pid: pid_t, chord: KeyChord) throws {
    let source = syntheticEventSource()
    guard let down = markSynthetic(CGEvent(keyboardEventSource: source, virtualKey: chord.keyCode, keyDown: true)),
          let up = markSynthetic(CGEvent(keyboardEventSource: source, virtualKey: chord.keyCode, keyDown: false)) else {
        throw ComputerError.operationFailed("could not create keyboard event")
    }
    down.flags = chord.modifiers.eventFlags
    up.flags = chord.modifiers.eventFlags
    try ComputerExecution.input()
    down.postToPid(pid)
    up.postToPid(pid)
}

func postUnicodeToPid(_ pid: pid_t, text: String) throws {
    for chunk in unicodeChunks(text) {
        guard let down = markSynthetic(CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: true)),
              let up = markSynthetic(CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: false)) else {
            throw ComputerError.operationFailed("could not create text input event")
        }
        chunk.withUnsafeBufferPointer { buffer in
            guard let baseAddress = buffer.baseAddress else { return }
            down.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: baseAddress)
            up.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: baseAddress)
        }
        try ComputerExecution.input()
        down.postToPid(pid)
        up.postToPid(pid)
        usleep(4_000)
    }
}

func postClickToPid(_ pid: pid_t, point: CGPoint, button name: String?, count: Int) throws {
    let source = syntheticEventSource()
    let (button, downType, upType) = mouseTypes(for: name)
    if let move = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: button)) {
        try ComputerExecution.input()
        move.postToPid(pid)
    }
    for click in 1...max(1, min(count, 3)) {
        guard let down = markSynthetic(CGEvent(mouseEventSource: source, mouseType: downType, mouseCursorPosition: point, mouseButton: button)),
              let up = markSynthetic(CGEvent(mouseEventSource: source, mouseType: upType, mouseCursorPosition: point, mouseButton: button)) else {
            throw ComputerError.operationFailed("could not create mouse event")
        }
        down.setIntegerValueField(.mouseEventClickState, value: Int64(click))
        up.setIntegerValueField(.mouseEventClickState, value: Int64(click))
        try ComputerExecution.input()
        down.postToPid(pid)
        up.postToPid(pid)
    }
}

func postScrollToPid(_ pid: pid_t, vertical: Int32, horizontal: Int32, steps: Int, at point: CGPoint?) throws {
    let source = syntheticEventSource()
    for _ in 0..<max(1, steps) {
        guard let event = markSynthetic(CGEvent(scrollWheelEvent2Source: source, units: .line, wheelCount: 2, wheel1: vertical, wheel2: horizontal, wheel3: 0)) else {
            throw ComputerError.operationFailed("could not create scroll event")
        }
        // postToPid does not move the cursor, so pin the scroll to the target point;
        // otherwise the app hit-tests it at the global origin.
        if let point { event.location = point }
        try ComputerExecution.input()
        event.postToPid(pid)
        usleep(16_000)
    }
}

func postDragToPid(_ pid: pid_t, from start: CGPoint, to end: CGPoint) throws {
    let source = syntheticEventSource()
    guard let down = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseDown, mouseCursorPosition: start, mouseButton: .left)),
          let up = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseUp, mouseCursorPosition: end, mouseButton: .left)) else {
        throw ComputerError.operationFailed("could not create drag event")
    }
    try ComputerExecution.input()
    down.postToPid(pid)
    var lastPoint = start
    defer { up.location = lastPoint; up.postToPid(pid) }
    for step in 1...12 {
        try ComputerExecution.checkpoint()
        let progress = Double(step) / 12
        let point = CGPoint(
            x: start.x + (end.x - start.x) * progress,
            y: start.y + (end.y - start.y) * progress
        )
        if let dragged = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseDragged, mouseCursorPosition: point, mouseButton: .left)) {
            dragged.postToPid(pid)
            lastPoint = point
        }
        usleep(8_000)
    }
}
