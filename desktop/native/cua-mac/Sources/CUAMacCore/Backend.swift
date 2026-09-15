import AppKit
import ApplicationServices
import CoreGraphics
import Darwin
import Foundation

private final class ApplicationBox: @unchecked Sendable {
    private let lock = NSLock()
    private var application: NSRunningApplication?
    private var error: Error?

    func set(application: NSRunningApplication?, error: Error?) {
        lock.lock()
        self.application = application
        self.error = error
        lock.unlock()
    }

    func get() -> (NSRunningApplication?, Error?) {
        lock.lock()
        defer { lock.unlock() }
        return (application, error)
    }
}

public final class MacComputerBackend: ComputerBackend {
    private let snapshotter = AXSnapshotter()
    private var snapshotTicket: SnapshotTicket?
    private var snapshotWindows: [CGWindowID: CGRect] = [:]
    private var storedSnapshotText = ""
    private var snapshotProcessID: pid_t?
    private var lastSnapshotText: [pid_t: String] = [:]
    private var axRevisions: [pid_t: UInt64] = [:]
    private var visualRevisions: [pid_t: UInt64] = [:]
    private var lastScreenshotData: [pid_t: Data] = [:]
    private var lastCaptureGeometry: [pid_t: CaptureGeometry] = [:]
    private var lastWindowIDs: [pid_t: CGWindowID] = [:]
    private var foregroundCaptureProcessIDs = Set<pid_t>()
    private var concealedWindowOrigins: [pid_t: [CGPoint]] = [:]

    public init() {}

    public func shutdown() {
        for pid in Array(concealedWindowOrigins.keys) {
            if let app = NSRunningApplication(processIdentifier: pid) {
                restoreConcealedWindows(app: app, application: AXUIElementCreateApplication(pid))
            }
        }
    }

    public func perform(_ command: ComputerCommand) throws -> ComputerResult {
        try ComputerExecution.checkpoint()
        switch command.action {
        case .permissionStatus:
            return permissionStatus()
        case .requestPermissions:
            return requestPermissions()
        case .listApps:
            return try listApps(command)
        default:
            break
        }

        guard let target = command.app?.trimmingCharacters(in: .whitespacesAndNewlines), !target.isEmpty else {
            throw ComputerError.invalidArguments("app is required for \(command.action.rawValue)")
        }
        let frontmostBefore = NSWorkspace.shared.frontmostApplication?.processIdentifier
        let app = try resolveApplication(target, launch: !command.isMutation)
        let appActionLock = try AppActionLock.acquire(processID: app.processIdentifier)
        defer { withExtendedLifetime(appActionLock) {} }
        try ComputerExecution.checkpoint()
        let axApplication = AXUIElementCreateApplication(app.processIdentifier)
        enableElectronAccessibility(axApplication)
        if command.action == .querySnapshot {
            return try querySnapshot(command, app: app)
        }
        if command.isMutation {
            try validateSnapshot(command, app: app)
            snapshotTicket?.invalidate()
        }
        if command.foregroundPolicy == .require {
            try ForegroundInputLock.withLock {
                restoreConcealedWindows(app: app, application: axApplication)
                try activate(app)
            }
        } else if command.foregroundPolicy == .allow {
            // A foreground input action must not post global events at a concealed
            // window's off-screen coordinates (the WindowServer would clamp them to a
            // screen edge and hit whatever is there). Bring the windows back first, so
            // element frames and coordinates resolve against the on-screen window.
            switch command.action {
            case .observe, .querySnapshot, .concealApp, .revealApp, .waitForChange, .waitFor,
                 .permissionStatus, .requestPermissions, .listApps, .sequence:
                break
            default:
                restoreConcealedWindows(app: app, application: axApplication)
            }
        }

        // Resolve the visual target before the action mutates or removes it. The
        // overlay is explanatory UI, so a best-effort position is sufficient.
        let intendedInteraction = interactionMetadata(command, app: app, application: axApplication)
        let mechanism: String
        switch command.action {
        case .observe:
            return try observe(command, app: app, axApplication: axApplication)
        case .concealApp:
            return try concealApp(app: app, application: axApplication)
        case .revealApp:
            return try revealApp(app: app, application: axApplication)
        case .click:
            mechanism = try click(command, app: app)
        case .drag:
            mechanism = try drag(command, app: app)
        case .pressKey:
            mechanism = try pressKey(command, app: app)
        case .pressKeys:
            mechanism = try pressKeys(command, app: app)
        case .scroll:
            mechanism = try scroll(command, app: app)
        case .setValue:
            try setValue(command, app: app)
            mechanism = "background_ax"
        case .typeText:
            mechanism = try typeText(command, app: app)
        case .selectText:
            try selectText(command, app: app)
            mechanism = "background_ax"
        case .performAction:
            try performSecondaryAction(command, app: app)
            mechanism = "background_ax"
        case .waitForChange:
            return try waitForChange(command, app: app, axApplication: axApplication)
        case .waitFor:
            let status = try verify(command.expectation!, application: axApplication, timeout: command.timeout ?? 5)
            let observation = try observe(command, app: app, axApplication: axApplication)
            var structured = observation.structured
            structured["verification"] = status
            return ComputerResult(text: "Postcondition: \(status).\n" + observation.text,
                screenshot: observation.screenshot, screenshotMIMEType: observation.screenshotMIMEType, structured: structured)
        case .sequence:
            throw ComputerError.unsupported("sequence is coordinated by the Wuu runtime")
        case .activateControl:
            try activateControl(command, app: app, application: axApplication)
            mechanism = "background_ax"
        case .permissionStatus, .requestPermissions, .listApps, .querySnapshot:
            preconditionFailure("global actions returned before target resolution")
        }
        ComputerExecution.current?.finishInput()
        let verification = try command.expectation.map { try verify($0, application: axApplication, timeout: command.timeout ?? 5) } ?? "not_requested"
        let frontmostAfter = NSWorkspace.shared.frontmostApplication?.processIdentifier
        let canonicalTarget = app.bundleIdentifier ?? app.bundleURL?.path ?? target
        var structured: [String: Any] = [
            "delivery": "delivered",
            "verification": verification,
            "action": command.action.rawValue,
            "app": canonicalTarget,
            "display_name": app.localizedName ?? "Unknown",
            "process_id": Int(app.processIdentifier),
            "mechanism": mechanism,
            "foreground_changed": frontmostBefore != frontmostAfter,
        ]
        if let windowID = lastWindowIDs[app.processIdentifier] {
            structured["window_id"] = Int(windowID)
        }
        if let interaction = intendedInteraction {
            structured["interaction"] = interaction
        }
        if let mode = command.after {
            do {
                let next = try ComputerCommand(arguments: ["action": "observe", "app": canonicalTarget,
                    "mode": mode.rawValue, "control_epoch": command.controlEpoch])
                let observation = try observe(next, app: app, axApplication: axApplication)
                structured["observation"] = observation.structured
                structured["snapshot_id"] = observation.structured["snapshot_id"]
                structured["window_id"] = observation.structured["window_id"]
                return ComputerResult(text: "Input delivered; verification=\(verification).\n" + observation.text,
                    screenshot: observation.screenshot, screenshotMIMEType: observation.screenshotMIMEType, structured: structured)
            } catch {
                structured["observation_error"] = error.localizedDescription
                return ComputerResult(text: "Input delivered; post-action observation failed: \(error.localizedDescription). Do not replay without observing.", structured: structured)
            }
        }
        return ComputerResult(text: "Input delivered to app=\"\(canonicalTarget)\"; verification=\(verification). Snapshot consumed. Observe when the outcome matters.", structured: structured)
    }

    private func interactionMetadata(
        _ command: ComputerCommand,
        app: NSRunningApplication,
        application: AXUIElement
    ) -> [String: Any]? {
        let kind: String
        switch command.action {
        case .click, .performAction, .activateControl: kind = "click"
        case .drag: kind = "drag"
        case .scroll: kind = "scroll"
        case .setValue, .typeText, .selectText, .pressKey, .pressKeys: kind = "type"
        default: return nil
        }
        let frame: CGRect? = {
            if command.elementID != nil, let element = try? element(command, app: app) {
                return axFrame(element)
            }
            if command.action == .activateControl,
               let target = snapshotter.uniqueElement(role: command.role, title: command.title, description: command.description) {
                return axFrame(target)
            }
            if kind == "type",
               let focused = axValue(application, kAXFocusedUIElementAttribute as String),
               CFGetTypeID(focused) == AXUIElementGetTypeID() {
                return axFrame(focused as! AXUIElement)
            }
            return nil
        }()
        let point: CGPoint? = {
            if let frame { return CGPoint(x: frame.midX, y: frame.midY) }
            if let x = command.x, let y = command.y {
                return try? inputPoint(x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
            }
            return primaryWindowFrame(application).map { CGPoint(x: $0.midX, y: $0.midY) }
        }()
        guard let point, let normalized = normalizedInteractionPoint(point, app: app) else { return nil }
        var interaction: [String: Any] = ["kind": kind, "x": normalized.x, "y": normalized.y]
        if kind == "drag", let x = command.toX, let y = command.toY {
            let destinationArguments: [String: Any] = [
                "action": "click", "app": command.app ?? "", "x": x, "y": y,
                "coordinate_space": command.coordinateSpace ?? "normalized",
            ]
            if let destinationCommand = try? ComputerCommand(arguments: destinationArguments),
               let destinationX = destinationCommand.x,
               let destinationY = destinationCommand.y,
               let destination = try? inputPoint(x: destinationX, y: destinationY, coordinateSpace: destinationCommand.coordinateSpace, app: app),
               let to = normalizedInteractionPoint(destination, app: app) {
                interaction["to_x"] = to.x
                interaction["to_y"] = to.y
            }
        }
        if let direction = command.direction { interaction["direction"] = direction }
        return interaction
    }

    private func normalizedInteractionPoint(_ point: CGPoint, app: NSRunningApplication) -> CGPoint? {
        let frame = lastCaptureGeometry[app.processIdentifier]?.windowFrame
            ?? primaryWindowFrame(AXUIElementCreateApplication(app.processIdentifier))
        guard let frame, frame.width > 0, frame.height > 0 else { return nil }
        return CGPoint(
            x: max(0, min(1, (point.x - frame.minX) / frame.width)),
            y: max(0, min(1, (point.y - frame.minY) / frame.height))
        )
    }

    // Capture the target window once and update the stored visual revision if the
    // pixels changed. Best effort: any capture failure simply reports no change.
    // Both capture paths run without activating the app. This never touches
    // lastCaptureGeometry — only observe, which hands the geometry back to the
    // model, may change the coordinate space the model is clicking against.
    private func captureVisualRevision(app: NSRunningApplication) -> Bool {
        guard #available(macOS 14.0, *) else { return false }
        let pid = app.processIdentifier
        var capture: WindowCapture?
        // Prefer the non-activating window capture. Current macOS releases may
        // proxy this legacy Core Graphics API through the system capture service,
        // so the live PiP uses a separate executable path and therefore a distinct
        // capture-client identity.
        if let foreground = try? captureForegroundWindowPNG(processID: pid) {
            capture = foreground
        } else if ProcessInfo.processInfo.environment["WUU_CUA_NO_SCK_OBSERVE"] != "1",
                  let background = try? captureWindowPNG(processID: pid) {
            foregroundCaptureProcessIDs.remove(pid)
            capture = background
        }
        guard let capture else { return false }
        let changed = lastScreenshotData[pid] != capture.data
        if changed {
            visualRevisions[pid, default: 0] += 1
            lastScreenshotData[pid] = capture.data
        }
        return changed
    }

    private func permissionStatus() -> ComputerResult {
        let accessibility = AXIsProcessTrusted()
        let screenRecording = CGPreflightScreenCaptureAccess()
        let text = "Accessibility: \(accessibility ? "granted" : "missing"); Screen Recording: \(screenRecording ? "granted" : "missing")"
        return ComputerResult(text: text, structured: [
            "accessibility": accessibility,
            "screen_recording": screenRecording,
            "accessibility_settings_url": "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
            "screen_recording_settings_url": "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
        ])
    }

    private func requestPermissions() -> ComputerResult {
        let options = ["AXTrustedCheckOptionPrompt": true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(options)
        if !CGPreflightScreenCaptureAccess() {
            _ = CGRequestScreenCaptureAccess()
        }
        return permissionStatus()
    }

    private func listApps(_ command: ComputerCommand) throws -> ComputerResult {
        let apps = NSWorkspace.shared.runningApplications
            .filter { $0.activationPolicy == .regular && !$0.isTerminated }
            .sorted { ($0.localizedName ?? "") < ($1.localizedName ?? "") }
            .map { app -> [String: Any] in
                [
                    "id": app.bundleIdentifier ?? app.bundleURL?.path ?? String(app.processIdentifier),
                    "displayName": app.localizedName ?? "Unknown",
                    "bundleIdentifier": app.bundleIdentifier ?? "",
                    "path": app.bundleURL?.path ?? "",
                    "processID": Int(app.processIdentifier),
                    "isActive": app.isActive,
                    "isHidden": app.isHidden,
                ]
            }
        let text = apps.map { app in
            "\(app["displayName"] ?? "Unknown")\tbundle=\(app["bundleIdentifier"] ?? "")\tpid=\(app["processID"] ?? 0)"
        }.joined(separator: "\n")
        var structured: [String: Any] = ["apps": apps]
        if let target = command.app?.trimmingCharacters(in: .whitespacesAndNewlines), !target.isEmpty {
            let resolved = try resolveApplication(target)
            structured["resolved_app"] = [
                "id": resolved.bundleIdentifier ?? resolved.bundleURL?.path ?? String(resolved.processIdentifier),
                "displayName": resolved.localizedName ?? "Unknown",
                "bundleIdentifier": resolved.bundleIdentifier ?? "",
                "path": resolved.bundleURL?.path ?? "",
                "processID": Int(resolved.processIdentifier),
            ]
        }
        return ComputerResult(text: text, structured: structured)
    }

    private func resolveApplication(_ target: String, launch: Bool = true) throws -> NSRunningApplication {
        let normalized = target.lowercased()
        let running = NSWorkspace.shared.runningApplications.filter { !$0.isTerminated && isProcessAlive($0.processIdentifier) }
        let exactMatches = running.filter {
            $0.bundleIdentifier?.lowercased() == normalized ||
            $0.localizedName?.lowercased() == normalized ||
            $0.bundleURL?.path.lowercased() == normalized
        }
        if let exact = preferWindowedInstance(exactMatches) {
            return exact
        }
        guard let url = applicationURL(target) else {
            if let partial = preferWindowedInstance(running.filter({
                $0.localizedName?.lowercased().contains(normalized) == true
            })) {
                return partial
            }
            throw ComputerError.appNotFound(target)
        }
        if let bundleIdentifier = Bundle(url: url)?.bundleIdentifier,
           let existing = preferWindowedInstance(running.filter({ $0.bundleIdentifier == bundleIdentifier })) {
            return existing
        }
        guard launch else { throw ComputerError.staleSnapshot("target app is no longer running") }
        let semaphore = DispatchSemaphore(value: 0)
        let box = ApplicationBox()
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = false
        NSWorkspace.shared.openApplication(at: url, configuration: configuration) { app, error in
            box.set(application: app, error: error)
            semaphore.signal()
        }
        guard waitForAsyncSignal(semaphore, timeout: 12) else {
            throw ComputerError.operationFailed("launching \(target) timed out")
        }
        let (app, error) = box.get()
        if let error { throw ComputerError.operationFailed("launch \(target): \(error.localizedDescription)") }
        guard let app else { throw ComputerError.appNotFound(target) }
        waitForApplicationWindow(app, timeout: 5)
        return app
    }

    private func isProcessAlive(_ pid: pid_t) -> Bool {
        // kill(pid, 0) probes existence without signalling; ESRCH means the process
        // is gone even if NSWorkspace has not yet dropped its stale record.
        kill(pid, 0) == 0 || errno == EPERM
    }

    private func hasWindow(_ app: NSRunningApplication) -> Bool {
        var value: CFTypeRef?
        let application = AXUIElementCreateApplication(app.processIdentifier)
        guard AXUIElementCopyAttributeValue(application, kAXWindowsAttribute as CFString, &value) == .success,
              let windows = value as? [AXUIElement] else { return false }
        return !windows.isEmpty
    }

    private func preferWindowedInstance(_ candidates: [NSRunningApplication]) -> NSRunningApplication? {
        guard !candidates.isEmpty else { return nil }
        // A windowless instance (e.g. a hidden launch that never created a window)
        // cannot be observed or controlled, so never let it shadow a usable one.
        return candidates.first(where: hasWindow) ?? candidates.first
    }

    private func waitForApplicationWindow(_ app: NSRunningApplication, timeout: TimeInterval) {
        let deadline = Date(timeIntervalSinceNow: timeout)
        let application = AXUIElementCreateApplication(app.processIdentifier)
        repeat {
            var value: CFTypeRef?
            let result = AXUIElementCopyAttributeValue(application, kAXWindowsAttribute as CFString, &value)
            if app.isFinishedLaunching,
               result == .success,
               let windows = value as? [AXUIElement],
               !windows.isEmpty {
                return
            }
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.1))
        } while Date() < deadline && !app.isTerminated
    }

    private func applicationURL(_ target: String) -> URL? {
        if target.contains("."), let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: target) {
            return url
        }
        let expanded = NSString(string: target).expandingTildeInPath
        if FileManager.default.fileExists(atPath: expanded) { return URL(fileURLWithPath: expanded) }
        let name = target.hasSuffix(".app") ? target : target + ".app"
        for root in [
            "/Applications",
            "/System/Applications",
            "/System/Applications/Utilities",
            "/System/Library/CoreServices",
            NSHomeDirectory() + "/Applications",
        ] {
            let path = URL(fileURLWithPath: root).appendingPathComponent(name).path
            if FileManager.default.fileExists(atPath: path) { return URL(fileURLWithPath: path) }
        }
        return nil
    }

    private func enableElectronAccessibility(_ application: AXUIElement) {
        _ = AXUIElementSetAttributeValue(application, "AXManualAccessibility" as CFString, kCFBooleanTrue)
        _ = AXUIElementSetAttributeValue(application, "AXEnhancedUserInterface" as CFString, kCFBooleanTrue)
    }

    private func requireAccessibility() throws {
        guard AXIsProcessTrusted() else {
            throw ComputerError.permissionDenied("Accessibility permission is required for app observation and control")
        }
    }

    private func observe(_ command: ComputerCommand, app: NSRunningApplication, axApplication: AXUIElement) throws -> ComputerResult {
        var root = axApplication
        if let rootID = command.rootElementID {
            try validateSnapshot(command, app: app)
            guard snapshotter.isCurrent(id: rootID), let element = snapshotter.element(id: rootID) else { throw ComputerError.staleSnapshot("subtree changed") }
            root = element
        }
        snapshotTicket = nil
        lastCaptureGeometry[app.processIdentifier] = nil
        lastWindowIDs[app.processIdentifier] = nil
        let accessibility = AXIsProcessTrusted() && command.observationMode != .vision
        let snapshot: AXSnapshot
        let previousText = lastSnapshotText[app.processIdentifier]
        if accessibility {
            snapshot = snapshotter.snapshot(application: root)
        } else {
            snapshotter.clear()
            snapshot = AXSnapshot(
                text: command.observationMode == .vision ? "Vision observation; AX tree omitted." : "Accessibility permission is unavailable.",
                elements: [:]
            )
        }
        snapshotProcessID = app.processIdentifier
        lastSnapshotText[app.processIdentifier] = snapshot.text
        if previousText != snapshot.text {
            axRevisions[app.processIdentifier, default: 0] += 1
        }
        var structured: [String: Any] = [
            "app": app.bundleIdentifier ?? app.localizedName ?? String(app.processIdentifier),
            "display_name": app.localizedName ?? "Unknown",
            "process_id": Int(app.processIdentifier),
            "element_count": snapshot.elements.count,
            "ax_available": accessibility,
            "ax_preferred": accessibility,
            "ax_truncated": snapshot.truncated,
        ]
        var screenshot: Data?
        if command.observationMode != .ax {
        do {
            if command.scope == .window {
                // Legacy single-window path: unchanged so scope=window (the default)
                // stays byte-for-byte identical to the pre-composite behaviour.
                let capture = try captureWindowWithForegroundFallback(command, app: app)
                screenshot = capture.data
                if lastScreenshotData[app.processIdentifier] != capture.data {
                    visualRevisions[app.processIdentifier, default: 0] += 1
                    lastScreenshotData[app.processIdentifier] = capture.data
                }
                lastCaptureGeometry[app.processIdentifier] = capture.geometry
                lastWindowIDs[app.processIdentifier] = capture.windowID
                structured["window_id"] = Int(capture.windowID)
                structured["screenshot"] = [
                    "width": capture.geometry.imageWidth,
                    "height": capture.geometry.imageHeight,
                    "window_frame": [
                        "x": capture.geometry.windowFrame.origin.x,
                        "y": capture.geometry.windowFrame.origin.y,
                        "width": capture.geometry.windowFrame.width,
                        "height": capture.geometry.windowFrame.height,
                    ],
                    "coordinate_space": "latest_screenshot_pixels",
                    "visible_image_frame": [
                        "x": capture.geometry.visibleImageFrame.origin.x,
                        "y": capture.geometry.visibleImageFrame.origin.y,
                        "width": capture.geometry.visibleImageFrame.width,
                        "height": capture.geometry.visibleImageFrame.height,
                    ],
                ]
            } else {
                let (capture, windows) = try captureCompositeForScope(command, app: app)
                screenshot = capture.data
                if lastScreenshotData[app.processIdentifier] != capture.data {
                    visualRevisions[app.processIdentifier, default: 0] += 1
                    lastScreenshotData[app.processIdentifier] = capture.data
                }
                // observe defines the coordinate space for the model's subsequent
                // clicks. For a composite the union rect replaces the single-window
                // frame, so screenshot/normalized/screen coordinates now resolve
                // against the whole composited image (CaptureGeometry's affine map
                // only needs the union frame and the image size).
                lastCaptureGeometry[app.processIdentifier] = capture.geometry
                lastWindowIDs[app.processIdentifier] = capture.windowID
                structured["window_id"] = Int(capture.windowID)
                var screenshotDict: [String: Any] = [
                    "width": capture.geometry.imageWidth,
                    "height": capture.geometry.imageHeight,
                    "window_frame": [
                        "x": capture.geometry.windowFrame.origin.x,
                        "y": capture.geometry.windowFrame.origin.y,
                        "width": capture.geometry.windowFrame.width,
                        "height": capture.geometry.windowFrame.height,
                    ],
                    "coordinate_space": "latest_screenshot_pixels",
                    "visible_image_frame": [
                        "x": capture.geometry.visibleImageFrame.origin.x,
                        "y": capture.geometry.visibleImageFrame.origin.y,
                        "width": capture.geometry.visibleImageFrame.width,
                        "height": capture.geometry.visibleImageFrame.height,
                    ],
                    "scope": command.scope.rawValue,
                ]
                // z-ordered window rectangles inside the composite (z_index 0 frontmost).
                screenshotDict["windows"] = windows.map(\.dictionary)
                structured["screenshot"] = screenshotDict
            }
        } catch {
            structured["screenshot_error"] = error.localizedDescription
        }
        }
        let canonicalTarget = app.bundleIdentifier ?? app.bundleURL?.path ?? String(app.processIdentifier)
        var header = "Target app=\"\(canonicalTarget)\" display_name=\"\(app.localizedName ?? "Unknown")\" pid=\(app.processIdentifier). Reuse this exact app value for follow-up actions."
        if let geometry = lastCaptureGeometry[app.processIdentifier], screenshot != nil {
            header += " Screenshot=\(geometry.imageWidth)×\(geometry.imageHeight) pixels maps to window_frame=(\(Int(geometry.windowFrame.origin.x)),\(Int(geometry.windowFrame.origin.y)),\(Int(geometry.windowFrame.width)),\(Int(geometry.windowFrame.height))). Prefer coordinate_space=\"normalized\" (0-1000) for visual targets so provider image resizing does not affect clicks; use coordinate_space=\"screenshot\" only for original image pixels."
        }
        let changes = previousText.map { snapshotChanges(from: $0, to: snapshot.text) } ?? []
        let ticket = SnapshotTicket(processID: app.processIdentifier, launchTime: app.launchDate?.timeIntervalSince1970 ?? 0, controlEpoch: command.controlEpoch)
        snapshotTicket = ticket
        snapshotWindows = currentWindowFrames(processID: app.processIdentifier)
        storedSnapshotText = snapshot.text
        let page = SnapshotPage(text: snapshot.text, query: nil, offset: command.offset, limit: command.limit)
        structured["snapshot_id"] = ticket.id
        structured["mode"] = command.observationMode.rawValue
        structured["ax_revision"] = axRevisions[app.processIdentifier] ?? 0
        structured["visual_revision"] = visualRevisions[app.processIdentifier] ?? 0
        structured["full_snapshot"] = true
        structured["changes"] = changes
        structured["total_lines"] = page.total
        structured["next_offset"] = page.nextOffset
        header += " snapshot_id=\"\(ticket.id)\"."
        if let next = page.nextOffset { header += " More AX lines: query_snapshot offset=\(next)." }
        let stateText = page.text
        return ComputerResult(
            text: header + "\n" + stateText,
            screenshot: screenshot,
            screenshotMIMEType: screenshot == nil ? nil : "image/png",
            structured: structured
        )
    }

    // scope=app / scope=screen capture path. Kept separate from the single-window
    // fallback so the default path is untouched. All new ScreenCaptureKit / CGWindow
    // work stays inside the MCP server process (this executable); it must never move
    // into the PiP helper, which replayd tracks as a distinct capture client by its
    // separate executable path.
    private func captureCompositeForScope(_ command: ComputerCommand, app: NSRunningApplication) throws -> (WindowCapture, [WindowScreenshotInfo]) {
        guard #available(macOS 14.0, *) else {
            throw ComputerError.unsupported("multi-window composite capture requires macOS 14 or newer")
        }
        return try captureAppCompositePNG(processID: app.processIdentifier, scope: command.scope)
    }

    private func captureWindowWithForegroundFallback(_ command: ComputerCommand, app: NSRunningApplication) throws -> WindowCapture {
        guard #available(macOS 14.0, *) else {
            throw ComputerError.unsupported("window screenshots require macOS 14 or newer")
        }
        // Capture without activating the app. Current macOS releases may proxy
        // this Core Graphics call through the system capture service; the PiP's
        // separate executable path keeps that proxy connection from replacing the
        // live stream's client identity.
        if let background = try? captureForegroundWindowPNG(processID: app.processIdentifier) {
            return background
        }
        // Test-only isolation switch: skip the explicit SCScreenshotManager path.
        // The Core Graphics path above may still be proxied through the system
        // capture service, so this is not a guarantee that observe avoids it.
        if ProcessInfo.processInfo.environment["WUU_CUA_NO_SCK_OBSERVE"] == "1" {
            throw ComputerError.operationFailed("observe capture unavailable (no-SCK isolation mode)")
        }
        if foregroundCaptureProcessIDs.contains(app.processIdentifier) {
            return try withForegroundInput(command, app: app) {
                try captureForegroundWindowPNG(processID: app.processIdentifier)
            }
        }
        do {
            return try captureWindowPNG(
                processID: app.processIdentifier,
                preferredWindowFrame: focusedWindowFrame(processID: app.processIdentifier)
            )
        } catch let backgroundError {
            foregroundCaptureProcessIDs.insert(app.processIdentifier)
            do {
                return try withForegroundInput(command, app: app) {
                    try captureForegroundWindowPNG(processID: app.processIdentifier)
                }
            } catch let foregroundError {
                throw ComputerError.operationFailed(
                    "window screenshot failed in background (\(backgroundError.localizedDescription)) and after foreground retry (\(foregroundError.localizedDescription))"
                )
            }
        }
    }

    private func focusedWindowFrame(processID: pid_t) -> CGRect? {
        let application = AXUIElementCreateApplication(processID)
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(application, kAXFocusedWindowAttribute as CFString, &value) == .success,
              let window = value as! AXUIElement? else { return nil }
        return axFrame(window)
    }

    private func element(_ command: ComputerCommand, app: NSRunningApplication) throws -> AXUIElement {
        guard snapshotProcessID == app.processIdentifier else {
            throw ComputerError.invalidArguments("observe this app before using an element_id")
        }
        guard let id = command.elementID else {
            throw ComputerError.invalidArguments("element_id is required")
        }
        guard let element = snapshotter.element(id: id), let descriptor = snapshotter.descriptor(id: id) else {
            throw ComputerError.elementNotFound(id)
        }
        guard snapshotter.isCurrent(id: id) else { throw ComputerError.staleSnapshot("element \(id) changed") }
        _ = descriptor
        return element
    }

    private func settleAccessibility(app: NSRunningApplication, application: AXUIElement, previous: String) -> AXSnapshot {
        guard AXIsProcessTrusted() else {
            return AXSnapshot(text: previous, elements: [:])
        }
        waitForAccessibilitySettle(application: application, processID: app.processIdentifier, debounce: 0.12, timeout: 1.5)
        let latest = snapshotter.snapshot(application: application)
        if latest.text != previous {
            axRevisions[app.processIdentifier, default: 0] += 1
        }
        lastSnapshotText[app.processIdentifier] = latest.text
        snapshotProcessID = app.processIdentifier
        return latest
    }

    // A single input action's control level, chosen deterministically by policy:
    //   avoid (default) → level 2 directed input via CGEvent.postToPid; the target
    //                     process receives the event without being activated, so the
    //                     user's frontmost app and real pointer never move.
    //   allow / require → level 3 visible foreground takeover. The model opts into
    //                     this explicitly, so directed input is not attempted first;
    //                     that avoids double-applying an effect a directed attempt
    //                     may already have had (there is no reliable way to detect a
    //                     dropped directed event without risking a double input).
    // Element clicks and other AX-native actions resolve to level 1 before reaching
    // here and never post synthetic input at all.
    private func performDirectedOrForeground(
        _ command: ComputerCommand,
        app: NSRunningApplication,
        directed: () throws -> Void,
        foreground: () throws -> Void
    ) throws -> String {
        if command.foregroundPolicy == .avoid {
            try directed()
            return "background_directed"
        }
        try withForegroundInput(command, app: app) { try foreground() }
        return "foreground_native"
    }

    private func snapshotChanges(from previous: String, to current: String) -> [String] {
        guard previous != current else { return [] }
        return Array(current.components(separatedBy: "\n").difference(from: previous.components(separatedBy: "\n")).prefix(240)).map { change in
            switch change {
            case let .insert(offset, line, _): return "+ line \(offset): \(line)"
            case let .remove(offset, line, _): return "- line \(offset): \(line)"
            }
        }
    }

    private func currentWindowFrames(processID: pid_t) -> [CGWindowID: CGRect] {
        let windows = CGWindowListCopyWindowInfo(.optionAll, kCGNullWindowID) as? [[String: Any]] ?? []
        var frames: [CGWindowID: CGRect] = [:]
        for window in windows {
            guard window[kCGWindowOwnerPID as String] as? Int32 == processID,
                  let id = window[kCGWindowNumber as String] as? UInt32,
                  let bounds = window[kCGWindowBounds as String] as? NSDictionary,
                  let frame = CGRect(dictionaryRepresentation: bounds) else { continue }
            frames[id] = frame
        }
        return frames
    }

    private func validateSnapshot(_ command: ComputerCommand, app: NSRunningApplication) throws {
        guard let ticket = snapshotTicket else { throw ComputerError.staleSnapshot("observe the target first") }
        try ticket.validate(reference: command.snapshotID, processID: app.processIdentifier,
            launchTime: app.launchDate?.timeIntervalSince1970 ?? 0, epoch: command.controlEpoch,
            requiresReference: command.referencesSnapshot || command.action == .querySnapshot)
        if command.referencesSnapshot {
            guard currentWindowFrames(processID: app.processIdentifier) == snapshotWindows else {
                throw ComputerError.staleSnapshot("target window geometry or identity changed")
            }
        }
    }

    private func querySnapshot(_ command: ComputerCommand, app: NSRunningApplication) throws -> ComputerResult {
        try validateSnapshot(command, app: app)
        let page = SnapshotPage(text: storedSnapshotText, query: command.query, offset: command.offset, limit: command.limit)
        let id = snapshotTicket!.id
        var structured: [String: Any] = ["snapshot_id": id, "total_lines": page.total, "process_id": Int(app.processIdentifier)]
        structured["next_offset"] = page.nextOffset
        let suffix = page.nextOffset.map { " Next offset=\($0)." } ?? ""
        return ComputerResult(text: "Stored snapshot_id=\"\(id)\".\(suffix)\n" + page.text, structured: structured)
    }

    private func activate(_ app: NSRunningApplication) throws {
        try ComputerExecution.input()
        app.unhide()
        guard app.activate(options: [.activateAllWindows, .activateIgnoringOtherApps]) else {
            throw ComputerError.operationFailed("could not activate \(app.localizedName ?? "target app")")
        }
        let application = AXUIElementCreateApplication(app.processIdentifier)
        _ = AXUIElementSetAttributeValue(application, kAXFrontmostAttribute as CFString, kCFBooleanTrue)
        var focusedWindow: CFTypeRef?
        if AXUIElementCopyAttributeValue(application, kAXFocusedWindowAttribute as CFString, &focusedWindow) == .success,
           let window = focusedWindow as! AXUIElement? {
            _ = AXUIElementPerformAction(window, kAXRaiseAction as CFString)
        }
        let deadline = Date(timeIntervalSinceNow: 2)
        repeat {
            if app.isActive || NSWorkspace.shared.frontmostApplication?.processIdentifier == app.processIdentifier {
                return
            }
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.02))
        } while Date() < deadline && !app.isTerminated
        throw ComputerError.operationFailed("\(app.localizedName ?? "target app") did not become active")
    }

    private func withForegroundInput<T>(_ command: ComputerCommand, app: NSRunningApplication, body: () throws -> T) throws -> T {
        guard command.foregroundPolicy != .avoid else {
            throw ComputerError.requiresForeground(
                "\(command.action.rawValue) needs native mouse or keyboard input for \(app.localizedName ?? "the target app"); retry with foreground_policy=\"allow\" only when foreground control is acceptable"
            )
        }
        return try ForegroundInputLock.withLock {
            restoreConcealedWindows(app: app, application: AXUIElementCreateApplication(app.processIdentifier))
            try activate(app)
            if command.referencesSnapshot, currentWindowFrames(processID: app.processIdentifier) != snapshotWindows {
                throw ComputerError.staleSnapshot("window moved during foreground activation")
            }
            guard let execution = ComputerExecution.current else { return try body() }
            return try execution.withInputGuard({
                guard NSWorkspace.shared.frontmostApplication?.processIdentifier == app.processIdentifier else {
                    throw ComputerError.cancelled("foreground focus changed; input interrupted")
                }
            }, body: body)
        }
    }

    // A rendered window keeps its full Accessibility tree and its ScreenCaptureKit
    // backing surface no matter where it sits, so parking it beyond every display
    // hides it from the user while background control and the live PiP keep working.
    // The Dock icon, Cmd-Tab entry, and Mission Control presence follow the target
    // app's own activation policy, which only that process can change; conceal_app
    // does not claim to remove them.
    private func offscreenOrigin() -> CGPoint {
        let union = NSScreen.screens.reduce(CGRect.zero) { $0.union($1.frame) }
        let width = max(union.width, 4000)
        let height = max(union.height, 4000)
        return CGPoint(x: union.maxX + width, y: union.maxY + height)
    }

    private func appWindows(_ application: AXUIElement) -> [AXUIElement] {
        axElements(application, attribute: kAXWindowsAttribute as String)
    }

    private func setWindowOrigin(_ window: AXUIElement, to origin: CGPoint) -> Bool {
        var point = origin
        guard let value = AXValueCreate(.cgPoint, &point) else { return false }
        return AXUIElementSetAttributeValue(window, kAXPositionAttribute as CFString, value) == .success
    }

    private func concealApp(app: NSRunningApplication, application: AXUIElement) throws -> ComputerResult {
        try requireAccessibility()
        let windows = appWindows(application)
        guard !windows.isEmpty else {
            throw ComputerError.operationFailed("no window to conceal yet; observe the app or wait for its window before concealing")
        }
        let target = offscreenOrigin()
        var origins = concealedWindowOrigins[app.processIdentifier] ?? []
        var concealed = 0
        var clamped = 0
        for (index, window) in windows.enumerated() {
            let current = axFrame(window)?.origin ?? .zero
            // Only record an original that is still on-screen so a re-conceal does not
            // overwrite the saved position with the off-screen one.
            if index < origins.count {
                if current.x < target.x - 1 { origins[index] = current }
            } else {
                origins.append(current)
            }
            guard setWindowOrigin(window, to: target) else { continue }
            if (axFrame(window)?.origin.x ?? current.x) >= target.x - 1 {
                concealed += 1
            } else {
                clamped += 1
            }
        }
        concealedWindowOrigins[app.processIdentifier] = origins
        let canonicalTarget = app.bundleIdentifier ?? app.bundleURL?.path ?? String(app.processIdentifier)
        var text = "Concealed \(concealed) window(s) for app=\"\(canonicalTarget)\" off-screen; Accessibility control and live capture continue."
        if clamped > 0 {
            text += " \(clamped) window(s) refused the off-screen position and stay visible."
        }
        return ComputerResult(text: text, structured: [
            "action": "conceal_app",
            "app": canonicalTarget,
            "process_id": Int(app.processIdentifier),
            "status": clamped == 0 ? "concealed" : "partial",
            "mechanism": "background_ax",
            "foreground_changed": false,
            "concealed_windows": concealed,
            "unsupported_windows": clamped,
        ])
    }

    private func revealApp(app: NSRunningApplication, application: AXUIElement) throws -> ComputerResult {
        try requireAccessibility()
        let restored = restoreConcealedWindows(app: app, application: application)
        let canonicalTarget = app.bundleIdentifier ?? app.bundleURL?.path ?? String(app.processIdentifier)
        return ComputerResult(
            text: "Revealed \(restored) window(s) for app=\"\(canonicalTarget)\" at their original positions.",
            structured: [
                "action": "reveal_app",
                "app": canonicalTarget,
                "process_id": Int(app.processIdentifier),
                "status": "revealed",
                "mechanism": "background_ax",
                "foreground_changed": false,
                "revealed_windows": restored,
            ]
        )
    }

    @discardableResult
    private func restoreConcealedWindows(app: NSRunningApplication, application: AXUIElement) -> Int {
        guard let origins = concealedWindowOrigins[app.processIdentifier] else { return 0 }
        let windows = appWindows(application)
        var restored = 0
        for (index, window) in windows.enumerated() where index < origins.count {
            if setWindowOrigin(window, to: origins[index]) { restored += 1 }
        }
        concealedWindowOrigins[app.processIdentifier] = nil
        return restored
    }

    private func click(_ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        var point: CGPoint
        if command.elementID != nil {
            let target = try element(command, app: app)
            let hasPress = axActions(target).contains(kAXPressAction as String)
            let frame = axFrame(target)
            // Under the default policy a semantic AXPress is the most reliable target
            // for buttons and menu items and needs neither the pointer nor focus.
            // Under allow/require the model has decided AXPress was not enough, so
            // escalate to a real click at the element's frame (the doc's "AX silently
            // swallowed, fall to a physical click" path); AXPress remains the fallback
            // for frameless elements such as some menu items.
            if command.foregroundPolicy == .avoid, hasPress {
                try performAXAction(target, action: kAXPressAction as String)
                return "background_ax"
            }
            if let frame {
                point = CGPoint(x: frame.midX, y: frame.midY)
            } else if hasPress {
                try performAXAction(target, action: kAXPressAction as String)
                return "background_ax"
            } else {
                throw ComputerError.invalidArguments("element exposes no AXPress action and no frame to click")
            }
        } else {
            guard let x = command.x, let y = command.y else {
                throw ComputerError.invalidArguments("click requires element_id or x and y")
            }
            point = try inputPoint(x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
        }
        let pid = app.processIdentifier
        return try performDirectedOrForeground(command, app: app,
            directed: { try postClickToPid(pid, point: point, button: command.mouseButton, count: command.clickCount ?? 1) },
            foreground: { try self.postClick(point: point, button: command.mouseButton, count: command.clickCount ?? 1) })
    }

    private func postClick(point: CGPoint, button name: String?, count: Int) throws {
        let source = syntheticEventSource()
        let button: CGMouseButton
        let downType: CGEventType
        let upType: CGEventType
        switch name?.lowercased() ?? "left" {
        case "right": button = .right; downType = .rightMouseDown; upType = .rightMouseUp
        case "middle": button = .center; downType = .otherMouseDown; upType = .otherMouseUp
        default: button = .left; downType = .leftMouseDown; upType = .leftMouseUp
        }
        try ComputerExecution.input()
        markSynthetic(CGEvent(mouseEventSource: source, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: button))?.post(tap: .cghidEventTap)
        usleep(20_000)
        for click in 1...max(1, min(count, 3)) {
            guard let down = markSynthetic(CGEvent(mouseEventSource: source, mouseType: downType, mouseCursorPosition: point, mouseButton: button)),
                  let up = markSynthetic(CGEvent(mouseEventSource: source, mouseType: upType, mouseCursorPosition: point, mouseButton: button)) else {
                throw ComputerError.operationFailed("could not create mouse event")
            }
            down.setIntegerValueField(.mouseEventClickState, value: Int64(click))
            up.setIntegerValueField(.mouseEventClickState, value: Int64(click))
            try ComputerExecution.input()
            down.post(tap: .cghidEventTap)
            usleep(35_000)
            up.post(tap: .cghidEventTap)
        }
    }

    private func drag(_ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let x = command.x, let y = command.y, let toX = command.toX, let toY = command.toY else {
            throw ComputerError.invalidArguments("drag requires from_x, from_y, to_x, and to_y")
        }
        let start = try inputPoint(x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
        let end = try inputPoint(x: toX, y: toY, coordinateSpace: command.coordinateSpace, app: app)
        let pid = app.processIdentifier
        return try performDirectedOrForeground(command, app: app,
            directed: { try postDragToPid(pid, from: start, to: end) },
            foreground: { try self.postDragGlobal(from: start, to: end) })
    }

    private func postDragGlobal(from start: CGPoint, to end: CGPoint) throws {
        let source = syntheticEventSource()
        // Create the terminating mouse-up up front so a late allocation failure
        // cannot leave the target believing the button is still held down.
        guard let down = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseDown, mouseCursorPosition: start, mouseButton: .left)),
              let up = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseUp, mouseCursorPosition: end, mouseButton: .left)) else {
            throw ComputerError.operationFailed("could not create drag event")
        }
        try ComputerExecution.input()
        down.post(tap: .cghidEventTap)
        defer { up.post(tap: .cghidEventTap) }
        for step in 1...12 {
            try ComputerExecution.input()
            let progress = Double(step) / 12
            let point = CGPoint(
                x: start.x + (end.x - start.x) * progress,
                y: start.y + (end.y - start.y) * progress
            )
            markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseDragged, mouseCursorPosition: point, mouseButton: .left))?.post(tap: .cghidEventTap)
            usleep(8_000)
        }
    }

    private func inputPoint(x: Double, y: Double, coordinateSpace: String?, app: NSRunningApplication) throws -> CGPoint {
        switch coordinateSpace ?? "screenshot" {
        case "screen":
            let point = CGPoint(x: x, y: y)
            let frame = lastCaptureGeometry[app.processIdentifier]?.windowFrame
                ?? primaryWindowFrame(AXUIElementCreateApplication(app.processIdentifier))
            guard let frame, frame.contains(point) else {
                throw ComputerError.invalidArguments("screen coordinates must be inside the target window frame")
            }
            return try ownedInputPoint(point, app: app)
        case "normalized":
            guard let geometry = lastCaptureGeometry[app.processIdentifier] else {
                throw ComputerError.invalidArguments("observe the app before using normalized coordinates")
            }
            return try ownedInputPoint(geometry.screenPoint(normalizedX: x, normalizedY: y), app: app)
        case "screenshot":
            guard let geometry = lastCaptureGeometry[app.processIdentifier] else {
                throw ComputerError.invalidArguments("observe the app before using screenshot coordinates")
            }
            return try ownedInputPoint(geometry.screenPoint(x: x, y: y), app: app)
        default:
            throw ComputerError.invalidArguments("coordinate_space must be normalized, screenshot, or screen")
        }
    }

    private func ownedInputPoint(_ point: CGPoint, app: NSRunningApplication) throws -> CGPoint {
        guard currentWindowFrames(processID: app.processIdentifier).values.contains(where: { $0.contains(point) }) else {
            throw ComputerError.invalidArguments("coordinates must belong to a window of the target app")
        }
        return point
    }

    private func pressKey(_ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let key = command.key else { throw ComputerError.invalidArguments("key is required") }
        let chord = try KeyChord.parse(key)
        let pid = app.processIdentifier
        return try performDirectedOrForeground(command, app: app,
            directed: { try postKeyChordToPid(pid, chord: chord) },
            foreground: { try self.postKey(key) })
    }

    private func pressKeys(_ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let keys = command.keys, !keys.isEmpty else { throw ComputerError.invalidArguments("keys is required") }
        let chords = try keys.map { try KeyChord.parse($0) }
        let pid = app.processIdentifier
        return try performDirectedOrForeground(command, app: app,
            directed: {
                for chord in chords {
                    try postKeyChordToPid(pid, chord: chord)
                    usleep(20_000)
                }
            },
            foreground: {
                for key in keys {
                    try self.postKey(key)
                    usleep(20_000)
                }
            })
    }

    private func postKey(_ key: String) throws {
        let chord = try KeyChord.parse(key)
        let source = syntheticEventSource()
        guard let down = markSynthetic(CGEvent(keyboardEventSource: source, virtualKey: chord.keyCode, keyDown: true)),
              let up = markSynthetic(CGEvent(keyboardEventSource: source, virtualKey: chord.keyCode, keyDown: false)) else {
            throw ComputerError.operationFailed("could not create keyboard event")
        }
        down.flags = chord.modifiers.eventFlags
        up.flags = chord.modifiers.eventFlags
        try ComputerExecution.input()
        down.post(tap: .cghidEventTap)
        up.post(tap: .cghidEventTap)
    }

    private func scroll(_ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        let pages = max(1, min(command.pages ?? 1, 20))
        let direction = command.direction?.lowercased() ?? "down"
        let vertical: Int32 = direction == "up" ? 3 : direction == "down" ? -3 : 0
        let horizontal: Int32 = direction == "left" ? 3 : direction == "right" ? -3 : 0
        // Scroll must land inside the target: an element frame when given, otherwise
        // the app's primary window center. Without a point, directed scroll hit-tests
        // at the global origin and is dropped.
        let point: CGPoint? = if command.elementID != nil {
            try command.elementID.flatMap { _ in axFrame(try element(command, app: app)) }
                .map { CGPoint(x: $0.midX, y: $0.midY) }
        } else if let x = command.x, let y = command.y {
            try inputPoint(x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
        } else {
            primaryWindowFrame(AXUIElementCreateApplication(app.processIdentifier))
                .map { CGPoint(x: $0.midX, y: $0.midY) }
        }
        let pid = app.processIdentifier
        return try performDirectedOrForeground(command, app: app,
            directed: { try postScrollToPid(pid, vertical: vertical, horizontal: horizontal, steps: pages * 4, at: point) },
            foreground: { try self.postScrollGlobal(vertical: vertical, horizontal: horizontal, steps: pages * 4, at: point) })
    }

    private func primaryWindowFrame(_ application: AXUIElement) -> CGRect? {
        let largest = appWindows(application)
            .compactMap { axFrame($0) }
            .filter { $0.width > 1 && $0.height > 1 }
            .max(by: { $0.width * $0.height < $1.width * $1.height })
        if let focused = axValue(application, kAXFocusedWindowAttribute as String),
           CFGetTypeID(focused) == AXUIElementGetTypeID(),
           let frame = axFrame(focused as! AXUIElement),
           frame.width * frame.height >= (largest?.width ?? 0) * (largest?.height ?? 0) * 0.35 {
            return frame
        }
        return largest
    }

    private func postScrollGlobal(vertical: Int32, horizontal: Int32, steps: Int, at point: CGPoint?) throws {
        if let point {
            try ComputerExecution.input()
            markSynthetic(CGEvent(mouseEventSource: nil, mouseType: .mouseMoved, mouseCursorPosition: point, mouseButton: .left))?.post(tap: .cghidEventTap)
        }
        for _ in 0..<max(1, steps) {
            guard let event = markSynthetic(CGEvent(scrollWheelEvent2Source: nil, units: .line, wheelCount: 2, wheel1: vertical, wheel2: horizontal, wheel3: 0)) else {
                throw ComputerError.operationFailed("could not create scroll event")
            }
            try ComputerExecution.input()
            event.post(tap: .cghidEventTap)
            usleep(16_000)
        }
    }

    private func setValue(_ command: ComputerCommand, app: NSRunningApplication) throws {
        let target = try element(command, app: app)
        let value = command.value ?? command.text
        guard let value else { throw ComputerError.invalidArguments("value is required") }
        try setAXValue(target, attribute: kAXValueAttribute as String, value: value as CFString)
    }

    private func typeText(_ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let text = command.text else { throw ComputerError.invalidArguments("text is required") }
        if text.isEmpty { return "background_directed" }
        let pid = app.processIdentifier
        var target: AXUIElement?
        if command.elementID != nil {
            let resolved = try element(command, app: app)
            target = resolved
            try ComputerExecution.input()
            _ = AXUIElementSetAttributeValue(resolved, kAXFocusedAttribute as CFString, kCFBooleanTrue)
        }
        return try performDirectedOrForeground(command, app: app,
            directed: { try postUnicodeToPid(pid, text: text) },
            foreground: {
                if let target {
                    try ComputerExecution.input()
                    _ = AXUIElementSetAttributeValue(target, kAXFocusedAttribute as CFString, kCFBooleanTrue)
                }
                try self.postUnicodeGlobal(text: text)
            })
    }

    private func postUnicodeGlobal(text: String) throws {
        for units in unicodeChunks(text) {
            guard let down = markSynthetic(CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: true)),
                  let up = markSynthetic(CGEvent(keyboardEventSource: nil, virtualKey: 0, keyDown: false)) else {
                throw ComputerError.operationFailed("could not create text input event")
            }
            units.withUnsafeBufferPointer { buffer in
                guard let baseAddress = buffer.baseAddress else { return }
                down.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: baseAddress)
                up.keyboardSetUnicodeString(stringLength: buffer.count, unicodeString: baseAddress)
            }
            try ComputerExecution.input()
            down.post(tap: .cghidEventTap)
            up.post(tap: .cghidEventTap)
            usleep(4_000)
        }
    }

    private func selectText(_ command: ComputerCommand, app: NSRunningApplication) throws {
        let target = try element(command, app: app)
        guard let needle = command.text, !needle.isEmpty else { throw ComputerError.invalidArguments("text is required") }
        guard let fullText = axString(target, kAXValueAttribute as String) else {
            throw ComputerError.unsupported("element has no string AXValue")
        }
        var searchStart = fullText.startIndex
        if let prefix = command.prefix, let range = fullText.range(of: prefix) { searchStart = range.upperBound }
        let suffixBound = command.suffix.flatMap { fullText.range(of: $0, range: searchStart..<fullText.endIndex)?.lowerBound } ?? fullText.endIndex
        guard let range = fullText.range(of: needle, range: searchStart..<suffixBound) else {
            throw ComputerError.invalidArguments("text was not found in the target element")
        }
        let nsRange = NSRange(range, in: fullText)
        var cfRange = CFRange(location: nsRange.location, length: nsRange.length)
        guard let value = AXValueCreate(.cfRange, &cfRange) else {
            throw ComputerError.operationFailed("could not create selected text range")
        }
        try setAXValue(target, attribute: kAXSelectedTextRangeAttribute as String, value: value)
    }

    private func performSecondaryAction(_ command: ComputerCommand, app: NSRunningApplication) throws {
        let target = try element(command, app: app)
        guard let action = command.actionName, !action.isEmpty else { throw ComputerError.invalidArguments("action_name is required") }
        try performAXAction(target, action: action)
    }

    private func activateControl(_ command: ComputerCommand, app: NSRunningApplication, application: AXUIElement) throws {
        guard command.title?.isEmpty == false || command.description?.isEmpty == false else {
            throw ComputerError.invalidArguments("activate_control requires title or description")
        }
        _ = snapshotter.snapshot(application: application)
        guard let target = snapshotter.uniqueElement(role: command.role, title: command.title, description: command.description) else {
            throw ComputerError.operationFailed("activate_control selector did not match exactly one element; observe and refine role, title, or description")
        }
        try performAXAction(target, action: kAXPressAction as String)
    }

    private func verify(_ expectation: AXExpectation, application: AXUIElement, timeout: TimeInterval) throws -> String {
        guard AXIsProcessTrusted() else { return "unavailable" }
        let deadline = Date().addingTimeInterval(max(0.1, min(timeout, 30)))
        let reader = AXSnapshotter()
        repeat {
            try ComputerExecution.checkpoint()
            let tree = reader.snapshot(application: application)
            let outcome = expectation.evaluate(Array(reader.descriptors.values), truncated: tree.truncated)
            if outcome == "matched" || outcome == "ambiguous" { return outcome }
            if Date() >= deadline { return outcome == "unavailable" ? outcome : "timed_out" }
            RunLoop.current.run(until: Date().addingTimeInterval(0.05))
        } while true
    }

    private func waitForChange(_ command: ComputerCommand, app: NSRunningApplication, axApplication: AXUIElement) throws -> ComputerResult {
        try requireAccessibility()
        let timeout = max(0.1, min(command.timeout ?? 5, 30))
        let notificationObserved = try waitForAccessibilityChange(
            application: axApplication,
            processID: app.processIdentifier,
            timeout: timeout
        )
        let observation = try observe(command, app: app, axApplication: axApplication)
        var structured = observation.structured
        let changed = notificationObserved || !(structured["changes"] as? [String] ?? []).isEmpty
        structured["changed"] = changed
        structured["timed_out"] = !changed
        structured["timeout_seconds"] = timeout
        let outcome = changed
            ? "Accessibility changed before the timeout."
            : "No accessibility change was observed within \(timeout) seconds."
        return ComputerResult(
            text: outcome + "\n" + observation.text,
            screenshot: observation.screenshot,
            screenshotMIMEType: observation.screenshotMIMEType,
            structured: structured
        )
    }
}
