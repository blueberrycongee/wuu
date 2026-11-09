package ai.wuu.nativeapp

import org.bouncycastle.crypto.params.Ed25519PrivateKeyParameters
import org.bouncycastle.crypto.params.Ed25519PublicKeyParameters
import org.bouncycastle.crypto.params.X25519PrivateKeyParameters
import org.bouncycastle.crypto.params.X25519PublicKeyParameters
import org.bouncycastle.crypto.signers.Ed25519Signer
import org.json.JSONObject
import java.nio.ByteBuffer
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

internal fun ByteArray.b64(): String = Base64.getUrlEncoder().withoutPadding().encodeToString(this)
internal fun String.unb64(): ByteArray = Base64.getUrlDecoder().decode(this)
internal fun randomBytes(size: Int) = ByteArray(size).also { SecureRandom().nextBytes(it) }
internal fun sha256(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes)
internal fun framed(parts: List<ByteArray>) = parts.fold(byteArrayOf()) { out, part ->
    out + ByteBuffer.allocate(4).putInt(part.size).array() + part
}
internal fun signing(label: String, parts: List<ByteArray>) = label.toByteArray() + byteArrayOf(0) + framed(parts)
internal fun json(vararg pairs: Pair<String, Any?>) = JSONObject().apply { pairs.forEach { put(it.first, it.second) } }

class Identity(val seed: ByteArray = randomBytes(32)) {
    private val key = Ed25519PrivateKeyParameters(seed, 0)
    val pub: ByteArray get() = key.generatePublicKey().encoded
    fun sign(data: ByteArray): ByteArray = Ed25519Signer().run {
        init(true, key); update(data, 0, data.size); generateSignature()
    }
    fun proof(nonce: ByteArray) = sign(signing("wuu/relay/auth/v1", listOf(nonce, pub, "phone".toByteArray()))).b64()
    fun enrollment(username: String) = proof("wuu/account/enroll/v1:$username".toByteArray())
}

class PhoneHandshake(private val identity: Identity, private val host: ByteArray) {
    private val ephemeral = X25519PrivateKeyParameters(SecureRandom())
    private val nonce = randomBytes(16)
    init { require(host.size == 32) }
    fun offer(): JSONObject {
        val eph = ephemeral.generatePublicKey().encoded
        val sig = identity.sign(signing("wuu/hs1/v1", listOf(host, identity.pub, eph, nonce)))
        return json("t" to "hs1", "device_pub" to identity.pub.b64(), "eph" to eph.b64(), "nonce" to nonce.b64(), "sig" to sig.b64())
    }
    fun finish(reply: JSONObject): SecureChannel {
        val eph = reply.getString("eph").unb64()
        val hostNonce = reply.getString("nonce").unb64()
        require(eph.size == 32 && hostNonce.size == 16) { "Invalid handshake" }
        val parts = listOf(host, identity.pub, ephemeral.generatePublicKey().encoded, nonce, eph, hostNonce)
        val signed = signing("wuu/hs2/v1", parts)
        val valid = Ed25519Signer().run {
            init(false, Ed25519PublicKeyParameters(host, 0)); update(signed, 0, signed.size)
            verifySignature(reply.getString("sig").unb64())
        }
        require(valid) { "Host authentication failed" }
        val shared = ByteArray(32)
        ephemeral.generateSecret(X25519PublicKeyParameters(eph, 0), shared, 0)
        require(shared.any { it != 0.toByte() }) { "Invalid shared secret" }
        return SecureChannel(shared, sha256("wuu/session/v1".toByteArray() + framed(parts)))
    }
}

/** Used only by the transport's serial event loop; a reconnect always creates new keys. */
class SecureChannel(private val shared: ByteArray, private val transcript: ByteArray, phoneSide: Boolean = true) {
    private fun hmac(key: ByteArray, data: ByteArray) = Mac.getInstance("HmacSHA256").run {
        init(SecretKeySpec(key, "HmacSHA256")); doFinal(data)
    }
    private fun key(direction: String): ByteArray = hmac(hmac(transcript, shared), "wuu e2e $direction v1".toByteArray() + byteArrayOf(1))
    private val sendKey = key(if (phoneSide) "phone->host" else "host->phone")
    private val receiveKey = key(if (phoneSide) "host->phone" else "phone->host")
    private var sent = 0L
    private var received = 0L
    fun seal(plain: ByteArray): ByteArray {
        check(sent < Long.MAX_VALUE) { "Channel counter exhausted" }
        val nonce = ByteBuffer.allocate(12).putInt(0).putLong(++sent).array()
        return nonce + crypt(Cipher.ENCRYPT_MODE, sendKey, nonce, plain)
    }
    fun open(frame: ByteArray): ByteArray {
        require(frame.size >= 28) { "Truncated frame" }
        val count = ByteBuffer.wrap(frame, 4, 8).long
        require(count > received) { "Replayed frame" }
        val plain = crypt(Cipher.DECRYPT_MODE, receiveKey, frame.copyOfRange(0, 12), frame.copyOfRange(12, frame.size))
        received = count
        return plain
    }
    private fun crypt(mode: Int, key: ByteArray, nonce: ByteArray, data: ByteArray) = Cipher.getInstance("AES/GCM/NoPadding").run {
        init(mode, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce)); updateAAD(transcript); doFinal(data)
    }
}
