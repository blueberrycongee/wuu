import AppKit
import ApplicationServices
import CoreGraphics
import Foundation
import ScreenCaptureKit

// This executable has no input, file, scripting, or model interface. Every
// capture is one explicit invocation, limited to the frontmost visible window.
private struct CaptureFailure: Error {
    let code: String
    let message: String
}

private struct Target {
    let pid: pid_t
    let launchIdentity: String
    let appName: String
    let bundleID: String
    let windowID: CGWindowID
    let frame: CGRect
    let title: String
}

private struct PinnedTarget: Decodable {
    let pid: Int32
    let launch_identity: String
    let window_id: UInt32
    let frame: [Double]
}

private func launchIdentity(_ app: NSRunningApplication) -> String? {
    guard let date = app.launchDate else { return nil }
    // Same IEEE-754 reference-time bits as the synchronous Node-API bridge.
    return String(format: "%016llx", date.timeIntervalSinceReferenceDate.bitPattern)
}

private func output(_ value: [String: Any]) {
    guard let data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]) else { return }
    FileHandle.standardOutput.write(data)
    FileHandle.standardOutput.write(Data([10]))
}

private func permissionStatus() -> [String: Any] {
    ["screen_recording": CGPreflightScreenCaptureAccess(), "accessibility": AXIsProcessTrusted()]
}

private func frontWindow(_ pid: pid_t) -> (CGWindowID, CGRect, String)? {
    guard let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] else { return nil }
    // CGWindowList is front-to-back. Never substitute the biggest/background
    // window, or broaden this to a screen capture when no window is available.
    for window in windows {
        guard (window[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == pid,
              (window[kCGWindowLayer as String] as? NSNumber)?.intValue == 0,
              let id = window[kCGWindowNumber as String] as? NSNumber,
              let bounds = window[kCGWindowBounds as String] as? [String: Any],
              let frame = CGRect(dictionaryRepresentation: bounds as CFDictionary),
              frame.width > 1, frame.height > 1,
              ((window[kCGWindowAlpha as String] as? NSNumber)?.doubleValue ?? 1) > 0 else { continue }
        return (id.uint32Value, frame, window[kCGWindowName as String] as? String ?? "")
    }
    return nil
}

@MainActor private func pinTarget(excludedPID: pid_t, expected: PinnedTarget) throws -> Target {
    guard CGPreflightScreenCaptureAccess() else {
        throw CaptureFailure(code: "screen_permission", message: "Screen Recording permission is required. Enable it in System Settings, then try the shortcut again.")
    }
    guard let app = NSWorkspace.shared.frontmostApplication, !app.isTerminated,
          app.processIdentifier != excludedPID,
          app.bundleIdentifier != "com.blueberrycongee.wuu",
          app.bundleIdentifier != "com.blueberrycongee.wuu.app-context" else {
        throw CaptureFailure(code: "no_target", message: "Switch to the app you want to capture, then press the snapshot shortcut.")
    }
    guard let identity = launchIdentity(app) else {
        throw CaptureFailure(code: "no_target", message: "The app identity is not available yet. Keep the target window in front and try again.")
    }
    guard let (windowID, frame, title) = frontWindow(app.processIdentifier) else {
        throw CaptureFailure(code: "no_target", message: "The frontmost app has no visible window to capture.")
    }
    guard expected.pid == app.processIdentifier, expected.launch_identity == identity,
          expected.window_id == windowID, expected.frame.count == 4,
          expected.frame.allSatisfy({ $0.isFinite }),
          frame.equalTo(CGRect(x: expected.frame[0], y: expected.frame[1], width: expected.frame[2], height: expected.frame[3])) else {
        throw CaptureFailure(code: "target_changed", message: "The app or window changed after the shortcut. No snapshot was captured. Try again with the target in front.")
    }
    return Target(pid: app.processIdentifier, launchIdentity: identity,
                  appName: app.localizedName ?? "App", bundleID: app.bundleIdentifier ?? "",
                  windowID: windowID, frame: frame, title: title)
}

@MainActor private func validate(_ target: Target) throws {
    guard CGPreflightScreenCaptureAccess() else {
        throw CaptureFailure(code: "screen_permission", message: "Screen Recording permission was removed. No snapshot was retained.")
    }
    guard let app = NSWorkspace.shared.frontmostApplication,
          app.processIdentifier == target.pid, launchIdentity(app) == target.launchIdentity,
          !app.isTerminated, let (id, frame, _) = frontWindow(target.pid),
          id == target.windowID, frame.equalTo(target.frame) else {
        throw CaptureFailure(code: "target_changed", message: "The app or window changed during capture. Keep the target window in front and try again.")
    }
}

private func axValue(_ element: AXUIElement, _ attribute: CFString) -> CFTypeRef? {
    var value: CFTypeRef?
    guard AXUIElementCopyAttributeValue(element, attribute, &value) == .success else { return nil }
    return value
}

private func axFrame(_ element: AXUIElement) -> CGRect? {
    guard let position = axValue(element, kAXPositionAttribute as CFString),
          let size = axValue(element, kAXSizeAttribute as CFString),
          CFGetTypeID(position) == AXValueGetTypeID(), CFGetTypeID(size) == AXValueGetTypeID() else { return nil }
    var point = CGPoint.zero
    var dimensions = CGSize.zero
    guard AXValueGetValue(position as! AXValue, .cgPoint, &point),
          AXValueGetValue(size as! AXValue, .cgSize, &dimensions) else { return nil }
    return CGRect(origin: point, size: dimensions)
}

// Public AX APIs do not offer a general CGWindowID mapping. Only accept
// an unambiguous single-window application; hidden/floating siblings count.
private func matchingTextWindow(_ target: Target) -> AXUIElement? {
    guard AXIsProcessTrusted(),
          let all = CGWindowListCopyWindowInfo([.optionAll, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] else { return nil }
    let windows = all.filter { ($0[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == target.pid }
    guard windows.count == 1, (windows[0][kCGWindowNumber as String] as? NSNumber)?.uint32Value == target.windowID else { return nil }
    let app = AXUIElementCreateApplication(target.pid)
    AXUIElementSetMessagingTimeout(app, 0.15)
    guard let accessible = axValue(app, kAXWindowsAttribute as CFString) as? [AXUIElement], accessible.count == 1,
          let value = axValue(app, kAXFocusedWindowAttribute as CFString),
          CFGetTypeID(value) == AXUIElementGetTypeID(), CFEqual(value, accessible[0]) else { return nil }
    let window = accessible[0]
    AXUIElementSetMessagingTimeout(window, 0.15)
    guard let frame = axFrame(window),
          abs(frame.minX - target.frame.minX) < 2, abs(frame.minY - target.frame.minY) < 2,
          abs(frame.width - target.frame.width) < 2, abs(frame.height - target.frame.height) < 2 else { return nil }
    return window
}

private func availableText(_ target: Target) -> (text: String, status: String) {
    guard AXIsProcessTrusted() else { return ("", "permission_missing") }
    guard let window = matchingTextWindow(target) else { return ("", "unavailable") }
    let deadline = Date().addingTimeInterval(2)
    var queue: [(AXUIElement, Int)] = [(window, 0)]
    var lines: [String] = []
    var seen = Set<String>()
    var characters = 0
    var visited = 0
    var truncated = false
    while !queue.isEmpty {
        guard visited < 600, characters < 40_000, Date() < deadline, AXIsProcessTrusted() else {
            truncated = true
            break
        }
        let (element, depth) = queue.removeFirst()
        AXUIElementSetMessagingTimeout(element, 0.15)
        visited += 1
        guard let role = axValue(element, kAXRoleAttribute as CFString) as? String else { continue }
        let subrole = axValue(element, kAXSubroleAttribute as CFString) as? String
        // Secure fields and their descendants are never queried for values.
        if role == "AXSecureTextField" || subrole == "AXSecureTextField"
            || (role == "AXTextField" && subrole == nil) { continue }
        for attribute in [kAXTitleAttribute, kAXDescriptionAttribute, kAXValueAttribute, kAXSelectedTextAttribute] {
            guard let string = axValue(element, attribute as CFString) as? String else { continue }
            let text = String(string.trimmingCharacters(in: .whitespacesAndNewlines).prefix(4_000))
            if text.isEmpty || !seen.insert(text).inserted { continue }
            let bounded = String(text.prefix(max(0, 40_000 - characters)))
            lines.append(bounded)
            characters += bounded.count
            if bounded.count < text.count || string.count > 4_000 { truncated = true }
        }
        guard depth < 12 else { truncated = true; continue }
        var children: CFArray?
        if AXUIElementCopyAttributeValues(element, kAXChildrenAttribute as CFString, 0, 120, &children) == .success,
           let elements = children as? [AXUIElement] {
            queue.append(contentsOf: elements.map { ($0, depth + 1) })
            if elements.count == 120 { truncated = true }
        }
    }
    guard AXIsProcessTrusted() else { return ("", "permission_missing") }
    guard let current = matchingTextWindow(target), CFEqual(current, window) else { return ("", "unavailable") }
    return (lines.joined(separator: "\n"), truncated ? "truncated" : lines.isEmpty ? "unavailable" : "included")
}

@main private struct AppContextCapture {
    @MainActor static func main() async {
        NSApplication.shared.setActivationPolicy(.prohibited)
        let arguments = Array(CommandLine.arguments.dropFirst())
        if arguments == ["--status"] {
            output(["ok": true, "permissions": permissionStatus()])
            return
        }
        if arguments == ["--request-screen-permission"] {
            _ = CGRequestScreenCaptureAccess()
            output(["ok": true, "permissions": permissionStatus()])
            return
        }
        if arguments == ["--request-text-permission"] {
            _ = AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary)
            output(["ok": true, "permissions": permissionStatus()])
            return
        }
        guard (arguments.count == 5 || arguments.count == 6), arguments[0] == "--capture",
              arguments[1] == "--exclude-pid", let excludedPID = Int32(arguments[2]), excludedPID > 0,
              arguments[3] == "--target", let data = arguments[4].data(using: .utf8), data.count <= 2048,
              let expected = try? JSONDecoder().decode(PinnedTarget.self, from: data),
              arguments.count == 5 || arguments[5] == "--include-text" else {
            output(["ok": false, "code": "invalid_request", "message": "Unsupported app snapshot request."])
            return
        }
        do {
            let target = try pinTarget(excludedPID: excludedPID, expected: expected)
            output(["event": "pinned", "app_name": target.appName])
            let content = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: true)
            try validate(target)
            guard let window = content.windows.first(where: { $0.windowID == target.windowID && $0.owningApplication?.processID == target.pid }),
                  window.isOnScreen, window.frame.equalTo(target.frame) else {
                throw CaptureFailure(code: "target_changed", message: "The selected app window is no longer available.")
            }
            let filter = SCContentFilter(desktopIndependentWindow: window)
            let config = SCStreamConfiguration()
            let nativeScale = max(1, CGFloat(filter.pointPixelScale))
            let scale = min(nativeScale, 3072 / max(target.frame.width, target.frame.height))
            config.width = max(1, Int(target.frame.width * scale))
            config.height = max(1, Int(target.frame.height * scale))
            config.showsCursor = false
            config.ignoreShadowsSingleWindow = true
            config.capturesAudio = false
            let image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: config)
            try validate(target)
            let text = arguments.contains("--include-text") ? availableText(target) : (text: "", status: "not_requested")
            try validate(target)
            guard let bytes = NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:]), bytes.count <= 16 * 1024 * 1024 else {
                throw CaptureFailure(code: "too_large", message: "The app snapshot is too large to attach. Make the target window smaller and try again.")
            }
            output(["ok": true, "snapshot": [
                "id": UUID().uuidString, "captured_at": ISO8601DateFormatter().string(from: Date()),
                "app_name": target.appName, "bundle_id": target.bundleID, "window_title": target.title,
                "window_id": target.windowID, "width": image.width, "height": image.height,
                "image_base64": bytes.base64EncodedString(), "available_text": text.text, "text_status": text.status
            ]])
        } catch let failure as CaptureFailure {
            output(["ok": false, "code": failure.code, "message": failure.message])
        } catch {
            // SDK diagnostics can contain window content. Keep them out of
            // the renderer and logs; callers receive a stable failure state.
            output(["ok": false, "code": "capture_failed", "message": "The app window could not be captured. Check permissions and try again."])
        }
    }
}
