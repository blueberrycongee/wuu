package ai.wuu.nativeapp

import org.json.JSONObject

/** Consent belongs to one login, including its bearer-token lifetime. */
object PushConsent {
    fun key(session: AccountSession) = sha256(listOf(session.server, session.username, session.pub, session.token).joinToString("\n").toByteArray()).b64()
    fun encode(session: AccountSession, enabled: Boolean) = json("login" to key(session), "enabled" to enabled)
    fun matches(session: AccountSession, consent: JSONObject) = consent.optString("login") == key(session)
}

suspend fun AccountAPI.pushStatus(token: String) = request("/push", token)
suspend fun AccountAPI.setPush(token: String, platform: String, deviceToken: String?) {
    if (deviceToken != null) {
        require(deviceToken.isNotEmpty() && platform in setOf("ios", "android")) { "Invalid push registration" }
        request("/push", token, json("platform" to platform, "token" to deviceToken))
    } else request("/push", token, method = "DELETE")
}
