import XCTest
@testable import WuuCore

@MainActor final class PreviewCacheTests: XCTestCase {
    private final class Loads {
        var starts: [String] = []
        var pending: [String: CheckedContinuation<Int, Error>] = [:]
        func read(_ key: String) async throws -> Int {
            starts.append(key)
            return try await withCheckedThrowingContinuation { pending[key] = $0 }
        }
        func finish(_ key: String, _ value: Int) { pending.removeValue(forKey: key)?.resume(returning: value) }
    }

    func testCoalescesConsumersAndCancelsOnlyTheDepartingConsumer() async throws {
        let cache = PreviewCache<Int>(concurrency: 1), loads = Loads()
        let started = expectation(description: "first transfer started")
        let first = Task { try await cache.value(for: "a") { started.fulfill(); return try await loads.read("a") } }
        await fulfillment(of: [started], timeout: 2)
        let entered = expectation(description: "second consumer entered")
        let second = Task { entered.fulfill(); return try await cache.value(for: "a") { XCTFail("Duplicate transfer"); return 2 } }
        await fulfillment(of: [entered], timeout: 2)
        first.cancel()
        do { _ = try await first.value; XCTFail("Cancelled consumer returned a value") } catch is CancellationError { }
        loads.finish("a", 42)
        let result = try await second.value
        XCTAssertEqual(result, 42)
        XCTAssertEqual(loads.starts, ["a"])
        let cached = try await cache.value(for: "a") { XCTFail("Cache miss"); return 0 }
        XCTAssertEqual(cached, 42)
    }

    func testCancelledQueuedWorkDoesNotConsumeTransferSlot() async throws {
        let cache = PreviewCache<Int>(concurrency: 1), loads = Loads()
        let started = expectation(description: "active transfer")
        let first = Task { try await cache.value(for: "a") { started.fulfill(); return try await loads.read("a") } }
        await fulfillment(of: [started], timeout: 2)
        let queued = expectation(description: "queued consumer entered")
        let second = Task { queued.fulfill(); return try await cache.value(for: "b") { XCTFail("Offscreen transfer ran"); return 2 } }
        await fulfillment(of: [queued], timeout: 2)
        second.cancel()
        do { _ = try await second.value; XCTFail("Cancelled queue returned") } catch is CancellationError { }
        loads.finish("a", 1)
        _ = try await first.value
        let next = try await cache.value(for: "c") { 3 }
        XCTAssertEqual(next, 3)
        XCTAssertEqual(loads.starts, ["a"])
    }

    func testClearRejectsOldResultAndLRUEvictsLeastRecentlyUsedPreview() async throws {
        let cache = PreviewCache<Int>(concurrency: 1, capacity: 2), loads = Loads()
        let started = expectation(description: "old transfer")
        let old = Task { try await cache.value(for: "a") { started.fulfill(); return try await loads.read("old") } }
        await fulfillment(of: [started], timeout: 2)
        cache.clear()
        do { _ = try await old.value; XCTFail("Cleared consumer returned") } catch is CancellationError { }
        loads.finish("old", 99)
        let current = try await cache.value(for: "a") { 1 }
        XCTAssertEqual(current, 1)
        _ = try await cache.value(for: "b") { 2 }
        _ = try await cache.value(for: "a") { XCTFail("Recently used preview lost"); return 0 }
        _ = try await cache.value(for: "c") { 3 }
        let evicted = try await cache.value(for: "b") { 4 }
        XCTAssertEqual(evicted, 4)
    }
}
