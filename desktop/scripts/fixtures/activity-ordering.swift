import AppKit
import CoreGraphics
if CommandLine.arguments[1] == "--cover" {
  let app = NSApplication.shared
  app.setActivationPolicy(.regular)
  let window = NSWindow(contentRect: NSRect(x: 180, y: 300, width: 760, height: 520), styleMask: [.titled, .closable], backing: .buffered, defer: false)
  window.title = "Synthetic external app — no user data"
  window.backgroundColor = NSColor.systemGreen
  window.makeKeyAndOrderFront(nil)
  app.activate(ignoringOtherApps: true)
  app.run()
  exit(0)
}
if CommandLine.arguments[1] == "--activate" {
  guard let target = NSRunningApplication(processIdentifier: pid_t(CommandLine.arguments[2])!),
        target.activate(options: [.activateIgnoringOtherApps, .activateAllWindows]) else {
    fputs("Could not activate synthetic cover app\n", stderr)
    exit(1)
  }
  exit(0)
}
let pid = Int(CommandLine.arguments[1])!
let coverPID = Int(CommandLine.arguments[2])!
let front = NSWorkspace.shared.frontmostApplication
let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]] ?? []
let own = windows.enumerated().compactMap { index, w -> [String: Any]? in
  guard (w[kCGWindowOwnerPID as String] as? Int) == pid else { return nil }
  return ["zIndex": index, "id": w[kCGWindowNumber as String] ?? 0, "layer": w[kCGWindowLayer as String] ?? 0]
}
let external = windows.firstIndex { ($0[kCGWindowOwnerPID as String] as? Int) == coverPID && ($0[kCGWindowLayer as String] as? Int) == 0 }
let result: [String: Any] = ["frontPID": Int(front?.processIdentifier ?? 0), "coverNormalWindowZIndex": external ?? -1, "probeWindows": own]
print(String(data: try! JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]), encoding: .utf8)!)
