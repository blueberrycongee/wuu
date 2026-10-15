import AppKit
import ApplicationServices
@preconcurrency import CoreImage
@preconcurrency import CoreMedia
import CoreVideo
import Darwin
import Foundation
import ImageIO
@preconcurrency import ScreenCaptureKit

public struct NativePreviewConfiguration: Sendable {
    public let activityID: String
    public let target: String
    public let processID: pid_t?
    public let windowID: CGWindowID?
    public let parentProcessID: pid_t?

    public init(activityID: String, target: String, processID: pid_t? = nil, windowID: CGWindowID? = nil, parentProcessID: pid_t? = nil) {
        self.activityID = activityID
        self.target = target
        self.processID = processID
        self.windowID = windowID
        self.parentProcessID = parentProcessID
    }
}

private final class PiPEventWriter: @unchecked Sendable {
    private let queue = DispatchQueue(label: "com.blueberrycongee.wuu.cua.pip-events")

    func send(_ event: String, fields: [String: Any] = [:]) {
        queue.sync {
            var payload = fields
            payload["event"] = event
            payload["timestamp_ns"] = DispatchTime.now().uptimeNanoseconds
            guard let data = try? JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys]) else { return }
            FileHandle.standardOutput.write(data)
            FileHandle.standardOutput.write(Data([0x0A]))
        }
    }
}

private final class PiPCommandReader: @unchecked Sendable {
    private let lock = NSLock()
    private var buffer = Data()

    func append(_ data: Data) -> [String] {
        lock.lock()
        defer { lock.unlock() }
        buffer.append(data)
        var lines: [String] = []
        while let newline = buffer.firstIndex(of: 0x0A) {
            let line = buffer[..<newline]
            buffer.removeSubrange(...newline)
            if !line.isEmpty { lines.append(String(decoding: line, as: UTF8.self)) }
        }
        return lines
    }
}

/// Only the latest uncoded frame and one acknowledged transport frame are kept.
/// Encoding never runs on the main actor or the ScreenCaptureKit callback queue.
private final class NativePreviewFrames: @unchecked Sendable {
    private struct Frame {
        let image: CVPixelBuffer
        let generation: UInt64
    }

    private let lock = NSLock()
    private let queue = DispatchQueue(label: "com.blueberrycongee.wuu.cua.preview-encode", qos: .userInitiated)
    private let context = CIContext(options: [.cacheIntermediates: false])
    private let colorSpace = CGColorSpace(name: CGColorSpace.sRGB)!
    private let writer: PiPEventWriter
    private var generation: UInt64 = 0
    private var sequence: UInt64 = 0
    private var pending: Frame?
    private var encoding = false
    private var awaitingAcknowledgement: UInt64?
    private var failure: String?

    init(writer: PiPEventWriter) { self.writer = writer }

    func beginStream() -> UInt64 {
        lock.withLock {
            generation += 1
            pending = nil
            awaitingAcknowledgement = nil
            return generation
        }
    }

    func invalidate() {
        lock.withLock {
            generation += 1
            pending = nil
            awaitingAcknowledgement = nil
        }
    }

    func isCurrent(_ generation: UInt64) -> Bool { lock.withLock { generation == self.generation } }
    func failureMessage() -> String? { lock.withLock { failure } }

    func submit(_ image: CVPixelBuffer, generation: UInt64) {
        lock.withLock {
            guard generation == self.generation else { return }
            pending = Frame(image: image, generation: generation)
            scheduleIfReady()
        }
    }

    func acknowledge(_ sequence: UInt64) {
        lock.withLock {
            guard awaitingAcknowledgement == sequence else { return }
            awaitingAcknowledgement = nil
            scheduleIfReady()
        }
    }

    // Caller holds lock. Keeping one queue operation, rather than dispatching
    // every captured sample, prevents a slow encoder/renderer growing a backlog.
    private func scheduleIfReady() {
        guard !encoding, awaitingAcknowledgement == nil, pending != nil else { return }
        encoding = true
        queue.async { [self] in encodeLatest() }
    }

    private func encodeLatest() {
        let selected: (Frame, UInt64)? = lock.withLock {
            guard let frame = pending, awaitingAcknowledgement == nil else {
                encoding = false
                return nil
            }
            pending = nil
            sequence += 1
            awaitingAcknowledgement = sequence
            return (frame, sequence)
        }
        guard let (frame, sequence) = selected else { return }
        let data = autoreleasepool {
            context.jpegRepresentation(
                of: CIImage(cvPixelBuffer: frame.image),
                colorSpace: colorSpace,
                options: [CIImageRepresentationOption(rawValue: kCGImageDestinationLossyCompressionQuality as String): 0.82]
            )
        }
        lock.withLock {
            encoding = false
            guard frame.generation == generation, awaitingAcknowledgement == sequence else {
                scheduleIfReady()
                return
            }
            // A preview is bounded to 1280 pixels on its longest edge. Reject a
            // malformed/oversized result instead of allowing unbounded pipe data.
            guard let data, data.count <= 3_000_000 else {
                generation += 1
                awaitingAcknowledgement = nil
                pending = nil
                let message = "native preview frame encoding failed"
                failure = message
                writer.send("capture_status", fields: ["status": "error", "message": message])
                return
            }
            writer.send("frame", fields: [
                "sequence": sequence,
                "width": CVPixelBufferGetWidth(frame.image),
                "height": CVPixelBufferGetHeight(frame.image),
                "data": data.base64EncodedString(),
            ])
        }
    }
}

@MainActor
private final class NativePreviewControl {
    private var requestedVisible = true
    private var requestedLive = true
    private var lastController = "agent"
    private var lastState = "starting"
    var shouldCapture: Bool { requestedVisible && requestedLive }

    func setVisible(_ visible: Bool) { requestedVisible = visible }
    func setLive(_ live: Bool) { requestedLive = live }

    func needsRestoration(_ payload: [String: Any]) -> Bool {
        let nextController = payload["controller"] as? String ?? "agent"
        let nextState = payload["state"] as? String ?? "active"
        let restore = (nextController != "agent" && lastController == "agent")
            || (nextState == "stopped" && lastState != "stopped")
        lastController = nextController
        lastState = nextState
        return restore
    }
}

private final class WindowGeometryMonitor: @unchecked Sendable {
    private let lock = NSLock()
    private var dirty = false
    private var observer: AXObserver?
    private let application: AXUIElement
    private var boundWindow: AXUIElement?

    init(processID: pid_t, windowFrame: CGRect) {
        application = AXUIElementCreateApplication(processID)
        var created: AXObserver?
        guard AXObserverCreate(processID, geometryObserverCallback, &created) == .success, let created else { return }
        observer = created
        CFRunLoopAddSource(CFRunLoopGetMain(), AXObserverGetRunLoopSource(created), .commonModes)
        bindWindow(nearestTo: windowFrame)
    }

    deinit {
        if let observer { CFRunLoopRemoveSource(CFRunLoopGetMain(), AXObserverGetRunLoopSource(observer), .commonModes) }
    }

    func handle(_ notification: CFString) {
        markDirty()
    }

    func takeDirty() -> Bool {
        lock.lock(); defer { lock.unlock() }
        let value = dirty
        dirty = false
        return value
    }

    private func markDirty() { lock.withLock { dirty = true } }

    func rebind(nearestTo frame: CGRect) { bindWindow(nearestTo: frame) }

    private func bindWindow(nearestTo frame: CGRect) {
        guard let observer else { return }
        let refcon = Unmanaged.passUnretained(self).toOpaque()
        if let boundWindow {
            AXObserverRemoveNotification(observer, boundWindow, kAXMovedNotification as CFString)
            AXObserverRemoveNotification(observer, boundWindow, kAXResizedNotification as CFString)
            AXObserverRemoveNotification(observer, boundWindow, kAXUIElementDestroyedNotification as CFString)
        }
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(application, kAXWindowsAttribute as CFString, &value) == .success,
              let windows = value as? [AXUIElement],
              let window = windows.compactMap({ item -> (AXUIElement, CGRect)? in
                  guard let candidate = axFrame(item) else { return nil }
                  return (item, candidate)
              }).min(by: { windowFrameDistance($0.1, frame) < windowFrameDistance($1.1, frame) })?.0 else {
            boundWindow = nil
            return
        }
        boundWindow = window
        AXObserverAddNotification(observer, window, kAXMovedNotification as CFString, refcon)
        AXObserverAddNotification(observer, window, kAXResizedNotification as CFString, refcon)
        AXObserverAddNotification(observer, window, kAXUIElementDestroyedNotification as CFString, refcon)
    }
}

private let geometryObserverCallback: AXObserverCallback = { _, _, notification, refcon in
    guard let refcon else { return }
    Unmanaged<WindowGeometryMonitor>.fromOpaque(refcon).takeUnretainedValue().handle(notification)
}

struct CaptureFreshness: Equatable {
    private(set) var hasDisplayedFrame = false
    private(set) var lastCallbackUptime: TimeInterval?
    private(set) var lastHealthyCallbackUptime: TimeInterval?
    private(set) var unavailableSinceUptime: TimeInterval?

    mutating func recordCallback(
        at uptime: TimeInterval,
        displayedFrame: Bool,
        captureUnavailable: Bool
    ) {
        lastCallbackUptime = uptime
        if displayedFrame { hasDisplayedFrame = true }
        if captureUnavailable {
            if unavailableSinceUptime == nil { unavailableSinceUptime = uptime }
        } else {
            lastHealthyCallbackUptime = uptime
            unavailableSinceUptime = nil
        }
    }

    func requiresRecovery(
        at uptime: TimeInterval,
        streamStartedAt: TimeInterval,
        startupTimeout: TimeInterval,
        callbackSilenceTimeout: TimeInterval,
        unavailableStatusTimeout: TimeInterval
    ) -> Bool {
        if let unavailableSinceUptime,
           uptime - unavailableSinceUptime >= unavailableStatusTimeout {
            return true
        }
        guard hasDisplayedFrame else {
            return uptime - streamStartedAt >= startupTimeout
        }
        guard let lastCallbackUptime else {
            return uptime - streamStartedAt >= callbackSilenceTimeout
        }
        return uptime - lastCallbackUptime >= callbackSilenceTimeout
    }
}

@available(macOS 14.0, *)
private final class NativePiPStreamOutput: NSObject, SCStreamOutput, SCStreamDelegate, @unchecked Sendable {
    private let writer: PiPEventWriter
    private let frames: NativePreviewFrames
    private let generation: UInt64
    private let lock = NSLock()
    private var stopped = false
    private var invalidated = false
    private var freshness = CaptureFreshness()
    private var lastStatus: SCFrameStatus?

    init(frames: NativePreviewFrames, writer: PiPEventWriter) {
        self.frames = frames
        self.writer = writer
        generation = frames.beginStream()
    }

    func stream(_ stream: SCStream, didOutputSampleBuffer sampleBuffer: CMSampleBuffer, of type: SCStreamOutputType) {
        guard type == .screen else { return }
        let valid = sampleBuffer.isValid
        let attachments = valid
            ? CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: false) as? [[SCStreamFrameInfo: Any]]
            : nil
        let status = (attachments?.first?[.status] as? NSNumber).flatMap { SCFrameStatus(rawValue: $0.intValue) }
        let imageBuffer = valid ? sampleBuffer.imageBuffer : nil
        let captureUnavailable = !valid || status == .blank || status == .suspended || status == .stopped
        let shouldDisplay = imageBuffer != nil && status == .complete
        lock.withLock {
            guard !invalidated, frames.isCurrent(generation) else { return }
            let statusChanged = status != nil && status != lastStatus
            if let status { lastStatus = status }
            if captureUnavailable {
                stopped = true
                frames.invalidate()
            }
            freshness.recordCallback(
                at: ProcessInfo.processInfo.systemUptime,
                displayedFrame: shouldDisplay,
                captureUnavailable: captureUnavailable
            )
            if statusChanged, let status {
                writer.send("capture_status", fields: ["status": captureStatusName(status)])
            } else if captureUnavailable {
                writer.send("capture_status", fields: ["status": "suspended"])
            }
            if shouldDisplay, let imageBuffer {
                frames.submit(imageBuffer, generation: generation)
            }
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) { lockFailure(error) }

    func invalidate() {
        lock.withLock {
            invalidated = true
            stopped = true
            frames.invalidate()
        }
    }

    func isStopped() -> Bool { lock.withLock { stopped || !frames.isCurrent(generation) } }
    func wasPaused() -> Bool { lock.withLock { !stopped && !frames.isCurrent(generation) } }
    func freshnessSnapshot() -> CaptureFreshness { lock.withLock { freshness } }
    func lockFailure(_ error: Error) {
        lock.withLock {
            guard !invalidated, frames.isCurrent(generation) else { return }
            stopped = true
            frames.invalidate()
            writer.send("capture_status", fields: ["status": "error", "message": error.localizedDescription])
        }
    }
}

private extension NSLock {
    func withLock<T>(_ body: () -> T) -> T { lock(); defer { unlock() }; return body() }
}

@available(macOS 14.0, *)
private func captureStatusName(_ status: SCFrameStatus) -> String {
    switch status {
    case .complete, .started: "healthy"
    case .idle: "idle"
    case .blank: "blank"
    case .suspended: "suspended"
    case .stopped: "stopped"
    @unknown default: "suspended"
    }
}

@available(macOS 14.0, *)
private func preferredStreamWindow(in content: SCShareableContent, processID: pid_t) -> SCWindow? {
    let candidates = content.windows.filter {
        $0.owningApplication?.processID == processID && $0.frame.width > 1 && $0.frame.height > 1
    }
    guard !candidates.isEmpty else { return nil }
    let largest = candidates.max { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height }
    if let focusedFrame = focusedWindowFrame(processID: processID),
       let focused = candidates.min(by: { windowFrameDistance($0.frame, focusedFrame) < windowFrameDistance($1.frame, focusedFrame) }),
       focused.frame.width >= 120, focused.frame.height >= 80 { return focused }
    return largest
}

private func focusedWindowFrame(processID: pid_t) -> CGRect? {
    let application = AXUIElementCreateApplication(processID)
    var focusedValue: CFTypeRef?
    guard AXUIElementCopyAttributeValue(application, kAXFocusedWindowAttribute as CFString, &focusedValue) == .success,
          let focused = focusedValue as! AXUIElement? else { return nil }
    return axFrame(focused)
}

private func windowFrameDistance(_ left: CGRect, _ right: CGRect) -> CGFloat {
    abs(left.minX - right.minX) + abs(left.minY - right.minY) + abs(left.width - right.width) + abs(left.height - right.height)
}

func captureStageTargetIndex(
    windowFrames: [CGRect],
    requestedFrame: CGRect?,
    focusedFrame: CGRect?
) -> Int? {
    guard !windowFrames.isEmpty else { return nil }
    if let requestedFrame {
        return windowFrames.indices.min {
            windowFrameDistance(windowFrames[$0], requestedFrame) < windowFrameDistance(windowFrames[$1], requestedFrame)
        }
    }
    if let focusedFrame {
        return windowFrames.indices.min {
            windowFrameDistance(windowFrames[$0], focusedFrame) < windowFrameDistance(windowFrames[$1], focusedFrame)
        }
    }
    return windowFrames.indices.max {
        windowFrames[$0].width * windowFrames[$0].height < windowFrames[$1].width * windowFrames[$1].height
    }
}

func captureStageWindowAssignments(
    currentFrames: [CGRect],
    currentTitles: [String],
    expectedFrames: [CGRect],
    expectedTitles: [String]
) -> [Int?] {
    guard currentFrames.count == currentTitles.count,
          expectedFrames.count == expectedTitles.count else {
        return Array(repeating: nil, count: currentFrames.count)
    }
    var remaining = Set(expectedFrames.indices)
    return currentFrames.indices.map { currentIndex in
        guard !remaining.isEmpty else { return nil }
        let matchingTitles = remaining.filter { expectedTitles[$0] == currentTitles[currentIndex] }
        let pool = matchingTitles.isEmpty ? Array(remaining) : Array(matchingTitles)
        guard let match = pool.min(by: {
            windowFrameDistance(currentFrames[currentIndex], expectedFrames[$0])
                < windowFrameDistance(currentFrames[currentIndex], expectedFrames[$1])
        }) else { return nil }
        remaining.remove(match)
        return match
    }
}

private func requestedWindowFrame(windowID: CGWindowID?, processID: pid_t) -> CGRect? {
    guard let windowID, windowID > 0,
          let windows = CGWindowListCopyWindowInfo(.optionIncludingWindow, windowID) as? [[String: Any]],
          let window = windows.first(where: {
              ($0[kCGWindowNumber as String] as? NSNumber)?.uint32Value == windowID
                  && ($0[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == processID
          }),
          let bounds = window[kCGWindowBounds as String] as? [String: Any] else { return nil }
    return CGRect(dictionaryRepresentation: bounds as CFDictionary)
}

private struct NativePiPWindowSnapshot {
    let frame: CGRect
    let title: String
    let wasMinimized: Bool
}

private struct NativePiPResolvedWindow {
    let element: AXUIElement
    let snapshotIndex: Int
}

private func windowSnapshots(_ application: AXUIElement) -> [NativePiPWindowSnapshot] {
    axElements(application, attribute: kAXWindowsAttribute as String).compactMap { window in
        guard let frame = axFrame(window) else { return nil }
        return NativePiPWindowSnapshot(
            frame: frame,
            title: axDisplayValue(window, kAXTitleAttribute as String) ?? "",
            wasMinimized: axBool(window, kAXMinimizedAttribute as String) == true
        )
    }
}

@MainActor
private final class NativePiPWindowStage {
    private let axApplication: AXUIElement
    private let processID: pid_t
    private let originalWindows: [NativePiPWindowSnapshot]
    private let targetSnapshotIndex: Int?
    private let wasHidden: Bool
    private var stagedWindows: [NativePiPResolvedWindow] = []
    private var stagedFrames: [Int: CGRect] = [:]
    private var staged = false
    private var preparationGeneration: UInt64 = 0
    // Kept until the preview exits so stopping and restarting capture does not
    // add and remove a display.
    private var agentDisplay: AgentDisplayLease?

    init(application: NSRunningApplication, requestedFrame: CGRect?, focusedFrame: CGRect?) {
        axApplication = AXUIElementCreateApplication(application.processIdentifier)
        processID = application.processIdentifier
        originalWindows = windowSnapshots(axApplication)
        targetSnapshotIndex = captureStageTargetIndex(
            windowFrames: originalWindows.map(\.frame),
            requestedFrame: requestedFrame,
            focusedFrame: focusedFrame
        )
        wasHidden = application.isHidden || axBool(axApplication, kAXHiddenAttribute as String) == true
    }

    func prepareIfNeeded() async {
        guard let targetSnapshotIndex,
              originalWindows.indices.contains(targetSnapshotIndex),
              wasHidden || originalWindows[targetSnapshotIndex].wasMinimized else { return }
        preparationGeneration &+= 1
        let generation = preparationGeneration
        // Without the agent display a hidden or minimized window stays where the
        // user left it; the PiP then shows no frames instead of a clamped sliver.
        guard let display = try? await Task.detached(operation: { try AgentDisplayLease.acquire() }).value else { return }
        guard generation == preparationGeneration else { return }
        agentDisplay = display
        staged = true
        let parked = display.parkingOrigin(index: 0)
        if wasHidden {
            _ = AXUIElementSetAttributeValue(axApplication, kAXHiddenAttribute as CFString, kCFBooleanFalse)
        }
        // Custom apps apply hidden/minimized changes asynchronously and may
        // initially place a newly revealed window back on a real display. Retry
        // the public AX writes briefly so the window settles on the agent display
        // before SCK resolves it, without ever activating the target app.
        var prepared = false
        var settledFrame: CGRect?
        for _ in 0..<20 {
            guard generation == preparationGeneration else { return }
            // Hiding and revealing some custom apps destroys and recreates their
            // AXWindows. Keep valid resolved elements, but rematch all windows to
            // their staged frame whenever the target goes stale or the hidden
            // app's window set has only partially reappeared.
            let targetIsStale = stagedWindows.first(where: { $0.snapshotIndex == targetSnapshotIndex })
                .flatMap { axFrame($0.element) } == nil
            let resolvedSnapshotCount = Set(stagedWindows.map(\.snapshotIndex)).count
            if targetIsStale || (wasHidden && resolvedSnapshotCount < originalWindows.count) {
                var merged: [Int: NativePiPResolvedWindow] = [:]
                for item in stagedWindows where axFrame(item.element) != nil {
                    merged[item.snapshotIndex] = item
                }
                for item in resolveWindows(expectedFrames: stagedFrames) {
                    merged[item.snapshotIndex] = item
                }
                stagedWindows = merged.values.sorted { $0.snapshotIndex < $1.snapshotIndex }
            }
            guard let target = stagedWindows.first(where: { $0.snapshotIndex == targetSnapshotIndex }) else {
                try? await Task.sleep(for: .milliseconds(50))
                continue
            }
            if wasHidden {
                for (offset, other) in stagedWindows.enumerated() where other.snapshotIndex != targetSnapshotIndex {
                    _ = AXUIElementSetAttributeValue(other.element, kAXMinimizedAttribute as CFString, kCFBooleanTrue)
                    if axBool(other.element, kAXMinimizedAttribute as String) != true {
                        _ = setOrigin(other.element, display.parkingOrigin(index: offset + 1))
                    }
                }
            }
            _ = AXUIElementSetAttributeValue(target.element, kAXMinimizedAttribute as CFString, kCFBooleanFalse)
            _ = setOrigin(target.element, parked)
            let currentFrame = axFrame(target.element)
            for item in stagedWindows {
                if let frame = axFrame(item.element) { stagedFrames[item.snapshotIndex] = frame }
            }
            let hidden = axBool(axApplication, kAXHiddenAttribute as String)
            let minimized = axBool(target.element, kAXMinimizedAttribute as String)
            let allOriginalWindowsResolved = !wasHidden
                || Set(stagedWindows.map(\.snapshotIndex)).count >= originalWindows.count
            let otherWindowsAreConcealed = !wasHidden || stagedWindows.allSatisfy { other in
                guard other.snapshotIndex != targetSnapshotIndex else { return true }
                if axBool(other.element, kAXMinimizedAttribute as String) == true { return true }
                guard let frame = axFrame(other.element) else { return false }
                return display.holds(frame)
            }
            // AX reports the requested origin at once while the WindowServer
            // animates the move; SCK must not resolve a window still in flight.
            let serverSettled = currentFrame.map { target in
                windowFrames(processID: processID).values.contains { $0.size == target.size && display.holds($0) }
            } == true
            if hidden != true,
               minimized != true,
               allOriginalWindowsResolved,
               otherWindowsAreConcealed,
               let currentFrame,
               display.holds(currentFrame),
               serverSettled {
                prepared = true
                settledFrame = currentFrame
                break
            }
            try? await Task.sleep(for: .milliseconds(50))
        }
        guard generation == preparationGeneration else { return }
        guard prepared else {
            restore()
            return
        }
        pipDebugLog("staged hidden or minimized target window from frame=\(originalWindows[targetSnapshotIndex].frame) to frame=\(String(describing: settledFrame)) windows=\(stagedWindows.count)")
    }

    func restore() {
        // A stop or user takeover may arrive while display acquisition or an AX
        // retry is suspended. Invalidate that work even before anything is staged.
        preparationGeneration &+= 1
        guard staged else { return }
        staged = false
        var concealedBeforeRestore = wasHidden
        if wasHidden {
            // Hide every ordered-in window before moving anything back to its
            // on-screen origin, so stopping PiP cannot flash the target app.
            _ = AXUIElementSetAttributeValue(axApplication, kAXHiddenAttribute as CFString, kCFBooleanTrue)
            for _ in 0..<10 where axBool(axApplication, kAXHiddenAttribute as String) != true {
                usleep(20_000)
            }
        } else if let targetSnapshotIndex,
                  let target = stagedWindows.first(where: { $0.snapshotIndex == targetSnapshotIndex }),
                  originalWindows[targetSnapshotIndex].wasMinimized {
            _ = AXUIElementSetAttributeValue(target.element, kAXMinimizedAttribute as CFString, kCFBooleanTrue)
            for _ in 0..<5 where axBool(target.element, kAXMinimizedAttribute as String) != true {
                usleep(20_000)
            }
            if axBool(target.element, kAXMinimizedAttribute as String) != true {
                // A custom window that refuses minimization must stay invisible
                // while its original position is restored.
                _ = AXUIElementSetAttributeValue(axApplication, kAXHiddenAttribute as CFString, kCFBooleanTrue)
                concealedBeforeRestore = true
            }
        }

        if stagedWindows.contains(where: { axFrame($0.element) == nil }) {
            // AXWindows can reappear incrementally after a hide. Preserve every
            // still-valid element and merge newly resolved snapshots until the
            // original set is complete; replacing the array with one partial
            // read would strand the missing window in its staged state.
            var recovered: [Int: NativePiPResolvedWindow] = [:]
            for item in stagedWindows where axFrame(item.element) != nil {
                recovered[item.snapshotIndex] = item
            }
            for attempt in 0..<20 {
                for item in resolveWindows(expectedFrames: stagedFrames) {
                    recovered[item.snapshotIndex] = item
                }
                if recovered.count >= originalWindows.count { break }
                if attempt < 19 { usleep(25_000) }
            }
            stagedWindows = recovered.values.sorted { $0.snapshotIndex < $1.snapshotIndex }
        }
        for item in stagedWindows {
            guard originalWindows.indices.contains(item.snapshotIndex) else { continue }
            let original = originalWindows[item.snapshotIndex]
            _ = setOrigin(item.element, original.frame.origin)
            _ = AXUIElementSetAttributeValue(
                item.element,
                kAXMinimizedAttribute as CFString,
                original.wasMinimized ? kCFBooleanTrue : kCFBooleanFalse
            )
        }
        if concealedBeforeRestore {
            _ = AXUIElementSetAttributeValue(axApplication, kAXHiddenAttribute as CFString, kCFBooleanTrue)
        }
        let targetFrame = targetSnapshotIndex.flatMap { originalWindows.indices.contains($0) ? originalWindows[$0].frame : nil }
        pipDebugLog("restored staged target window frame=\(String(describing: targetFrame)) hidden=\(wasHidden)")
    }

    private func resolveWindows(expectedFrames: [Int: CGRect]) -> [NativePiPResolvedWindow] {
        let candidates = axElements(axApplication, attribute: kAXWindowsAttribute as String).compactMap { window -> (AXUIElement, CGRect, String)? in
            guard let frame = axFrame(window) else { return nil }
            return (window, frame, axDisplayValue(window, kAXTitleAttribute as String) ?? "")
        }
        let assignments = captureStageWindowAssignments(
            currentFrames: candidates.map { $0.1 },
            currentTitles: candidates.map { $0.2 },
            expectedFrames: originalWindows.indices.map { expectedFrames[$0] ?? originalWindows[$0].frame },
            expectedTitles: originalWindows.map(\.title)
        )
        return zip(candidates, assignments).compactMap { candidate, match in
            guard let match else { return nil }
            return NativePiPResolvedWindow(element: candidate.0, snapshotIndex: match)
        }
    }

    private func setOrigin(_ window: AXUIElement, _ origin: CGPoint) -> Bool {
        var point = origin
        guard let value = AXValueCreate(.cgPoint, &point) else { return false }
        return AXUIElementSetAttributeValue(window, kAXPositionAttribute as CFString, value) == .success
    }
}

// The filter reports the backing scale of the display that currently hosts the
// window. A buffer sized from another display leaves the frame partly empty.
@available(macOS 14.0, *)
private func streamConfiguration(for filter: SCContentFilter) -> SCStreamConfiguration {
    let configuration = SCStreamConfiguration()
    let size = filter.contentRect.size
    let previewScale = min(CGFloat(filter.pointPixelScale), 1280 / max(1, max(size.width, size.height)))
    configuration.width = max(1, Int(size.width * previewScale))
    configuration.height = max(1, Int(size.height * previewScale))
    configuration.minimumFrameInterval = CMTime(value: 1, timescale: 30)
    configuration.queueDepth = 3
    configuration.showsCursor = false
    configuration.pixelFormat = kCVPixelFormatType_32BGRA
    configuration.ignoreShadowsSingleWindow = true
    return configuration
}

private func pipDebugLog(_ message: String) {
    FileHandle.standardError.write(Data("[pip] \(message)\n".utf8))
}

// Guards a one-shot resume so the capture-start completion handler and its
// timeout can race without ever resuming the continuation twice.
private final class CaptureStartGate: @unchecked Sendable {
    private let lock = NSLock()
    private var settled = false
    func settle(_ body: () -> Void) {
        lock.lock(); defer { lock.unlock() }
        if settled { return }
        settled = true
        body()
    }
}

// Carries a non-Sendable value across a continuation resume. SCShareableContent
// is an immutable snapshot, so handing it from the completion handler to the
// awaiting task does not actually race.
private final class UncheckedBox<T>: @unchecked Sendable {
    let value: T
    init(_ value: T) { self.value = value }
}

@available(macOS 14.0, *)
private func shareableContent(timeout: TimeInterval) async -> SCShareableContent? {
    // The async SCShareableContent API can leave its continuation unresumed when
    // the capture daemon is wedged (it happens right after a stream's connection
    // is interrupted), which permanently stalls the supervision loop. Drive the
    // completion handler and race it against a timeout so the loop can always
    // keep retrying instead of leaking a continuation and freezing.
    let gate = CaptureStartGate()
    let box: UncheckedBox<SCShareableContent?> = await withCheckedContinuation { continuation in
        SCShareableContent.getExcludingDesktopWindows(false, onScreenWindowsOnly: false) { content, _ in
            gate.settle { continuation.resume(returning: UncheckedBox(content)) }
        }
        DispatchQueue.global().asyncAfter(deadline: .now() + timeout) {
            gate.settle { continuation.resume(returning: UncheckedBox(nil)) }
        }
    }
    return box.value
}

@available(macOS 14.0, *)
private func startWindowStream(window: SCWindow, output: NativePiPStreamOutput, timeout: TimeInterval) async throws -> SCStream {
    let filter = SCContentFilter(desktopIndependentWindow: window)
    let stream = SCStream(filter: filter, configuration: streamConfiguration(for: filter), delegate: output)
    try stream.addStreamOutput(output, type: .screen, sampleHandlerQueue: DispatchQueue(label: "com.blueberrycongee.wuu.cua.native-pip"))
    // ScreenCaptureKit can report a startup failure through the delegate's
    // didStopWithError instead of resuming startCapture's completion handler,
    // which leaks the async continuation and suspends the caller forever. Drive
    // the completion handler directly and race it against a timeout so a stalled
    // start surfaces as a thrown error the supervision loop can retry.
    let gate = CaptureStartGate()
    let started: Bool = await withCheckedContinuation { (continuation: CheckedContinuation<Bool, Never>) in
        stream.startCapture { error in
            gate.settle { continuation.resume(returning: error == nil) }
        }
        DispatchQueue.global().asyncAfter(deadline: .now() + timeout) {
            gate.settle { continuation.resume(returning: false) }
        }
    }
    guard started else {
        // A merely-slow start can still complete after the timeout fires, so
        // stop the stream — fire-and-forget via the completion handler so we
        // never re-hang on a genuinely stuck one — rather than leaking a stream
        // that keeps capturing with no output attached.
        stopWindowStream(stream, output: output)
        throw ComputerError.operationFailed("capture failed to start")
    }
    return stream
}

@available(macOS 14.0, *)
private func stopWindowStream(_ stream: SCStream, output: NativePiPStreamOutput) {
    output.invalidate()
    try? stream.removeStreamOutput(output, type: .screen)
    // A wedged capture daemon must not delay cancellation or target restoration.
    stream.stopCapture { _ in }
}

@MainActor
public func runNativePreview(configuration: NativePreviewConfiguration) async throws {
    guard CGPreflightScreenCaptureAccess() else {
        throw ComputerError.permissionDenied("Screen Recording permission is required for live window capture")
    }
    guard #available(macOS 14.0, *) else {
        throw ComputerError.unsupported("native picture-in-picture requires macOS 14 or newer")
    }
    let normalized = configuration.target.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    guard let app = NSWorkspace.shared.runningApplications.first(where: {
        if let processID = configuration.processID { return !$0.isTerminated && $0.processIdentifier == processID && ($0.bundleIdentifier?.lowercased() == normalized || $0.localizedName?.lowercased() == normalized || $0.bundleURL?.path.lowercased() == normalized) }
        return !$0.isTerminated && ($0.bundleIdentifier?.lowercased() == normalized || $0.localizedName?.lowercased() == normalized || $0.bundleURL?.path.lowercased() == normalized)
    }) else { throw ComputerError.appNotFound(configuration.target) }

    pipDebugLog("preview activity=\(configuration.activityID) process=\(app.processIdentifier)")
    let writer = PiPEventWriter()
    let frames = NativePreviewFrames(writer: writer)
    let controller = NativePreviewControl()
    let requestedFrame = requestedWindowFrame(windowID: configuration.windowID, processID: app.processIdentifier)
    guard configuration.windowID == nil || requestedFrame != nil else {
        writer.send("gone")
        return
    }
    let windowStage = NativePiPWindowStage(
        application: app,
        requestedFrame: requestedFrame,
        focusedFrame: focusedWindowFrame(processID: app.processIdentifier)
    )
    defer { windowStage.restore() }
    let terminationSignals = [SIGTERM, SIGINT].map { code in
        signal(code, SIG_IGN)
        let source = DispatchSource.makeSignalSource(signal: code, queue: .main)
        source.setEventHandler { frames.invalidate(); windowStage.restore(); exit(0) }
        source.resume()
        return source
    }
    defer { for source in terminationSignals { source.cancel() } }

    // Install the command reader before staging or capture work. Both staging
    // retries and stream startup suspend instead of blocking the main actor, so
    // close/stdin EOF can restore the target immediately even during startup.
    let commandReader = PiPCommandReader()
    FileHandle.standardInput.readabilityHandler = { input in
        let data = input.availableData
        if data.isEmpty {
            Task { @MainActor in
                frames.invalidate()
                windowStage.restore()
                exit(0)
            }
            return
        }
        for encoded in commandReader.append(data) {
            Task { @MainActor in
                guard let commandData = encoded.data(using: .utf8),
                      let command = try? JSONSerialization.jsonObject(with: commandData) as? [String: Any],
                      let type = command["type"] as? String else { return }
                switch type {
                case "visible":
                    controller.setVisible(command["visible"] as? Bool == true)
                    if !controller.shouldCapture { frames.invalidate() }
                case "live":
                    controller.setLive(command["live"] as? Bool == true)
                    if !controller.shouldCapture { frames.invalidate(); windowStage.restore() }
                case "ack":
                    if let sequence = command["sequence"] as? UInt64 { frames.acknowledge(sequence) }
                case "activity":
                    let nextController = command["controller"] as? String ?? "agent"
                    if nextController == "none" || command["state"] as? String == "stopped" {
                        controller.setLive(false)
                        frames.invalidate()
                    }
                    if controller.needsRestoration(command) {
                        windowStage.restore()
                        let pid = app.processIdentifier
                        do {
                            try await Task.detached { try WindowRestorationJournal.restoreForUser(processID: pid) }.value
                            if nextController == "user" { app.activate(options: [.activateAllWindows, .activateIgnoringOtherApps]) }
                        } catch {
                            writer.send("capture_status", fields: ["status": "error", "message": error.localizedDescription])
                        }
                    }
                case "close":
                    frames.invalidate()
                    windowStage.restore()
                    exit(0)
                default: break
                }
            }
        }
    }
    defer { FileHandle.standardInput.readabilityHandler = nil }
    await windowStage.prepareIfNeeded()

    let geometryMonitor = WindowGeometryMonitor(processID: app.processIdentifier, windowFrame: .zero)
    let captureStartTimeout: TimeInterval = 3
    let startupTimeout: TimeInterval = 5
    let callbackSilenceTimeout: TimeInterval = 3
    let unavailableStatusTimeout: TimeInterval = 3

    var stream: SCStream?
    var output: NativePiPStreamOutput?
    defer {
        if let stream, let output { stopWindowStream(stream, output: output) }
        frames.invalidate()
    }
    var trackedWindowID: CGWindowID? = configuration.windowID
    var trackedFrame: CGRect = .zero
    var streamStartedAt = ProcessInfo.processInfo.systemUptime
    var lastVerifiedCaptureAt = streamStartedAt
    var establishFailures = 0
    var lastHealthyOutput: ObjectIdentifier?
    var lastSafetyRefresh = Date.distantPast

    while !app.isTerminated && processIsAlive(configuration.parentProcessID) {
        if let failure = frames.failureMessage() { throw ComputerError.operationFailed(failure) }
        if !controller.shouldCapture {
            if let existing = stream, let existingOutput = output {
                stopWindowStream(existing, output: existingOutput)
                writer.send("capture_status", fields: ["status": "stopped"])
            }
            stream = nil
            output = nil
            try await Task.sleep(for: .milliseconds(100))
            continue
        }
        if establishFailures >= 4 {
            throw ComputerError.operationFailed("capture recovery exhausted")
        }
        // Recover only the same target. The Electron host owns all presentation
        // and displays unavailable status while capture backs off.
        if stream == nil || output?.isStopped() == true {
            if let existing = stream, let existingOutput = output {
                stopWindowStream(existing, output: existingOutput)
            }
            stream = nil
            output = nil
            if establishFailures > 0 {
                if ProcessInfo.processInfo.systemUptime - lastVerifiedCaptureAt >= callbackSilenceTimeout {
                    writer.send("capture_status", fields: ["status": "suspended"])
                }
                try await Task.sleep(for: .milliseconds(250 * (1 << min(5, establishFailures))))
            }
            guard let content = await shareableContent(timeout: 3) else {
                establishFailures += 1
                continue
            }
            guard controller.shouldCapture else { continue }
            guard !app.isTerminated, processIsAlive(configuration.parentProcessID) else { break }
            guard let target = (trackedWindowID != nil
                ? content.windows.first(where: { $0.windowID == trackedWindowID && $0.owningApplication?.processID == app.processIdentifier })
                : preferredStreamWindow(in: content, processID: app.processIdentifier)) else {
                writer.send("gone")
                break
            }
            // A helper never silently retargets to another window after a close.
            trackedWindowID = target.windowID
            pipDebugLog("resolve window id=\(target.windowID) frame=\(target.frame) title=\(target.title ?? "-")")
            let next = NativePiPStreamOutput(frames: frames, writer: writer)
            do {
                stream = try await startWindowStream(window: target, output: next, timeout: captureStartTimeout)
                output = next
                trackedWindowID = target.windowID
                trackedFrame = target.frame
                geometryMonitor.rebind(nearestTo: target.frame)
                streamStartedAt = ProcessInfo.processInfo.systemUptime
                // Do NOT reset establishFailures here. A stream that starts but
                // never delivers a displayable frame (blank/suspended/DRM window)
                // must let the backoff escalate; the counter resets only when a
                // real frame arrives (freshness.hasDisplayedFrame below).
                pipDebugLog("capture started id=\(target.windowID)")
            } catch {
                establishFailures += 1
                pipDebugLog("capture start failed attempt=\(establishFailures)")
            }
            continue
        }

        guard let activeStream = stream, let activeOutput = output else { continue }
        try await Task.sleep(for: .milliseconds(100))
        guard controller.shouldCapture else { continue }
        if activeOutput.wasPaused() {
            // A hide/show pair can arrive within one supervision tick. Restart
            // its invalidated stream without counting a user pause as failure.
            stopWindowStream(activeStream, output: activeOutput)
            stream = nil
            output = nil
            continue
        }
        let now = ProcessInfo.processInfo.systemUptime
        let freshness = activeOutput.freshnessSnapshot()
        if let healthyAt = freshness.lastHealthyCallbackUptime {
            lastVerifiedCaptureAt = max(lastVerifiedCaptureAt, healthyAt)
        }
        let outputID = ObjectIdentifier(activeOutput)
        if freshness.hasDisplayedFrame, lastHealthyOutput != outputID {
            establishFailures = 0
            lastHealthyOutput = outputID
        }
        let recoveryRequired = freshness.requiresRecovery(
            at: now,
            streamStartedAt: streamStartedAt,
            startupTimeout: startupTimeout,
            callbackSilenceTimeout: callbackSilenceTimeout,
            unavailableStatusTimeout: unavailableStatusTimeout
        )
        if activeOutput.isStopped() || recoveryRequired {
            stopWindowStream(activeStream, output: activeOutput)
            if now - lastVerifiedCaptureAt >= callbackSilenceTimeout {
                writer.send("capture_status", fields: ["status": "suspended"])
            }
            establishFailures += 1
            stream = nil
            output = nil
            continue
        }
        if !freshness.hasDisplayedFrame, now - lastVerifiedCaptureAt >= callbackSilenceTimeout {
            writer.send("capture_status", fields: ["status": "suspended"])
        }
        let safetyRefreshDue = Date().timeIntervalSince(lastSafetyRefresh) >= 5
        guard geometryMonitor.takeDirty() || safetyRefreshDue else { continue }
        lastSafetyRefresh = Date()
        guard CGPreflightScreenCaptureAccess() else {
            throw ComputerError.permissionDenied("Screen Recording permission was revoked")
        }
        guard let refreshed = await shareableContent(timeout: 3) else { continue }
        guard let nextWindow = refreshed.windows.first(where: {
            $0.windowID == trackedWindowID && $0.owningApplication?.processID == app.processIdentifier
        }) else {
            stopWindowStream(activeStream, output: activeOutput)
            stream = nil
            output = nil
            writer.send("gone")
            break
        }
        guard windowFrameDistance(nextWindow.frame, trackedFrame) > 1 else { continue }
        do {
            try await activeStream.updateConfiguration(streamConfiguration(for: SCContentFilter(desktopIndependentWindow: nextWindow)))
            trackedFrame = nextWindow.frame
        } catch { activeOutput.lockFailure(error) }
    }
}

private func processIsAlive(_ processID: pid_t?) -> Bool {
    guard let processID, processID > 0 else { return true }
    return kill(processID, 0) == 0 || errno == EPERM
}
