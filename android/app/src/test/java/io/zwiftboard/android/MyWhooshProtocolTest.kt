package io.zwiftboard.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class MyWhooshProtocolTest {
    @Test
    fun mapsClickButtonsToMyWhooshControls() {
        assertEquals(
            """{"MessageType":"Controls","InGameControls":{"GearShifting":"-1"}}""" + "\n",
            MyWhooshProtocol.controlLine("PLUS", true),
        )
        assertEquals(
            """{"MessageType":"Controls","InGameControls":{"GearShifting":"1"}}""" + "\n",
            MyWhooshProtocol.controlLine("B", true),
        )
        assertEquals(
            """{"MessageType":"Controls","InGameControls":{"Steering":"-1"}}""" + "\n",
            MyWhooshProtocol.controlLine("Z", true),
        )
        assertEquals(
            """{"MessageType":"Controls","InGameControls":{"Steering":"0"}}""" + "\n",
            MyWhooshProtocol.controlLine("Z", false),
        )
    }

    @Test
    fun ignoresUnmappedAndMomentaryReleaseEvents() {
        assertNull(MyWhooshProtocol.controlLine("MIN", true))
        assertNull(MyWhooshProtocol.controlLine("PLUS", false))
    }
}
