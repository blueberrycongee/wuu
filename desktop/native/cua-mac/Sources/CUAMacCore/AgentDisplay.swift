import AppKit
import CoreGraphics
import CUAVirtualDisplay
import Darwin
import Foundation

// A window moved beyond every display is clamped back by the WindowServer, so
// a sliver stays visible and the window adopts that display's backing scale.
// Parked windows instead live on one invisible display shared by every helper
// process: they stay fully rendered while no physical screen shows them.
//
// A detached daemon owns the display. Helpers hold flocked lease files while
// they keep windows parked; the daemon restores windows from abandoned
// restoration journals and exits after an idle period, which removes the
// display. If the daemon dies, macOS moves the windows back to a real display.
enum AgentDisplay {
    static let vendorID: UInt32 = 0x7775
    static let productID: UInt32 = 0xC0A1
    static let serialNumber: UInt32 = 1
    static let daemonArgument = "--agent-display"

    static var directory: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Wuu/CUA/agent-display")
    }

    fileprivate static var readyFile: URL { directory.appendingPathComponent("ready") }
    fileprivate static var daemonLockFile: URL { directory.appendingPathComponent("daemon.lock") }

    static func activeDisplayID() -> CGDirectDisplayID? {
        activeDisplays().first(where: isAgentDisplay)
    }

    fileprivate static func activeDisplays() -> [CGDirectDisplayID] {
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { return [] }
        var displays = [CGDirectDisplayID](repeating: 0, count: Int(count))
        guard CGGetActiveDisplayList(count, &displays, &count) == .success else { return [] }
        return Array(displays.prefix(Int(count)))
    }

    fileprivate static func isAgentDisplay(_ display: CGDirectDisplayID) -> Bool {
        CGDisplayVendorNumber(display) == vendorID
            && CGDisplayModelNumber(display) == productID
            && CGDisplaySerialNumber(display) == serialNumber
    }

    fileprivate static func createDirectory() throws {
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
    }

    /// The display that is ready for parking: online and configured by the daemon.
    fileprivate static func readyDisplayID() -> CGDirectDisplayID? {
        guard let active = activeDisplayID(),
              let saved = try? String(contentsOf: readyFile, encoding: .utf8),
              UInt32(saved.trimmingCharacters(in: .whitespacesAndNewlines)) == active else { return nil }
        return active
    }
}

/// Keeps the shared agent display alive while held. Immutable, so it can move
/// between the capture task and the main actor.
final class AgentDisplayLease: @unchecked Sendable {
    let displayID: CGDirectDisplayID
    private let descriptor: Int32
    private let file: URL

    private init(displayID: CGDirectDisplayID, descriptor: Int32, file: URL) {
        self.displayID = displayID
        self.descriptor = descriptor
        self.file = file
    }

    deinit {
        try? FileManager.default.removeItem(at: file)
        flock(descriptor, LOCK_UN)
        close(descriptor)
    }

    /// Blocks until the daemon has configured the display. Call off the main
    /// actor; the wait covers daemon launch and the WindowServer reconfiguration.
    static func acquire(timeout: TimeInterval = 5) throws -> AgentDisplayLease {
        guard CUAVirtualDisplay.isSupported() else {
            throw ComputerError.unsupported("this macOS release does not provide the virtual display used to park windows")
        }
        try AgentDisplay.createDirectory()
        // Lock under a name the daemon ignores, then publish it, so the daemon
        // never mistakes a lease created but not yet locked for an abandoned one.
        let name = UUID().uuidString
        let pending = AgentDisplay.directory.appendingPathComponent(name + ".pending")
        let file = AgentDisplay.directory.appendingPathComponent(name + ".lease")
        let descriptor = open(pending.path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard descriptor >= 0 else { throw ComputerError.operationFailed("could not create an agent display lease") }
        guard flock(descriptor, LOCK_EX | LOCK_NB) == 0, rename(pending.path, file.path) == 0 else {
            try? FileManager.default.removeItem(at: pending)
            close(descriptor)
            throw ComputerError.operationFailed("could not lock an agent display lease")
        }
        let deadline = Date().addingTimeInterval(timeout)
        var launched = false
        while Date() < deadline {
            if let display = AgentDisplay.readyDisplayID() {
                return AgentDisplayLease(displayID: display, descriptor: descriptor, file: file)
            }
            if !launched, daemonIsAbsent() {
                launched = true
                try? launchDaemon()
            }
            // CoreGraphics learns about the new display through main run loop
            // notifications; blocking the main thread would hide it forever.
            if Thread.isMainThread {
                RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
            } else {
                usleep(50_000)
            }
        }
        try? FileManager.default.removeItem(at: file)
        flock(descriptor, LOCK_UN)
        close(descriptor)
        throw ComputerError.operationFailed("the agent display did not become ready")
    }

    var isValid: Bool { AgentDisplay.readyDisplayID() == displayID }

    var bounds: CGRect { CGDisplayBounds(displayID) }

    /// Windows cascade so none is fully covered by another parked window;
    /// apps throttle rendering of fully occluded windows.
    func parkingOrigin(index: Int) -> CGPoint {
        let display = bounds
        let step = CGFloat(index % 16) * 32
        return CGPoint(x: display.minX + 24 + step, y: display.minY + 24 + step)
    }

    func holds(_ frame: CGRect) -> Bool {
        let visible = bounds.intersection(frame)
        return !visible.isNull && visible.width * visible.height >= frame.width * frame.height * 0.98
    }

    private static func daemonIsAbsent() -> Bool {
        let descriptor = open(AgentDisplay.daemonLockFile.path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard descriptor >= 0 else { return false }
        defer { close(descriptor) }
        guard flock(descriptor, LOCK_EX | LOCK_NB) == 0 else { return false }
        flock(descriptor, LOCK_UN)
        return true
    }

    private static func launchDaemon() throws {
        guard let executable = Bundle.main.executablePath else {
            throw ComputerError.operationFailed("could not resolve the helper executable")
        }
        var attributes: posix_spawnattr_t?
        posix_spawnattr_init(&attributes)
        defer { posix_spawnattr_destroy(&attributes) }
        // A new session survives the parent's process group teardown; closing
        // inherited descriptors keeps the daemon off the MCP stdio pipes.
        posix_spawnattr_setflags(&attributes, Int16(POSIX_SPAWN_SETSID | POSIX_SPAWN_CLOEXEC_DEFAULT))
        var actions: posix_spawn_file_actions_t?
        posix_spawn_file_actions_init(&actions)
        defer { posix_spawn_file_actions_destroy(&actions) }
        let log = AgentDisplay.directory.appendingPathComponent("daemon.log").path
        posix_spawn_file_actions_addopen(&actions, 0, "/dev/null", O_RDONLY, 0)
        posix_spawn_file_actions_addopen(&actions, 1, "/dev/null", O_WRONLY, 0)
        posix_spawn_file_actions_addopen(&actions, 2, log, O_WRONLY | O_CREAT | O_TRUNC, S_IRUSR | S_IWUSR)
        let arguments = [executable, AgentDisplay.daemonArgument]
        var argv: [UnsafeMutablePointer<CChar>?] = arguments.map { strdup($0) } + [nil]
        defer { for argument in argv { free(argument) } }
        var processID: pid_t = 0
        let result = posix_spawn(&processID, executable, &actions, &attributes, &argv, environ)
        guard result == 0 else { throw ComputerError.operationFailed("could not launch the agent display daemon") }
        // Reap the daemon whenever it exits so a long-lived helper keeps no zombie.
        let daemonProcessID = processID
        DispatchQueue.global(qos: .utility).async {
            var status: Int32 = 0
            waitpid(daemonProcessID, &status, 0)
        }
    }
}

/// The agent display touches the real arrangement only at the outer corner of
/// the bottom-right display, so the pointer cannot wander onto it along an edge.
func agentDisplayOrigin(realDisplays: [CGRect]) -> CGPoint {
    guard let corner = realDisplays.max(by: { $0.maxX + $0.maxY < $1.maxX + $1.maxY }) else { return .zero }
    return CGPoint(x: corner.maxX, y: corner.maxY)
}

private func agentDisplayLog(_ message: String) {
    FileHandle.standardError.write(Data("[agent-display] \(message)\n".utf8))
}

@MainActor
private final class AgentDisplayDaemon {
    private let display: CUAVirtualDisplay
    private let idleLifetime: TimeInterval = 60
    private var idleSince: Date?

    init(display: CUAVirtualDisplay) { self.display = display }

    func tick() {
        WindowRestorationJournal.recoverAbandoned(using: restoreWindow)
        guard AgentDisplay.activeDisplays().contains(display.displayID) else {
            agentDisplayLog("display went offline")
            exit(0)
        }
        guard liveAgentDisplayLeases() == 0 else {
            idleSince = nil
            return
        }
        let now = Date()
        let since = idleSince ?? now
        idleSince = since
        if now.timeIntervalSince(since) >= idleLifetime {
            try? FileManager.default.removeItem(at: AgentDisplay.readyFile)
            // Acquirers publish their locked lease before reading ready. Once
            // readiness is withdrawn, a final scan either sees every helper
            // that could have received this display or makes new helpers wait
            // for the next daemon instead of accepting a disappearing display.
            guard !FileManager.default.fileExists(atPath: AgentDisplay.readyFile.path) else { return }
            if liveAgentDisplayLeases() > 0 {
                idleSince = nil
                do {
                    try String(display.displayID).write(to: AgentDisplay.readyFile, atomically: true, encoding: .utf8)
                } catch {
                    agentDisplayLog("could not restore display readiness")
                    exit(1)
                }
                return
            }
            agentDisplayLog("idle; removing display")
            exit(0)
        }
    }
}

@MainActor
public func runAgentDisplayDaemon() -> Never {
    let log = agentDisplayLog
    do { try AgentDisplay.createDirectory() } catch { exit(1) }
    let lock = open(AgentDisplay.daemonLockFile.path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
    guard lock >= 0, flock(lock, LOCK_EX | LOCK_NB) == 0 else { exit(0) }
    try? FileManager.default.removeItem(at: AgentDisplay.readyFile)

    let realDisplays = AgentDisplay.activeDisplays().filter { !AgentDisplay.isAgentDisplay($0) }
    let realBounds = realDisplays.map { CGDisplayBounds($0) }
    let largest = realBounds.reduce(CGSize(width: 1440, height: 900)) {
        CGSize(width: max($0.width, $1.width), height: max($0.height, $1.height))
    }
    guard let display = CUAVirtualDisplay(
        name: "Wuu Agent Display",
        pointWidth: UInt32(min(largest.width, 2560).rounded()),
        pointHeight: UInt32(min(largest.height, 1600).rounded()),
        vendorID: AgentDisplay.vendorID,
        productID: AgentDisplay.productID,
        serialNumber: AgentDisplay.serialNumber,
        terminationHandler: {
            try? FileManager.default.removeItem(at: AgentDisplay.readyFile)
            exit(0)
        }
    ) else {
        log("virtual display creation failed")
        exit(1)
    }
    let displayID = display.displayID
    for _ in 0..<40 where !AgentDisplay.activeDisplays().contains(displayID) {
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.05))
    }

    // macOS inserts a new display beside the main one and shifts the others.
    // Put every real display back and move the agent display to the corner.
    // Session scope lets the WindowServer remember the placement, so a later
    // daemon in this login session does not shift the real displays again.
    var configuration: CGDisplayConfigRef?
    guard CGBeginDisplayConfiguration(&configuration) == .success, let configuration else {
        log("display arrangement could not begin")
        exit(1)
    }
    for (real, bounds) in zip(realDisplays, realBounds) {
        guard CGConfigureDisplayOrigin(configuration, real, Int32(bounds.minX), Int32(bounds.minY)) == .success else {
            CGCancelDisplayConfiguration(configuration)
            log("display arrangement failed")
            exit(1)
        }
    }
    let origin = agentDisplayOrigin(realDisplays: realBounds)
    guard CGConfigureDisplayOrigin(configuration, displayID, Int32(origin.x), Int32(origin.y)) == .success else {
        CGCancelDisplayConfiguration(configuration)
        log("agent display placement failed")
        exit(1)
    }
    guard CGCompleteDisplayConfiguration(configuration, .forSession) == .success else {
        log("display arrangement failed")
        exit(1)
    }
    do {
        try String(displayID).write(to: AgentDisplay.readyFile, atomically: true, encoding: .utf8)
    } catch {
        log("could not publish display readiness")
        exit(1)
    }
    log("ready display=\(displayID) bounds=\(CGDisplayBounds(displayID))")

    let daemon = AgentDisplayDaemon(display: display)
    Task { @MainActor in
        while true {
            try? await Task.sleep(for: .seconds(1))
            daemon.tick()
        }
    }
    NSApplication.shared.run()
    exit(0)
}

/// Counts leases held by live processes and removes abandoned lease files.
private func liveAgentDisplayLeases() -> Int {
    let files = (try? FileManager.default.contentsOfDirectory(at: AgentDisplay.directory, includingPropertiesForKeys: nil)) ?? []
    var live = 0
    for file in files where file.pathExtension == "lease" {
        let descriptor = open(file.path, O_RDWR)
        guard descriptor >= 0 else { continue }
        defer { close(descriptor) }
        if flock(descriptor, LOCK_EX | LOCK_NB) == 0 {
            try? FileManager.default.removeItem(at: file)
            flock(descriptor, LOCK_UN)
        } else {
            live += 1
        }
    }
    return live
}
