package ai.wuu.nativeapp

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import org.json.JSONArray
import org.json.JSONObject

@Composable fun QuestionView(model: AppModel, request: JSONObject) {
    val selected = remember(request.getString("request_id")) { mutableStateMapOf<String, Set<String>>() }
    val custom = remember(request.getString("request_id")) { mutableStateMapOf<String, String>() }
    var submitting by remember(request.getString("request_id")) { mutableStateOf(false) }
    val questions = request.getJSONArray("questions").objects()
    val valid = questions.all { (selected[it.getString("id")] ?: emptySet()).isNotEmpty() || !custom[it.getString("id")].isNullOrBlank() }
    fun submit(answers: JSONArray?) {
        submitting = true
        model.perform { try { model.answerQuestion(request, answers) } finally { submitting = false } }
    }
    Surface(color = MaterialTheme.colorScheme.surfaceContainer) {
        Column(Modifier.heightIn(max = 320.dp).verticalScroll(rememberScrollState()).padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            questions.forEach { question ->
                val id = question.getString("id")
                Text(question.getString("question"), style = MaterialTheme.typography.titleSmall)
                if (question.optString("detail").isNotBlank()) Text(question.getString("detail"), style = MaterialTheme.typography.bodySmall)
                question.optJSONArray("options")?.objects()?.forEach { option ->
                    val label = option.getString("label")
                    FilterChip(selected = selected[id]?.contains(label) == true,
                        onClick = {
                            val values = selected[id] ?: emptySet()
                            selected[id] = if (label in values) values - label else if (question.optBoolean("multi_select")) values + label else setOf(label)
                        }, label = {
                            Column { Text(label); if (option.optString("description").isNotBlank()) Text(option.getString("description"), style = MaterialTheme.typography.bodySmall) }
                        }, enabled = !submitting && model.connected)
                }
                if (question.optBoolean("allow_custom")) OutlinedTextField(custom[id] ?: "", { custom[id] = it }, label = { Text("其他回答") }, enabled = !submitting && model.connected)
            }
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                TextButton(onClick = { submit(null) }, enabled = !submitting && model.connected) { Text("取消请求") }
                Button(onClick = {
                    submit(JSONArray(questions.map { question ->
                        val id = question.getString("id")
                        json("id" to id, "selected" to JSONArray((selected[id] ?: emptySet()).sorted()), "custom" to (custom[id] ?: ""))
                    }))
                }, enabled = valid && !submitting && model.connected) { Text("提交") }
            }
        }
    }
}
