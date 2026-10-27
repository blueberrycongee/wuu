package ai.wuu.nativeapp

import kotlinx.coroutines.test.runTest
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class PersonalControlsTest {
    private fun initialized() = json("user_space_id" to "local", "product_domain" to "personal", "extension_inventory" to JSONArray().put(json("id" to "plugin:bundled:personal", "fingerprint" to "personal-one", "graph_generation" to "graph-one", "runtime_state" to "active")).put(json("id" to "plugin:bundled:personal-calendar", "fingerprint" to "calendar-one", "graph_generation" to "calendar-graph-one", "runtime_state" to "active")))
    @Test fun calendarDayWindowUsesRFC3339SecondsAndLocalDSTBoundaries() {
        val day = java.time.LocalDate.of(2026, 3, 8)
        val (from, to) = personalCalendarDayWindow(day, java.time.ZoneId.of("America/New_York"))
        val timestamp = Regex("^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(?:\\.\\d{1,3})?(?:Z|[+-]\\d{2}:\\d{2})$")
        assertTrue(timestamp.matches(from)); assertTrue(timestamp.matches(to))
        assertEquals(23L, java.time.Duration.between(java.time.Instant.parse(from), java.time.Instant.parse(to)).toHours())
        val (midnight, _) = personalCalendarDayWindow(java.time.LocalDate.of(2026, 10, 18), java.time.ZoneOffset.UTC)
        assertTrue(midnight.endsWith("T00:00:00Z"))
    }
    @Test fun agentSettingsAndSpecialistDecisionKeepExplicitIdentities() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { _, p -> calls += p; json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local"))) }
        client.configure(initialized()); client.selectAgent("main")
        client.updateAgent(8, "Mosaic", "Keep it brief")
        client.decideSpecialist("proposal-one", false)
        assertEquals(8, calls[1].getJSONObject("input").getInt("expected_revision")); assertEquals("main", calls[1].getJSONObject("input").getString("agent_id")); assertFalse(calls[1].getJSONObject("input").has("session_id"))
        assertEquals("proposal-one", calls[2].getJSONObject("input").getString("proposal_id")); assertFalse(calls[2].getJSONObject("input").getBoolean("approved"))
        client.invalidate()
        try { client.decideSpecialist("proposal-one", true); fail("Offline decision admitted") } catch (_: PersonalClientException) {}
    }
    @Test fun taskMutationKeepsDisplayedCASAndRejectsForeignOwner() = runTest {
        val calls = mutableListOf<JSONObject>(); var foreign = false
        val client = PersonalClient { _, p -> calls += p; if (p.getString("method") == "agents.open") json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local"))) else json("result" to json("task" to json("task_id" to "task-one", "agent_id" to if (foreign) "foreign" else "main", "user_space_id" to "local", "revision" to 9, "requirements_revision" to 3))) }
        client.configure(initialized()); client.selectAgent("main")
        client.changeTask("task-one", 7, PersonalTaskAction.UPDATE, "New exact goal")
        assertEquals("tasks.update", calls.last().getString("method")); assertEquals(7, calls.last().getJSONObject("input").getInt("expected_revision"))
        assertFalse(calls.last().getJSONObject("input").has("requirements_revision"))
        client.taskHistory("task-one", 2)
        assertEquals(2, calls.last().getJSONObject("input").getInt("requirements_revision")); assertFalse(calls.last().getJSONObject("input").has("session_id"))
        foreign = true
        try { client.changeTask("task-one", 9, PersonalTaskAction.STOP); fail("Foreign mutation admitted") } catch (_: PersonalClientException) {}
        assertEquals("tasks.get", calls.last().getString("method"))
    }
    @Test fun memoryAndSettingsKeepExplicitCASWithoutSendProof() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { _, p -> calls += p; json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local"))) }
        client.configure(initialized()); client.selectAgent("main")
        client.memory()
        assertTrue(calls.last().getJSONObject("input").getBoolean("include_inactive"))
        client.correctMemory("fact-one", 4, "Exact correction", "edit-one")
        client.updateSettings(2, false, json("enabled" to true, "start" to "22:00", "end" to "08:00", "timezone" to "UTC"), 300)
        assertEquals("main", calls[2].getJSONObject("input").getString("agent_id")); assertEquals("edit-one", calls[2].getJSONObject("input").getString("request_id"))
        assertEquals(4, calls[2].getJSONObject("input").getInt("expected_revision")); assertFalse(calls[2].has("user_input")); assertEquals(2, calls[3].getJSONObject("input").getInt("expected_revision"))
        assertEquals(300, calls[3].getJSONObject("input").getInt("notification_batch_seconds"))
        client.searchHistory("Ada travel", "2026-10-18T00:00:00Z", "2026-10-19T00:00:00Z", "snapshot-cursor")
        assertEquals("agents.search", calls.last().getString("method")); assertEquals("main", calls.last().getJSONObject("input").getString("agent_id"))
        assertEquals("2026-10-18T00:00:00Z", calls.last().getJSONObject("input").getString("time_min")); assertEquals("snapshot-cursor", calls.last().getJSONObject("input").getString("cursor"))

    }
    @Test fun calendarConfirmationCopiesDisplayedProposalAndUsesDedicatedFingerprint() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { _, p -> calls += p; if (p.getString("method") == "agents.open") json("result" to json("agent" to json("agent_id" to p.getJSONObject("input").getString("agent_id"), "user_space_id" to "local"))) else json("result" to json("status" to "uncertain")) }
        client.configure(initialized()); client.selectAgent("main")
        val preview = json("operation_id" to "operation-one", "agent_id" to "main", "user_space_id" to "local", "revision" to 3, "status" to "proposed", "preview_digest" to "a".repeat(64), "kind" to "create", "account_id" to "calendar-account", "calendar_id" to "primary", "task_id" to "task-one", "requirements_revision" to 1,
            "fields" to json("summary" to "Planning", "location" to "Office", "start" to json("date_time" to "2026-10-19T10:00:00Z", "timezone" to "UTC"), "end" to json("date_time" to "2026-10-19T11:00:00Z", "timezone" to "UTC"), "attendees" to JSONArray().put(json("email" to "guest@example.test", "optional" to false)), "send_updates" to "all"), "availability" to json("checked_at" to "2026-10-18T10:00:00Z", "conflicting_event_ids" to JSONArray()), "notification_notice" to "Provider may notify attendees")
        val incomplete = JSONObject(preview.toString()); incomplete.getJSONObject("fields").put("start", json("timezone" to "UTC"))
        try { client.prepareCalendarConfirmation(incomplete); fail("Incomplete displayed event time admitted") } catch (_: PersonalClientException) {}
        val confirmation = client.prepareCalendarConfirmation(preview)
        preview.put("revision", 99).put("preview_digest", "b".repeat(64)); client.confirmCalendar(confirmation)
        assertEquals("plugin:bundled:personal-calendar", calls.last().getString("id")); assertEquals("calendar-one", calls.last().getString("fingerprint"))
        assertEquals("calendar-graph-one", calls.last().getString("graph_generation"))
        assertEquals(3, calls.last().getJSONObject("input").getInt("expected_revision")); assertEquals("a".repeat(64), calls.last().getJSONObject("input").getString("preview_digest"))
        client.selectAgent("other")
        try { client.confirmCalendar(confirmation); fail("Approval crossed agents") } catch (_: PersonalClientException) {}
        client.selectAgent("main")
        try { client.confirmCalendar(confirmation); fail("Approval survived leaving and reopening the assistant") } catch (_: PersonalClientException) {}
        val beforeReconnect = client.prepareCalendarConfirmation(preview)
        client.configure(initialized()); client.selectAgent("main")
        try { client.confirmCalendar(beforeReconnect); fail("Approval survived reconnect") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.SELECTION_CHANGED, error.reason) }
        val beforeRefresh = client.prepareCalendarConfirmation(preview)
        client.refreshInventory(initialized().also {
            it.getJSONArray("extension_inventory").getJSONObject(0).put("graph_generation", "graph-two")
            it.getJSONArray("extension_inventory").getJSONObject(1).put("graph_generation", "calendar-graph-two")
        })
        client.selectAgent("main")
        try { client.confirmCalendar(beforeRefresh); fail("Approval crossed graphs with unchanged source") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.SELECTION_CHANGED, error.reason) }
        client.confirmCalendar(client.prepareCalendarConfirmation(preview))
        assertEquals("calendar-graph-two", calls.last().getString("graph_generation"))
        assertEquals(2, calls.count { it.getString("method") == "operations.confirm" })
        client.invalidate()
        try { client.calendarOperations(); fail("Authority survived disconnect") } catch (_: PersonalClientException) {}
    }

    @Test fun missingCalendarGraphDoesNotBorrowPersonalAuthority() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { _, p -> calls += p; json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local"))) }
        client.configure(initialized().also { it.getJSONArray("extension_inventory").getJSONObject(1).remove("graph_generation") })
        client.selectAgent("main")
        try { client.calendarOperations(); fail("Calendar borrowed Personal's graph") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.UNAVAILABLE, error.reason) }
        assertEquals(1, calls.size)
    }
}
