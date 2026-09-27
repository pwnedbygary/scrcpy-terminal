package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.peer.ControlPolicy.Verdict
import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Position
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class SessionPolicyTest {
    private val caps = Capabilities("t", video = true, audio = true, control = ALL_CONTROL)
    private val touch = ControlMessage.InjectTouch(0, 0, Position(1, 2, 100, 200), 0xffff, 0, 0)

    @Test
    fun viewersMayOnlyAskForKeyframes() {
        val p = ControlPolicy(Grant.VIEW_ONLY, caps)
        assertEquals(Verdict.KEYFRAME, p.evaluate(ControlMessage.ResetVideo, holdsLease = false))
        assertEquals(Verdict.FORBIDDEN, p.evaluate(touch, holdsLease = true))
        assertEquals(Verdict.FORBIDDEN, p.evaluate(ControlMessage.GetClipboard(0), holdsLease = true))
    }

    @Test
    fun controlNeedsTheLeaseAndBackendSupport() {
        val p = ControlPolicy(setOf(Grant.VIEW, Grant.CONTROL), caps)
        assertEquals(Verdict.ALLOW, p.evaluate(touch, holdsLease = true))
        assertEquals(Verdict.NO_LEASE, p.evaluate(touch, holdsLease = false))
        val limited = ControlPolicy(setOf(Grant.CONTROL), caps.copy(control = listOf("inject_touch_event")))
        assertEquals(Verdict.UNSUPPORTED, limited.evaluate(ControlMessage.RotateDevice, holdsLease = true))
        assertEquals(Verdict.ALLOW, limited.evaluate(touch, holdsLease = true))
    }

    @Test
    fun clipboardRules() {
        val clip = ControlPolicy(setOf(Grant.CLIPBOARD), caps)
        assertEquals(Verdict.ALLOW, clip.evaluate(ControlMessage.GetClipboard(ControlMessage.COPY_KEY_NONE), holdsLease = false))
        assertEquals(Verdict.FORBIDDEN, clip.evaluate(ControlMessage.GetClipboard(ControlMessage.COPY_KEY_COPY), holdsLease = true))
        assertEquals(Verdict.NO_LEASE, clip.evaluate(ControlMessage.SetClipboard(1, false, "x"), holdsLease = false))
        assertEquals(Verdict.FORBIDDEN, clip.evaluate(ControlMessage.SetClipboard(1, true, "x"), holdsLease = true))
        val both = ControlPolicy(setOf(Grant.CLIPBOARD, Grant.CONTROL), caps)
        assertEquals(Verdict.ALLOW, both.evaluate(ControlMessage.SetClipboard(1, true, "x"), holdsLease = true))
    }

    @Test
    fun dangerousTypesAreNeverForwarded() {
        val p = ControlPolicy(Grant.FULL, caps)
        for (msg in listOf(
            ControlMessage.StartApp("com.example"),
            ControlMessage.UhidCreate(1, 1, 1, "kbd", byteArrayOf()),
            ControlMessage.UhidInput(1, byteArrayOf()),
            ControlMessage.UhidDestroy(1),
            ControlMessage.OpenHardKeyboardSettings,
            ControlMessage.CameraSetTorch(true),
            ControlMessage.CameraZoomIn,
            ControlMessage.CameraZoomOut,
            ControlMessage.ResizeDisplay(1, 1),
            ControlMessage.ScanFile("/sdcard"),
        )) {
            assertEquals(Verdict.FORBIDDEN, p.evaluate(msg, holdsLease = true), msg.typeName)
        }
    }

    @Test
    fun trackerReleasesEverythingStillHeld() {
        val t = InputTracker()
        val pos = Position(10, 20, 100, 200)
        t.observe(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_DOWN, 7, pos, 0xffff, 0, 0))
        t.observe(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_MOVE, 7, pos.copy(x = 50), 0xffff, 0, 0))
        t.observe(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_DOWN, 8, pos, 0xffff, 0, 0))
        t.observe(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_UP, 8, pos, 0, 0, 0))
        t.observe(ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, 59, 0, AndroidInput.META_SHIFT_ON))
        t.observe(ControlMessage.BackOrScreenOn(AndroidInput.KEY_ACTION_DOWN))

        val releases = t.releaseAll()
        assertEquals(
            listOf(
                ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_UP, 7, pos.copy(x = 50), 0, 0, 0),
                ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_UP, 59, 0, AndroidInput.META_SHIFT_ON),
                ControlMessage.BackOrScreenOn(AndroidInput.KEY_ACTION_UP),
            ),
            releases,
        )
        assertTrue(t.isIdle)
        assertEquals(emptyList(), t.releaseAll())
    }

    @Test
    fun movesAreCoalescedOnlyWithinARunOfMoves() {
        fun ctl(action: Int, id: Long, x: Int) = ControlMessage.InjectTouch(action, id, Position(x, 0, 100, 100), 0, 0, 0)
        val batch = listOf(
            ctl(AndroidInput.MOTION_ACTION_DOWN, 1, 0),
            ctl(AndroidInput.MOTION_ACTION_MOVE, 1, 1),
            ctl(AndroidInput.MOTION_ACTION_MOVE, 2, 1),
            ctl(AndroidInput.MOTION_ACTION_MOVE, 1, 2),
            ctl(AndroidInput.MOTION_ACTION_UP, 1, 3),
            ctl(AndroidInput.MOTION_ACTION_MOVE, 2, 4),
        )
        val kept = keptAfterCoalescing(batch)
        assertEquals(listOf(0, 2, 3, 4, 5), kept, "only the move superseded by a later move of pointer 1 goes")
    }

    private fun keptAfterCoalescing(messages: List<ControlMessage>): List<Int> {
        val queued = messages.map(ControllerSession::queued)
        return queued.indices.filter { !ControllerSession.isSuperseded(queued, it) }
    }
}
