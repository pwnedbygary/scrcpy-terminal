package io.github.pwnedbygary.scterm.ui

import io.github.pwnedbygary.scterm.protocol.Grant

/**
 * What a device just paired from here may do on this device, in wire names.
 * "Also let it see, hear and control this device" grants view, audio and
 * control, never the clipboard (an invitation's defaults); unchecked, the
 * device keeps what it had, or nothing.
 */
fun inboundAfterPairing(allowBack: Boolean, previous: List<String>?): List<String> =
    if (allowBack) Grant.toWire(Grant.FULL - Grant.CLIPBOARD) else previous ?: emptyList()
