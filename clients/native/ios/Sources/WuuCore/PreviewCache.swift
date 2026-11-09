import Foundation

/// Bounded, coalesced preview work. Offscreen consumers release their place immediately.
@MainActor public final class PreviewCache<Value: Sendable> {
    private struct Request {
        let id = UUID()
        let load: @MainActor () async throws -> Value
        var waiters: [UUID: CheckedContinuation<Value, Error>]
        var task: Task<Void, Never>?
    }
    private let concurrency: Int
    private let capacity: Int
    private var cache: [String: Value] = [:]
    private var order: [String] = []
    private var requests: [String: Request] = [:]
    private var queue: [String] = []
    private var active: Set<UUID> = []

    public init(concurrency: Int = 2, capacity: Int = 48) {
        precondition(concurrency > 0 && capacity > 0)
        self.concurrency = concurrency; self.capacity = capacity
    }
    public func clear() {
        cache.removeAll(); order.removeAll(); queue.removeAll()
        let previous = requests; requests.removeAll()
        for request in previous.values {
            request.task?.cancel()
            for waiter in request.waiters.values { waiter.resume(throwing: CancellationError()) }
        }
    }
    public func value(for key: String, load: @escaping @MainActor () async throws -> Value) async throws -> Value {
        let waiter = UUID()
        return try await withTaskCancellationHandler {
            try Task.checkCancellation()
            if let value = cache[key] { touch(key); return value }
            return try await withCheckedThrowingContinuation { continuation in
                if requests[key] != nil { requests[key]?.waiters[waiter] = continuation }
                else {
                    requests[key] = Request(load: load, waiters: [waiter: continuation])
                    queue.append(key)
                }
                startNext()
            }
        } onCancel: { Task { @MainActor in self.cancel(key: key, waiter: waiter) } }
    }
    private func cancel(key: String, waiter: UUID) {
        guard let continuation = requests[key]?.waiters.removeValue(forKey: waiter) else { return }
        continuation.resume(throwing: CancellationError())
        if requests[key]?.waiters.isEmpty == true {
            requests.removeValue(forKey: key)?.task?.cancel()
            queue.removeAll { $0 == key }
        }
    }
    private func startNext() {
        while active.count < concurrency, !queue.isEmpty {
            let key = queue.removeFirst()
            guard let request = requests[key] else { continue }
            active.insert(request.id)
            requests[key]?.task = Task {
                let result: Result<Value, Error>
                do { result = .success(try await request.load()) }
                catch { result = .failure(error) }
                finish(key: key, id: request.id, result: result)
            }
        }
    }
    private func finish(key: String, id: UUID, result: Result<Value, Error>) {
        active.remove(id)
        if requests[key]?.id == id, let request = requests.removeValue(forKey: key) {
            if case .success(let value) = result {
                cache[key] = value; touch(key)
                if order.count > capacity { cache[order.removeFirst()] = nil }
            }
            for waiter in request.waiters.values { waiter.resume(with: result) }
        }
        startNext()
    }
    private func touch(_ key: String) { order.removeAll { $0 == key }; order.append(key) }
}
