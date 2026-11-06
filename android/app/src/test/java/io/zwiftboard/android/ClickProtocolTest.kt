package io.zwiftboard.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ClickProtocolTest {
    @Test
    fun decodesButtonBitmapAndEdges() {
        val previous = 0xFFFFFFFFL
        val pressed = ClickProtocol.decodeButtonBitmap(
            byteArrayOf(0x23, 0x08, 0xDF.toByte(), 0xFF.toByte(), 0xFF.toByte(), 0xFF.toByte(), 0x0F),
        )

        assertEquals(0xFFFFFFDFL, pressed)
        assertEquals(listOf(ButtonEdge("B", true)), ClickProtocol.edges(previous, pressed!!))
        assertEquals(listOf(ButtonEdge("B", false)), ClickProtocol.edges(pressed, previous))
    }

    @Test
    fun rejectsMalformedAndNonButtonFrames() {
        assertNull(ClickProtocol.decodeButtonBitmap(byteArrayOf(0x15, 0x08, 0x00)))
        assertNull(ClickProtocol.decodeButtonBitmap(byteArrayOf(0x23, 0x08, 0x80.toByte())))
        assertNull(ClickProtocol.decodeButtonBitmap(byteArrayOf(0x23, 0x0A, 0x05, 0x01)))
    }

    @Test
    fun skipsUnknownProtobufFields() {
        val bitmap = ClickProtocol.decodeButtonBitmap(
            byteArrayOf(0x23, 0x10, 0x07, 0x08, 0xFF.toByte(), 0xFF.toByte(), 0xFF.toByte(), 0xFF.toByte(), 0x0F),
        )

        assertEquals(0xFFFFFFFFL, bitmap)
    }
}
