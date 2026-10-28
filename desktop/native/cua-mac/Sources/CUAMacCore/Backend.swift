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

private final class AppControlContext {
    let application: NSRunningApplication
    let epoch: String
    let snapshotter = AXSnapshotter()
    let directedFocus = DirectedFocus()
    var snapshotTicket: SnapshotTicket?
    var resourceRevision = ""
    var snapshotWindows: [CGWindowID: CGRect] = [:]
    var storedSnapshot = AXSnapshot(text: "", elements: [:])
    var snapshotRoot: AXUIElement?
    var snapshotProcessID: pid_t?
    var axRevision: UInt64 = 0
    var visualRevision: UInt64 = 0
    var lastScreenshotData: Data?
    var lastCaptureGeometry: CaptureGeometry?
    var lastWindowID: CGWindowID?
    var requiresForegroundCapture = false

    init(application: NSRunningApplication, epoch: String) {
        self.application = application
        self.epoch = epoch
    }
}

public final class MacComputerBackend: ComputerBackend {
    // Requests are serialized by the server. State survives interleaved calls to
    // other apps, but never crosses a process lifetime or control lease.
    private var contexts: [pid_t: AppControlContext] = [:]
    private var restoration: WindowRestorationJournal?
    // Acquired on first concealment and kept until shutdown: adding or removing
    // a display reconfigures every app's screens, so it should happen rarely.
    private var agentDisplay: AgentDisplayLease?

    public init() {
        restoration = try? WindowRestorationJournal()
        restoration?.recoverAbandoned(using: restoreWindow)
    }

    public func revokeControl(epoch: String) {
        for (pid, context) in contexts where context.epoch == epoch {
            context.directedFocus.revoke(epoch: epoch)
            contexts[pid] = nil
        }
    }

    public func shutdown() {
        for context in contexts.values { context.directedFocus.end() }
        contexts.removeAll()
        _ = try? restoration?.restore(using: restoreWindow)
        restoration?.finish()
        agentDisplay = nil
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
        for (pid, existing) in contexts where existing.application.isTerminated {
            existing.directedFocus.end()
            contexts[pid] = nil
        }
        let pid = app.processIdentifier
        if let existing = contexts[pid],
           existing.epoch != command.controlEpoch || existing.application.launchDate != app.launchDate {
            existing.directedFocus.end()
            contexts[pid] = nil
        }
        let context = contexts[pid] ?? AppControlContext(application: app, epoch: command.controlEpoch)
        contexts[pid] = context
        let appActionLock = try AppActionLock.acquire(processID: app.processIdentifier)
        defer { withExtendedLifetime(appActionLock) {} }
        context.resourceRevision = appActionLock.revision
        try ComputerExecution.checkpoint()
        let axApplication = AXUIElementCreateApplication(app.processIdentifier)
        context.directedFocus.reconcile(app: app, epoch: command.controlEpoch, foreground: command.foregroundPolicy != .avoid)
        defer { context.directedFocus.idle() }
        enableElectronAccessibility(axApplication)
        if command.action == .querySnapshot {
            return try querySnapshot(context: context, command, app: app)
        }
        if command.isMutation {
            try validateSnapshot(context: context, command, app: app)
            context.resourceRevision = try appActionLock.advanceRevision()
            context.snapshotTicket?.invalidate(resourceRevision: context.resourceRevision)
        }
        if command.foregroundPolicy == .require {
            try ForegroundInputLock.withLock {
                restoreConcealedWindows(app: app, application: axApplication)
                try activate(app)
            }
        } else if command.foregroundPolicy == .allow {
            // A foreground input action must not post global events at a concealed
            // window's agent display coordinates: the real pointer would vanish onto
            // a display the user cannot see. Bring the windows back first, so
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
        let intendedInteraction = interactionMetadata(context: context, command, app: app, application: axApplication)
        let activity = command.isMutation ? AXActivityMonitor(application: axApplication, processID: app.processIdentifier) : nil
        if command.isMutation, command.foregroundPolicy == .avoid,
           command.action != .concealApp, command.action != .revealApp {
            let frames = currentWindowFrames(processID: app.processIdentifier)
            let point: CGPoint? = if command.elementID != nil {
                try axFrame(element(context: context, command, app: app)).map { CGPoint(x: $0.midX, y: $0.midY) }
            } else if let x = command.x, let y = command.y {
                try inputPoint(context: context, x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
            } else { nil }
            let window = point.flatMap { point in frames.filter { $0.value.contains(point) }.min { $0.value.width * $0.value.height < $1.value.width * $1.value.height }?.key }
                ?? context.lastWindowID
                ?? frames.max { $0.value.width * $0.value.height < $1.value.width * $1.value.height }?.key
            if let window { try context.directedFocus.begin(app: app, windowID: window) }
        }
        let mechanism: String
        switch command.action {
        case .observe:
            let settling = try settle(AXActivityMonitor(application: axApplication, processID: app.processIdentifier), app: app, application: axApplication)
            return try observe(context: context, command, app: app, axApplication: axApplication, settling: settling)
        case .concealApp:
            return try concealApp(app: app, application: axApplication)
        case .revealApp:
            return try revealApp(app: app, application: axApplication)
        case .click:
            mechanism = try click(context: context, command, app: app)
        case .drag:
            mechanism = try drag(context: context, command, app: app)
        case .pressKey:
            mechanism = try pressKey(context: context, command, app: app)
        case .pressKeys:
            mechanism = try pressKeys(context: context, command, app: app)
        case .scroll:
            mechanism = try scroll(context: context, command, app: app)
        case .setValue:
            try setValue(context: context, command, app: app)
            mechanism = "background_ax"
        case .typeText:
            mechanism = try typeText(context: context, command, app: app)
        case .paste:
            mechanism = try paste(context: context, command, app: app)
        case .selectText:
            try selectText(context: context, command, app: app)
            mechanism = "background_ax"
        case .performAction:
            try performSecondaryAction(context: context, command, app: app)
            mechanism = "background_ax"
        case .waitForChange:
            return try waitForChange(context: context, command, app: app, axApplication: axApplication)
        case .waitFor:
            let status = try verify(command.expectation!, application: axApplication, timeout: command.timeout ?? 5)
            let observation = try observe(context: context, command, app: app, axApplication: axApplication)
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
        let settling = try activity.map { try settle($0, app: app, application: axApplication) } ?? "not_requested"
        let verification = try command.expectation.map { try verify($0, application: axApplication, timeout: command.timeout ?? 5) } ?? "not_requested"
        let frontmostAfter = NSWorkspace.shared.frontmostApplication?.processIdentifier
        let canonicalTarget = app.bundleIdentifier ?? app.bundleURL?.path ?? target
        var structured: [String: Any] = [
            "delivery": "delivered",
            "settling": settling,
            "verification": verification,
            "action": command.action.rawValue,
            "app": canonicalTarget,
            "display_name": app.localizedName ?? "Unknown",
            "process_id": Int(app.processIdentifier),
            "mechanism": mechanism,
            "foreground_changed": frontmostBefore != frontmostAfter,
        ]
        if let windowID = context.lastWindowID {
            structured["window_id"] = Int(windowID)
        }
        if let interaction = intendedInteraction {
            structured["interaction"] = interaction
        }
        if let mode = command.after {
            do {
                let next = try ComputerCommand(arguments: ["action": "observe", "app": canonicalTarget,
                    "mode": mode.rawValue, "control_epoch": command.controlEpoch, "disable_diff": command.disableDiff])
                let observation = try observe(context: context, next, app: app, axApplication: axApplication, settling: settling)
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
        context: AppControlContext,
        _ command: ComputerCommand,
        app: NSRunningApplication,
        application: AXUIElement
    ) -> [String: Any]? {
        let kind: String
        switch command.action {
        case .click, .performAction, .activateControl: kind = "click"
        case .drag: kind = "drag"
        case .scroll: kind = "scroll"
        case .setValue, .typeText, .paste, .selectText, .pressKey, .pressKeys: kind = "type"
        default: return nil
        }
        let frame: CGRect? = {
            if command.elementID != nil, let element = try? element(context: context, command, app: app) {
                return axFrame(element)
            }
            if command.action == .activateControl,
               let target = context.snapshotter.uniqueElement(role: command.role, title: command.title, description: command.description) {
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
                return try? inputPoint(context: context, x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
            }
            return primaryWindowFrame(application).map { CGPoint(x: $0.midX, y: $0.midY) }
        }()
        guard let point, let normalized = normalizedInteractionPoint(context: context, point, app: app) else { return nil }
        var interaction: [String: Any] = ["kind": kind, "x": normalized.x, "y": normalized.y]
        if kind == "drag", let x = command.toX, let y = command.toY {
            let destinationArguments: [String: Any] = [
                "action": "click", "app": command.app ?? "", "x": x, "y": y,
                "coordinate_space": command.coordinateSpace ?? "normalized",
            ]
            if let destinationCommand = try? ComputerCommand(arguments: destinationArguments),
               let destinationX = destinationCommand.x,
               let destinationY = destinationCommand.y,
               let destination = try? inputPoint(context: context, x: destinationX, y: destinationY, coordinateSpace: destinationCommand.coordinateSpace, app: app),
               let to = normalizedInteractionPoint(context: context, destination, app: app) {
                interaction["to_x"] = to.x
                interaction["to_y"] = to.y
            }
        }
        if let direction = command.direction { interaction["direction"] = direction }
        return interaction
    }

    private func normalizedInteractionPoint(context: AppControlContext, _ point: CGPoint, app: NSRunningApplication) -> CGPoint? {
        let frame = context.lastCaptureGeometry?.windowFrame
            ?? primaryWindowFrame(AXUIElementCreateApplication(app.processIdentifier))
        guard let frame, frame.width > 0, frame.height > 0 else { return nil }
        return CGPoint(
            x: max(0, min(1, (point.x - frame.minX) / frame.width)),
            y: max(0, min(1, (point.y - frame.minY) / frame.height))
        )
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

    private func observe(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication, axApplication: AXUIElement, settling: String? = nil) throws -> ComputerResult {
        var root = command.scope == .window ? preferredAXWindow(axApplication) ?? axApplication : axApplication
        if let rootID = command.rootElementID {
            try validateSnapshot(context: context, command, app: app)
            guard context.snapshotter.isCurrent(id: rootID), let element = context.snapshotter.element(id: rootID) else { throw ComputerError.staleSnapshot("subtree changed") }
            root = element
        }
        let rootWindow = axString(root, kAXRoleAttribute as String) == kAXWindowRole as String
            ? root : (axValue(root, kAXWindowAttribute as String) as! AXUIElement?) ?? preferredAXWindow(axApplication)
        let previousTicket = context.snapshotTicket
        let previousSnapshot = context.storedSnapshot
        let sameOwner = previousTicket?.processID == app.processIdentifier &&
            previousTicket?.launchTime == (app.launchDate?.timeIntervalSince1970 ?? 0) &&
            previousTicket?.controlEpoch == command.controlEpoch
        let sameRoot = sameOwner && context.snapshotRoot.map { CFEqual($0, root) } == true
        if !sameOwner { context.snapshotter.clear() }
        context.snapshotTicket = nil
        context.snapshotRoot = nil
        context.lastCaptureGeometry = nil
        context.lastWindowID = nil
        let accessibility = AXIsProcessTrusted() && command.observationMode != .vision
        let snapshot: AXSnapshot
        if accessibility {
            snapshot = context.snapshotter.snapshot(application: root)
        } else {
            context.snapshotter.clear()
            snapshot = AXSnapshot(
                text: command.observationMode == .vision ? "Vision observation; AX tree omitted." : "Accessibility permission is unavailable.",
                elements: [:]
            )
        }
        context.snapshotProcessID = app.processIdentifier
        let axChanged = accessibility && (!sameRoot || previousSnapshot.nodes != snapshot.nodes || previousSnapshot.truncated != snapshot.truncated)
        if axChanged {
            context.axRevision += 1
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
        structured["settling"] = settling
        var screenshot: Data?
        if command.observationMode != .ax {
        do {
            if command.scope == .window {
                // Legacy single-window path: unchanged so scope=window (the default)
                // stays byte-for-byte identical to the pre-composite behaviour.
                let capture = try captureWindowWithForegroundFallback(context: context, command, app: app, preferredFrame: rootWindow.flatMap(axFrame))
                screenshot = capture.data
                if context.lastScreenshotData != capture.data {
                    context.visualRevision += 1
                    context.lastScreenshotData = capture.data
                }
                context.lastCaptureGeometry = capture.geometry
                context.lastWindowID = capture.windowID
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
                if context.lastScreenshotData != capture.data {
                    context.visualRevision += 1
                    context.lastScreenshotData = capture.data
                }
                // observe defines the coordinate space for the model's subsequent
                // clicks. For a composite the union rect replaces the single-window
                // frame, so screenshot/normalized/screen coordinates now resolve
                // against the whole composited image (CaptureGeometry's affine map
                // only needs the union frame and the image size).
                context.lastCaptureGeometry = capture.geometry
                context.lastWindowID = capture.windowID
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
        if let geometry = context.lastCaptureGeometry, screenshot != nil {
            header += " Screenshot=\(geometry.imageWidth)×\(geometry.imageHeight) pixels maps to window_frame=(\(Int(geometry.windowFrame.origin.x)),\(Int(geometry.windowFrame.origin.y)),\(Int(geometry.windowFrame.width)),\(Int(geometry.windowFrame.height))). Prefer coordinate_space=\"normalized\" (0-1000) for visual targets so provider image resizing does not affect clicks; use coordinate_space=\"screenshot\" only for original image pixels."
        }
        let ticket = SnapshotTicket(processID: app.processIdentifier, launchTime: app.launchDate?.timeIntervalSince1970 ?? 0, controlEpoch: command.controlEpoch, resourceRevision: context.resourceRevision)
        context.snapshotTicket = ticket
        context.snapshotWindows = currentWindowFrames(processID: app.processIdentifier)
        if context.lastWindowID == nil, let frame = rootWindow.flatMap(axFrame) {
            let matches = context.snapshotWindows.filter { $0.value == frame }.map(\.key)
            if matches.count == 1 {
                // AX-only observations must retain the same window identity used
                // by screenshot observations for the next directed keyboard input.
                context.lastWindowID = matches[0]
                structured["window_id"] = Int(matches[0])
            }
        }
        context.storedSnapshot = snapshot
        context.snapshotRoot = accessibility ? root : nil
        let page = SnapshotPage(text: snapshot.text, query: nil, offset: command.offset, limit: command.limit)
        structured["snapshot_id"] = ticket.id
        structured["mode"] = command.observationMode.rawValue
        structured["ax_revision"] = context.axRevision
        structured["visual_revision"] = context.visualRevision
        structured["full_snapshot"] = true
        structured["ax_changed"] = axChanged
        structured["ax_nodes"] = page.lineIndices.compactMap { $0 < snapshot.nodes.count ? snapshot.nodes[$0].dictionary : nil }
        if !command.disableDiff, accessibility, sameRoot, let previousTicket {
            let difference = snapshot.difference(from: previousSnapshot, baseID: previousTicket.id)
            structured["ax_diff"] = difference.structured
            structured["changes"] = difference.summary
        }
        let focused = axValue(axApplication, kAXFocusedUIElementAttribute as String)
        let focusedNode = focused.flatMap { focused in
            snapshot.nodes.first { node in snapshot.elements[node.id].map { CFEqual($0, focused) } == true }
        } ?? snapshot.nodes.first { $0.attributes["focused"] as? Bool == true }
        if let node = focusedNode {
            structured["focus"] = node.dictionary
            header += " Focused element=[\(node.id)]."
            if let range = node.attributes["selection_range"] as? [String: Any] {
                header += " Selection UTF-16 location=\(range["location"]!), length=\(range["length"]!)."
            }
        }
        structured["selected_element_ids"] = snapshot.nodes.filter { $0.attributes["selected"] as? Bool == true }.map(\.id)
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

    private func captureWindowWithForegroundFallback(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication, preferredFrame: CGRect? = nil) throws -> WindowCapture {
        guard #available(macOS 14.0, *) else {
            throw ComputerError.unsupported("window screenshots require macOS 14 or newer")
        }
        // Capture without activating the app. Current macOS releases may proxy
        // this Core Graphics call through the system capture service; the PiP's
        // separate executable path keeps that proxy connection from replacing the
        // live stream's client identity.
        if let background = try? captureForegroundWindowPNG(processID: app.processIdentifier, preferredWindowFrame: preferredFrame ?? focusedWindowFrame(processID: app.processIdentifier)) {
            return background
        }
        // Test-only isolation switch: skip the explicit SCScreenshotManager path.
        // The Core Graphics path above may still be proxied through the system
        // capture service, so this is not a guarantee that observe avoids it.
        if ProcessInfo.processInfo.environment["WUU_CUA_NO_SCK_OBSERVE"] == "1" {
            throw ComputerError.operationFailed("observe capture unavailable (no-SCK isolation mode)")
        }
        if context.requiresForegroundCapture {
            return try withForegroundInput(context: context, command, app: app) {
                try captureForegroundWindowPNG(processID: app.processIdentifier, preferredWindowFrame: preferredFrame ?? focusedWindowFrame(processID: app.processIdentifier))
            }
        }
        do {
            return try captureWindowPNG(
                processID: app.processIdentifier,
                preferredWindowFrame: preferredFrame ?? focusedWindowFrame(processID: app.processIdentifier)
            )
        } catch let backgroundError {
            context.requiresForegroundCapture = true
            do {
                return try withForegroundInput(context: context, command, app: app) {
                    try captureForegroundWindowPNG(processID: app.processIdentifier, preferredWindowFrame: preferredFrame ?? focusedWindowFrame(processID: app.processIdentifier))
                }
            } catch let foregroundError {
                throw ComputerError.operationFailed(
                    "window screenshot failed in background (\(backgroundError.localizedDescription)) and after foreground retry (\(foregroundError.localizedDescription))"
                )
            }
        }
    }

    private func preferredAXWindow(_ application: AXUIElement) -> AXUIElement? {
        if let focused = axValue(application, kAXFocusedWindowAttribute as String), CFGetTypeID(focused) == AXUIElementGetTypeID() {
            return (focused as! AXUIElement)
        }
        return appWindows(application).max {
            let left = axFrame($0) ?? .zero
            let right = axFrame($1) ?? .zero
            return left.width * left.height < right.width * right.height
        }
    }

    private func focusedWindowFrame(processID: pid_t) -> CGRect? {
        preferredAXWindow(AXUIElementCreateApplication(processID)).flatMap(axFrame)
    }

    private func element(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> AXUIElement {
        guard context.snapshotProcessID == app.processIdentifier else {
            throw ComputerError.invalidArguments("observe this app before using an element_id")
        }
        guard let id = command.elementID else {
            throw ComputerError.invalidArguments("element_id is required")
        }
        guard let element = context.snapshotter.element(id: id), let descriptor = context.snapshotter.descriptor(id: id) else {
            throw ComputerError.elementNotFound(id)
        }
        guard context.snapshotter.isCurrent(id: id) else { throw ComputerError.staleSnapshot("element \(id) changed") }
        _ = descriptor
        return element
    }

    private func settle(_ activity: AXActivityMonitor, app: NSRunningApplication, application: AXUIElement) throws -> String {
        let reader = AXSnapshotter()
        let accessibility = AXIsProcessTrusted()
        let capture = CGPreflightScreenCaptureAccess()
        return try activity.settle {
            var samples: [String] = []
            if accessibility {
                samples.append(reader.snapshot(application: preferredAXWindow(application) ?? application).text)
            }
            if capture, let pixels = windowVisualFingerprint(processID: app.processIdentifier) {
                samples.append(String(pixels))
            }
            return samples.isEmpty ? nil : samples.joined(separator: "\n")
        }
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
        context: AppControlContext,
        _ command: ComputerCommand,
        app: NSRunningApplication,
        directed: () throws -> Void,
        foreground: () throws -> Void
    ) throws -> String {
        if command.foregroundPolicy == .avoid {
            try directed()
            return "background_directed"
        }
        try withForegroundInput(context: context, command, app: app) { try foreground() }
        return "foreground_native"
    }

    private func currentWindowFrames(processID: pid_t) -> [CGWindowID: CGRect] {
        windowFrames(processID: processID)
    }

    private func validateSnapshot(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws {
        guard let ticket = context.snapshotTicket else { throw ComputerError.staleSnapshot("observe the target first") }
        try ticket.validate(reference: command.snapshotID, processID: app.processIdentifier,
            launchTime: app.launchDate?.timeIntervalSince1970 ?? 0, epoch: command.controlEpoch,
            requiresReference: command.referencesSnapshot || command.action == .querySnapshot, resourceRevision: context.resourceRevision)
        if command.referencesSnapshot {
            guard currentWindowFrames(processID: app.processIdentifier) == context.snapshotWindows else {
                throw ComputerError.staleSnapshot("target window geometry or identity changed")
            }
        }
    }

    private func querySnapshot(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> ComputerResult {
        try validateSnapshot(context: context, command, app: app)
        let page = SnapshotPage(text: context.storedSnapshot.text, query: command.query, offset: command.offset, limit: command.limit)
        let id = context.snapshotTicket!.id
        var structured: [String: Any] = ["snapshot_id": id, "total_lines": page.total, "process_id": Int(app.processIdentifier)]
        structured["next_offset"] = page.nextOffset
        structured["ax_truncated"] = context.storedSnapshot.truncated
        structured["ax_nodes"] = page.lineIndices.compactMap { $0 < context.storedSnapshot.nodes.count ? context.storedSnapshot.nodes[$0].dictionary : nil }
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

    private func withForegroundInput<T>(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication, body: () throws -> T) throws -> T {
        guard command.foregroundPolicy != .avoid else {
            throw ComputerError.requiresForeground(
                "\(command.action.rawValue) needs native mouse or keyboard input for \(app.localizedName ?? "the target app"); retry with foreground_policy=\"allow\" only when foreground control is acceptable"
            )
        }
        return try ForegroundInputLock.withLock {
            restoreConcealedWindows(app: app, application: AXUIElementCreateApplication(app.processIdentifier))
            try activate(app)
            if command.referencesSnapshot, currentWindowFrames(processID: app.processIdentifier) != context.snapshotWindows {
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

    private func appWindows(_ application: AXUIElement) -> [AXUIElement] {
        axElements(application, attribute: kAXWindowsAttribute as String)
    }

    private func setWindowOrigin(_ window: AXUIElement, to origin: CGPoint) -> Bool {
        var point = origin
        guard let value = AXValueCreate(.cgPoint, &point) else { return false }
        return AXUIElementSetAttributeValue(window, kAXPositionAttribute as CFString, value) == .success
    }

    // A window parked on the invisible agent display keeps rendering, its
    // Accessibility tree, and its capture surface, so background control and the
    // live PiP keep working while no physical screen shows it. The Dock icon,
    // Cmd-Tab entry, and Mission Control presence follow the target app's own
    // activation policy, which only that process can change.
    private func concealApp(app: NSRunningApplication, application: AXUIElement) throws -> ComputerResult {
        try requireAccessibility()
        let windows = appWindows(application)
        guard !windows.isEmpty else {
            throw ComputerError.operationFailed("no window to conceal yet; observe the app or wait for its window before concealing")
        }
        guard let restoration else { throw ComputerError.operationFailed("window restoration journal is unavailable") }
        // A daemon crash or display removal invalidates even a held lease.
        // Reacquire before moving another window so recovery needs no helper restart.
        if agentDisplay?.isValid == false { agentDisplay = nil }
        let display = try agentDisplay ?? AgentDisplayLease.acquire()
        agentDisplay = display
        let frames = currentWindowFrames(processID: app.processIdentifier)
        var concealed = 0
        var unsupported = 0
        var moved: [CGWindowID] = []
        for (index, window) in windows.enumerated() {
            try ComputerExecution.checkpoint()
            guard let frame = axFrame(window) else { unsupported += 1; continue }
            let ids = frames.filter { $0.value == frame }.map(\.key)
            guard ids.count == 1 else { unsupported += 1; continue }
            let parked = display.parkingOrigin(index: index)
            if let previous = restoration.records.first(where: { $0.processID == app.processIdentifier && $0.windowID == ids[0] }) {
                if previous.parked == frame, frames[ids[0]].map(display.holds) == true { concealed += 1; continue }
                try restoration.discard(processID: app.processIdentifier, windowID: ids[0])
            }
            try restoration.record(WindowRestorationRecord(processID: app.processIdentifier,
                launchTime: app.launchDate?.timeIntervalSince1970 ?? 0, windowID: ids[0],
                identifier: axString(window, kAXIdentifierAttribute as String) ?? "", original: frame,
                parked: CGRect(origin: parked, size: frame.size)))
            try ComputerExecution.input()
            guard setWindowOrigin(window, to: parked) else { unsupported += 1; continue }
            guard let actual = axFrame(window) else { unsupported += 1; continue }
            try restoration.updateParked(processID: app.processIdentifier, windowID: ids[0], frame: actual)
            moved.append(ids[0])
        }
        // AX reports the requested origin at once, but the WindowServer animates
        // the move. Report success only once the real geometry is on the display.
        let deadline = Date().addingTimeInterval(1.5)
        var settled = currentWindowFrames(processID: app.processIdentifier)
        while Date() < deadline, !moved.allSatisfy({ settled[$0].map(display.holds) == true }) {
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.03))
            settled = currentWindowFrames(processID: app.processIdentifier)
        }
        for id in moved {
            if settled[id].map(display.holds) == true { concealed += 1 } else { unsupported += 1 }
        }
        let canonicalTarget = app.bundleIdentifier ?? app.bundleURL?.path ?? String(app.processIdentifier)
        var text = "Concealed \(concealed) window(s) for app=\"\(canonicalTarget)\" on the invisible agent display; Accessibility control and live capture continue."
        if unsupported > 0 {
            text += " \(unsupported) window(s) refused the move and stay visible."
        }
        return ComputerResult(text: text, structured: [
            "action": "conceal_app",
            "app": canonicalTarget,
            "process_id": Int(app.processIdentifier),
            "status": unsupported == 0 ? "concealed" : "partial",
            "mechanism": "background_ax",
            "foreground_changed": false,
            "concealed_windows": concealed,
            "unsupported_windows": unsupported,
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
        (try? restoration?.restore(processID: app.processIdentifier, using: restoreWindow)) ?? 0
    }

    private func click(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        var point: CGPoint
        let heldKey = try command.key.map { key in
            let chord = try KeyChord.parse(key)
            return KeyChord(keyCode: chord.keyCode, modifiers: chord.modifiers.union(command.modifiers))
        }
        let modifiers = command.modifiers.union(heldKey?.modifiers ?? [])
        let semanticClick = heldKey == nil && modifiers.isEmpty && command.duration == 0
            && (command.clickCount ?? 1) == 1 && (command.mouseButton ?? "left") == "left"
        if command.elementID != nil {
            let target = try element(context: context, command, app: app)
            let hasPress = axActions(target).contains(kAXPressAction as String)
            let frame = axFrame(target)
            // Under the default policy a semantic AXPress is the most reliable target
            // for buttons and menu items and needs neither the pointer nor focus.
            // Under allow/require the model has decided AXPress was not enough, so
            // escalate to a real click at the element's frame (the doc's "AX silently
            // swallowed, fall to a physical click" path); AXPress remains the fallback
            // for frameless elements such as some menu items.
            if command.foregroundPolicy == .avoid, hasPress, semanticClick {
                try performAXAction(target, action: kAXPressAction as String)
                return "background_ax"
            }
            if let frame {
                point = CGPoint(x: frame.midX, y: frame.midY)
            } else if hasPress, semanticClick {
                try performAXAction(target, action: kAXPressAction as String)
                return "background_ax"
            } else {
                throw ComputerError.invalidArguments("element exposes no AXPress action and no frame to click")
            }
        } else {
            guard let x = command.x, let y = command.y else {
                throw ComputerError.invalidArguments("click requires element_id or x and y")
            }
            point = try inputPoint(context: context, x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
        }
        let pid = app.processIdentifier
        return try performDirectedOrForeground(context: context, command, app: app,
            directed: {
                try postClickToPid(pid, point: point, button: command.mouseButton, count: command.clickCount ?? 1, modifiers: modifiers, duration: command.duration, heldKey: heldKey)
            }, foreground: {
                try withHeldKey(heldKey, post: { $0.post(tap: .cghidEventTap) }) {
                    try postClicks(point: point, button: command.mouseButton, count: command.clickCount ?? 1, modifiers: modifiers, duration: command.duration) {
                        $0.post(tap: .cghidEventTap)
                    }
                }
            })
    }

    private func drag(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let x = command.x, let y = command.y, let toX = command.toX, let toY = command.toY else {
            throw ComputerError.invalidArguments("drag requires from_x, from_y, to_x, and to_y")
        }
        let start = try inputPoint(context: context, x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
        let end = try inputPoint(context: context, x: toX, y: toY, coordinateSpace: command.coordinateSpace, app: app)
        let pid = app.processIdentifier
        return try performDirectedOrForeground(context: context, command, app: app,
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
        var lastPoint = start
        defer { up.location = lastPoint; up.post(tap: .cghidEventTap) }
        for step in 1...12 {
            try ComputerExecution.input()
            let progress = Double(step) / 12
            let point = CGPoint(
                x: start.x + (end.x - start.x) * progress,
                y: start.y + (end.y - start.y) * progress
            )
            if let dragged = markSynthetic(CGEvent(mouseEventSource: source, mouseType: .leftMouseDragged, mouseCursorPosition: point, mouseButton: .left)) {
                dragged.post(tap: .cghidEventTap)
                lastPoint = point
            }
            usleep(8_000)
        }
    }

    private func inputPoint(context: AppControlContext, x: Double, y: Double, coordinateSpace: String?, app: NSRunningApplication) throws -> CGPoint {
        switch coordinateSpace ?? "screenshot" {
        case "screen":
            let point = CGPoint(x: x, y: y)
            let frame = context.lastCaptureGeometry?.windowFrame
                ?? primaryWindowFrame(AXUIElementCreateApplication(app.processIdentifier))
            guard let frame, frame.contains(point) else {
                throw ComputerError.invalidArguments("screen coordinates must be inside the target window frame")
            }
            return try ownedInputPoint(point, app: app)
        case "normalized":
            guard let geometry = context.lastCaptureGeometry else {
                throw ComputerError.invalidArguments("observe the app before using normalized coordinates")
            }
            return try ownedInputPoint(geometry.screenPoint(normalizedX: x, normalizedY: y), app: app)
        case "screenshot":
            guard let geometry = context.lastCaptureGeometry else {
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

    private func pressKey(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let key = command.key else { throw ComputerError.invalidArguments("key is required") }
        let chord = try KeyChord.parse(key)
        let pid = app.processIdentifier
        return try performDirectedOrForeground(context: context, command, app: app,
            directed: { try postKeyChordToPid(pid, chord: chord, duration: command.duration) },
            foreground: { try self.postKey(key, duration: command.duration) })
    }

    private func pressKeys(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let keys = command.keys, !keys.isEmpty else { throw ComputerError.invalidArguments("keys is required") }
        let chords = try keys.map { try KeyChord.parse($0) }
        let pid = app.processIdentifier
        return try performDirectedOrForeground(context: context, command, app: app,
            directed: {
                for chord in chords {
                    try postKeyChordToPid(pid, chord: chord, duration: command.duration)
                    usleep(20_000)
                }
            },
            foreground: {
                for key in keys {
                    try self.postKey(key, duration: command.duration)
                    usleep(20_000)
                }
            })
    }

    private func postKey(_ key: String, duration: TimeInterval = 0) throws {
        try withHeldKey(KeyChord.parse(key), duration: duration, post: { $0.post(tap: .cghidEventTap) }) {}
    }

    private func scroll(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        let pages = max(1, min(command.pages ?? 1, 20))
        let direction = command.direction?.lowercased() ?? "down"
        let vertical: Int32 = direction == "up" ? 3 : direction == "down" ? -3 : 0
        let horizontal: Int32 = direction == "left" ? 3 : direction == "right" ? -3 : 0
        // Scroll must land inside the target: an element frame when given, otherwise
        // the app's primary window center. Without a point, directed scroll hit-tests
        // at the global origin and is dropped.
        let point: CGPoint? = if command.elementID != nil {
            try command.elementID.flatMap { _ in axFrame(try element(context: context, command, app: app)) }
                .map { CGPoint(x: $0.midX, y: $0.midY) }
        } else if let x = command.x, let y = command.y {
            try inputPoint(context: context, x: x, y: y, coordinateSpace: command.coordinateSpace, app: app)
        } else {
            primaryWindowFrame(AXUIElementCreateApplication(app.processIdentifier))
                .map { CGPoint(x: $0.midX, y: $0.midY) }
        }
        let pid = app.processIdentifier
        return try performDirectedOrForeground(context: context, command, app: app,
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

    private func setValue(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws {
        let target = try element(context: context, command, app: app)
        let value = command.value ?? command.text
        guard let value else { throw ComputerError.invalidArguments("value is required") }
        try setAXValue(target, attribute: kAXValueAttribute as String, value: value as CFString)
    }

    private func typeText(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let text = command.text else { throw ComputerError.invalidArguments("text is required") }
        if text.isEmpty { return "background_directed" }
        let pid = app.processIdentifier
        var target: AXUIElement?
        if command.elementID != nil {
            let resolved = try element(context: context, command, app: app)
            target = resolved
            try setAXValue(resolved, attribute: kAXFocusedAttribute as String, value: kCFBooleanTrue)
        }
        return try performDirectedOrForeground(context: context, command, app: app,
            directed: { try postUnicode(text) { $0.postToPid(pid) } },
            foreground: {
                if let target {
                    try setAXValue(target, attribute: kAXFocusedAttribute as String, value: kCFBooleanTrue)
                }
                try postUnicode(text) { $0.post(tap: .cghidEventTap) }
            })
    }

    private func paste(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws -> String {
        guard let text = command.text else { throw ComputerError.invalidArguments("text is required") }
        let target = command.elementID == nil ? nil : try element(context: context, command, app: app)
        let application = AXUIElementCreateApplication(app.processIdentifier)
        return try withPasteContent(text, format: command.format) {
            let activity = AXActivityMonitor(application: application, processID: app.processIdentifier)
            let focus = {
                if let target { try setAXValue(target, attribute: kAXFocusedAttribute as String, value: kCFBooleanTrue) }
            }
            let dispatch = Result {
                try performDirectedOrForeground(context: context, command, app: app,
                    directed: {
                        try focus()
                        try postKeyChordToPid(app.processIdentifier, chord: KeyChord.parse("command+v"))
                    }, foreground: {
                        try focus()
                        try self.postKey("command+v")
                    })
            }
            // A cancelled request can still have queued Cmd-V. Keep the paste
            // content available during delivery even when the wait is interrupted;
            // restoring immediately could paste the user's previous clipboard.
            let clipboardReadDeadline = Date().addingTimeInterval(0.25)
            defer { RunLoop.current.run(until: clipboardReadDeadline) }
            let mechanism = try dispatch.get()
            try holdInput(for: max(0, clipboardReadDeadline.timeIntervalSinceNow))
            _ = try settle(activity, app: app, application: application)
            return mechanism
        }
    }

    private func selectText(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws {
        let target = try element(context: context, command, app: app)
        guard let needle = command.text, !needle.isEmpty else { throw ComputerError.invalidArguments("text is required") }
        guard let fullText = axString(target, kAXValueAttribute as String) else {
            throw ComputerError.unsupported("element has no string AXValue")
        }
        var matches: [Range<String.Index>] = []
        var start = fullText.startIndex
        while start < fullText.endIndex, let range = fullText.range(of: needle, range: start..<fullText.endIndex) {
            if command.prefix.map({ fullText[..<range.lowerBound].hasSuffix($0) }) ?? true,
               command.suffix.map({ fullText[range.upperBound...].hasPrefix($0) }) ?? true {
                matches.append(range)
                if matches.count > 1 { break }
            }
            start = fullText.index(after: range.lowerBound)
        }
        guard matches.count == 1 else {
            throw ComputerError.invalidArguments(matches.isEmpty
                ? "text and its prefix/suffix context were not found in the target element"
                : "text is ambiguous; supply prefix and/or suffix to select one occurrence")
        }
        let nsRange = NSRange(matches[0], in: fullText)
        var cfRange: CFRange
        switch command.selectionType {
        case .text: cfRange = CFRange(location: nsRange.location, length: nsRange.length)
        case .cursorBefore: cfRange = CFRange(location: nsRange.location, length: 0)
        case .cursorAfter: cfRange = CFRange(location: nsRange.location + nsRange.length, length: 0)
        }
        guard let value = AXValueCreate(.cfRange, &cfRange) else {
            throw ComputerError.operationFailed("could not create selected text range")
        }
        try setAXValue(target, attribute: kAXFocusedAttribute as String, value: kCFBooleanTrue)
        try setAXValue(target, attribute: kAXSelectedTextRangeAttribute as String, value: value)
    }

    private func performSecondaryAction(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication) throws {
        let target = try element(context: context, command, app: app)
        guard let action = command.actionName, !action.isEmpty else { throw ComputerError.invalidArguments("action_name is required") }
        try performAXAction(target, action: action)
    }

    private func activateControl(_ command: ComputerCommand, app: NSRunningApplication, application: AXUIElement) throws {
        guard command.title?.isEmpty == false || command.description?.isEmpty == false else {
            throw ComputerError.invalidArguments("activate_control requires title or description")
        }
        let reader = AXSnapshotter()
        _ = reader.snapshot(application: application)
        guard let target = reader.uniqueElement(role: command.role, title: command.title, description: command.description) else {
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

    private func waitForChange(context: AppControlContext, _ command: ComputerCommand, app: NSRunningApplication, axApplication: AXUIElement) throws -> ComputerResult {
        try requireAccessibility()
        let timeout = max(0.1, min(command.timeout ?? 5, 30))
        let notificationObserved = try waitForAccessibilityChange(
            application: axApplication,
            processID: app.processIdentifier,
            timeout: timeout
        )
        if notificationObserved {
            _ = try settle(AXActivityMonitor(application: axApplication, processID: app.processIdentifier), app: app, application: axApplication)
        }
        let observation = try observe(context: context, command, app: app, axApplication: axApplication)
        var structured = observation.structured
        let changed = notificationObserved || (structured["ax_changed"] as? Bool ?? false)
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
