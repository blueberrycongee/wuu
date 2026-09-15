import Foundation

// The reader can revoke a request while the main thread is inside native input.
// Cancellation stops between complete input units; key/mouse releases still run.
public final class ComputerExecution: @unchecked Sendable {
    private let lock = NSLock()
    private var cancelled = false
    private var units = 0
    private var delivered = false
    private var inputGuard: (() throws -> Void)?
    private let deadline: Date
    private static let threadKey = "wuu.cua.execution"

    public init(timeout: TimeInterval = 60) { deadline = Date().addingTimeInterval(timeout) }

    public func cancel() { lock.lock(); cancelled = true; lock.unlock() }

    public func check() throws {
        lock.lock()
        let stopped = cancelled
        lock.unlock()
        if stopped { throw ComputerError.cancelled("request cancelled; input may have partially executed") }
        if Date() >= deadline { throw ComputerError.cancelled("request deadline exceeded; input may have partially executed") }
    }

    public func beginInput() throws {
        try inputGuard?()
        lock.lock()
        defer { lock.unlock() }
        guard !cancelled, Date() < deadline else { throw ComputerError.cancelled("input interrupted; do not replay without observing") }
        units += 1
    }

    func finishInput() { lock.lock(); delivered = true; lock.unlock() }

    public var evidence: [String: Any] {
        lock.lock(); defer { lock.unlock() }
        return ["delivery": delivered ? "delivered" : units == 0 ? "not_delivered" : "unknown", "input_units_attempted": units,
                "verification": "not_requested"]
    }

    public func run<T>(_ body: () throws -> T) rethrows -> T {
        let previous = Thread.current.threadDictionary[Self.threadKey]
        Thread.current.threadDictionary[Self.threadKey] = self
        defer { Thread.current.threadDictionary[Self.threadKey] = previous }
        return try body()
    }

    func withInputGuard<T>(_ guardInput: @escaping () throws -> Void, body: () throws -> T) rethrows -> T {
        let previous = inputGuard
        inputGuard = guardInput
        defer { inputGuard = previous }
        return try body()
    }

    static var current: ComputerExecution? { Thread.current.threadDictionary[threadKey] as? ComputerExecution }
    static func checkpoint() throws { try current?.check() }
    static func input() throws { try current?.beginInput() }
}

private struct RequestMessage: @unchecked Sendable { let value: [String: Any] }

// Native work is serialized on the supplied queue; notification handling never
// waits behind that work. Register before enqueueing so queued calls cancel too.
public final class MCPRequestQueue: @unchecked Sendable {
    private let lock = NSLock()
    private var pending: [String: ComputerExecution] = [:]
    private let server: MCPServer
    private let queue: DispatchQueue

    public init(backend: ComputerBackend, queue: DispatchQueue = .main) {
        self.server = MCPServer(backend: backend)
        self.queue = queue
    }

    public func submit(_ request: [String: Any], reply: @escaping @Sendable ([String: Any]) -> Void) {
        if request["method"] as? String == "notifications/cancelled" {
            if let params = request["params"] as? [String: Any], let id = params["requestId"] {
                lock.lock(); let execution = pending[Self.key(id)]; lock.unlock()
                execution?.cancel()
            }
            return
        }
        let execution = ComputerExecution()
        let key = request["id"].map(Self.key)
        lock.lock()
        if let key { pending[key] = execution }
        lock.unlock()
        let message = RequestMessage(value: request)
        queue.async { [self] in
            defer { lock.lock(); if let key { pending.removeValue(forKey: key) }; lock.unlock() }
            do {
                if let response = try server.handle(message.value, execution: execution) { reply(response) }
            } catch {
                reply(["jsonrpc": "2.0", "id": message.value["id"] ?? NSNull(),
                       "error": ["code": -32603, "message": error.localizedDescription]])
            }
        }
    }

    public func shutdown(completion: @escaping @Sendable () -> Void) {
        lock.lock(); for execution in pending.values { execution.cancel() }; lock.unlock()
        queue.async { [self] in server.shutdown(); completion() }
    }

    private static func key(_ id: Any) -> String {
        if let number = id as? NSNumber { return "n:\(number)" }
        return "s:\(id)"
    }
}
