package ai.wuu.nativeapp

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class DirectoryTest {
    @Test fun offlineDirectoryDoesNotRestorePresenceOrCrossLogins() {
        val session = AccountSession("https://example.test", "first", "alice", "phone", "seed")
        val directory = json("devices" to JSONArray().put(json("pub" to "host", "name" to "Computer", "role" to "host", "online" to true)), "auth_method" to "password")
        val disk = JSONObject(RememberedDirectory.encode(session, directory).toString())
        val offline = RememberedDirectory.restore(session, disk)!!.getJSONArray("devices").getJSONObject(0)
        assertEquals("host", offline.getString("pub")); assertFalse(offline.getBoolean("online"))
        for (other in listOf(session.copy(token = "second"), session.copy(server = "https://other.test"), session.copy(username = "bob"), session.copy(pub = "other-phone"))) {
            assertNull(RememberedDirectory.restore(other, disk))
        }
    }
}
