package ai.wuu.nativeapp

import org.json.JSONObject

/** A bookmark never grants access: restore only for the same login and an authorized computer. */
object NavigationLocation {
    private fun key(account: AccountSession) = sha256(org.json.JSONArray(listOf(account.server, account.username, account.pub, account.token)).toString().toByteArray()).b64()
    fun encode(account: AccountSession, host: String, workspace: String, thread: String?, room: String?, collaboration: Boolean) =
        json("login" to key(account), "host" to host, "workspace" to workspace, "thread" to thread, "room" to room, "collaboration" to collaboration)
    fun restore(account: AccountSession, devices: List<JSONObject>, value: JSONObject): JSONObject? {
        if (value.optString("login") != key(account) || devices.none { it.optString("pub") == value.optString("host") && it.optString("role") == "host" }) return null
        return JSONObject(value.toString())
    }
}
