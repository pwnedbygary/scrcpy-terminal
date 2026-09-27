package io.github.pwnedbygary.scterm.viewer

import android.view.InputDevice
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceAction
import io.github.pwnedbygary.scterm.protocol.FixedPoint
import io.github.pwnedbygary.scterm.protocol.PointerId
import io.github.pwnedbygary.scterm.protocol.Position

/**
 * Local input to scrcpy control messages for one session (UI thread only).
 *
 * Fingers keep their Android pointer ids, so multi-touch reaches targets that
 * support it; a mouse uses scrcpy's mouse pointer, with right click = Back and
 * middle click = Home as in the terminal and browser clients. Positions are in
 * video pixels with the current video size attached, so a touch generated
 * against an older geometry is dropped by the target instead of landing in
 * the wrong place.
 */
class InputForwarder(private val send: (ControlMessage) -> Unit) {
    private var videoWidth = 0
    private var videoHeight = 0
    private val downPointers = HashMap<Int, Position>()
    private var mouseDown: Position? = null

    fun setVideoSize(width: Int, height: Int) {
        if (width != videoWidth || height != videoHeight) releaseAll()
        videoWidth = width
        videoHeight = height
    }

    fun onTouch(view: View, event: MotionEvent): Boolean {
        if (videoWidth == 0 || view.width == 0) return true
        if (event.isFromSource(InputDevice.SOURCE_MOUSE)) return onMouse(view, event)
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN, MotionEvent.ACTION_POINTER_DOWN -> {
                val i = event.actionIndex
                val pos = position(view, event, i)
                downPointers[event.getPointerId(i)] = pos
                send(touch(AndroidInput.MOTION_ACTION_DOWN, event.getPointerId(i), pos, pressure(event, i)))
            }
            MotionEvent.ACTION_MOVE -> for (i in 0 until event.pointerCount) {
                val id = event.getPointerId(i)
                val last = downPointers[id] ?: continue
                val pos = position(view, event, i)
                if (pos == last) continue
                downPointers[id] = pos
                send(touch(AndroidInput.MOTION_ACTION_MOVE, id, pos, pressure(event, i)))
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_POINTER_UP -> {
                val i = event.actionIndex
                val id = event.getPointerId(i)
                if (downPointers.remove(id) != null) send(touch(AndroidInput.MOTION_ACTION_UP, id, position(view, event, i), 0))
            }
            MotionEvent.ACTION_CANCEL -> releaseAll()
        }
        return true
    }

    /** Hover, wheel and mouse buttons other than the primary one. */
    fun onGenericMotion(view: View, event: MotionEvent): Boolean {
        if (videoWidth == 0 || view.width == 0) return false
        when (event.actionMasked) {
            MotionEvent.ACTION_HOVER_MOVE -> send(
                ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_HOVER_MOVE, PointerId.MOUSE, position(view, event, 0), 0, 0, 0),
            )
            MotionEvent.ACTION_SCROLL -> send(
                ControlMessage.InjectScroll(
                    position(view, event, 0),
                    FixedPoint.scroll(event.getAxisValue(MotionEvent.AXIS_HSCROLL)),
                    FixedPoint.scroll(event.getAxisValue(MotionEvent.AXIS_VSCROLL)),
                    0,
                ),
            )
            MotionEvent.ACTION_BUTTON_PRESS -> when (event.actionButton) {
                MotionEvent.BUTTON_SECONDARY -> press(DeviceAction.BACK)
                MotionEvent.BUTTON_TERTIARY -> press(DeviceAction.HOME)
                else -> return false
            }
            else -> return false
        }
        return true
    }

    private fun onMouse(view: View, event: MotionEvent): Boolean {
        val pos = position(view, event, 0)
        val primary = AndroidInput.BUTTON_PRIMARY
        when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                // Secondary/tertiary clicks arrive as ACTION_BUTTON_PRESS (onGenericMotion).
                val others = MotionEvent.BUTTON_SECONDARY or MotionEvent.BUTTON_TERTIARY
                if (event.buttonState and others != 0 && event.buttonState and MotionEvent.BUTTON_PRIMARY == 0) return true
                mouseDown = pos
                send(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_DOWN, PointerId.MOUSE, pos, 0xffff, primary, primary))
            }
            MotionEvent.ACTION_MOVE -> if (mouseDown != null && pos != mouseDown) {
                mouseDown = pos
                send(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_MOVE, PointerId.MOUSE, pos, 0xffff, 0, primary))
            }
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> if (mouseDown != null) {
                mouseDown = null
                send(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_UP, PointerId.MOUSE, pos, 0, primary, 0))
            }
        }
        return true
    }

    /** Hardware keyboard keys. Returns true when consumed. */
    fun onKey(event: KeyEvent): Boolean {
        val down = event.action == KeyEvent.ACTION_DOWN
        if (event.isAltPressed && !event.isCtrlPressed) {
            // Alt+letter mnemonics: the same table as the terminal and browser.
            val action = DeviceAction.byMnemonic(event.getUnicodeChar(0).toChar())
            if (action != null) {
                if (down && event.repeatCount == 0) press(action)
                return true
            }
        }
        if (event.keyCode == KeyEvent.KEYCODE_ESCAPE) {
            if (down && event.repeatCount == 0) press(DeviceAction.BACK)
            return true
        }
        val action = when (event.action) {
            KeyEvent.ACTION_DOWN -> AndroidInput.KEY_ACTION_DOWN
            KeyEvent.ACTION_UP -> AndroidInput.KEY_ACTION_UP
            else -> return false
        }
        send(ControlMessage.InjectKeycode(action, event.keyCode, event.repeatCount, event.metaState))
        return true
    }

    /**
     * Soft-keyboard text: letters, digits, space and Enter as key presses
     * (games and remote IMEs react to real keys), everything else as injected
     * text in scrcpy-sized chunks. Mirrors the web client's typing.
     */
    fun type(text: String) {
        val pending = StringBuilder()
        fun flush() {
            if (pending.isEmpty()) return
            ControlMessage.textChunks(pending.toString()).forEach(send)
            pending.setLength(0)
        }
        for (ch in text) {
            val key = asciiKey(ch)
            if (key == null) {
                pending.append(ch)
            } else {
                flush()
                pressKey(key.first, key.second)
            }
        }
        flush()
    }

    fun pressKey(keycode: Int, metaState: Int = 0) {
        send(ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, keycode, 0, metaState))
        send(ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_UP, keycode, 0, metaState))
    }

    fun press(action: DeviceAction) = action.messages().forEach(send)

    /** Lifts every finger and mouse button still down (focus loss, rotation, pause). */
    fun releaseAll() {
        for ((id, pos) in downPointers) send(touch(AndroidInput.MOTION_ACTION_UP, id, pos, 0))
        downPointers.clear()
        mouseDown?.let {
            send(ControlMessage.InjectTouch(AndroidInput.MOTION_ACTION_UP, PointerId.MOUSE, it, 0, AndroidInput.BUTTON_PRIMARY, 0))
        }
        mouseDown = null
    }

    private fun touch(action: Int, id: Int, pos: Position, pressure: Int) =
        ControlMessage.InjectTouch(action, id.toLong(), pos, pressure, 0, 0)

    /** Stylus pressure is real; fingers press at full strength like the other clients. */
    private fun pressure(event: MotionEvent, i: Int): Int =
        if (event.getToolType(i) == MotionEvent.TOOL_TYPE_STYLUS) FixedPoint.u16(event.getPressure(i)) else 0xffff

    private fun position(view: View, event: MotionEvent, i: Int): Position {
        val x = (event.getX(i) * videoWidth / view.width).toInt().coerceIn(0, videoWidth - 1)
        val y = (event.getY(i) * videoHeight / view.height).toInt().coerceIn(0, videoHeight - 1)
        return Position(x, y, videoWidth, videoHeight)
    }

    private fun asciiKey(c: Char): Pair<Int, Int>? = when (c) {
        ' ' -> AndroidInput.KEYCODE_SPACE to 0
        '\n' -> AndroidInput.KEYCODE_ENTER to 0
        in 'a'..'z' -> (KEYCODE_A + (c - 'a')) to 0
        in 'A'..'Z' -> (KEYCODE_A + (c - 'A')) to AndroidInput.META_SHIFT_ON
        in '0'..'9' -> (KEYCODE_0 + (c - '0')) to 0
        else -> null
    }

    private companion object {
        const val KEYCODE_A = 29
        const val KEYCODE_0 = 7
    }
}
