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

    @Test fun resumeUsesCurrentPublicReplyAndRejectsStaleSelection() = runTest {
        val reply = json("id" to "reply", "agent_id" to "agent", "session_ref" to "session", "turn_id" to "turn", "state" to "failed")
        var resumed = 0
        lateinit var state: Collaboration
        state = Collaboration { method, params -> when (method) {
            "channel/bootstrap" -> json("rooms" to JSONArray(listOf(room("a"), room("b"))))
            "channel/message/list" -> json("messages" to JSONArray(), "responses" to JSONArray(listOf(reply)))
            "channel/session/resume" -> { assertEquals("session", params.getString("sessionRef")); resumed++; reply.put("state", "thinking"); json() }
            else -> json()
        } }
        state.mode(true); state.select("a"); state.refresh()
        state.resume(reply)
        assertEquals(1, resumed)
        assertTrue(runCatching { state.resume(reply) }.isFailure)
        state.select("b")
        assertTrue(runCatching { state.resume(reply) }.isFailure)
        assertEquals(1, resumed)
    }

    @Test fun attachmentReadUsesBoundedProtocolAndRejectsNavigationRace() = runTest {
        val data = java.util.Base64.getEncoder().encodeToString("%PDF-test".toByteArray())
        val digest = sha256(("application/pdf\u0000" + data).toByteArray()).joinToString("") { "%02x".format(it) }
        val reference = "channel:" + java.util.Base64.getUrlEncoder().withoutPadding().encodeToString(JSONArray(listOf("a", "file", 7, 0, digest, "files")).toString().toByteArray())
        val message = message("file", 7).put("files", JSONArray(listOf(json("media_type" to "application/pdf", "filename" to "brief.pdf", "remote_ref" to reference))))
        var switchRoom = false
        lateinit var state: Collaboration
        state = Collaboration { method, params -> when (method) {
            "channel/bootstrap" -> json("rooms" to JSONArray(listOf(room("a"), room("b"))))
            "channel/message/list" -> json("messages" to JSONArray(listOf(message)))
            "channel/attachment/read" -> {
                assertEquals("a", params.getString("room_id")); assertEquals("file", params.getString("message_id")); assertEquals(7, params.getInt("seq"))
                assertEquals(0, params.getInt("offset")); assertEquals(digest, params.getString("sha256"))
                if (switchRoom) state.select("b")
                json("data" to data, "total" to data.length, "offset" to 0, "content_type" to "application/pdf")
            }
            else -> json()
        } }
        state.mode(true); state.select("a"); state.refresh()
        assertEquals("brief.pdf", state.readAttachment(message, "files", 0).filename)
        switchRoom = true
        assertTrue(runCatching { state.readAttachment(message, "files", 0) }.exceptionOrNull() is kotlinx.coroutines.CancellationException)
    }

    @Test fun attachmentReadRejectsUnsupportedOrMalformedInlineContent() {
        assertTrue(runCatching { decodeInlineAttachment(json("media_type" to "text/html", "data" to "dGVzdA==")) }.isFailure)
        assertTrue(runCatching { decodeInlineAttachment(json("media_type" to "application/pdf", "data" to "dGVzdA==")) }.isFailure)
        assertTrue(runCatching { decodeInlineAttachment(json("media_type" to "image/png", "url" to "https://example.test/photo.png")) }.isFailure)
    }

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
