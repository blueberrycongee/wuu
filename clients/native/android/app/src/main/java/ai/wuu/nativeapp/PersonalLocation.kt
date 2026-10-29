package ai.wuu.nativeapp

import java.net.URI
import java.net.URLDecoder
import java.util.Base64

/** A location, never an authorization grant. Revalidate the current account's host directory before opening. */
data class PersonalLocation private constructor(val host: String, val agentID: String, val taskID: String?, val messageSequence: Long?) {
    companion object {
        private fun validID(value: String) = value.isNotEmpty() && value.toByteArray().size <= 256 && value == value.trim() && value.none { it.isISOControl() }
        private fun parse(values: Map<String, String>): PersonalLocation? {
            if (values["version"] != "1" || values["product_domain"] != "personal" || values["user_space_id"] != "local" || values.containsKey("session_id") || values.containsKey("workspace_id")) return null
            val host = values["host"] ?: return null
            if (runCatching { Base64.getUrlDecoder().decode(host).size }.getOrNull() != 32) return null
            val agent = values["agent_id"]?.takeIf(::validID) ?: return null
            val task = values["task_id"]
            if (task != null && !validID(task)) return null
            val seq = values["message_seq"]?.let { value -> value.toLongOrNull()?.takeIf { it > 0 && it.toString() == value } ?: return null }
            return PersonalLocation(host, agent, task, seq)
        }
        fun notification(values: Map<String, String>, recipient: String): PersonalLocation? =
            if (recipient.isNotEmpty() && values["recipient"] == recipient) parse(values) else null
        fun fromURL(value: String): PersonalLocation? = runCatching {
            val uri = URI(value)
            if (uri.scheme != "wuu" || uri.host != "personal" || !uri.path.isNullOrEmpty() || uri.userInfo != null || uri.port != -1 || uri.fragment != null) return null
            val values = mutableMapOf<String, String>()
            for (part in uri.rawQuery.orEmpty().split("&")) {
                val pair = part.split("=", limit = 2)
                if (pair.size != 2) return null
                val key = URLDecoder.decode(pair[0], "UTF-8"); val content = URLDecoder.decode(pair[1], "UTF-8")
                if (values.put(key, content) != null) return null
            }
            parse(values)
        }.getOrNull()
    }
}
