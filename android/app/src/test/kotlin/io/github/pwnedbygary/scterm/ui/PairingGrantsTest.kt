package io.github.pwnedbygary.scterm.ui

import io.github.pwnedbygary.scterm.protocol.Grant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class PairingGrantsTest {
    @Test
    fun allowingBackGrantsViewAudioAndControlButNeverTheClipboard() {
        for (previous in listOf<List<String>?>(null, emptyList(), listOf("view", "clipboard"), listOf("view", "audio", "control", "clipboard"))) {
            val granted = inboundAfterPairing(allowBack = true, previous = previous)
            assertEquals(setOf(Grant.VIEW, Grant.AUDIO, Grant.CONTROL), Grant.parse(granted), "previously $previous")
            assertFalse("clipboard" in granted, "previously $previous")
        }
    }

    @Test
    fun leavingItUncheckedKeepsWhatTheDeviceHad() {
        assertEquals(emptyList(), inboundAfterPairing(allowBack = false, previous = null))
        assertEquals(listOf("view", "clipboard"), inboundAfterPairing(allowBack = false, previous = listOf("view", "clipboard")))
    }
}
