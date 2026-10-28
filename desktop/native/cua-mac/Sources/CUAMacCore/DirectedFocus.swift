import AppKit
import ApplicationServices
import CoreGraphics

// AppKit activation events describe focus to the receiving process without
// asking WindowServer to bring it forward. Keep that state across observations
// so a background menu can stay open until its next input. All callbacks run on
// the helper's main run loop; shutdown and retargeting restore native state.
final class DirectedFocus: @unchecked Sendable {
    private var application: NSRunningApplication?
    private var windowID: CGWindowID = 0
    private var tap: CFMachPort?
    private var source: CFRunLoopSource?
    private var expiry: Timer?
    private var epoch = ""
    private let runLoop = CFRunLoopGetCurrent()

    func reconcile(app: NSRunningApplication, epoch: String, foreground: Bool) {
        if foreground || application?.processIdentifier != app.processIdentifier || self.epoch != epoch {
            end()
        } else {
            expiry?.invalidate()
            expiry = nil
        }
        self.epoch = epoch
    }

    func begin(app: NSRunningApplication, windowID: CGWindowID) throws {
        guard NSWorkspace.shared.frontmostApplication?.processIdentifier != app.processIdentifier else { end(); return }
        if application?.processIdentifier != app.processIdentifier { end() }
        application = app
        self.windowID = windowID
        if tap == nil {
            tap = CGEvent.tapCreateForPid(pid: app.processIdentifier, place: .headInsertEventTap,
                options: .defaultTap, eventsOfInterest: CGEventMask(1) << NSEvent.EventType.appKitDefined.rawValue,
                callback: { _, type, event, refcon in
                    guard let refcon else { return Unmanaged.passUnretained(event) }
                    let focus = Unmanaged<DirectedFocus>.fromOpaque(refcon).takeUnretainedValue()
                    if type == .tapDisabledByTimeout {
                        if let tap = focus.tap { CGEvent.tapEnable(tap: tap, enable: true) }
                        return Unmanaged.passUnretained(event)
                    }
                    if type == .tapDisabledByUserInput { return Unmanaged.passUnretained(event) }
                    // A real foreground activation gives focus back to the user.
                    // Suppress only background deactivation while this lease lives.
                    if event.getIntegerValueField(.eventSourceUserData) != wuuSyntheticEventMarker,
                       let target = focus.application,
                       let native = NSEvent(cgEvent: event), native.type == .appKitDefined {
                        let foreground = NSWorkspace.shared.frontmostApplication?.processIdentifier == target.processIdentifier
                        if native.subtype == .applicationActivated, foreground { focus.end() }
                        if native.subtype == .applicationDeactivated, !foreground { return nil }
                    }
                    return Unmanaged.passUnretained(event)
                }, userInfo: Unmanaged.passUnretained(self).toOpaque())
            if let tap {
                source = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0)
                CFRunLoopAddSource(runLoop, source, .commonModes)
            }
        }
        try ComputerExecution.input()
        post(.applicationActivated, to: app.processIdentifier)
        do {
            // Activation is queued in the target process. Let it reach AppKit
            // before the first directed key or pointer event.
            try holdInput(for: 0.050)
        } catch {
            end()
            throw error
        }
    }

    func idle() {
        guard application != nil else { return }
        expiry?.invalidate()
        expiry = Timer(timeInterval: 30, repeats: false) { [weak self] _ in self?.end() }
        RunLoop.current.add(expiry!, forMode: .common)
    }

    func revoke(epoch: String) {
        guard self.epoch == epoch else { return }
        end()
    }

    func end() {
        expiry?.invalidate()
        expiry = nil
        if let source { CFRunLoopRemoveSource(runLoop, source, .commonModes) }
        if let tap { CFMachPortInvalidate(tap) }
        source = nil
        tap = nil
        if let app = application, !app.isTerminated,
           NSRunningApplication(processIdentifier: app.processIdentifier)?.launchDate == app.launchDate,
           NSWorkspace.shared.frontmostApplication?.processIdentifier != app.processIdentifier {
            post(.applicationDeactivated, to: app.processIdentifier)
        }
        application = nil
    }

    private func post(_ subtype: NSEvent.EventSubtype, to pid: pid_t) {
        let native = NSEvent.otherEvent(with: .appKitDefined, location: .zero, modifierFlags: [],
            timestamp: ProcessInfo.processInfo.systemUptime, windowNumber: Int(windowID), context: nil,
            subtype: subtype.rawValue, data1: 0, data2: 0)
        markSynthetic(native?.cgEvent)?.postToPid(pid)
    }

    deinit { end() }
}
