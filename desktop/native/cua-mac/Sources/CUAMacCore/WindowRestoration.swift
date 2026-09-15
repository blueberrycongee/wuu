import AppKit
import ApplicationServices
import Darwin
import Foundation

struct WindowRestorationRecord: Codable, Equatable {
    let processID: Int32
    let launchTime: TimeInterval
    let windowID: UInt32
    let identifier: String
    let original: CGRect
    let parked: CGRect
}

// Each helper owns a locked journal. A successor only recovers unlocked journals,
// so one helper cannot reveal windows that another live helper is still using.
final class WindowRestorationJournal {
    let directory: URL
    private let file: URL
    private let lockFile: URL
    private let descriptor: Int32
    private(set) var records: [WindowRestorationRecord] = []

    static var defaultDirectory: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent("Wuu/CUA/restoration")
    }

    init(directory: URL = WindowRestorationJournal.defaultDirectory) throws {
        self.directory = directory
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        file = directory.appendingPathComponent(UUID().uuidString + ".json")
        lockFile = file.appendingPathExtension("lock")
        descriptor = open(lockFile.path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard descriptor >= 0, flock(descriptor, LOCK_EX | LOCK_NB) == 0 else {
            if descriptor >= 0 { close(descriptor) }
            throw ComputerError.operationFailed("could not lock the window restoration journal")
        }
    }

    deinit {
        flock(descriptor, LOCK_UN)
        close(descriptor)
        if records.isEmpty { try? FileManager.default.removeItem(at: lockFile) }
    }

    func finish() { if records.isEmpty { try? FileManager.default.removeItem(at: lockFile) } }

    func record(_ record: WindowRestorationRecord) throws {
        guard !records.contains(where: { $0.processID == record.processID && $0.windowID == record.windowID }) else { return }
        let next = records + [record]
        try Self.persist(next, at: file)
        records = next
    }

    func discard(processID: Int32, windowID: UInt32) throws {
        let next = records.filter { $0.processID != processID || $0.windowID != windowID }
        try Self.persist(next, at: file)
        records = next
    }

    func updateParked(processID: Int32, windowID: UInt32, frame: CGRect) throws {
        let next = records.map { record in
            record.processID == processID && record.windowID == windowID
                ? WindowRestorationRecord(processID: record.processID, launchTime: record.launchTime, windowID: record.windowID,
                    identifier: record.identifier, original: record.original, parked: frame)
                : record
        }
        try Self.persist(next, at: file)
        records = next
    }

    func restore(processID: Int32? = nil, using restore: (WindowRestorationRecord) -> Bool) throws -> Int {
        var count = 0
        let next = records.filter { record in
            if let processID, record.processID != processID { return true }
            if restore(record) { count += 1; return false }
            return true
        }
        try Self.persist(next, at: file)
        records = next
        return count
    }

    func recoverAbandoned(using restore: (WindowRestorationRecord) -> Bool) {
        guard let files = try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil) else { return }
        for candidate in files where candidate.pathExtension == "json" && candidate != file {
            let lockPath = candidate.appendingPathExtension("lock")
            let fd = open(lockPath.path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
            guard fd >= 0 else { continue }
            defer { close(fd) }
            guard flock(fd, LOCK_EX | LOCK_NB) == 0 else { continue }
            defer { flock(fd, LOCK_UN) }
            guard let data = try? Data(contentsOf: candidate),
                  let saved = try? JSONDecoder().decode([WindowRestorationRecord].self, from: data) else { continue }
            let remaining = saved.filter { !restore($0) }
            try? Self.persist(remaining, at: candidate)
        }
    }

    // A confirmed user takeover supersedes a live helper's concealment. Wait
    // for its current app transaction, restore only this target, and leave its
    // locked journal intact so the owning helper can retire records normally.
    static func restoreForUser(processID: Int32) throws {
        let execution = ComputerExecution(timeout: 5)
        try execution.run {
            let appLock = try AppActionLock.acquire(processID: processID)
            defer { withExtendedLifetime(appLock) {} }
            _ = try appLock.advanceRevision()
            let files = (try? FileManager.default.contentsOfDirectory(at: defaultDirectory, includingPropertiesForKeys: nil)) ?? []
            for file in files where file.pathExtension == "json" {
                try execution.check()
                guard let data = try? Data(contentsOf: file), let records = try? JSONDecoder().decode([WindowRestorationRecord].self, from: data) else { continue }
                for record in records where record.processID == processID {
                    guard restoreWindow(record) else { throw ComputerError.operationFailed("could not restore the controlled window") }
                }
            }
        }
    }

    private static func persist(_ records: [WindowRestorationRecord], at file: URL) throws {
        if records.isEmpty {
            if FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
        } else {
            let data = try JSONEncoder().encode(records)
            try data.write(to: file, options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
        }
    }
}

func windowFrames(processID: pid_t) -> [CGWindowID: CGRect] {
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

// WindowServer identity + process incarnation determine which window may be
// restored. AX array order and titles are not identities; ambiguity retains the
// record for a later recovery instead of moving a different window.
func restoreWindow(_ record: WindowRestorationRecord) -> Bool {
    guard let app = NSRunningApplication(processIdentifier: record.processID), !app.isTerminated,
          app.launchDate?.timeIntervalSince1970 == record.launchTime else { return true }
    let current = windowFrames(processID: record.processID)[record.windowID]
    let application = AXUIElementCreateApplication(record.processID)
    let windows = axElements(application, attribute: kAXWindowsAttribute as String)
    // WindowServer geometry can lag a synchronous AX move. Resolve against both
    // the recorded parked frame and the current CG frame before retiring a record.
    let matches = windows.filter {
        guard let frame = axFrame($0) else { return false }
        return (frame == current || frame == record.parked) &&
            (record.identifier.isEmpty || axString($0, kAXIdentifierAttribute as String) == record.identifier)
    }
    guard matches.count == 1, let actual = axFrame(matches[0]) else { return windows.isEmpty && current == nil }
    if actual.origin == record.original.origin { return true }
    // A window moved elsewhere since concealment belongs to the user again.
    guard abs(actual.minX - record.parked.minX) < 2, abs(actual.minY - record.parked.minY) < 2 else { return true }
    var point = record.original.origin
    guard let value = AXValueCreate(.cgPoint, &point),
          AXUIElementSetAttributeValue(matches[0], kAXPositionAttribute as CFString, value) == .success else { return false }
    guard let restored = axFrame(matches[0]) else { return false }
    return abs(restored.minX - point.x) < 2 && abs(restored.minY - point.y) < 2
}
