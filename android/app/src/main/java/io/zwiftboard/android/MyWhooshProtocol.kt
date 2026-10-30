package io.zwiftboard.android

internal object MyWhooshProtocol {
    const val PORT = 21587

    val connectedDevicesList =
        """{"ConnectedDevices":[],"MessageType":"ConnectedDevicesList"}""" + "\n"

    fun controlLine(button: String, pressed: Boolean): String? {
        val control = when (button to pressed) {
            "PLUS" to true -> """"GearShifting":"-1""""
            "B" to true -> """"GearShifting":"1""""
            "A" to true -> """"Steering":"1""""
            "A" to false, "Z" to false -> """"Steering":"0""""
            "Z" to true -> """"Steering":"-1""""
            else -> return null
        }
        return """{"MessageType":"Controls","InGameControls":{$control}}""" + "\n"
    }
}
