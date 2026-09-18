import AppKit
import CoreGraphics
import Foundation
@preconcurrency import ScreenCaptureKit

public struct CaptureGeometry: Sendable {
    public let windowFrame: CGRect
    public let imageWidth: Int
    public let imageHeight: Int
    public let visibleImageFrame: CGRect

    public init(windowFrame: CGRect, imageWidth: Int, imageHeight: Int, visibleImageFrame: CGRect? = nil) {
        self.windowFrame = windowFrame
        self.imageWidth = imageWidth
        self.imageHeight = imageHeight
        self.visibleImageFrame = visibleImageFrame ?? CGRect(x: 0, y: 0, width: imageWidth, height: imageHeight)
    }

    public func screenPoint(x: Double, y: Double) throws -> CGPoint {
        guard imageWidth > 0, imageHeight > 0 else {
            throw ComputerError.operationFailed("screenshot geometry is invalid")
        }
        guard x >= 0, y >= 0, x <= Double(imageWidth), y <= Double(imageHeight) else {
            throw ComputerError.invalidArguments("coordinates must be inside the latest screenshot (\(imageWidth)×\(imageHeight))")
        }
        return CGPoint(
            x: windowFrame.origin.x + x * windowFrame.width / Double(imageWidth),
            y: windowFrame.origin.y + y * windowFrame.height / Double(imageHeight)
        )
    }

    public func screenPoint(normalizedX x: Double, normalizedY y: Double) throws -> CGPoint {
        guard x >= 0, y >= 0, x <= 1000, y <= 1000 else {
            throw ComputerError.invalidArguments("normalized coordinates must be between 0 and 1000")
        }
        return try screenPoint(
            x: x * Double(imageWidth) / 1000,
            y: y * Double(imageHeight) / 1000
        )
    }
}

struct WindowCapture {
    let data: Data
    let geometry: CaptureGeometry
    let windowID: CGWindowID
}

private final class CaptureBox: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Result<WindowCapture, Error>?

    func set(_ result: Result<WindowCapture, Error>) {
        lock.lock()
        value = result
        lock.unlock()
    }

    func get() -> Result<WindowCapture, Error>? {
        lock.lock()
        defer { lock.unlock() }
        return value
    }
}

func captureWindowPNG(
    processID: pid_t,
    timeout: TimeInterval = 8,
    preferredWindowFrame: CGRect? = nil
) throws -> WindowCapture {
    guard CGPreflightScreenCaptureAccess() else {
        throw ComputerError.permissionDenied("Screen Recording permission is required for window screenshots")
    }
    guard #available(macOS 14.0, *) else {
        throw ComputerError.unsupported("window screenshots require macOS 14 or newer")
    }
    let semaphore = DispatchSemaphore(value: 0)
    let box = CaptureBox()
    SCShareableContent.getExcludingDesktopWindows(false, onScreenWindowsOnly: false) { content, error in
        if let error {
            box.set(.failure(error))
            semaphore.signal()
            return
        }
        let candidates = content?.windows
            .filter({ $0.owningApplication?.processID == processID && $0.frame.width > 1 && $0.frame.height > 1 }) ?? []
        let window = preferredWindowFrame.flatMap { preferred in
            candidates.min { left, right in
                screenshotWindowFrameDistance(left.frame, preferred) < screenshotWindowFrameDistance(right.frame, preferred)
            }
        } ?? candidates.max(by: { $0.frame.width * $0.frame.height < $1.frame.width * $1.frame.height })
        guard let window else {
            box.set(.failure(ComputerError.operationFailed("no capturable window found")))
            semaphore.signal()
            return
        }
        let configuration = SCStreamConfiguration()
        configuration.width = max(1, Int(window.frame.width * 2))
        configuration.height = max(1, Int(window.frame.height * 2))
        configuration.showsCursor = false
        let filter = SCContentFilter(desktopIndependentWindow: window)
        let windowFrame = window.frame
        SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration) { image, captureError in
            if let captureError {
                box.set(.failure(captureError))
            } else if let image,
                      let data = NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:]) {
                box.set(.success(WindowCapture(
                    data: data,
                    geometry: CaptureGeometry(
                        windowFrame: windowFrame,
                        imageWidth: image.width,
                        imageHeight: image.height,
                        visibleImageFrame: visiblePixelBounds(image)
                    ),
                    windowID: window.windowID
                )))
            } else {
                box.set(.failure(ComputerError.operationFailed("window screenshot produced no image")))
            }
            semaphore.signal()
        }
    }
    guard semaphore.wait(timeout: .now() + timeout) == .success else {
        throw ComputerError.operationFailed("window screenshot timed out")
    }
    return try box.get()?.get() ?? { throw ComputerError.operationFailed("window screenshot did not complete") }()
}

private func screenshotWindowFrameDistance(_ left: CGRect, _ right: CGRect) -> CGFloat {
    abs(left.minX - right.minX)
        + abs(left.minY - right.minY)
        + abs(left.width - right.width)
        + abs(left.height - right.height)
}

@available(macOS 14.0, *)
func captureForegroundWindowPNG(processID: pid_t, preferredWindowFrame: CGRect? = nil) throws -> WindowCapture {
    guard CGPreflightScreenCaptureAccess() else {
        throw ComputerError.permissionDenied("Screen Recording permission is required for window screenshots")
    }
    guard let windowList = CGWindowListCopyWindowInfo(.optionAll, kCGNullWindowID) as? [[String: Any]] else {
        throw ComputerError.operationFailed("could not list foreground windows")
    }
    let candidates = windowList.compactMap { info -> (id: CGWindowID, frame: CGRect, shareable: Bool)? in
        guard (info[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == processID,
              (info[kCGWindowLayer as String] as? NSNumber)?.intValue == 0,
              let number = info[kCGWindowNumber as String] as? NSNumber,
              let bounds = info[kCGWindowBounds as String] as? [String: Any],
              let frame = CGRect(dictionaryRepresentation: bounds as CFDictionary),
              frame.width > 1,
              frame.height > 1,
              preferredWindowFrame != nil || info[kCGWindowIsOnscreen as String] as? Bool == true else {
            return nil
        }
        let shareable = (info[kCGWindowSharingState as String] as? NSNumber)?.intValue != 0
        return (CGWindowID(number.uint32Value), frame, shareable)
    }
    let index = preferredCaptureFrameIndex(candidates.map(\.frame), preferred: preferredWindowFrame)
    guard let index else {
        throw ComputerError.operationFailed("no foreground window found")
    }
    let window = candidates[index]
    let windowImage = window.shareable
        ? CGWindowListCreateImage(.null, .optionIncludingWindow, window.id, [.boundsIgnoreFraming, .bestResolution])
        : nil
    let captured: (image: CGImage, frame: CGRect)
    if let windowImage {
        captured = (windowImage, window.frame)
    } else {
        return try captureSystemWindowPNG(window.id, frame: window.frame)
    }
    guard let data = NSBitmapImageRep(cgImage: captured.image).representation(using: .png, properties: [:]) else {
        throw ComputerError.operationFailed("foreground window screenshot produced no image")
    }
    return WindowCapture(
        data: data,
        geometry: CaptureGeometry(
            windowFrame: captured.frame,
            imageWidth: captured.image.width,
            imageHeight: captured.image.height,
            visibleImageFrame: visiblePixelBounds(captured.image)
        ),
        windowID: window.id
    )
}

private func visiblePixelBounds(_ image: CGImage) -> CGRect {
    let bitmap = NSBitmapImageRep(cgImage: image)
    var minX = image.width
    var minY = image.height
    var maxX = -1
    var maxY = -1
    for y in 0..<image.height {
        for x in 0..<image.width {
            guard let color = bitmap.colorAt(x: x, y: y), color.alphaComponent >= 0.5 else { continue }
            minX = min(minX, x)
            minY = min(minY, y)
            maxX = max(maxX, x)
            maxY = max(maxY, y)
        }
    }
    guard maxX >= minX, maxY >= minY else {
        return CGRect(x: 0, y: 0, width: image.width, height: image.height)
    }
    return CGRect(x: minX, y: minY, width: maxX - minX + 1, height: maxY - minY + 1)
}

private func captureSystemWindowPNG(_ windowID: CGWindowID, frame: CGRect, timeout: TimeInterval = 4) throws -> WindowCapture {
    let path = FileManager.default.temporaryDirectory
        .appendingPathComponent("wuu-cua-\(UUID().uuidString).png")
    defer { try? FileManager.default.removeItem(at: path) }

    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
    process.arguments = [
        "-x",
        "-o",
        "-l\(windowID)",
        path.path,
    ]
    let finished = DispatchSemaphore(value: 0)
    process.terminationHandler = { _ in finished.signal() }
    do {
        try process.run()
    } catch {
        throw ComputerError.operationFailed("start system screenshot: \(error.localizedDescription)")
    }
    guard finished.wait(timeout: .now() + timeout) == .success else {
        process.terminate()
        throw ComputerError.operationFailed("system screenshot timed out")
    }
    guard process.terminationStatus == 0,
          let data = try? Data(contentsOf: path),
          let image = NSBitmapImageRep(data: data),
          image.pixelsWide > 0,
          image.pixelsHigh > 0 else {
        throw ComputerError.operationFailed("system screenshot produced no image")
    }
    return WindowCapture(
        data: data,
        geometry: CaptureGeometry(
            windowFrame: frame,
            imageWidth: image.pixelsWide,
            imageHeight: image.pixelsHigh
        ),
        windowID: windowID
    )
}

// One on-screen window's metadata, kept in front-to-back z-order.
struct AppWindowInfo {
    let windowID: CGWindowID
    // Global, top-left (Core Graphics) coordinates in points, as reported by
    // kCGWindowBounds.
    let frame: CGRect
    // kCGWindowLayer: the window level (0 for ordinary document windows, higher
    // for floating panels). Kept for diagnostics; the array position, not the
    // layer, is the source of intra-layer z-order.
    let layer: Int
    // Position within the on-screen list: 0 is frontmost. This array order is the
    // only reliable z-order source — SCWindow exposes windowLayer but no
    // within-layer ordering, and SCShareableContent.windows makes no order promise.
    let zIndex: Int
    let shareable: Bool
}

// One window's placement inside a composite image, in composite pixel coordinates.
struct WindowScreenshotInfo {
    let id: Int
    let zIndex: Int
    let originX: Int
    let originY: Int
    let width: Int
    let height: Int

    var dictionary: [String: Any] {
        [
            "id": id,
            "z_index": zIndex,
            "origin_x": originX,
            "origin_y": originY,
            "width": width,
            "height": height,
        ]
    }
}

// Enumerate on-screen windows for compositing. processID == nil selects every
// app's windows (scope=screen); a concrete pid selects one app (scope=app).
//
// .optionOnScreenOnly is deliberate: its array order (front to back) is the only
// dependable z-order signal, and it excludes off-screen (e.g. concealed) windows,
// which therefore never appear in a composite. .excludeDesktopElements drops the
// wallpaper/desktop backing so the union frame is not inflated to the full screen.
func appWindowInfos(processID: pid_t?) -> [AppWindowInfo] {
    guard let windowList = CGWindowListCopyWindowInfo(
        [.optionOnScreenOnly, .excludeDesktopElements],
        kCGNullWindowID
    ) as? [[String: Any]] else {
        return []
    }
    var infos: [AppWindowInfo] = []
    for info in windowList {
        if let processID,
           (info[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value != processID {
            continue
        }
        guard let number = info[kCGWindowNumber as String] as? NSNumber,
              let bounds = info[kCGWindowBounds as String] as? [String: Any],
              let frame = CGRect(dictionaryRepresentation: bounds as CFDictionary),
              frame.width > 1,
              frame.height > 1 else {
            continue
        }
        let layer = (info[kCGWindowLayer as String] as? NSNumber)?.intValue ?? 0
        let shareable = (info[kCGWindowSharingState as String] as? NSNumber)?.intValue != 0
        // zIndex is dense over the matched windows so it stays a valid 0..n-1
        // back-to-front index even when other apps' windows are filtered out.
        infos.append(AppWindowInfo(
            windowID: CGWindowID(number.uint32Value),
            frame: frame,
            layer: layer,
            zIndex: infos.count,
            shareable: shareable
        ))
    }
    return infos
}

// Single-window capture primitive: the Core Graphics window image when the window
// is shareable, else the /usr/sbin/screencapture -l fallback for windows that opt
// out of sharing. This is a standalone copy of captureForegroundWindowPNG's
// per-window path so the legacy single-window observe path stays byte-for-byte
// unchanged while the composite path reuses the same primitive.
@available(macOS 14.0, *)
func captureWindowImage(windowID: CGWindowID, frame: CGRect, shareable: Bool) throws -> CGImage {
    if shareable,
       let image = CGWindowListCreateImage(.null, .optionIncludingWindow, windowID, [.boundsIgnoreFraming, .bestResolution]) {
        return image
    }
    let capture = try captureSystemWindowPNG(windowID, frame: frame)
    guard let rep = NSBitmapImageRep(data: capture.data),
          let image = rep.cgImage(forProposedRect: nil, context: nil, hints: nil) else {
        throw ComputerError.operationFailed("could not decode system window screenshot for composite")
    }
    return image
}

// Every composited window is redrawn at one shared scale. A mixed-DPI, multi-display
// arrangement otherwise yields per-window pixel densities that misalign the placement
// math and break click mapping. Use the densest display so no window loses detail.
func unifiedCaptureScale() -> CGFloat {
    let densest = NSScreen.screens.map(\.backingScaleFactor).max() ?? 2
    return densest > 0 ? densest : 2
}

// Pure layout math for a composite: the union of all window frames, plus each
// window's placement in composite pixel coordinates (top-left origin, y-down, to
// match the screenshot pixel convention CaptureGeometry uses). Kept free of AppKit
// so it can be unit-tested without a display.
func compositeLayout(frames: [CGRect], scale: CGFloat) -> (union: CGRect, placements: [CGRect]) {
    let safeScale = scale > 0 ? scale : 1
    let unionRaw = frames.reduce(CGRect.null) { $0.union($1) }
    let union = unionRaw.isNull ? .zero : unionRaw
    let placements = frames.map { frame in
        CGRect(
            x: (frame.origin.x - union.origin.x) * safeScale,
            y: (frame.origin.y - union.origin.y) * safeScale,
            width: frame.width * safeScale,
            height: frame.height * safeScale
        )
    }
    return (union, placements)
}

// Composite every selected on-screen window into one image, drawn back-to-front so
// the frontmost window (z_index 0) wins overlaps. windowFrame is the union rect, so
// CaptureGeometry's affine map turns a click anywhere in the composite into the right
// global point with no other change. visiblePixelBounds is intentionally skipped: it
// is an O(W×H) per-pixel scan and a multi-window composite is large; the union rect
// already bounds the content.
@available(macOS 14.0, *)
func captureAppCompositePNG(processID: pid_t, scope: CaptureScope) throws -> (capture: WindowCapture, windows: [WindowScreenshotInfo]) {
    guard CGPreflightScreenCaptureAccess() else {
        throw ComputerError.permissionDenied("Screen Recording permission is required for window screenshots")
    }
    let infos = appWindowInfos(processID: scope == .screen ? nil : processID)
    guard !infos.isEmpty else {
        throw ComputerError.operationFailed("no capturable on-screen window found for composite capture")
    }
    let scale = unifiedCaptureScale()
    let layout = compositeLayout(frames: infos.map(\.frame), scale: scale)
    let widthPixels = max(1, Int((layout.union.width * scale).rounded()))
    let heightPixels = max(1, Int((layout.union.height * scale).rounded()))
    guard let context = CGContext(
        data: nil,
        width: widthPixels,
        height: heightPixels,
        bitsPerComponent: 8,
        bytesPerRow: 0,
        space: CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
    ) else {
        throw ComputerError.operationFailed("could not allocate composite image context")
    }
    context.interpolationQuality = .high

    let placed: [(info: AppWindowInfo, placement: CGRect)] = infos.indices.map { index in
        (info: infos[index], placement: layout.placements[index])
    }
    // Draw from back to front: highest zIndex first so a lower zIndex (nearer the
    // front) paints over it. The CGContext origin is bottom-left, so the top-left
    // placement rect is flipped into a bottom-left draw rect; the resulting image is
    // therefore top-down, matching the screenshot pixel convention.
    for entry in placed.sorted(by: { $0.info.zIndex > $1.info.zIndex }) {
        guard let image = try? captureWindowImage(
            windowID: entry.info.windowID,
            frame: entry.info.frame,
            shareable: entry.info.shareable
        ) else { continue }
        let drawRect = CGRect(
            x: entry.placement.origin.x,
            y: CGFloat(heightPixels) - entry.placement.origin.y - entry.placement.height,
            width: entry.placement.width,
            height: entry.placement.height
        )
        context.draw(image, in: drawRect)
    }
    guard let composite = context.makeImage(),
          let data = NSBitmapImageRep(cgImage: composite).representation(using: .png, properties: [:]) else {
        throw ComputerError.operationFailed("composite screenshot produced no image")
    }
    let windows = placed.map { entry in
        WindowScreenshotInfo(
            id: Int(entry.info.windowID),
            zIndex: entry.info.zIndex,
            originX: Int(entry.placement.origin.x.rounded()),
            originY: Int(entry.placement.origin.y.rounded()),
            width: Int(entry.placement.width.rounded()),
            height: Int(entry.placement.height.rounded())
        )
    }
    let capture = WindowCapture(
        data: data,
        geometry: CaptureGeometry(
            windowFrame: layout.union,
            imageWidth: composite.width,
            imageHeight: composite.height
        ),
        // infos[0] is the frontmost window; keep its id for lastWindowIDs continuity.
        windowID: infos[0].windowID
    )
    return (capture, windows)
}

func preferredCaptureFrameIndex(_ frames: [CGRect], preferred: CGRect?) -> Int? {
    if let preferred {
        return frames.indices.min { screenshotWindowFrameDistance(frames[$0], preferred) < screenshotWindowFrameDistance(frames[$1], preferred) }
    }
    return frames.indices.max { frames[$0].width * frames[$0].height < frames[$1].width * frames[$1].height }
}
