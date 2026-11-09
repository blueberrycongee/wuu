package ai.wuu.nativeapp

import org.junit.Assert.*
import org.junit.Test

class PersonalLocationTest {
    private val host = "A".repeat(43)
    private val payload get() = mapOf("version" to "1", "product_domain" to "personal", "user_space_id" to "local", "host" to host, "agent_id" to "main", "task_id" to "task-one", "message_seq" to "17", "recipient" to "phone")
    @Test fun notificationRejectsWrongRecipientAndSessionRouting() {
        val location = requireNotNull(PersonalLocation.notification(payload, "phone"))
        assertEquals("main", location.agentID); assertEquals("task-one", location.taskID); assertEquals(17L, location.messageSequence)
        assertNull(PersonalLocation.notification(payload, "other"))
        assertNull(PersonalLocation.notification(payload + ("session_id" to "new-thread"), "phone"))
        assertNull(PersonalLocation.notification(payload + ("message_seq" to "0"), "phone"))
        assertNull(PersonalLocation.notification(payload + ("version" to "2"), "phone"))
    }
    @Test fun urlRejectsAmbiguityAndKeepsBusinessLocation() {
        val url = "wuu://personal?version=1&product_domain=personal&user_space_id=local&host=$host&agent_id=main&message_seq=17"
        val location = requireNotNull(PersonalLocation.fromURL(url))
        assertEquals("main", location.agentID); assertEquals(17L, location.messageSequence)
        assertNull(PersonalLocation.fromURL("$url&agent_id=other"))
        assertNull(PersonalLocation.fromURL("$url&session_id=arbitrary"))
        assertNull(PersonalLocation.fromURL(url.replace("wuu:", "https:")))
    }
}
