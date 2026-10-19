import Foundation
import zlib

public enum RemoteEvent: Sendable {
    case attached
    case disconnected(String)
    case notification(String, JSONValue)
    case state(JSONValue)
    case request(JSONValue)
    case snapshot(String, JSONValue)
}

/// Foreground transport. A reconnect uses a fresh app-server attachment and refetches state.
/// No uplink request is replayed after a disconnect or timeout.
public actor RemoteConnection {
    public nonisolated let events: AsyncStream<RemoteEvent>
    private let eventSink: AsyncStream<RemoteEvent>.Continuation
    private let identity: DeviceIdentity
    private let host: String
    private let relay: URL
    private let session: URLSession
    private var socket: URLSessionWebSocketTask?
    private var reader: Task<Void, Never>?
    private var heartbeat: Task<Void, Never>?
    private var channel: SecureChannel?
    private var handshake: PhoneHandshake?
    private var attached = false
    private var challenged = false
    private var authenticated = false
    private var epoch: UInt64 = 0
    private var received: Double = 0
    private var lastPong = Date()
    private var pending: [String: CheckedContinuation<JSONValue, Error>] = [:]
    private var snapshotTags: [String: String] = [:]
    private var deadlines: [String: Task<Void, Never>] = [:]
    public private(set) var supportsPreview = false
    private var previewActivity: PreviewActivity?
    private var previewID: String?
    private var previewSink: AsyncThrowingStream<PreviewEvent, Error>.Continuation?
    private var previewOpening: UUID?
    private var previewSequence = 0
    private var attachWaiter: CheckedContinuation<Void, Error>?

    public init(account: AccountSession, host: String) throws {
        let identity = try DeviceIdentity(seed: Data(base64URL: account.deviceSeed))
        guard identity.publicKey.base64URL == account.pub else { throw NativeError.invalid("Device identity mismatch") }
        var url = URLComponents(url: try AccountAPI.validateOrigin(account.server), resolvingAgainstBaseURL: false)!
        url.scheme = url.scheme == "https" ? "wss" : "ws"
        url.path = "/v1/connect"
        try self.init(identity: identity, host: host, relay: url.url!)
    }
    public init(computer: PairedComputer) throws {
        try self.init(identity: DeviceIdentity(seed: Data(base64URL: computer.deviceSeed)),
                      host: computer.hostPub, relay: validateRelay(computer.relayURL))
    }
    private init(identity: DeviceIdentity, host: String, relay: URL) throws {
        guard try Data(base64URL: host).count == 32 else { throw NativeError.invalid("Invalid computer identity") }
        self.identity = identity
        self.host = host
        self.relay = relay
        session = URLSession(configuration: .ephemeral, delegate: NoRedirect(), delegateQueue: nil)
        let stream = AsyncStream<RemoteEvent>.makeStream()
        events = stream.stream
        eventSink = stream.continuation
    }
    deinit {
        reader?.cancel()
        heartbeat?.cancel()
        socket?.cancel(with: .goingAway, reason: nil)
        session.invalidateAndCancel()
        eventSink.finish()
    }
    public func connect() async throws {
        disconnect()
        epoch &+= 1
        let stamp = epoch
        let ws = session.webSocketTask(with: relay)
        ws.maximumMessageSize = 8 * 1024 * 1024
        socket = ws
        ws.resume()
        reader = Task { [weak self] in
            do {
                while !Task.isCancelled {
                    let message = try await ws.receive()
                    let data: Data
                    switch message {
                    case .data(let value): data = value
                    case .string(let value): data = Data(value.utf8)
                    @unknown default: throw NativeError.invalid("Unsupported websocket message")
                    }
                    let value = try JSONDecoder().decode(JSONValue.self, from: data)
                    try await self?.handle(value, epoch: stamp)
                }
            } catch { await self?.failed(error, epoch: stamp) }
        }
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                attachWaiter = continuation
                deadlines["attach"] = Task { [weak self] in
                    do { try await Task.sleep(for: .seconds(20)) } catch { return }
                    await self?.failed(NativeError.invalid("Computer connection timed out"), epoch: stamp)
                }
                Task {
                    do {
                        guard self.epoch == stamp else { return }
                        try await self.send(["type": "hello", "proto": 1, "role": "phone",
                                             "pub": .string(self.identity.publicKey.base64URL), "to": .string(self.host)])
                    } catch { self.failed(error, epoch: stamp) }
                }
            }
        } onCancel: { Task { await self.failed(CancellationError(), epoch: stamp) } }
    }
    public func disconnect() {
        epoch &+= 1
        reader?.cancel(); reader = nil
        heartbeat?.cancel(); heartbeat = nil
        socket?.cancel(with: .goingAway, reason: nil); socket = nil
        channel = nil; handshake = nil; attached = false; received = 0
        supportsPreview = false
        finishPreview(NativeError.invalid("电脑连接已断开"))
        challenged = false; authenticated = false
        for task in deadlines.values { task.cancel() }
        deadlines.removeAll()
        let error = NativeError.invalid("Connection lost. A sent request may already have reached the computer; check before retrying.")
        for continuation in pending.values { continuation.resume(throwing: error) }
        pending.removeAll()
        snapshotTags.removeAll()
        attachWaiter?.resume(throwing: error); attachWaiter = nil
    }
    private func failed(_ error: Error, epoch stamp: UInt64) {
        guard epoch == stamp else { return }
        disconnect()
        eventSink.yield(.disconnected(error.localizedDescription))
    }
    private func send(_ value: JSONValue) async throws {
        guard let socket else { throw NativeError.invalid("Not connected") }
        let data = try JSONEncoder().encode(value)
        try await socket.send(.string(String(decoding: data, as: UTF8.self)))
    }
    private func frame(kind: UInt8, body: Data) async throws {
        try await send(["type": "frame", "to": .string(host), "payload": .string((Data([kind]) + body).base64URL)])
    }
    private func sealed(_ value: JSONValue) async throws {
        guard let channel else { throw NativeError.invalid("Computer is not authenticated") }
        try await frame(kind: 2, body: channel.seal(JSONEncoder().encode(value)))
    }
    private func handle(_ value: JSONValue, epoch stamp: UInt64) async throws {
        guard epoch == stamp else { return }
        switch value["type"].string {
        case "challenge":
            guard !challenged else { throw NativeError.invalid("Repeated relay challenge") }
            challenged = true
            let nonce = try Data(base64URL: value["nonce"].string ?? "")
            guard nonce.count == 32 else { throw NativeError.invalid("Invalid relay challenge") }
            try await send(["type": "auth", "sig": .string(identity.relayProof(nonce: nonce))])
        case "auth_ok":
            guard challenged, !authenticated else { throw NativeError.invalid("Unexpected relay authentication") }
            authenticated = true
            if value["online"].bool { try await startHandshake() }
        case "presence":
            guard authenticated else { return }
            guard value["pub"].string == host else { return }
            if value["online"].bool, handshake == nil, channel == nil { try await startHandshake() }
            else if !value["online"].bool { throw NativeError.invalid("Computer is offline") }
        case "frame":
            guard authenticated else { throw NativeError.invalid("Unauthenticated relay frame") }
            let payload = try Data(base64URL: value["payload"].string ?? "")
            guard let kind = payload.first else { throw NativeError.invalid("Empty relay frame") }
            if kind == 1, let handshake {
                let body = Data(payload.dropFirst())
                let envelope = try JSONDecoder().decode(JSONValue.self, from: body)
                guard envelope["t"].string == "hs2" else { throw NativeError.invalid("Unexpected handshake reply") }
                channel = try handshake.finish(JSONDecoder().decode(HandshakeReply.self, from: body))
                self.handshake = nil
                try await sealed(["t": "attach", "client_profile": "mobile_activity", "accept_line_compression": "gzip", "preview_v1": true])
            } else if kind == 2, let channel {
                let message = try JSONDecoder().decode(JSONValue.self, from: channel.open(Data(payload.dropFirst())))
                try await handleSealed(message, epoch: stamp)
            }
        case "deliver_err", "error": throw NativeError.invalid(value["msg"].string ?? value["code"].string ?? "Relay rejected connection")
        default: break
        }
    }
    private func startHandshake() async throws {
        let hs = try PhoneHandshake(identity: identity, host: Data(base64URL: host))
        handshake = hs
        try await frame(kind: 1, body: JSONEncoder().encode(hs.offer()))
    }
    private func handleSealed(_ value: JSONValue, epoch stamp: UInt64) async throws {
        switch value["t"].string {
        case "attached":
            guard !attached else { throw NativeError.invalid("Repeated attachment") }
            attached = true
            supportsPreview = value["preview_v1"].bool
            received = 0
            deadlines.removeValue(forKey: "attach")?.cancel()
            attachWaiter?.resume(); attachWaiter = nil
            eventSink.yield(.attached)
            lastPong = Date()
            heartbeat = Task { [weak self] in
                while !Task.isCancelled {
                    do {
                        try await Task.sleep(for: .seconds(15))
                        try await self?.ping(epoch: stamp)
                    } catch {
                        if !Task.isCancelled { await self?.failed(error, epoch: stamp) }
                        return
                    }
                }
            }
        case "rpc":
            guard attached, let sequence = value["seq"].number, sequence > received else { return }
            let line = try decodeRPCLine(value)
            if let method = line["method"].string {
                if line["id"] != .null { eventSink.yield(.request(line)) }
                else { eventSink.yield(.notification(method, line["params"])) }
            } else if let id = line["id"].string, let continuation = pending.removeValue(forKey: id) {
                deadlines.removeValue(forKey: id)?.cancel()
                let tag = snapshotTags.removeValue(forKey: id)
                if line["error"] != .null { continuation.resume(throwing: NativeError.invalid(line["error"]["message"].string ?? "Computer request failed")) }
                else {
                    if let tag { eventSink.yield(.snapshot(tag, line["result"])) }
                    continuation.resume(returning: line["result"])
                }
            }
            received = sequence
            try await sealed(["t": "ack", "recv": .number(sequence)])
        case "preview":
            handlePreview(value["line"])
        case "state": eventSink.yield(.state(value))
        case "pong": lastPong = Date()
        case "ping": try await sealed(["t": "pong"])
        case "bye": throw NativeError.invalid(value["reason"].string ?? "Computer closed connection")
        default: break
        }
    }
    /// Preview packets are ephemeral and bounded independently of reliable chat events.
    public func openPreview(_ activity: PreviewActivity) async throws -> PreviewSubscription {
        guard attached, supportsPreview else { throw NativeError.invalid("电脑尚不支持实时预览") }
        if let current = previewID { await closePreview(current) }
        let opening = UUID()
        let stream = AsyncThrowingStream<PreviewEvent, Error>.makeStream(bufferingPolicy: .bufferingOldest(4))
        previewOpening = opening; previewActivity = activity; previewSink = stream.continuation
        previewID = nil; previewSequence = 0
        do {
            let result = try await call("desktop/activity/preview/open", params: activity.params, workdir: activity.workdir)
            guard previewOpening == opening else { throw CancellationError() }
            guard let id = result["stream_id"].string, !id.isEmpty, previewID == nil || previewID == id else {
                throw NativeError.invalid("Invalid preview stream")
            }
            previewID = id
            return PreviewSubscription(id: id, events: stream.stream)
        } catch {
            if previewOpening == opening { finishPreview(error) }
            throw error
        }
    }
    public func acknowledgePreview(_ id: String, sequence: Int) async throws {
        guard previewID == id else { throw CancellationError() }
        // Sending releases a network credit; waiting for the RPC response would cap decode at one frame per RTT.
        // Its ordinary response still advances/acknowledges reliable RPC sequencing, without retaining a waiter.
        try await sendPreviewControl("desktop/activity/preview/ack", params: ["stream_id": .string(id), "sequence": .number(Double(sequence))])
    }
    public func renewPreview(_ id: String) async throws {
        guard previewID == id else { throw CancellationError() }
        _ = try await call("desktop/activity/preview/renew", params: ["stream_id": .string(id)])
    }
    public func closePreview(_ id: String) async {
        guard previewID == id else { return }
        finishPreview()
        await sendPreviewClose(id)
    }
    private func sendPreviewControl(_ method: String, params: JSONValue) async throws {
        try await sealed(["t": "rpc", "line": ["id": .string(UUID().uuidString), "method": .string(method), "params": params]])
    }
    private func sendPreviewClose(_ id: String) async {
        let stamp = epoch
        // SwiftUI cancels the viewer task on dismissal. Its close packet still gets an uncancelled send.
        await Task {
            guard self.epoch == stamp, self.attached else { return }
            try? await self.sendPreviewControl("desktop/activity/preview/close", params: ["stream_id": .string(id)])
        }.value
    }
    private func finishPreview(_ error: Error? = nil) {
        previewSink?.finish(throwing: error)
        previewSink = nil; previewID = nil; previewActivity = nil; previewOpening = nil; previewSequence = 0
    }
    private func handlePreview(_ line: JSONValue) {
        let value = line["params"]
        guard let activity = previewActivity, activity.accepts(value), let id = value["stream_id"].string,
              previewID == id, let sink = previewSink else { return }
        do {
            let event: PreviewEvent
            switch line["method"].string {
            case "desktop/activity/preview/frame":
                let frame = try PreviewFrame(value)
                guard frame.sequence > previewSequence else { return }
                previewSequence = frame.sequence
                event = .frame(frame)
            case "desktop/activity/preview/state":
                let state = value["state"].string ?? "error"
                // The viewer derives loading/live from opening and decoded pixels; keep all three frame slots available.
                if state == "loading" || state == "live" { return }
                event = .state(state, value["message"].string ?? "")
            default: return
            }
            if case .dropped = sink.yield(event) {
                // Losing an H.264 delta invalidates its descendants. Reopening obtains a fresh IDR.
                finishPreview(NativeError.invalid("预览连接需要重新同步"))
                Task { await self.sendPreviewClose(id) }
            }
        } catch {
            finishPreview(error)
            Task { await self.sendPreviewClose(id) }
        }
    }
    private func decodeRPCLine(_ value: JSONValue) throws -> JSONValue {
        guard let encoded = value["line_gzip"].string else { return value["line"] }
        // Match the host's decoded RPC line limit; each gzip member is independent.
        let limit = 32 * 1024 * 1024
        guard value["line"] == .null, encoded.utf8.count <= (limit * 4 + 2) / 3 else {
            throw NativeError.invalid("Invalid compressed RPC line")
        }
        let input = try Data(base64URL: encoded)
        var stream = z_stream()
        guard inflateInit2_(&stream, MAX_WBITS + 16, ZLIB_VERSION, Int32(MemoryLayout<z_stream>.size)) == Z_OK else {
            throw NativeError.invalid("Cannot decode compressed RPC line")
        }
        defer { inflateEnd(&stream) }
        var output = Data()
        try input.withUnsafeBytes { raw in
            stream.next_in = UnsafeMutablePointer<Bytef>(mutating: raw.bindMemory(to: Bytef.self).baseAddress)
            stream.avail_in = uInt(raw.count)
            var buffer = [UInt8](repeating: 0, count: 64 * 1024)
            while true {
                let status = buffer.withUnsafeMutableBytes { destination in
                    stream.next_out = destination.bindMemory(to: Bytef.self).baseAddress
                    stream.avail_out = uInt(destination.count)
                    return inflate(&stream, Z_NO_FLUSH)
                }
                let count = buffer.count - Int(stream.avail_out)
                guard output.count + count <= limit else { throw NativeError.invalid("Decompressed RPC line is too large") }
                output.append(contentsOf: buffer.prefix(count))
                if status == Z_STREAM_END {
                    guard stream.avail_in == 0 else { throw NativeError.invalid("Unexpected data after compressed RPC line") }
                    break
                }
                guard status == Z_OK, count > 0 else { throw NativeError.invalid("Incomplete or invalid compressed RPC line") }
            }
        }
        return try JSONDecoder().decode(JSONValue.self, from: output)
    }
    private func ping(epoch stamp: UInt64) async throws {
        guard epoch == stamp else { throw CancellationError() }
        guard Date().timeIntervalSince(lastPong) < 45 else { throw NativeError.invalid("Computer stopped responding") }
        try await sealed(["t": "ping"])
    }
    /// A tagged snapshot is delivered in the same ordered stream as subsequent notifications.
    public func call(_ method: String, params: JSONValue = [:], workdir: String? = nil, snapshotTag: String? = nil) async throws -> JSONValue {
        guard attached else { throw NativeError.invalid("Connect to an online computer first") }
        let id = UUID().uuidString
        let stamp = epoch
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                pending[id] = continuation
                snapshotTags[id] = snapshotTag
                deadlines[id] = Task { [weak self] in
                    do { try await Task.sleep(for: .seconds(30)) } catch { return }
                    await self?.expire(id)
                }
                Task {
                    do {
                        guard self.epoch == stamp, self.pending[id] != nil else { return }
                        var line: [String: JSONValue] = ["id": .string(id), "method": .string(method), "params": params]
                        if let workdir { line["workdir"] = .string(workdir) }
                        try await self.sealed(["t": "rpc", "line": .object(line)])
                    } catch { self.failed(error, epoch: stamp) }
                }
            }
        } onCancel: { Task { await self.expire(id) } }
    }
    private func expire(_ id: String) {
        snapshotTags.removeValue(forKey: id)
        deadlines.removeValue(forKey: id)?.cancel()
        pending.removeValue(forKey: id)?.resume(throwing: NativeError.invalid("Response not received. Check the conversation before sending again."))
    }
    public func respond(id: JSONValue, result: JSONValue) async throws {
        guard attached else { throw NativeError.invalid("Not attached") }
        try await sealed(["t": "rpc", "line": ["id": id, "result": result]])
    }
    public func reject(id: JSONValue, message: String) async throws {
        guard attached else { throw NativeError.invalid("Not attached") }
        try await sealed(["t": "rpc", "line": ["id": id, "error": ["code": -32601, "message": .string(message)]]])
    }
}
