package ai.wuu.nativeapp

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class NavigationLocationTest {
    @Test fun coldBookmarkRequiresSameLoginAndAuthorizedHost() {
        val account = AccountSession("https://example.test", "token", "alice", "phone", "seed")
        val devices = listOf(json("pub" to "host", "role" to "host", "online" to false))
        val location = NavigationLocation.encode(account, "host", "/work", "thread")
        val disk = JSONObject(location.toString())
        assertEquals(location.toString(), NavigationLocation.restore(account, devices, disk)?.toString())
        assertNull(NavigationLocation.restore(account, emptyList(), disk))
        assertNull(NavigationLocation.restore(account, listOf(json("pub" to "host", "role" to "phone")), disk))
        for (other in listOf(account.copy(server = "https://other.test"), account.copy(token = "new-login"), account.copy(username = "bob"))) {
            assertNull(NavigationLocation.restore(other, devices, disk))
        }
    }
}
