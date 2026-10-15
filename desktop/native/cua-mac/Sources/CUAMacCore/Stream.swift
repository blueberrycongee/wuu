import AppKit
import ApplicationServices
@preconcurrency import CoreMedia
import CoreVideo
import Darwin
import Foundation
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

// Every preview session writes to the one stdout pipe; a shared queue keeps
// each binary packet contiguous.
private let pipEventQueue = DispatchQueue(label: "com.blueberrycongee.wuu.cua.pip-events")

private final class PiPEventWriter: @unchecked Sendable {
    private let session: String?

    init(session: String?) { self.session = session }

    func send(_ event: String, fields: [String: Any] = [:], payload: Data = Data()) {
        pipEventQueue.sync {
            var header = fields
            header["event"] = event
            if let session { header["session"] = session }
            header["timestamp_ns"] = DispatchTime.now().uptimeNanoseconds
            guard let data = try? JSONSerialization.data(withJSONObject: header, options: [.sortedKeys]),
                  data.count <= 16_384, payload.count <= 3_000_000 else { return }
            // WCU1, two big-endian lengths, JSON metadata, then raw AVC bytes.
            var packet = Data("WCU1".utf8)
            for length in [data.count, payload.count] {
                var value = UInt32(length).bigEndian
                withUnsafeBytes(of: &value) { packet.append(contentsOf: $0) }
            }
            packet.append(data)
            packet.append(payload)
            FileHandle.standardOutput.write(packet)
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

/// Keep the latest capture for idle recovery and one frame being encoded or awaiting ACK.
/// Encoding never runs on the main actor or the ScreenCaptureKit callback queue.
private final class NativePreviewFrames: @unchecked Sendable {
    private struct Frame: @unchecked Sendable {
        let image: CVPixelBuffer
        let timestamp: CMTime
        let generation: UInt64
    }

    private let lock = NSLock()
    private let queue = DispatchQueue(label: "com.blueberrycongee.wuu.cua.preview-encode", qos: .userInitiated)
    private let encoder = PreviewEncoder()
    private let writer: PiPEventWriter
    private var generation: UInt64 = 0
    private var sequence: UInt64 = 0
    private var pending: Frame?
    private var latest: Frame?
    private var needsKeyframe = true
    private var encoding = false
    private var awaitingAcknowledgement: UInt64?
    private var failure: String?

    init(writer: PiPEventWriter) { self.writer = writer }

    func beginStream() -> UInt64 {
        lock.withLock {
            generation += 1
            pending = nil
            latest = nil
            needsKeyframe = true
            awaitingAcknowledgement = nil
            return generation
        }
    }

    func invalidate() {
        lock.withLock {
            generation += 1
            pending = nil
            latest = nil
            needsKeyframe = true
            awaitingAcknowledgement = nil
        }
    }

    func isCurrent(_ generation: UInt64) -> Bool { lock.withLock { generation == self.generation } }
    func failureMessage() -> String? { lock.withLock { failure } }

    func submit(_ image: CVPixelBuffer, timestamp: CMTime, generation: UInt64) {
        lock.withLock {
            guard generation == self.generation else { return }
            let frame = Frame(image: image, timestamp: timestamp, generation: generation)
            latest = frame
            pending = frame
            scheduleIfReady()
        }
    }

    func requestKeyframe() {
        lock.withLock {
            needsKeyframe = true
            pending = latest
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
        let selected: (Frame, UInt64, Bool)? = lock.withLock {
            guard let frame = pending, awaitingAcknowledgement == nil else {
                encoding = false
                return nil
            }
            pending = nil
            sequence += 1
            awaitingAcknowledgement = sequence
            let keyframe = needsKeyframe
            needsKeyframe = false
            return (frame, sequence, keyframe)
        }
        guard let (frame, sequence, keyframe) = selected else { return }
        do {
            try encoder.encode(frame.image, generation: frame.generation, timestamp: frame.timestamp, keyframe: keyframe) { [self] result in
                queue.async { [self] in complete(result, frame: frame, sequence: sequence) }
            }
        } catch { complete(.failure(error), frame: frame, sequence: sequence) }
    }

    private func complete(_ result: Result<PreviewEncoder.Output?, Error>, frame: Frame, sequence: UInt64) {
        lock.withLock {
            encoding = false
            guard frame.generation == generation, awaitingAcknowledgement == sequence else {
                scheduleIfReady()
                return
            }
            switch result {
            case .failure(let error):
                generation += 1
                awaitingAcknowledgement = nil
                pending = nil
                latest = nil
                failure = error.localizedDescription
                writer.send("capture_status", fields: ["status": "error", "message": error.localizedDescription])
            case .success(nil):
                awaitingAcknowledgement = nil
                needsKeyframe = true
                scheduleIfReady()
            case .success(let output?):
                var fields: [String: Any] = [
                    "sequence": sequence, "generation": frame.generation,
                    "configuration": output.configuration, "codec": output.codec,
                    "keyframe": output.keyframe, "timestamp": output.timestamp,
                    "width": CVPixelBufferGetWidth(frame.image),
                    "height": CVPixelBufferGetHeight(frame.image),
                ]
                if output.keyframe { fields["description"] = Array(output.description) }
                writer.send("frame", fields: fields, payload: output.data)
            }
        }
    }
}

@MainActor
private final class NativePreviewControl {
    private var requestedVisible = true
    private var requestedLive = true
    private var lastController = "agent"
    private var lastState = "starting"
    var closed = false
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
                frames.submit(imageBuffer, timestamp: sampleBuffer.presentationTimeStamp, generation: generation)
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

// Minimizing or hiding an app is the user's choice. Unminimizing it to capture
// frames would animate the window out of the Dock across the user's screens, so
// the preview rests until the user brings a window back.
private func applicationIsPutAway(_ app: NSRunningApplication) -> Bool {
    if app.isHidden { return true }
    let windows = axElements(AXUIElementCreateApplication(app.processIdentifier), attribute: kAXWindowsAttribute as String)
    return !windows.isEmpty && windows.allSatisfy { axBool($0, kAXMinimizedAttribute as String) == true }
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

// The filter reports the backing scale of the display that currently hosts the
// window. A buffer sized from another display leaves the frame partly empty.
@available(macOS 14.0, *)
private func streamConfiguration(for filter: SCContentFilter) -> SCStreamConfiguration {
    let configuration = SCStreamConfiguration()
    let size = filter.contentRect.size
    let previewScale = min(CGFloat(filter.pointPixelScale), 1280 / max(1, max(size.width, size.height)))
    // AVC uses 4:2:0 chroma, so scaled dimensions must remain even.
    configuration.width = max(2, Int(size.width * previewScale) / 2 * 2)
    configuration.height = max(2, Int(size.height * previewScale) / 2 * 2)
    configuration.minimumFrameInterval = CMTime(value: 1, timescale: 30)
    configuration.queueDepth = 3
    configuration.showsCursor = false
    configuration.pixelFormat = kCVPixelFormatType_32BGRA
    configuration.colorSpaceName = CGColorSpace.sRGB
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

/// Delivers host commands to one preview session, buffering those that arrive
/// before the session installs its handler.
@MainActor
private final class NativePreviewInbox {
    private var handler: (([String: Any]) -> Void)?
    private var pending: [[String: Any]] = []

    func deliver(_ command: [String: Any]) {
        if let handler { handler(command) } else { pending.append(command) }
    }

    func setHandler(_ next: @escaping ([String: Any]) -> Void) {
        handler = next
        let queued = pending
        pending = []
        for command in queued { next(command) }
    }
}

/// One helper process streams every live preview. replayd groups capture
/// clients by executable path, so separate helper processes of one executable
/// would interrupt each other's streams.
///
/// Commands arrive as JSON lines on stdin. In host mode every line names its
/// session; `open` starts one and `close` ends it. A single-session helper
/// starts its session at launch, accepts unnamed commands, and exits with it.
@MainActor
public final class NativePreviewHost {
    private let parentProcessID: pid_t?
    private let singleSession: NativePreviewConfiguration?
    private var sessions: [String: NativePreviewInbox] = [:]
    private var signalSources: [DispatchSourceSignal] = []

    public init(parentProcessID: pid_t?, singleSession: NativePreviewConfiguration? = nil) {
        self.parentProcessID = parentProcessID
        self.singleSession = singleSession
    }

    public func run() {
        signalSources = [SIGTERM, SIGINT].map { code in
            signal(code, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: code, queue: .main)
            source.setEventHandler { exit(0) }
            source.resume()
            return source
        }
        let reader = PiPCommandReader()
        FileHandle.standardInput.readabilityHandler = { input in
            let data = input.availableData
            if data.isEmpty {
                Task { @MainActor in exit(0) }
                return
            }
            for encoded in reader.append(data) {
                guard let commandData = encoded.data(using: .utf8),
                      let command = try? JSONSerialization.jsonObject(with: commandData) as? [String: Any] else { continue }
                let box = UncheckedBox(command)
                Task { @MainActor in self.route(box.value) }
            }
        }
        if let singleSession {
            open(session: nil, configuration: singleSession)
        } else {
            Task { @MainActor in
                while processIsAlive(self.parentProcessID) { try? await Task.sleep(for: .seconds(1)) }
                exit(0)
            }
        }
    }

    private func route(_ command: [String: Any]) {
        if singleSession != nil {
            sessions[""]?.deliver(command)
            return
        }
        guard let session = command["session"] as? String, !session.isEmpty else { return }
        if command["type"] as? String == "open" {
            guard sessions[session] == nil, let target = command["target"] as? String else { return }
            let processID = (command["process_id"] as? NSNumber)?.int32Value ?? 0
            let windowID = (command["window_id"] as? NSNumber)?.uint32Value ?? 0
            open(session: session, configuration: NativePreviewConfiguration(
                activityID: command["activity_id"] as? String ?? session,
                target: target,
                processID: processID > 0 ? processID : nil,
                windowID: windowID > 0 ? windowID : nil,
                parentProcessID: parentProcessID
            ))
            return
        }
        sessions[session]?.deliver(command)
    }

    private func open(session: String?, configuration: NativePreviewConfiguration) {
        let key = session ?? ""
        let inbox = NativePreviewInbox()
        sessions[key] = inbox
        let writer = PiPEventWriter(session: session)
        Task { @MainActor in
            var failure: String?
            do {
                try await runNativePreview(configuration: configuration, writer: writer, inbox: inbox)
            } catch {
                failure = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
            self.sessions[key] = nil
            if session == nil {
                if let failure {
                    FileHandle.standardError.write(Data("wuu-cua-mac native preview failed: \(failure)\n".utf8))
                    exit(1)
                }
                exit(0)
            }
            if let failure { writer.send("failed", fields: ["message": failure]) }
            writer.send("closed")
        }
    }
}

@MainActor
private func runNativePreview(
    configuration: NativePreviewConfiguration,
    writer: PiPEventWriter,
    inbox: NativePreviewInbox
) async throws {
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
    let frames = NativePreviewFrames(writer: writer)
    let controller = NativePreviewControl()
    let requestedFrame = requestedWindowFrame(windowID: configuration.windowID, processID: app.processIdentifier)
    guard configuration.windowID == nil || requestedFrame != nil else {
        writer.send("gone")
        return
    }
    // Install the command handler before capture work. Stream startup suspends
    // instead of blocking the main actor, so a close ends the session promptly.
    inbox.setHandler { command in
            Task { @MainActor in
                guard let type = command["type"] as? String else { return }
                switch type {
                case "visible":
                    controller.setVisible(command["visible"] as? Bool == true)
                    if !controller.shouldCapture { frames.invalidate() }
                case "live":
                    controller.setLive(command["live"] as? Bool == true)
                    if !controller.shouldCapture { frames.invalidate() }
                case "ack":
                    if let sequence = command["sequence"] as? UInt64 { frames.acknowledge(sequence) }
                case "keyframe":
                    if controller.shouldCapture { frames.requestKeyframe() }
                case "activity":
                    let nextController = command["controller"] as? String ?? "agent"
                    if nextController == "none" || command["state"] as? String == "stopped" {
                        controller.setLive(false)
                        frames.invalidate()
                    }
                    if controller.needsRestoration(command) {
                        let pid = app.processIdentifier
                        do {
                            try await Task.detached { try WindowRestorationJournal.restoreForUser(processID: pid) }.value
                            if nextController == "user" { app.activate(options: [.activateAllWindows, .activateIgnoringOtherApps]) }
                        } catch {
                            writer.send("capture_status", fields: ["status": "error", "message": error.localizedDescription])
                        }
                    }
                case "close":
                    controller.closed = true
                    frames.invalidate()
                default: break
                }
            }
    }

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
    var putAway = false

    while !controller.closed && !app.isTerminated && processIsAlive(configuration.parentProcessID) {
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
        if applicationIsPutAway(app) {
            if let existing = stream, let existingOutput = output {
                stopWindowStream(existing, output: existingOutput)
            }
            stream = nil
            output = nil
            if !putAway {
                putAway = true
                writer.send("capture_status", fields: ["status": "suspended"])
            }
            establishFailures = 0
            try await Task.sleep(for: .milliseconds(250))
            continue
        }
        if putAway {
            putAway = false
            lastVerifiedCaptureAt = ProcessInfo.processInfo.systemUptime
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
