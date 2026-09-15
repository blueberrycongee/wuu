import AppKit
import ApplicationServices
import AVFoundation
@preconcurrency import CoreMedia
import Darwin
import Foundation
import QuartzCore
@preconcurrency import ScreenCaptureKit

public struct NativePiPConfiguration: Sendable {
    public let activityID: String
    public let target: String
    public let frame: CGRect
    public let processID: pid_t?
    public let windowID: CGWindowID?
    public let parentProcessID: pid_t?

    public init(activityID: String, target: String, frame: CGRect, processID: pid_t? = nil, windowID: CGWindowID? = nil, parentProcessID: pid_t? = nil) {
        self.activityID = activityID
        self.target = target
        self.frame = frame
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

private final class CapturedFrame: @unchecked Sendable {
    let sampleBuffer: CMSampleBuffer

    init(_ sampleBuffer: CMSampleBuffer) {
        self.sampleBuffer = sampleBuffer
    }
}

@MainActor
private final class NativePiPLiveView: NSView {
    private let displayLayer = AVSampleBufferDisplayLayer()
    private let pointerLayer = CAShapeLayer()
    private let rippleLayer = CAShapeLayer()
    private var videoSize = CGSize(width: 16, height: 9)
    private var pointerPosition = CGPoint(x: 0.5, y: 0.5)

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
        layer?.backgroundColor = NSColor.clear.cgColor

        displayLayer.videoGravity = .resizeAspect
        displayLayer.backgroundColor = NSColor.clear.cgColor
        layer?.addSublayer(displayLayer)

        let pointerPath = CGMutablePath()
        // Core Animation uses a bottom-left origin here. Define the cursor in
        // that coordinate space so its tip points toward the upper-left.
        pointerPath.move(to: CGPoint(x: 2, y: 28))
        pointerPath.addLine(to: CGPoint(x: 2, y: 5))
        pointerPath.addLine(to: CGPoint(x: 8, y: 11))
        pointerPath.addLine(to: CGPoint(x: 12, y: 2))
        pointerPath.addLine(to: CGPoint(x: 17, y: 4))
        pointerPath.addLine(to: CGPoint(x: 13, y: 13))
        pointerPath.addLine(to: CGPoint(x: 21, y: 13))
        pointerPath.closeSubpath()
        pointerLayer.opacity = 0
        pointerLayer.path = pointerPath
        pointerLayer.fillColor = NSColor.white.cgColor
        pointerLayer.strokeColor = NSColor.systemOrange.cgColor
        pointerLayer.lineWidth = 2
        pointerLayer.lineJoin = .round
        pointerLayer.bounds = CGRect(x: 0, y: 0, width: 24, height: 30)
        pointerLayer.anchorPoint = CGPoint(x: 2.0 / 24.0, y: 28.0 / 30.0)
        layer?.addSublayer(pointerLayer)

        rippleLayer.path = CGPath(ellipseIn: CGRect(x: 0, y: 0, width: 18, height: 18), transform: nil)
        rippleLayer.fillColor = NSColor.clear.cgColor
        rippleLayer.strokeColor = NSColor.systemOrange.withAlphaComponent(0.9).cgColor
        rippleLayer.lineWidth = 2
        rippleLayer.bounds = CGRect(x: 0, y: 0, width: 18, height: 18)
        rippleLayer.opacity = 0
        layer?.addSublayer(rippleLayer)
    }

    required init?(coder: NSCoder) { nil }

    override func layout() {
        super.layout()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        displayLayer.frame = bounds
        placePointer()
        CATransaction.commit()
    }

    func enqueue(_ sampleBuffer: CMSampleBuffer) {
        // AVSampleBufferDisplayLayer reads DisplayImmediately from the per-sample
        // attachments array, never from buffer-level attachments. Without a
        // control timebase, a frame only presents when this flag is set on the
        // sample itself; writing it with CMSetAttachment lands it in the wrong
        // bucket, so the layer holds every frame in its queue and stays black
        // while the overlay pointer (a separate CAShapeLayer) still renders.
        if let attachments = CMSampleBufferGetSampleAttachmentsArray(sampleBuffer, createIfNecessary: true),
           CFArrayGetCount(attachments) > 0 {
            let sampleAttachments = unsafeBitCast(CFArrayGetValueAtIndex(attachments, 0), to: CFMutableDictionary.self)
            CFDictionarySetValue(
                sampleAttachments,
                Unmanaged.passUnretained(kCMSampleAttachmentKey_DisplayImmediately).toOpaque(),
                Unmanaged.passUnretained(kCFBooleanTrue).toOpaque()
            )
        }
        if displayLayer.status == .failed { displayLayer.flush() }
        displayLayer.enqueue(sampleBuffer)
        if let imageBuffer = sampleBuffer.imageBuffer {
            let size = CGSize(width: CVPixelBufferGetWidth(imageBuffer), height: CVPixelBufferGetHeight(imageBuffer))
            setVideoSize(size)
        }
    }

    func setVideoSize(_ size: CGSize) {
        guard size.width > 0, size.height > 0 else { return }
        videoSize = size
        needsLayout = true
    }

    func hideInteraction() {
        pointerLayer.opacity = 0
        pointerLayer.removeAllAnimations()
        rippleLayer.removeAllAnimations()
    }

    func animateInteraction(_ payload: [String: Any]) {
        guard let kind = payload["kind"] as? String,
              let x = payload["x"] as? Double,
              let y = payload["y"] as? Double else { return }
        let destination = CGPoint(
            x: min(1, max(0, payload["to_x"] as? Double ?? x)),
            y: min(1, max(0, payload["to_y"] as? Double ?? y))
        )
        pointerLayer.opacity = 1
        let from = pointerPoint(pointerPosition)
        let to = pointerPoint(destination)
        pointerPosition = destination

        let animation = CAKeyframeAnimation(keyPath: "position")
        animation.values = [from, CGPoint(x: (from.x + to.x) / 2 + 18, y: (from.y + to.y) / 2 + 14), to]
        animation.keyTimes = [0, 0.52, 1]
        animation.duration = 0.52
        animation.timingFunction = CAMediaTimingFunction(controlPoints: 0.22, 0.8, 0.24, 1)
        if !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion { pointerLayer.add(animation, forKey: "move") }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        pointerLayer.position = to
        rippleLayer.position = to
        CATransaction.commit()

        guard !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion else { return }
        switch kind {
        case "click": animateClick()
        case "scroll": animateNudge(key: "scroll", y: 8)
        case "type": animatePulse()
        case "drag": animateNudge(key: "drag", y: -4)
        default: break
        }
    }

    private func animateClick() {
        let scale = CABasicAnimation(keyPath: "transform.scale")
        scale.fromValue = 0.35
        scale.toValue = 1.8
        let opacity = CABasicAnimation(keyPath: "opacity")
        opacity.fromValue = 0.95
        opacity.toValue = 0
        let group = CAAnimationGroup()
        group.animations = [scale, opacity]
        group.duration = 0.48
        rippleLayer.add(group, forKey: "click")
    }

    private func animateNudge(key: String, y: CGFloat) {
        let animation = CAKeyframeAnimation(keyPath: "transform.translation.y")
        animation.values = [0, y, 0]
        animation.duration = 0.62
        pointerLayer.add(animation, forKey: key)
    }

    private func animatePulse() {
        let animation = CAKeyframeAnimation(keyPath: "opacity")
        animation.values = [1, 0.45, 1]
        animation.duration = 0.55
        pointerLayer.add(animation, forKey: "type")
    }

    private func presentationRect() -> CGRect {
        AVMakeRect(aspectRatio: videoSize, insideRect: bounds)
    }

    private func pointerPoint(_ normalized: CGPoint) -> CGPoint {
        let rect = presentationRect()
        return CGPoint(x: rect.minX + normalized.x * rect.width, y: rect.maxY - normalized.y * rect.height)
    }

    private func placePointer() {
        pointerLayer.position = pointerPoint(pointerPosition)
        rippleLayer.position = pointerLayer.position
    }
}

/// Container that shows a frosted placeholder (target app icon) the instant the
/// preview is requested, then cross-fades to the live capture once the first
/// real frame is presented. The placeholder communicates *which* app is being
/// observed; it is not a status badge and never carries error text.
@MainActor
private final class NativePiPView: NSView {
    private let frostView = NSVisualEffectView()
    private let iconView = NSImageView()
    private let live = NativePiPLiveView()
    private let closeButton = NSButton()
    private let controls = NSView()
    private let stateLabel = NSTextField(labelWithString: "")
    private let controlButton = NSButton()
    private let stopButton = NSButton()
    private var userControlled = false
    private let chinese = Locale.preferredLanguages.first?.hasPrefix("zh") == true
    var onControl: ((String) -> Void)?
    private var trackingArea: NSTrackingArea?
    private var isLive = false
    var onClose: (() -> Void)?

    override init(frame frameRect: NSRect) {
        super.init(frame: frameRect)
        wantsLayer = true
        layer?.backgroundColor = NSColor.clear.cgColor
        layer?.cornerRadius = 14
        layer?.masksToBounds = true

        frostView.material = .hudWindow
        frostView.state = .active
        frostView.blendingMode = .behindWindow
        frostView.wantsLayer = true
        addSubview(frostView)

        iconView.imageScaling = .scaleProportionallyUpOrDown
        iconView.contentTintColor = .secondaryLabelColor
        iconView.wantsLayer = true
        iconView.image = NativePiPView.fallbackIcon()
        addSubview(iconView)

        // Live capture sits above the placeholder and starts transparent, so the
        // frosted icon shows through until the first frame reveals it.
        live.alphaValue = 0
        addSubview(live)

        closeButton.title = "×"
        closeButton.isBordered = false
        closeButton.font = .systemFont(ofSize: 17, weight: .regular)
        closeButton.contentTintColor = .labelColor
        closeButton.wantsLayer = true
        closeButton.layer?.backgroundColor = NSColor.windowBackgroundColor.withAlphaComponent(0.88).cgColor
        closeButton.layer?.cornerRadius = 8
        closeButton.alphaValue = 1
        closeButton.target = self
        closeButton.action = #selector(closePressed)
        closeButton.toolTip = chinese ? "关闭预览" : "Close preview"
        closeButton.setAccessibilityLabel(closeButton.toolTip)
        addSubview(closeButton)

        controls.wantsLayer = true
        controls.layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
        addSubview(controls)
        stateLabel.font = .systemFont(ofSize: 10, weight: .medium)
        stateLabel.textColor = .secondaryLabelColor
        stateLabel.lineBreakMode = .byTruncatingTail
        controls.addSubview(stateLabel)
        for button in [controlButton, stopButton] {
            button.bezelStyle = .inline
            button.isBordered = false
            button.contentTintColor = .labelColor
            button.font = .systemFont(ofSize: 11, weight: .medium)
            button.target = self
            controls.addSubview(button)
        }
        controlButton.action = #selector(controlPressed)
        stopButton.title = chinese ? "停止" : "Stop"
        stopButton.action = #selector(stopPressed)
        updateActivity(["state": "starting", "controller": "agent"])
        startPlaceholderPulse()
    }

    required init?(coder: NSCoder) { nil }

    private static func fallbackIcon() -> NSImage? {
        NSImage(systemSymbolName: "macwindow", accessibilityDescription: "preparing live preview")
    }

    func setIcon(_ image: NSImage?) {
        if let image {
            iconView.contentTintColor = nil
            iconView.image = image
        } else {
            iconView.contentTintColor = .secondaryLabelColor
            iconView.image = NativePiPView.fallbackIcon()
        }
    }

    override func layout() {
        super.layout()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        frostView.frame = bounds
        live.frame = bounds
        let side = max(28, min(bounds.width, bounds.height) * 0.42)
        iconView.frame = CGRect(x: bounds.midX - side / 2, y: bounds.midY - side / 2, width: side, height: side)
        closeButton.frame = CGRect(x: bounds.maxX - 30, y: bounds.maxY - 30, width: 22, height: 22)
        controls.frame = CGRect(x: 0, y: 0, width: bounds.width, height: 30)
        stopButton.frame = CGRect(x: bounds.width - 48, y: 4, width: 40, height: 22)
        controlButton.frame = CGRect(x: bounds.width - 112, y: 4, width: 60, height: 22)
        stateLabel.frame = CGRect(x: 10, y: 8, width: max(20, bounds.width - 128), height: 14)
        CATransaction.commit()
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        effectiveAppearance.performAsCurrentDrawingAppearance {
            closeButton.layer?.backgroundColor = NSColor.windowBackgroundColor.withAlphaComponent(0.88).cgColor
            controls.layer?.backgroundColor = NSColor.windowBackgroundColor.cgColor
        }
    }

    private func startPlaceholderPulse() {
        guard !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion else { return }
        let pulse = CABasicAnimation(keyPath: "opacity")
        pulse.fromValue = 0.85
        pulse.toValue = 0.4
        pulse.duration = 1.35
        pulse.autoreverses = true
        pulse.repeatCount = .infinity
        pulse.timingFunction = CAMediaTimingFunction(name: .easeInEaseOut)
        iconView.layer?.add(pulse, forKey: "pulse")
    }

    func revealLive() {
        guard !isLive else { return }
        isLive = true
        iconView.layer?.removeAnimation(forKey: "pulse")
        NSAnimationContext.runAnimationGroup { context in
            context.duration = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 0 : 0.28
            live.animator().alphaValue = 1
            iconView.animator().alphaValue = 0
        }
    }

    func showPlaceholder() {
        guard isLive else { return }
        isLive = false
        iconView.alphaValue = 1
        NSAnimationContext.runAnimationGroup { context in
            context.duration = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion ? 0 : 0.2
            live.animator().alphaValue = 0
        }
        startPlaceholderPulse()
    }

    func enqueue(_ sampleBuffer: CMSampleBuffer) { live.enqueue(sampleBuffer) }
    func setVideoSize(_ size: CGSize) { live.setVideoSize(size) }
    func animateInteraction(_ payload: [String: Any]) { live.animateInteraction(payload) }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let trackingArea { removeTrackingArea(trackingArea) }
        let next = NSTrackingArea(rect: bounds, options: [.mouseEnteredAndExited, .activeAlways], owner: self)
        addTrackingArea(next)
        trackingArea = next
    }

    override func mouseEntered(with event: NSEvent) {
        NSAnimationContext.runAnimationGroup { _ in closeButton.animator().alphaValue = 1 }
    }

    override func mouseExited(with event: NSEvent) {
        closeButton.alphaValue = 1
    }

    func updateActivity(_ payload: [String: Any]) {
        let state = payload["state"] as? String ?? "starting"
        userControlled = payload["controller"] as? String == "user"
        let stopped = state == "stopped"
        let error = payload["error"] as? String ?? ""
        stateLabel.stringValue = !error.isEmpty ? (chinese ? "操作失败" : "Action failed")
            : stopped ? (chinese ? "已停止" : "Stopped")
            : userControlled ? (chinese ? "你在控制" : "Your control")
            : (chinese ? "Agent 控制" : "Agent control")
        stateLabel.toolTip = error.isEmpty ? nil : error
        controlButton.title = userControlled ? (chinese ? "交还" : "Release") : (chinese ? "接管" : "Take over")
        controlButton.setAccessibilityLabel(controlButton.title)
        controlButton.isEnabled = !stopped
        stopButton.isEnabled = !stopped
        if stopped || userControlled { live.hideInteraction() }
    }

    private func requestControl(_ action: String) {
        controlButton.isEnabled = false
        stopButton.isEnabled = false
        stateLabel.stringValue = chinese ? "正在处理" : "Working"
        onControl?(action)
    }

    @objc private func controlPressed() { requestControl(userControlled ? "release" : "takeover") }
    @objc private func stopPressed() { requestControl("stop") }
    @objc private func closePressed() { onClose?() }
}

@MainActor
private final class NativePiPWindowController: NSObject, NSWindowDelegate {
    let panel: NSPanel
    let content: NativePiPView
    private let writer: PiPEventWriter
    private let desktopTop: CGFloat
    private var shown = false
    private var requestedVisible = true
    private var requestedLive = true
    private var lastController = "agent"
    private var lastState = "starting"
    var shouldCapture: Bool { requestedVisible && requestedLive }
    private var captureAvailable = false

    init(configuration: NativePiPConfiguration, writer: PiPEventWriter) {
        self.writer = writer
        content = NativePiPView(frame: CGRect(origin: .zero, size: configuration.frame.size))
        desktopTop = NSScreen.screens.first?.frame.maxY ?? 0
        let nativeFrame = CGRect(
            x: configuration.frame.minX,
            y: desktopTop - configuration.frame.maxY,
            width: configuration.frame.width,
            height: configuration.frame.height
        )
        panel = NSPanel(
            contentRect: nativeFrame,
            styleMask: [.borderless, .nonactivatingPanel, .resizable, .fullSizeContentView],
            backing: .buffered,
            defer: false
        )
        super.init()
        panel.delegate = self
        panel.contentView = content
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.hidesOnDeactivate = false
        panel.becomesKeyOnlyIfNeeded = true
        // The helper runs in a separate process, so it cannot be attached as an
        // NSPanel child of Electron's main window. Keep the preview above normal
        // application windows while its owning activity is visible.
        panel.level = .floating
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
        panel.isMovableByWindowBackground = true
        panel.minSize = CGSize(width: 220, height: 140)
        panel.maxSize = CGSize(width: 720, height: 800)
        panel.contentAspectRatio = configuration.frame.size
        content.onControl = { [weak self] action in self?.writer.send("control", fields: ["action": action]) }
        content.onClose = { [weak self] in
            self?.requestedVisible = false
            self?.writer.send("user_close")
            self?.panel.orderOut(nil)
        }
    }

    func showAfterFirstFrame(videoSize: CGSize) {
        content.setVideoSize(videoSize)
        let ratio = videoSize.width / max(1, videoSize.height)
        let previousRatio = panel.contentAspectRatio.width / max(0.01, panel.contentAspectRatio.height)
        panel.contentAspectRatio = CGSize(width: ratio, height: 1)
        if shown, abs(previousRatio - ratio) > 0.001 {
            resizeForAspectRatio(ratio)
        }
        presentContent(width: Int(videoSize.width), height: Int(videoSize.height), ratio: ratio)
    }

    var hasShownContent: Bool { shown }

    func setVisible(_ visible: Bool) {
        requestedVisible = visible
        // Show the surface immediately so the frosted placeholder appears while
        // capture spins up; visibility is no longer gated on the first frame.
        if visible { panel.orderFrontRegardless() } else { panel.orderOut(nil) }
    }

    func setAppearance(dark: Bool) {
        panel.appearance = NSAppearance(named: dark ? .darkAqua : .aqua)
    }
    func setLive(_ live: Bool) { requestedLive = live }
    func updateActivity(_ payload: [String: Any]) { content.updateActivity(payload) }
    func needsRestoration(_ payload: [String: Any]) -> Bool {
        let nextController = payload["controller"] as? String ?? "agent"
        let nextState = payload["state"] as? String ?? "active"
        let restore = (nextController == "user" && lastController != "user") || (nextState == "stopped" && lastState != "stopped")
        lastController = nextController
        lastState = nextState
        return restore
    }
    func setIcon(_ image: NSImage?) { content.setIcon(image) }

    func markCaptureUnavailable() {
        guard captureAvailable else { return }
        captureAvailable = false
        // Fall back to the frosted placeholder instead of vanishing; a briefly
        // unavailable window should still read as "observing this app".
        content.showPlaceholder()
    }

    func animateInteraction(_ payload: [String: Any]) { content.animateInteraction(payload) }

    func windowWillResize(_ sender: NSWindow, to frameSize: NSSize) -> NSSize {
        let ratio = max(0.01, panel.contentAspectRatio.width / max(0.01, panel.contentAspectRatio.height))
        let widthDriven = CGSize(width: frameSize.width, height: frameSize.width / ratio)
        if widthDriven.height <= panel.maxSize.height { return widthDriven }
        return CGSize(width: frameSize.height * ratio, height: frameSize.height)
    }

    func windowDidResize(_ notification: Notification) { publishGeometry() }
    func windowDidMove(_ notification: Notification) { publishGeometry() }

    private func presentContent(width: Int, height: Int, ratio: CGFloat) {
        captureAvailable = true
        // First real frame: cross-fade the live capture over the placeholder and
        // grow from the fixed placeholder size to the window's true aspect ratio.
        content.revealLive()
        if !shown {
            let fitted = fittedPiPSize(ratio: ratio, preferredWidth: panel.frame.width)
            let current = panel.frame
            panel.setFrame(
                CGRect(x: current.maxX - fitted.width, y: current.maxY - fitted.height, width: fitted.width, height: fitted.height),
                display: true,
                animate: true
            )
            shown = true
            writer.send("ready", fields: ["width": width, "height": height])
        }
        if requestedVisible, !panel.isVisible {
            panel.orderFrontRegardless()
        }
    }

    private func fittedPiPSize(ratio: CGFloat, preferredWidth: CGFloat) -> CGSize {
        var width = preferredWidth
        var height = width / max(0.01, ratio)
        if height > 800 { height = 800; width = height * ratio }
        if height < 140 { height = 140; width = height * ratio }
        if width > 720 { width = 720; height = width / ratio }
        return CGSize(width: max(220, width), height: max(140, height))
    }

    private func resizeForAspectRatio(_ ratio: CGFloat) {
        let current = panel.frame
        var width = current.width
        var height = width / max(0.01, ratio)
        if height > panel.maxSize.height { height = panel.maxSize.height; width = height * ratio }
        if height < panel.minSize.height { height = panel.minSize.height; width = height * ratio }
        if width > panel.maxSize.width { width = panel.maxSize.width; height = width / ratio }
        if width < panel.minSize.width { width = panel.minSize.width; height = width / ratio }
        panel.setFrame(
            CGRect(x: current.maxX - width, y: current.maxY - height, width: width, height: height),
            display: true,
            animate: true
        )
    }

    private func publishGeometry() {
        let frame = panel.frame
        writer.send("geometry", fields: [
            "x": frame.minX,
            "y": desktopTop - frame.maxY,
            "width": frame.width,
            "height": frame.height,
        ])
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
    let controller: NativePiPWindowController
    let writer: PiPEventWriter
    private let lock = NSLock()
    private var stopped = false
    private var freshness = CaptureFreshness()
    private var lastStatus: SCFrameStatus?

    init(controller: NativePiPWindowController, writer: PiPEventWriter) {
        self.controller = controller
        self.writer = writer
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
        let shouldDisplay = imageBuffer != nil && !captureUnavailable
        lock.lock()
        let statusChanged = status != nil && status != lastStatus
        if let status { lastStatus = status }
        if status == .stopped { stopped = true }
        freshness.recordCallback(
            at: ProcessInfo.processInfo.systemUptime,
            displayedFrame: shouldDisplay,
            captureUnavailable: captureUnavailable
        )
        lock.unlock()
        if statusChanged, let status { writer.send("capture_status", fields: ["status": captureStatusName(status)]) }
        guard shouldDisplay, let imageBuffer = sampleBuffer.imageBuffer else { return }
        let size = CGSize(width: CVPixelBufferGetWidth(imageBuffer), height: CVPixelBufferGetHeight(imageBuffer))
        // SCStream invokes this callback on its capture queue. Core Animation
        // layers belong to the main actor, so enqueue and publish readiness in
        // one ordered main-actor operation. Publishing ready before the layer
        // receives the frame creates a visible-but-empty PiP window.
        let frame = CapturedFrame(sampleBuffer)
        Task { @MainActor [weak controller, frame] in
            guard let controller else { return }
            controller.content.enqueue(frame.sampleBuffer)
            controller.showAfterFirstFrame(videoSize: size)
        }
    }

    func stream(_ stream: SCStream, didStopWithError error: Error) {
        lock.lock(); stopped = true; lock.unlock()
        let ns = error as NSError
        pipDebugLog("stream didStopWithError domain=\(ns.domain) code=\(ns.code)")
        writer.send("capture_status", fields: ["status": "error", "message": error.localizedDescription])
    }

    func isStopped() -> Bool { lock.withLock { stopped } }
    func freshnessSnapshot() -> CaptureFreshness { lock.withLock { freshness } }
    func lockFailure(_ error: Error) {
        lock.lock(); stopped = true; lock.unlock()
        writer.send("capture_status", fields: ["status": "error", "message": error.localizedDescription])
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

func captureStageOffscreenOrigin(screenFrames: [CGRect]) -> CGPoint {
    let union = screenFrames.reduce(CGRect.zero) { $0.union($1) }
    return CGPoint(
        x: union.maxX + max(union.width * 3, 5800),
        y: union.maxY + max(union.height * 3, 4000)
    )
}

func captureStageVisibleFraction(windowFrame: CGRect, screenFrames: [CGRect]) -> CGFloat {
    let windowArea = max(1, windowFrame.width * windowFrame.height)
    let visibleArea = screenFrames.reduce(CGFloat.zero) { total, screen in
        let intersection = windowFrame.intersection(screen)
        guard !intersection.isNull, !intersection.isEmpty else { return total }
        return total + intersection.width * intersection.height
    }
    return min(1, visibleArea / windowArea)
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

private func captureDisplayFrames() -> [CGRect] {
    var count: UInt32 = 0
    guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { return [] }
    var displays = [CGDirectDisplayID](repeating: 0, count: Int(count))
    let result = displays.withUnsafeMutableBufferPointer {
        CGGetActiveDisplayList(count, $0.baseAddress, &count)
    }
    guard result == .success else { return [] }
    return displays.prefix(Int(count)).map(CGDisplayBounds)
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
    private let originalWindows: [NativePiPWindowSnapshot]
    private let targetSnapshotIndex: Int?
    private let wasHidden: Bool
    private var stagedWindows: [NativePiPResolvedWindow] = []
    private var stagedFrames: [Int: CGRect] = [:]
    private var staged = false

    init(application: NSRunningApplication, requestedFrame: CGRect?, focusedFrame: CGRect?) {
        axApplication = AXUIElementCreateApplication(application.processIdentifier)
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
        let displayFrames = captureDisplayFrames()
        guard !displayFrames.isEmpty else { return }
        staged = true
        let parked = captureStageOffscreenOrigin(screenFrames: displayFrames)
        if wasHidden {
            _ = AXUIElementSetAttributeValue(axApplication, kAXHiddenAttribute as CFString, kCFBooleanFalse)
        }
        // Custom apps apply hidden/minimized changes asynchronously and may
        // initially clamp a newly revealed window back onto a display. Retry the
        // public AX writes briefly so the window settles offscreen before SCK
        // resolves it, without ever activating the target app.
        var prepared = false
        var settledFrame: CGRect?
        for _ in 0..<20 {
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
                        _ = setOrigin(other.element, CGPoint(
                            x: parked.x + CGFloat(offset + 1) * 32,
                            y: parked.y + CGFloat(offset + 1) * 32
                        ))
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
                return captureStageVisibleFraction(windowFrame: frame, screenFrames: displayFrames) <= 0.02
            }
            if hidden != true,
               minimized != true,
               allOriginalWindowsResolved,
               otherWindowsAreConcealed,
               let currentFrame,
               captureStageVisibleFraction(windowFrame: currentFrame, screenFrames: displayFrames) <= 0.02 {
                prepared = true
                settledFrame = currentFrame
                break
            }
            try? await Task.sleep(for: .milliseconds(50))
        }
        guard prepared else {
            restore()
            return
        }
        pipDebugLog("staged hidden or minimized target window from frame=\(originalWindows[targetSnapshotIndex].frame) to frame=\(String(describing: settledFrame)) windows=\(stagedWindows.count)")
    }

    func restore() {
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

@available(macOS 14.0, *)
private func streamConfiguration(for window: SCWindow) -> SCStreamConfiguration {
    let configuration = SCStreamConfiguration()
    let scale = window.owningApplication == nil ? 1 : NSScreen.main?.backingScaleFactor ?? 2
    configuration.width = max(1, Int(window.frame.width * scale))
    configuration.height = max(1, Int(window.frame.height * scale))
    configuration.queueDepth = 4
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
    let stream = SCStream(
        filter: SCContentFilter(desktopIndependentWindow: window),
        configuration: streamConfiguration(for: window),
        delegate: output
    )
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
        try? stream.removeStreamOutput(output, type: .screen)
        stream.stopCapture { _ in }
        throw ComputerError.operationFailed("capture failed to start")
    }
    return stream
}

@MainActor
public func runNativePiP(configuration: NativePiPConfiguration) async throws {
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

    let writer = PiPEventWriter()
    let controller = NativePiPWindowController(configuration: configuration, writer: writer)
    controller.setIcon(app.icon)
    let windowStage = NativePiPWindowStage(
        application: app,
        requestedFrame: requestedWindowFrame(windowID: configuration.windowID, processID: app.processIdentifier),
        focusedFrame: focusedWindowFrame(processID: app.processIdentifier)
    )
    defer { windowStage.restore() }
    let terminationSignals = [SIGTERM, SIGINT].map { code in
        signal(code, SIG_IGN)
        let source = DispatchSource.makeSignalSource(signal: code, queue: .main)
        source.setEventHandler { windowStage.restore(); exit(0) }
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
                case "visible": controller.setVisible(command["visible"] as? Bool == true)
                case "live": controller.setLive(command["live"] as? Bool == true)
                case "appearance": controller.setAppearance(dark: command["dark"] as? Bool == true)
                case "activity":
                    controller.updateActivity(command)
                    let nextController = command["controller"] as? String ?? "agent"
                    if controller.needsRestoration(command) {
                        windowStage.restore()
                        let pid = app.processIdentifier
                        do {
                            try await Task.detached { try WindowRestorationJournal.restoreForUser(processID: pid) }.value
                            if nextController == "user" { app.activate(options: [.activateAllWindows, .activateIgnoringOtherApps]) }
                        } catch {
                            var failed = command
                            failed["error"] = error.localizedDescription
                            controller.updateActivity(failed)
                        }
                    }
                case "interaction": controller.animateInteraction(command)
                case "close":
                    controller.panel.orderOut(nil)
                    windowStage.restore()
                    exit(0)
                default: break
                }
            }
        }
    }
    await windowStage.prepareIfNeeded()

    let geometryMonitor = WindowGeometryMonitor(processID: app.processIdentifier, windowFrame: .zero)
    let captureStartTimeout: TimeInterval = 3
    let startupTimeout: TimeInterval = 5
    let callbackSilenceTimeout: TimeInterval = 3
    let unavailableStatusTimeout: TimeInterval = 3

    var stream: SCStream?
    var output: NativePiPStreamOutput?
    var trackedWindowID: CGWindowID?
    var trackedFrame: CGRect = .zero
    var streamStartedAt = ProcessInfo.processInfo.systemUptime
    var lastVerifiedCaptureAt = streamStartedAt
    var establishFailures = 0
    var lastHealthyOutput: ObjectIdentifier?
    var lastSafetyRefresh = Date.distantPast

    while !app.isTerminated && processIsAlive(configuration.parentProcessID) {
        if !controller.shouldCapture {
            if let existing = stream, let existingOutput = output {
                try? existing.removeStreamOutput(existingOutput, type: .screen)
                try? await existing.stopCapture()
                writer.send("capture_status", fields: ["status": "stopped"])
            }
            stream = nil
            output = nil
            try await Task.sleep(for: .milliseconds(100))
            continue
        }
        if establishFailures >= 4 {
            writer.send("capture_status", fields: ["status": "error", "message": "capture recovery exhausted"])
            break
        }
        // Establish (or re-establish) the capture stream whenever we do not have
        // a live one. Failures are non-fatal: the frosted placeholder stays up
        // and we back off and retry, matching how the reference implementation
        // keeps a placeholder when a capture stream stops with an error.
        if stream == nil || output?.isStopped() == true {
            if let existing = stream, let existingOutput = output {
                try? existing.removeStreamOutput(existingOutput, type: .screen)
                try? await existing.stopCapture()
            }
            stream = nil
            output = nil
            if establishFailures > 0 {
                if ProcessInfo.processInfo.systemUptime - lastVerifiedCaptureAt >= callbackSilenceTimeout {
                    controller.markCaptureUnavailable()
                }
                try await Task.sleep(for: .milliseconds(250 * (1 << min(5, establishFailures))))
            }
            guard let content = await shareableContent(timeout: 3),
                  let target = (configuration.windowID != nil
                    ? content.windows.first(where: { $0.windowID == configuration.windowID && $0.owningApplication?.processID == app.processIdentifier })
                    : preferredStreamWindow(in: content, processID: app.processIdentifier)) else {
                establishFailures += 1
                continue
            }
            pipDebugLog("resolve window id=\(target.windowID) frame=\(target.frame) title=\(target.title ?? "-")")
            let next = NativePiPStreamOutput(controller: controller, writer: writer)
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
            try? activeStream.removeStreamOutput(activeOutput, type: .screen)
            try? await activeStream.stopCapture()
            if now - lastVerifiedCaptureAt >= callbackSilenceTimeout {
                controller.markCaptureUnavailable()
            }
            establishFailures += 1
            stream = nil
            output = nil
            continue
        }
        if !freshness.hasDisplayedFrame, now - lastVerifiedCaptureAt >= callbackSilenceTimeout {
            controller.markCaptureUnavailable()
        }
        let safetyRefreshDue = Date().timeIntervalSince(lastSafetyRefresh) >= 5
        guard geometryMonitor.takeDirty() || safetyRefreshDue else { continue }
        lastSafetyRefresh = Date()
        guard let refreshed = await shareableContent(timeout: 3),
              let nextWindow = refreshed.windows.first(where: { $0.windowID == (trackedWindowID ?? 0) }) else { continue }
        guard windowFrameDistance(nextWindow.frame, trackedFrame) > 1 else { continue }
        do {
            try await activeStream.updateConfiguration(streamConfiguration(for: nextWindow))
            trackedFrame = nextWindow.frame
        } catch { activeOutput.lockFailure(error) }
    }
    if let stream, let output {
        try? stream.removeStreamOutput(output, type: .screen)
        try? await stream.stopCapture()
    }
}

private func processIsAlive(_ processID: pid_t?) -> Bool {
    guard let processID, processID > 0 else { return true }
    return kill(processID, 0) == 0 || errno == EPERM
}
