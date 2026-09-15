import CoreGraphics
import Darwin
import Foundation

public enum ForegroundInputLock {
    private static let path = "/tmp/wuu-cua-foreground-input-\(getuid()).lock"

    public static func withLock<T>(_ body: () throws -> T) throws -> T {
        let descriptor = open(path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard descriptor >= 0 else {
            throw ComputerError.operationFailed("could not open the global foreground input lock")
        }
        defer { close(descriptor) }
        try acquireCancellableLock(descriptor)
        defer { flock(descriptor, LOCK_UN) }
        return try body()
    }
}

public final class AppActionLock {
    private var descriptor: Int32

    private init(descriptor: Int32) { self.descriptor = descriptor }

    public static func acquire(processID: pid_t) throws -> AppActionLock {
        let path = "/tmp/wuu-cua-app-\(getuid())-\(processID).lock"
        let descriptor = open(path, O_CREAT | O_RDWR, S_IRUSR | S_IWUSR)
        guard descriptor >= 0 else {
            if descriptor >= 0 { close(descriptor) }
            throw ComputerError.operationFailed("could not acquire the target app action lock")
        }
        do { try acquireCancellableLock(descriptor) } catch { close(descriptor); throw error }
        return AppActionLock(descriptor: descriptor)
    }

    deinit {
        if descriptor >= 0 {
            flock(descriptor, LOCK_UN)
            close(descriptor)
            descriptor = -1
        }
    }
}

let wuuSyntheticEventMarker: Int64 = 0x575555435541

func markSynthetic(_ event: CGEvent?) -> CGEvent? {
    event?.setIntegerValueField(.eventSourceUserData, value: wuuSyntheticEventMarker)
    return event
}

func syntheticEventSource() -> CGEventSource? {
    let source = CGEventSource(stateID: .hidSystemState)
    source?.localEventsSuppressionInterval = 0
    return source
}

public struct KeyModifiers: OptionSet, Sendable {
    public let rawValue: UInt64
    public init(rawValue: UInt64) { self.rawValue = rawValue }

    public static let command = KeyModifiers(rawValue: 1 << 0)
    public static let shift = KeyModifiers(rawValue: 1 << 1)
    public static let option = KeyModifiers(rawValue: 1 << 2)
    public static let control = KeyModifiers(rawValue: 1 << 3)
    public static let function = KeyModifiers(rawValue: 1 << 4)

    var eventFlags: CGEventFlags {
        var flags: CGEventFlags = []
        if contains(.command) { flags.insert(.maskCommand) }
        if contains(.shift) { flags.insert(.maskShift) }
        if contains(.option) { flags.insert(.maskAlternate) }
        if contains(.control) { flags.insert(.maskControl) }
        if contains(.function) { flags.insert(.maskSecondaryFn) }
        return flags
    }
}

public struct KeyChord: Sendable {
    public let keyCode: CGKeyCode
    public let modifiers: KeyModifiers

    public static func parse(_ raw: String) throws -> KeyChord {
        let keyPart: String
        let modifierParts: [String]
        if raw == "+" {
            keyPart = "+"
            modifierParts = []
        } else if raw.hasSuffix("+") {
            keyPart = "+"
            modifierParts = raw.dropLast().split(separator: "+", omittingEmptySubsequences: true).map(String.init)
        } else {
            let parts = raw.split(separator: "+", omittingEmptySubsequences: true).map(String.init)
            guard let last = parts.last, !last.isEmpty else {
                throw ComputerError.invalidArguments("key is required")
            }
            keyPart = last
            modifierParts = Array(parts.dropLast())
        }
        guard !keyPart.isEmpty else {
            throw ComputerError.invalidArguments("key is required")
        }
        var modifiers: KeyModifiers = []
        for part in modifierParts {
            switch part.lowercased() {
            case "command", "cmd", "super", "meta": modifiers.insert(.command)
            case "shift": modifiers.insert(.shift)
            case "option", "alt": modifiers.insert(.option)
            case "control", "ctrl": modifiers.insert(.control)
            case "fn", "function": modifiers.insert(.function)
            default: throw ComputerError.invalidArguments("unsupported key modifier \(part)")
            }
        }
        if keyPart.count == 1, keyPart != keyPart.lowercased() {
            modifiers.insert(.shift)
        }
        let shiftedSymbols = [
            "~": "`", "!": "1", "@": "2", "#": "3", "$": "4", "%": "5",
            "^": "6", "&": "7", "*": "8", "(": "9", ")": "0", "_": "-",
            "+": "=", "{": "[", "}": "]", "|": "\\", ":": ";", "\"": "'",
            "<": ",", ">": ".", "?": "/",
        ]
        let normalized: String
        if let base = shiftedSymbols[keyPart] {
            modifiers.insert(.shift)
            normalized = base
        } else {
            normalized = keyPart.lowercased().replacingOccurrences(of: "_", with: "")
        }
        guard let code = keyCodes[normalized] else {
            throw ComputerError.invalidArguments("unsupported key \(keyPart)")
        }
        return KeyChord(keyCode: code, modifiers: modifiers)
    }
}

private let keyCodes: [String: CGKeyCode] = [
    "a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7,
    "c": 8, "v": 9, "b": 11, "q": 12, "w": 13, "e": 14, "r": 15,
    "y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21, "6": 22,
    "5": 23, "=": 24, "9": 25, "7": 26, "-": 27, "8": 28, "0": 29,
    "]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35, "return": 36,
    "enter": 36, "l": 37, "j": 38, "'": 39, "k": 40, ";": 41, "\\": 42,
    ",": 43, "/": 44, "n": 45, "m": 46, ".": 47, "tab": 48, "space": 49,
    "`": 50, "delete": 51, "backspace": 51, "escape": 53, "esc": 53,
    "home": 115, "end": 119, "pageup": 116, "pagedown": 121,
    "left": 123, "right": 124, "down": 125, "up": 126,
    "f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
    "f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
]

private func acquireCancellableLock(_ descriptor: Int32) throws {
    while flock(descriptor, LOCK_EX | LOCK_NB) != 0 {
        guard errno == EWOULDBLOCK || errno == EINTR else {
            throw ComputerError.operationFailed("could not acquire input lock")
        }
        try ComputerExecution.checkpoint()
        usleep(10_000)
    }
    do { try ComputerExecution.checkpoint() } catch { flock(descriptor, LOCK_UN); throw error }
}
