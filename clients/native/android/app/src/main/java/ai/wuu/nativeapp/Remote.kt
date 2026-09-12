package ai.wuu.nativeapp

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import org.json.JSONObject
import java.io.IOException
import java.util.UUID

/** One instance per foreground connection. All mutations and socket writes run on Main. */
class Remote(private val account: AccountSession, private val host: String) {
    sealed interface Event {
        data class Notification(val method: String, val params: JSONObject) : Event
        data class Approval(val line: JSONObject) : Event
        data class Disconnected(val reason: String) : Event
        data class Snapshot(val tag: Long, val result: JSONObject) : Event
    }
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val identity = Identity(account.seed.unb64())
    private val eventsChannel = Channel<Event>(256)
    val events = eventsChannel
    private var socket: WebSocket? = null
    private var handshake: PhoneHandshake? = null
    private var channel: SecureChannel? = null
    private val ready = CompletableDeferred<Unit>()
    private val pending = mutableMapOf<String, CompletableDeferred<JSONObject>>()
    private val snapshotTags = mutableMapOf<String, Long>()
    private var challenged = false
    private var authenticated = false
    private var attached = false
    private var closed = false
    private var received = 0L
    private var lastPong = 0L
    private var heartbeat: Job? = null
    init { require(identity.pub.b64() == account.pub) { "Device identity mismatch" } }

    suspend fun connect() = withContext(Dispatchers.Main.immediate) {
        check(socket == null && !closed)
        val url = AccountAPI(account.server).origin.newBuilder().encodedPath("/v1/connect").build()
        socket = AccountAPI.client.newWebSocket(Request.Builder().url(url).build(), object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) = dispatch {
                send(json("type" to "hello", "proto" to 1, "role" to "phone", "pub" to account.pub, "to" to host))
            }
            override fun onMessage(webSocket: WebSocket, text: String) = dispatch {
                require(text.length <= 8 * 1024 * 1024) { "Frame too large" }; handle(JSONObject(text))
            }
            override fun onMessage(webSocket: WebSocket, bytes: ByteString) = onMessage(webSocket, bytes.utf8())
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = dispatch { throw t }
            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) = dispatch { throw IOException("Computer disconnected") }
        })
        try { withTimeoutOrNull(20_000) { ready.await() } ?: throw IOException("连接电脑超时，请重试") }
        catch (e: Throwable) { close(e.message ?: "Connection failed"); throw e }
    }
    private fun dispatch(block: () -> Unit) {
        scope.launch { if (!closed) try { block() } catch (e: Throwable) { close(e.message ?: "Connection failed") } }
    }
    private fun emit(event: Event) {
        check(eventsChannel.trySend(event).isSuccess) { "Client cannot keep up; reconnect to refresh state" }
    }
    fun close(reason: String = "Connection closed") {
        if (closed) return
        closed = true
        socket?.cancel(); socket = null; heartbeat?.cancel()
        val error = IOException("$reason。已发送的请求可能已经到达电脑，请检查后再重试。")
        ready.completeExceptionally(error)
        pending.values.forEach { it.completeExceptionally(error) }; pending.clear(); snapshotTags.clear()
        channel = null; handshake = null; attached = false
        eventsChannel.trySend(Event.Disconnected(reason)); eventsChannel.close(); scope.cancel()
    }
    private fun send(value: JSONObject) { check(socket?.send(value.toString()) == true) { "Socket write failed" } }
    private fun frame(kind: Byte, body: ByteArray) = send(json("type" to "frame", "to" to host, "payload" to (byteArrayOf(kind) + body).b64()))
    private fun sealed(value: JSONObject) = frame(2, checkNotNull(channel).seal(value.toString().toByteArray()))
    private fun startHandshake() {
        check(handshake == null && channel == null)
        handshake = PhoneHandshake(identity, host.unb64()).also { frame(1, it.offer().toString().toByteArray()) }
    }
    private fun handle(value: JSONObject) {
        when (value.optString("type")) {
            "challenge" -> {
                check(!challenged); challenged = true
                val nonce = value.getString("nonce").unb64(); require(nonce.size == 32)
                send(json("type" to "auth", "sig" to identity.proof(nonce)))
            }
            "auth_ok" -> { check(challenged && !authenticated); authenticated = true; if (value.optBoolean("online")) startHandshake() }
            "presence" -> if (authenticated && value.optString("pub") == host) {
                check(value.optBoolean("online")) { "电脑已离线" }
                if (handshake == null && channel == null) startHandshake()
            }
            "frame" -> {
                check(authenticated)
                val data = value.getString("payload").unb64(); require(data.isNotEmpty())
                val body = data.copyOfRange(1, data.size)
                when (data[0].toInt()) {
                    1 -> {
                        val reply = JSONObject(String(body, Charsets.UTF_8)); require(reply.getString("t") == "hs2")
                        channel = checkNotNull(handshake).finish(reply); handshake = null
                        sealed(json("t" to "attach", "client_profile" to "mobile_chat"))
                    }
                    2 -> handleSealed(JSONObject(String(checkNotNull(channel).open(body), Charsets.UTF_8)))
                    else -> error("Unknown encrypted frame")
                }
            }
            "error", "deliver_err" -> error(value.optString("msg", value.optString("code", "Relay rejected connection")))
        }
    }
    private fun handleSealed(value: JSONObject) {
        when (value.optString("t")) {
            "attached" -> {
                check(!attached); attached = true; ready.complete(Unit); lastPong = System.nanoTime()
                heartbeat = scope.launch {
                    while (!closed) {
                        delay(15_000)
                        try {
                            check(System.nanoTime() - lastPong < 45_000_000_000L) { "Connection timed out" }
                            sealed(json("t" to "ping"))
                        } catch (e: Exception) { close(e.message ?: "Connection failed") }
                    }
                }
            }
            "rpc" -> {
                check(attached)
                val seq = value.getLong("seq")
                if (seq <= received) return
                val line = value.getJSONObject("line")
                if (line.has("method")) {
                    if (line.has("id") && !line.isNull("id")) emit(Event.Approval(line))
                    else emit(Event.Notification(line.getString("method"), line.optJSONObject("params") ?: JSONObject()))
                } else {
                    pending.remove(line.optString("id"))?.let { waiter ->
                        val tag = snapshotTags.remove(line.optString("id"))
                        if (line.has("error")) waiter.completeExceptionally(IOException(line.getJSONObject("error").optString("message", "Request failed")))
                        else {
                            val result = line.optJSONObject("result") ?: JSONObject()
                            if (tag != null) emit(Event.Snapshot(tag, result))
                            waiter.complete(result)
                        }
                    }
                }
                received = seq; sealed(json("t" to "ack", "recv" to received))
            }
            "ping" -> sealed(json("t" to "pong"))
            "pong" -> lastPong = System.nanoTime()
            "bye" -> error(value.optString("reason", "Computer disconnected"))
        }
    }
    // Tagged snapshots share notification ordering, independent of coroutine resumption order.
    suspend fun call(method: String, params: JSONObject = JSONObject(), snapshotTag: Long? = null): JSONObject = withContext(Dispatchers.Main.immediate) {
        check(attached && !closed) { "请先连接电脑" }
        val id = UUID.randomUUID().toString()
        val result = CompletableDeferred<JSONObject>(); pending[id] = result
        if (snapshotTag != null) snapshotTags[id] = snapshotTag
        try {
            // Deliberately unnumbered: host must never replay a turn/start after reconnect.
            sealed(json("t" to "rpc", "line" to json("jsonrpc" to "2.0", "id" to id, "method" to method, "params" to params)))
            withTimeoutOrNull(30_000) { result.await() }
                ?: throw IOException("未收到电脑响应。发送的消息可能已被接受，请先检查会话，再决定是否重发。")
        } finally { pending.remove(id); snapshotTags.remove(id) }
    }
    fun reject(line: JSONObject) {
        check(attached && !closed)
        sealed(json("t" to "rpc", "line" to json("jsonrpc" to "2.0", "id" to line.get("id"),
            "error" to json("code" to -32601, "message" to "This mobile client does not support this request. Continue on the computer."))))
    }
}
