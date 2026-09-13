package ai.wuu.nativeapp

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runTest
import org.json.JSONArray
import org.junit.Assert.*
import org.junit.Test

class CollaborationTest {
    private fun room(id: String) = json("id" to id, "name" to id, "kind" to "channel")
    private fun message(id: String, seq: Int, body: String = id) = json("id" to id, "seq" to seq, "body" to body)

    @Test fun lateDirectoryCannotReturnAfterLeavingHost() = runTest {
        val pending = CompletableDeferred<Unit>()
        val started = CompletableDeferred<Unit>()
        val state = Collaboration { _, _ -> started.complete(Unit); pending.await(); json("rooms" to JSONArray(listOf(room("old")))) }
        state.mode(true)
        val read = async { state.refresh() }
        started.await(); state.clear(); pending.complete(Unit)
        runCatching { read.await() }
        assertTrue(state.rooms.isEmpty()); assertNull(state.selectedID); assertFalse(state.visible)
    }

    @Test fun switchingModeRetainsIndependentDraftAndAcknowledgesInFlightSend() = runTest {
        val pending = CompletableDeferred<Unit>(); val started = CompletableDeferred<Unit>()
        var sends = 0
        val state = Collaboration { method, params ->
            assertEquals("channel/message/send", method); assertEquals("room-a", params.getString("room_id"))
            sends++; started.complete(Unit); pending.await(); json("message" to message("sent", 1))
        }
        state.mode(true); state.select("room-a"); state.drafts["room-a"] = "hello"
        val send = async { state.send() }
        started.await(); state.mode(false); pending.complete(Unit); send.await()
        assertEquals(1, sends); assertNull(state.drafts["room-a"])
        state.mode(true); assertEquals("room-a", state.selectedID); assertEquals("sent", state.messages.single().getString("id"))
    }

    @Test fun newerDraftSurvivesAcknowledgementAndSendIsNotRetried() = runTest {
        val pending = CompletableDeferred<Unit>(); val started = CompletableDeferred<Unit>()
        var sends = 0
        val state = Collaboration { _, _ -> sends++; started.complete(Unit); pending.await(); json("message" to message("sent", 1)) }
        state.select("a"); state.drafts["a"] = "first"
        val send = async { state.send() }; started.await()
        state.drafts["a"] = "next"; state.send()
        pending.complete(Unit); send.await()
        assertEquals(1, sends); assertEquals("next", state.drafts["a"])
    }

    @Test fun taskUpdatesReplaceWithoutDuplicatingAndOlderPagesRemainChronological() {
        val initial = listOf(message("b", 2, "working"), message("c", 3))
        val merged = mergeRoomMessages(initial, listOf(message("a", 1), message("b", 2, "done")))
        assertEquals(listOf("a", "b", "c"), merged.map { it.getString("id") })
        assertEquals("done", merged[1].getString("body"))
    }

    @Test fun oldRoomReadCannotReplaceNewSelection() = runTest {
        val pending = CompletableDeferred<Unit>(); val started = CompletableDeferred<Unit>()
        val state = Collaboration { method, _ ->
            if (method == "channel/bootstrap") json("rooms" to JSONArray(listOf(room("a"), room("b"))))
            else { started.complete(Unit); pending.await(); json("messages" to JSONArray(listOf(message("old", 1)))) }
        }
        state.mode(true); state.select("a")
        val read = async { state.refresh() }; started.await(); state.select("b"); pending.complete(Unit)
        runCatching { read.await() }
        assertEquals("b", state.selectedID); assertTrue(state.messages.isEmpty())
    }

    @Test fun reselectingActiveTabDoesNotCancelRefresh() = runTest {
        val pending = CompletableDeferred<Unit>(); val started = CompletableDeferred<Unit>()
        val state = Collaboration { _, _ -> started.complete(Unit); pending.await(); json("rooms" to JSONArray(listOf(room("a")))) }
        state.mode(true)
        val read = async { state.refresh() }; started.await(); state.mode(true); pending.complete(Unit); read.await()
        assertEquals("a", state.rooms.single().getString("id"))
    }

    @Test fun olderTaskStateRefreshesOutsideLatestWindow() = runTest {
        var revision = 0
        var oldTaskReads = 0
        val state = Collaboration { method, params ->
            when (method) {
                "channel/bootstrap" -> json("rooms" to JSONArray(listOf(room("a"))))
                "channel/room/read" -> json()
                else -> {
                    assertTrue(params.getBoolean("attachment_metadata_only"))
                    val values = when {
                        params.optInt("limit") == 1 -> { oldTaskReads++; listOf(message("task", 1, "done").put("kind", "task")) }
                        revision == 0 -> listOf(message("task", 1, "working").put("kind", "task"), message("b", 2))
                        else -> listOf(message("c", 3))
                    }
                    json("messages" to JSONArray(values))
                }
            }
        }
        state.mode(true); state.select("a"); state.refresh(); revision = 1; state.refresh()
        assertEquals(1, oldTaskReads)
        assertEquals("done", state.messages.first().getString("body"))
        assertEquals(listOf("task", "b", "c"), state.messages.map { it.getString("id") })
    }

    @Test fun lateSendCannotRestoreRemovedRoomCache() = runTest {
        val pending = CompletableDeferred<Unit>(); val started = CompletableDeferred<Unit>()
        var removed = false
        val state = Collaboration { method, _ -> when (method) {
            "channel/bootstrap" -> json("rooms" to JSONArray(if (removed) emptyList() else listOf(room("a"))))
            "channel/message/list" -> json("messages" to JSONArray(listOf(message("old", 1))))
            "channel/message/send" -> { started.complete(Unit); pending.await(); json("message" to message("late", 2)) }
            else -> json()
        } }
        state.mode(true); state.select("a"); state.refresh(); state.drafts["a"] = "sent"
        val send = async { state.send() }; started.await()
        removed = true; state.refresh()
        assertNull(state.selectedID); assertTrue(state.drafts.isEmpty())
        pending.complete(Unit); send.await()
        state.select("a") // Inspect the old room cache without fetching it again.
        assertTrue(state.messages.isEmpty()); assertTrue(state.drafts.isEmpty()); assertFalse(state.sending)
    }

    @Test fun inaccessibleRoomsLoseTheirCachedContentAndDrafts() = runTest {
        var removed = false
        val state = Collaboration { method, _ -> when(method) {
            "channel/bootstrap" -> json("rooms" to JSONArray(if (removed) emptyList() else listOf(room("a"))))
            "channel/message/list" -> json("messages" to JSONArray(listOf(message("private", 1))))
            else -> json()
        } }
        state.mode(true); state.select("a"); state.refresh(); state.drafts["a"] = "draft"
        removed = true; state.refresh()
        assertNull(state.selectedID); assertTrue(state.messages.isEmpty()); assertTrue(state.drafts.isEmpty())
    }
}
