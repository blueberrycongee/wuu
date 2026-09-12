package ai.wuu.nativeapp

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.json.JSONObject
import java.io.IOException
import java.security.KeyStore
import java.util.concurrent.TimeUnit
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

data class AccountSession(val server: String, val token: String, val username: String, val pub: String, val seed: String, val recovery: String = "") {
    fun encode() = json("server" to server, "token" to token, "username" to username, "pub" to pub, "seed" to seed, "recovery" to recovery)
    companion object {
        fun decode(j: JSONObject) = AccountSession(j.getString("server"), j.getString("token"), j.getString("username"), j.getString("pub"), j.getString("seed"), j.optString("recovery"))
    }
}
class HttpFailure(val status: Int, message: String) : IOException("HTTP $status: $message")

/** Only ciphertext goes into preferences. Keys cannot leave Android Keystore; backups are disabled. */
class Vault(context: Context) {
    private val prefs = context.getSharedPreferences("wuu-private", Context.MODE_PRIVATE)
    private val key: SecretKey = KeyStore.getInstance("AndroidKeyStore").run {
        load(null)
        (getKey("wuu.native.credentials", null) as? SecretKey) ?: KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder("wuu.native.credentials", KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE).build())
            generateKey()
        }
    }
    fun read(name: String): JSONObject? {
        val data = prefs.getString(name, null)?.unb64() ?: return null
        require(data.size >= 28)
        val plain = Cipher.getInstance("AES/GCM/NoPadding").run {
            init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, data.copyOfRange(0, 12)))
            updateAAD(name.toByteArray()); doFinal(data.copyOfRange(12, data.size))
        }
        return JSONObject(String(plain, Charsets.UTF_8))
    }
    fun write(name: String, value: JSONObject) {
        val encrypted = Cipher.getInstance("AES/GCM/NoPadding").run {
            init(Cipher.ENCRYPT_MODE, key); updateAAD(name.toByteArray()); iv + doFinal(value.toString().toByteArray())
        }
        check(prefs.edit().putString(name, encrypted.b64()).commit()) { "Could not save credentials" }
    }
    fun delete(name: String) { check(prefs.edit().remove(name).commit()) }
    fun identity(server: String, user: String): Identity {
        val name = "identity-" + sha256("$server\n$user".toByteArray()).b64()
        return read(name)?.let { Identity(it.getString("seed").unb64()) } ?: Identity().also {
            write(name, json("seed" to it.seed.b64()))
        }
    }
}

class AccountAPI(server: String) {
    val origin: HttpUrl
    init {
        val raw = java.net.URI(server.trim())
        val parsed = server.trim().toHttpUrl()
        val loopback = parsed.host in setOf("localhost", "127.0.0.1", "::1")
        require((parsed.isHttps || loopback) && raw.rawUserInfo == null &&
            (raw.rawPath.isNullOrEmpty() || raw.rawPath == "/") && raw.rawQuery == null && raw.rawFragment == null) { "请输入 HTTPS 服务器地址，不含路径" }
        origin = parsed
    }
    companion object {
        val client = OkHttpClient.Builder().followRedirects(false).followSslRedirects(false)
            .connectTimeout(15, TimeUnit.SECONDS).readTimeout(30, TimeUnit.SECONDS).callTimeout(30, TimeUnit.SECONDS).build()
    }
    suspend fun request(path: String, token: String? = null, body: JSONObject? = null, query: Map<String, String> = emptyMap(), method: String? = null): JSONObject {
        val url = origin.newBuilder().encodedPath("/v1/account$path").apply { query.forEach { (key, value) -> addQueryParameter(key, value) } }.build()
        val req = Request.Builder().url(url).apply {
            if (token != null) header("Authorization", "Bearer $token")
            if (body != null) post(body.toString().toRequestBody("application/json".toMediaType()))
            if (method == "DELETE") delete()
        }.build()
        val call = client.newCall(req)
        val response = suspendCancellableCoroutine<Response> { continuation ->
            continuation.invokeOnCancellation { call.cancel() }
            call.enqueue(object : Callback {
                override fun onFailure(call: Call, e: IOException) { if (continuation.isActive) continuation.resumeWithException(e) }
                override fun onResponse(call: Call, response: Response) {
                    continuation.resume(response) { _, value, _ -> value.close() }
                }
            })
        }
        return response.use {
            withContext(Dispatchers.IO) {
                val source = it.body?.source() ?: throw IOException("Empty response")
                require(!source.request(16L * 1024 * 1024 + 1)) { "Response too large" }
                val text = source.readUtf8()
                if (!it.isSuccessful) throw HttpFailure(it.code, runCatching { JSONObject(text).optString("error") }.getOrDefault("Request failed"))
                if (text.isBlank()) JSONObject() else JSONObject(text)
            }
        }
    }
    suspend fun login(username: String, password: String, identity: Identity, name: String, register: Boolean = false): AccountSession {
        val result = request(if (register) "/register" else "/login", body = enrollment(username, identity, name).put("password", password))
        return session(result, username, identity)
    }
    private fun enrollment(user: String, identity: Identity, name: String) = json("username" to user, "name" to name,
        "role" to "phone", "pub" to identity.pub.b64(), "proof" to identity.enrollment(user))
    private fun session(result: JSONObject, user: String, identity: Identity): AccountSession {
        require(result.getString("pub") == identity.pub.b64() && result.getString("username") == user) { "Account identity mismatch" }
        return AccountSession(origin.toString(), result.getString("token"), user, identity.pub.b64(), identity.seed.b64(), result.optString("recovery"))
    }
    suspend fun startGitHub(): JSONObject {
        val verifier = randomBytes(32).b64()
        val result = request("/github/start", body = json("challenge" to sha256(verifier.toByteArray()).b64(), "native" to true))
        val url = result.getString("authorize_url").toHttpUrl()
        require(url.scheme == origin.scheme && url.host == origin.host && url.port == origin.port && url.username.isEmpty() && url.password.isEmpty() &&
            url.encodedPath == "/v1/account/github/authorize" && url.queryParameter("state") == result.getString("request_id")) { "Invalid authorization URL" }
        return result.put("server", origin.toString()).put("verifier", verifier)
            .put("expires", System.currentTimeMillis() + result.getLong("expires_in") * 1000)
    }
    suspend fun pollGitHub(pending: JSONObject) = request("/github/poll", body = json("request_id" to pending.getString("request_id"), "verifier" to pending.getString("verifier")))
    suspend fun completeGitHub(pending: JSONObject, user: String, identity: Identity, name: String): AccountSession {
        val result = request("/github/complete", body = enrollment(user, identity, name)
            .put("request_id", pending.getString("request_id")).put("verifier", pending.getString("verifier")))
        return session(result, user, identity)
    }
}
