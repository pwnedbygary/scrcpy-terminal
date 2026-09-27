package io.github.pwnedbygary.scterm.viewer

import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceAction
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class InputForwarderTest {
    private val sent = ArrayList<ControlMessage>()
    private val input = InputForwarder { sent += it }

    private fun press(keycode: Int, meta: Int = 0): List<ControlMessage> = listOf(
        ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, keycode, 0, meta),
        ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_UP, keycode, 0, meta),
    )

    /** Same split as the web client: letters, digits and space are keys, the rest is text. */
    @Test
    fun typingSplitsKeysFromText() {
        input.type("Hi, é!")
        assertEquals(
            press(36, AndroidInput.META_SHIFT_ON) + press(37) +
                ControlMessage.InjectText(",") + press(AndroidInput.KEYCODE_SPACE) +
                ControlMessage.InjectText("é!"),
            sent,
        )
    }

    @Test
    fun digitsAndEnterAreKeys() {
        input.type("07\n")
        assertEquals(press(7) + press(14) + press(AndroidInput.KEYCODE_ENTER), sent)
    }

    @Test
    fun longTextIsChunkedForTheWire() {
        input.type("…".repeat(250)) // 750 UTF-8 bytes
        assertTrue(sent.all { it is ControlMessage.InjectText && it.text.toByteArray().size <= ControlMessage.MAX_INJECT_TEXT_BYTES })
        assertEquals("…".repeat(250), sent.joinToString("") { (it as ControlMessage.InjectText).text })
        assertEquals(3, sent.size)
    }

    @Test
    fun actionsSendTheSharedCatalogMessages() {
        input.press(DeviceAction.MUTE)
        assertEquals(press(AndroidInput.KEYCODE_VOLUME_MUTE), sent)
    }
}
