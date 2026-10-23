package io.github.pwnedbygary.scterm.ui

import kotlin.test.Test
import kotlin.test.assertEquals

class PairingGrantsTest {
    @Test
    fun allowingBackGrantsViewAudioAndControlButNeverTheClipboard() {
        for (previous in listOf<List<String>?>(null, emptyList(), listOf("view", "clipboard"), listOf("view", "audio", "control", "clipboard"))) {
            assertEquals(listOf("view", "audio", "control"), inboundAfterPairing(allowBack = true, previous = previous), "previously $previous")
        }
    }

    @Test
    fun leavingItUncheckedKeepsWhatTheDeviceHad() {
        assertEquals(emptyList(), inboundAfterPairing(allowBack = false, previous = null))
        assertEquals(listOf("view", "clipboard"), inboundAfterPairing(allowBack = false, previous = listOf("view", "clipboard")))
    }
}
