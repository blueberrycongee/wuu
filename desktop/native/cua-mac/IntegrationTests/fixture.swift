import AppKit
import Darwin
let application = NSApplication.shared
application.setActivationPolicy(.regular)
final class FixtureWindow: NSWindow { override func constrainFrameRect(_ frameRect: NSRect, to screen: NSScreen?) -> NSRect { frameRect } }
let window = FixtureWindow(contentRect: NSRect(x: 120, y: 150, width: 460, height: 240), styleMask: [.titled, .closable, .resizable], backing: .buffered, defer: false)
window.title = "Wuu CUA Fixture"
let field = NSTextField(frame: NSRect(x: 24, y: 130, width: 300, height: 28))
field.stringValue = "Initial"
field.setAccessibilityTitle("Name")
field.setAccessibilityIdentifier("fixture-name")
window.contentView!.addSubview(field)
let label = NSTextField(labelWithString: "A disposable window for CUA verification")
label.frame = NSRect(x: 24, y: 180, width: 410, height: 22)
window.contentView!.addSubview(label)
window.makeKeyAndOrderFront(nil)
signal(SIGUSR1, SIG_IGN)
let movement = DispatchSource.makeSignalSource(signal: SIGUSR1, queue: .main)
movement.setEventHandler { window.setFrameOrigin(NSPoint(x: window.frame.minX + 80, y: window.frame.minY)) }
movement.resume()
application.run()
