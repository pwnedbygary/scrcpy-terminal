package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Position

/**
 * What a target does with each control message from one session. Every
 * message is fully parsed before this runs; nothing is forwarded on trust.
 */
class ControlPolicy(private val grants: Set<Grant>, private val capabilities: Capabilities) {
    enum class Verdict {
        /** Forward to the backend. */
        ALLOW,

        /** A viewer asking for a decodable frame: handled by the fanout, not forwarded. */
        KEYFRAME,

        /** Input from a session that does not hold the control lease. */
        NO_LEASE,

        /** The pairing grants do not cover it, or it is never allowed remotely. */
        FORBIDDEN,

        /** Allowed, but this backend cannot perform it. */
        UNSUPPORTED,
    }

    fun evaluate(msg: ControlMessage, holdsLease: Boolean): Verdict {
        val needs: Set<Grant>
        val needsLease: Boolean
        when (msg) {
            is ControlMessage.ResetVideo ->
                return if (Grant.VIEW in grants) Verdict.KEYFRAME else Verdict.FORBIDDEN
            is ControlMessage.InjectKeycode, is ControlMessage.InjectText, is ControlMessage.InjectTouch,
            is ControlMessage.InjectScroll, is ControlMessage.BackOrScreenOn, is ControlMessage.ExpandNotificationPanel,
            is ControlMessage.ExpandSettingsPanel, is ControlMessage.CollapsePanels, is ControlMessage.RotateDevice,
            is ControlMessage.SetDisplayPower,
            -> {
                needs = setOf(Grant.CONTROL)
                needsLease = true
            }
            is ControlMessage.GetClipboard -> {
                // COPY/CUT inject a key press on the device; a plain read does not.
                val injects = msg.copyKey != ControlMessage.COPY_KEY_NONE
                needs = if (injects) setOf(Grant.CLIPBOARD, Grant.CONTROL) else setOf(Grant.CLIPBOARD)
                needsLease = injects
            }
            is ControlMessage.SetClipboard -> {
                needs = if (msg.paste) setOf(Grant.CLIPBOARD, Grant.CONTROL) else setOf(Grant.CLIPBOARD)
                needsLease = true
            }
            // Launching apps, virtual HID devices, display resizing, media scans and
            // camera controls are outside what a remote peer may do.
            else -> return Verdict.FORBIDDEN
        }
        if (!grants.containsAll(needs)) return Verdict.FORBIDDEN
        if (needsLease && !holdsLease) return Verdict.NO_LEASE
        if (!capabilities.supports(msg.type)) return Verdict.UNSUPPORTED
        return Verdict.ALLOW
    }
}

/**
 * What one session currently holds down on the device. When the session ends
 * or loses the control lease, [releaseAll] produces the matching releases, so
 * a dropped connection can never leave a finger or key stuck down.
 */
class InputTracker {
    private class Pointer(var position: Position, val actionButton: Int)

    private val keys = LinkedHashMap<Int, Int>()
    private val pointers = LinkedHashMap<Long, Pointer>()
    private var backDown = false

    val isIdle: Boolean get() = keys.isEmpty() && pointers.isEmpty() && !backDown

    fun observe(msg: ControlMessage) {
        when (msg) {
            is ControlMessage.InjectKeycode -> when (msg.action) {
                AndroidInput.KEY_ACTION_DOWN -> keys[msg.keycode] = msg.metaState
                AndroidInput.KEY_ACTION_UP -> keys.remove(msg.keycode)
            }
            is ControlMessage.InjectTouch -> when (msg.action) {
                AndroidInput.MOTION_ACTION_DOWN -> pointers[msg.pointerId] = Pointer(msg.position, msg.actionButton)
                AndroidInput.MOTION_ACTION_MOVE -> pointers[msg.pointerId]?.position = msg.position
                AndroidInput.MOTION_ACTION_UP, AndroidInput.MOTION_ACTION_CANCEL -> pointers.remove(msg.pointerId)
            }
            is ControlMessage.BackOrScreenOn -> backDown = msg.action == AndroidInput.KEY_ACTION_DOWN
            else -> Unit
        }
    }

    fun releaseAll(): List<ControlMessage> {
        val out = ArrayList<ControlMessage>(pointers.size + keys.size + 1)
        for ((id, p) in pointers) {
            out += ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_UP, id, p.position, 0, p.actionButton, 0)
        }
        for ((keycode, meta) in keys) {
            out += ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_UP, keycode, 0, meta)
        }
        if (backDown) out += ControlMessage.BackOrScreenOn(AndroidInput.KEY_ACTION_UP)
        pointers.clear()
        keys.clear()
        backDown = false
        return out
    }
}
