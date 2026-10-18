package ai.wuu.nativeapp

import java.io.File
import java.nio.file.Files
import kotlinx.coroutines.test.runTest
import org.json.JSONArray
import org.junit.Assert.*
import org.junit.Test

class PersonalLifecycleTest {
    @Test fun failingDraftDeletionCannotPreserveLivePersonalAuthority() = runTest {
        val root = Files.createTempDirectory("personal-lifecycle").toFile()
        try {
            val adapter = PersonalClient { _, p -> json("result" to json("agent" to json("agent_id" to p.getJSONObject("input").getString("agent_id"), "user_space_id" to "local"))) }
            adapter.configure(json("user_space_id" to "local", "product_domain" to "personal", "extension_inventory" to JSONArray().put(json("id" to "plugin:bundled:personal", "fingerprint" to "one", "runtime_state" to "active"))))
            adapter.selectAgent("main")
            val drafts = File(root, "drafts"); val identity = listOf("account", "login", "host")
            PersonalDraftStore(drafts, identity).save(adapter.prepareMessage("Uncertain request"))
            val model = PersonalModel(initialClient = adapter)
            model.configureCache(File(root, "cache"), drafts, identity)
            val file = drafts.listFiles()!!.single(); check(file.delete()); check(file.mkdir()); File(file, "prevent-delete").writeText("synthetic")
            val warning = model.reset(removeCache = true)
            assertNotNull(warning); assertFalse(model.available); assertFalse(model.hasPending); assertNull(model.selectedID)
            try { adapter.listAgents(); fail("Deletion failure retained authority") }
            catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.UNAVAILABLE, error.reason) }
        } finally { root.deleteRecursively() }
    }
}
