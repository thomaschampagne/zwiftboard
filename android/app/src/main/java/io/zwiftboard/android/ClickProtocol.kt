package io.zwiftboard.android

internal data class ButtonEdge(val button: String, val pressed: Boolean)

internal object ClickProtocol {
    const val COMPANY_ID = 0x094A
    const val LEFT_DEVICE_ID = 0x0B
    const val RIGHT_DEVICE_ID = 0x0A
    const val KEY_PAD_MESSAGE = 0x23

    val asyncUuid = java.util.UUID.fromString("00000002-19ca-4651-86e5-fa29dcdd09d1")
    val syncRxUuid = java.util.UUID.fromString("00000003-19ca-4651-86e5-fa29dcdd09d1")
    val syncTxUuid = java.util.UUID.fromString("00000004-19ca-4651-86e5-fa29dcdd09d1")
    val clientConfigurationUuid =
        java.util.UUID.fromString("00002902-0000-1000-8000-00805f9b34fb")

    val handshakeStart = "RideOn".toByteArray() + byteArrayOf(0x02, 0x03)
    val handshakeSteps = listOf(
        handshakeStart,
        byteArrayOf(0x00, 0x08, 0x00),
        byteArrayOf(0x00, 0x08, 0x10),
    )
    val keepalive = "RideOn".toByteArray()
    val challengeResponse = byteArrayOf(0xFF.toByte(), 0x04, 0x00)
    val idleReset = byteArrayOf(0x18)

    private val buttonBits = listOf(
        0x00001L to "LEFT",
        0x00002L to "UP",
        0x00004L to "RIGHT",
        0x00008L to "DOWN",
        0x00010L to "A",
        0x00020L to "B",
        0x00040L to "Y",
        0x00080L to "Z",
        0x00100L to "MIN",
        0x01000L to "PLUS",
    )

    fun decodeButtonBitmap(frame: ByteArray): Long? {
        if (frame.isEmpty() || (frame[0].toInt() and 0xFF) != KEY_PAD_MESSAGE) return null

        var index = 1
        while (index < frame.size) {
            val tag = readVarint(frame, index) ?: return null
            index = tag.second
            val fieldNumber = (tag.first ushr 3).toInt()
            val wireType = (tag.first and 7L).toInt()
            if (fieldNumber == 0) return null

            when (wireType) {
                0 -> {
                    val value = readVarint(frame, index) ?: return null
                    index = value.second
                    if (fieldNumber == 1) return value.first and 0xFFFFFFFFL
                }
                1 -> {
                    if (frame.size - index < 8) return null
                    index += 8
                }
                2 -> {
                    val length = readVarint(frame, index) ?: return null
                    index = length.second
                    if (length.first > (frame.size - index).toLong()) return null
                    index += length.first.toInt()
                }
                5 -> {
                    if (frame.size - index < 4) return null
                    index += 4
                }
                else -> return null
            }
        }
        return null
    }

    fun edges(previous: Long, current: Long): List<ButtonEdge> =
        buttonBits.mapNotNull { (bit, name) ->
            if ((previous xor current) and bit == 0L) {
                null
            } else {
                ButtonEdge(name, current and bit == 0L)
            }
        }

    private fun readVarint(bytes: ByteArray, start: Int): Pair<Long, Int>? {
        var value = 0L
        var index = start
        for (byteIndex in 0 until 10) {
            if (index >= bytes.size) return null
            val byte = bytes[index].toInt() and 0xFF
            index++
            if (byteIndex == 9 && byte > 1) return null
            value = value or ((byte and 0x7F).toLong() shl (byteIndex * 7))
            if (byte and 0x80 == 0) return value to index
        }
        return null
    }
}
