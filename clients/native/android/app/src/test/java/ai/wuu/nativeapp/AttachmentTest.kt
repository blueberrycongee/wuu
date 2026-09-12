package ai.wuu.nativeapp

import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayInputStream

class AttachmentTest {
    @Test fun inputAndCombinedBudgetRejectOversizeBeforeSending() {
        val large = InputAttachment("document.pdf", "application/pdf", ByteArray(InputAttachment.MAX_BYTES))
        val small = InputAttachment("small.pdf", "application/pdf", byteArrayOf(1))
        InputAttachment.validate(listOf(large), "")
        assertThrows(IllegalArgumentException::class.java) { InputAttachment.validate(listOf(large, small), "") }
        assertThrows(IllegalArgumentException::class.java) { InputAttachment.validate(List(5) { small }, "") }
        assertThrows(IllegalArgumentException::class.java) { InputAttachment.validate(emptyList(), "🌱".repeat(40_000)) }
        assertThrows(IllegalArgumentException::class.java) { InputAttachment("empty.pdf", "application/pdf", byteArrayOf()) }
        assertArrayEquals(byteArrayOf(1, 2), ByteArrayInputStream(byteArrayOf(1, 2)).readBounded(2))
        assertThrows(IllegalArgumentException::class.java) { ByteArrayInputStream(byteArrayOf(1, 2, 3)).readBounded(2) }
    }
}
