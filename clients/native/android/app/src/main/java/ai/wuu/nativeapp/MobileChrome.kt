package ai.wuu.nativeapp

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import org.json.JSONObject
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

@Composable internal fun ChromeButton(icon: ImageVector, label: String, enabled: Boolean = true, onClick: () -> Unit) {
    Surface(onClick = onClick, enabled = enabled, shape = CircleShape, color = MaterialTheme.colorScheme.surface,
        border = BorderStroke(0.5.dp, MaterialTheme.colorScheme.outlineVariant), modifier = Modifier.size(44.dp)) {
        Box(contentAlignment = Alignment.Center) { Icon(icon, label, Modifier.size(21.dp)) }
    }
}

@Composable internal fun AgentMark(agent: JSONObject?, size: Dp = 36.dp, status: String? = null, subtle: Boolean = false) {
    SharedAvatar(json("agent" to avatarRecord(agent), "status" to status, "subtle" to subtle), size)
}

internal fun roomAgents(room: JSONObject, agents: List<JSONObject>): List<JSONObject> =
    room.optJSONArray("members")?.objects().orEmpty().filter { it.optString("member_type") == "agent" }
        .map { member -> agents.firstOrNull { it.optString("id") == member.optString("member_id") } ?: member }

@Composable internal fun RoomMark(room: JSONObject, agents: List<JSONObject>, size: Dp = 40.dp) {
    val record = json("id" to room.optString("id"), "kind" to room.optString("kind"), "avatar_image" to room.optString("avatar_image"),
        "created_at" to room.optString("created_at"), "members" to (room.optJSONArray("members") ?: org.json.JSONArray()))
    SharedAvatar(json("room" to record, "agents" to org.json.JSONArray(roomAgents(room, agents).map(::avatarRecord))), size)
}

internal fun messageDate(value: String): java.time.ZonedDateTime? = runCatching { Instant.parse(value).atZone(ZoneId.systemDefault()) }.getOrNull()
internal fun messageTime(value: String, separator: Boolean = false): String {
    val date = messageDate(value) ?: return ""
    val today = java.time.LocalDate.now()
    val day = when (date.toLocalDate()) { today -> if (separator) "今天 " else ""; today.minusDays(1) -> "昨天"; else -> date.format(DateTimeFormatter.ofPattern("M月d日")) }
    return if (separator || date.toLocalDate() == today) day + (if (separator && day.isNotEmpty() && !day.endsWith(' ')) " " else "") + date.format(DateTimeFormatter.ofPattern("HH:mm")) else day
}
