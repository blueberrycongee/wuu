package ai.wuu.nativeapp

import org.json.JSONObject

data class ThreadSettings(val provider: String, val model: String, val variant: String, val permission: String) {
    // Empty thread_id would mutate computer defaults rather than this conversation.
    fun updateParams(threadID: String): JSONObject {
        require(threadID.isNotBlank() && provider.isNotBlank() && model.isNotBlank() &&
            permission in listOf("standard", "read_only", "unconfined")) { "请选择当前会话的模型和权限" }
        return json("thread_id" to threadID, "provider" to provider, "model" to model,
            "variant" to variant, "permission_mode" to permission)
    }
    companion object {
        fun from(thread: JSONObject) = ThreadSettings(thread.optString("model_provider"), thread.optString("model"),
            thread.optString("model_variant").ifEmpty { thread.optString("model_effort") },
            thread.optString("permission_mode").ifEmpty { "standard" })
    }
}
data class RemoteModel(val id: String, val name: String, val variants: List<String>) {
    companion object {
        fun from(value: JSONObject): RemoteModel {
            val id = value.optString("id")
            val declared = value.optJSONArray("variants")?.objects()?.map { it.optString("id") }?.filter { it.isNotEmpty() }.orEmpty()
            val efforts = value.optJSONArray("supported_efforts")
            return RemoteModel(id, value.optString("display_name").ifEmpty { id },
                declared.ifEmpty { (0 until (efforts?.length() ?: 0)).map { efforts!!.getString(it) } })
        }
    }
}
data class RemoteProvider(val id: String, val models: List<RemoteModel>) {
    companion object {
        fun from(value: JSONObject): RemoteProvider {
            val models = value.optJSONArray("models")?.objects()?.map(RemoteModel::from)?.filter { it.id.isNotEmpty() }.orEmpty()
            val current = value.optString("model")
            return RemoteProvider(value.optString("name"), models.ifEmpty {
                if (current.isEmpty()) emptyList() else listOf(RemoteModel(current, current, emptyList()))
            })
        }
    }
}
