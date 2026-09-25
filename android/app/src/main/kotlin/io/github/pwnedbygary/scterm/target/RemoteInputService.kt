package io.github.pwnedbygary.scterm.target

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.graphics.Path
import android.graphics.PointF
import android.media.AudioManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.provider.Settings
import android.view.KeyCharacterMap
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.FixedPoint
import io.github.pwnedbygary.scterm.protocol.Position
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Remote input for the screen-capture backend, via the public Accessibility
 * API. It is inert unless serving is on ([servingActive]) and only ever runs
 * messages the peer server has already authorized for the lease holder.
 *
 * Limits of this path (reported through capabilities, never faked): one
 * finger, keys only where a global action or text commit exists, no rotation,
 * no display power, no clipboard.
 */
class RemoteInputService : AccessibilityService() {
    private val main = Handler(Looper.getMainLooper())
    private val keyMap: KeyCharacterMap by lazy { KeyCharacterMap.load(KeyCharacterMap.VIRTUAL_KEYBOARD) }

    // Main-thread gesture state: one continued stroke at a time.
    private var stroke: GestureDescription.StrokeDescription? = null
    private var strokePointer = 0L
    private var lastPoint = PointF()
    private var lastEventAt = 0L

    override fun onServiceConnected() {
        instance = this
        notifyAvailability()
    }

    override fun onUnbind(intent: Intent?): Boolean {
        instance = null
        notifyAvailability()
        return super.onUnbind(intent)
    }

    override fun onDestroy() {
        instance = null
        notifyAvailability()
        super.onDestroy()
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) = Unit

    override fun onInterrupt() = Unit

    /**
     * Decides synchronously whether [msg] can be performed (the caller holds
     * the server's input lock), then performs it on the main thread.
     */
    fun perform(msg: ControlMessage, toScreen: (Position) -> PointF?): Boolean {
        if (!servingActive) return false
        return when (msg) {
            is ControlMessage.InjectTouch -> {
                // A touch aimed at an older geometry is dropped, as the scrcpy server does.
                toScreen(msg.position)?.let { p -> main.post { touch(msg.action, msg.pointerId, p) } }
                true
            }
            is ControlMessage.InjectScroll -> {
                toScreen(msg.position)?.let { p -> main.post { scroll(p, FixedPoint.scrollToFloat(msg.hScroll), FixedPoint.scrollToFloat(msg.vScroll)) } }
                true
            }
            is ControlMessage.InjectKeycode -> key(msg)
            is ControlMessage.InjectText -> {
                main.post { commitText(msg.text) }
                true
            }
            is ControlMessage.BackOrScreenOn -> {
                if (msg.action == AndroidInput.KEY_ACTION_DOWN) main.post { performGlobalAction(GLOBAL_ACTION_BACK) }
                true
            }
            is ControlMessage.ExpandNotificationPanel -> global(GLOBAL_ACTION_NOTIFICATIONS)
            is ControlMessage.ExpandSettingsPanel -> global(GLOBAL_ACTION_QUICK_SETTINGS)
            is ControlMessage.CollapsePanels ->
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) global(GLOBAL_ACTION_DISMISS_NOTIFICATION_SHADE) else false
            else -> false
        }
    }

    private fun global(action: Int): Boolean {
        main.post { performGlobalAction(action) }
        return true
    }

    /** Keys act on press; the release is accepted and ignored so nothing fires twice. */
    private fun key(msg: ControlMessage.InjectKeycode): Boolean {
        val down = msg.action == AndroidInput.KEY_ACTION_DOWN
        val action: () -> Unit = when (msg.keycode) {
            AndroidInput.KEYCODE_HOME -> { { performGlobalAction(GLOBAL_ACTION_HOME) } }
            AndroidInput.KEYCODE_BACK -> { { performGlobalAction(GLOBAL_ACTION_BACK) } }
            AndroidInput.KEYCODE_APP_SWITCH -> { { performGlobalAction(GLOBAL_ACTION_RECENTS) } }
            AndroidInput.KEYCODE_POWER -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                { performGlobalAction(GLOBAL_ACTION_LOCK_SCREEN) }
            } else {
                null
            }
            AndroidInput.KEYCODE_VOLUME_UP -> { { adjustVolume(AudioManager.ADJUST_RAISE) } }
            AndroidInput.KEYCODE_VOLUME_DOWN -> { { adjustVolume(AudioManager.ADJUST_LOWER) } }
            AndroidInput.KEYCODE_VOLUME_MUTE -> { { adjustVolume(AudioManager.ADJUST_TOGGLE_MUTE) } }
            AndroidInput.KEYCODE_DEL -> { { deleteBackward() } }
            AndroidInput.KEYCODE_ENTER -> { { pressEnter() } }
            in DPAD -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) dpad(msg.keycode) else null
            else -> {
                // Printable keys (the web and Android controllers send letters as
                // key events) become text: accessibility cannot inject raw keys.
                val char = keyMap.get(msg.keycode, msg.metaState)
                if (char != 0 && char and KeyCharacterMap.COMBINING_ACCENT == 0) {
                    { commitText(String(Character.toChars(char))) }
                } else {
                    null
                }
            }
        } ?: return false
        if (down) main.post(action)
        return true
    }

    private fun dpad(keycode: Int): (() -> Unit)? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return null
        val action = when (keycode) {
            AndroidInput.KEYCODE_DPAD_UP -> GLOBAL_ACTION_DPAD_UP
            AndroidInput.KEYCODE_DPAD_DOWN -> GLOBAL_ACTION_DPAD_DOWN
            AndroidInput.KEYCODE_DPAD_LEFT -> GLOBAL_ACTION_DPAD_LEFT
            AndroidInput.KEYCODE_DPAD_RIGHT -> GLOBAL_ACTION_DPAD_RIGHT
            else -> GLOBAL_ACTION_DPAD_CENTER
        }
        return { performGlobalAction(action) }
    }

    private fun adjustVolume(direction: Int) {
        getSystemService(AudioManager::class.java)
            ?.adjustStreamVolume(AudioManager.STREAM_MUSIC, direction, AudioManager.FLAG_SHOW_UI)
    }

    // ------------------------------------------------------------- gestures

    /**
     * Streams one finger as a continued stroke (API 26+): each move extends the
     * stroke by the path since the last point, lasting the time since the last
     * event, so the gesture plays back at the pace it was performed.
     */
    private fun touch(action: Int, pointer: Long, p: PointF) {
        val now = SystemClock.uptimeMillis()
        when (action) {
            AndroidInput.MOTION_ACTION_DOWN -> {
                if (stroke != null) return // one finger only
                val path = Path().apply { moveTo(p.x, p.y) }
                start(GestureDescription.StrokeDescription(path, 0, 1, true))
                strokePointer = pointer
            }
            AndroidInput.MOTION_ACTION_MOVE -> {
                val current = stroke ?: return
                if (pointer != strokePointer || (p.x == lastPoint.x && p.y == lastPoint.y)) return
                val duration = (now - lastEventAt).coerceIn(1, 100)
                extend(current.continueStroke(segment(p), 0, duration, true))
            }
            AndroidInput.MOTION_ACTION_UP, AndroidInput.MOTION_ACTION_CANCEL -> {
                val current = stroke ?: return
                if (pointer != strokePointer) return
                val path = if (p.x == lastPoint.x && p.y == lastPoint.y) {
                    Path().apply { moveTo(p.x, p.y) }
                } else {
                    segment(p)
                }
                extend(current.continueStroke(path, 0, 1, false))
                stroke = null
            }
            else -> return
        }
        lastPoint = p
        lastEventAt = now
    }

    private fun segment(to: PointF) = Path().apply {
        moveTo(lastPoint.x, lastPoint.y)
        lineTo(to.x, to.y)
    }

    private fun start(first: GestureDescription.StrokeDescription) {
        stroke = first
        extend(first)
    }

    private fun extend(next: GestureDescription.StrokeDescription) {
        if (next.willContinue()) stroke = next
        val gesture = GestureDescription.Builder().addStroke(next).build()
        dispatchGesture(
            gesture,
            object : GestureResultCallback() {
                override fun onCancelled(gestureDescription: GestureDescription?) {
                    // The system dropped the gesture (another gesture, a window change):
                    // forget it, the next DOWN starts fresh.
                    if (stroke === next) stroke = null
                }
            },
            main,
        )
    }

    private fun scroll(p: PointF, hNotches: Float, vNotches: Float) {
        val metrics = resources.displayMetrics
        // One notch moves content a tenth of the screen; positive vertical scrolls up.
        val dx = hNotches * metrics.widthPixels / 10f
        val dy = vNotches * metrics.heightPixels / 10f
        val path = Path().apply {
            moveTo(p.x, p.y)
            lineTo(
                (p.x - dx).coerceIn(0f, metrics.widthPixels - 1f),
                (p.y + dy).coerceIn(0f, metrics.heightPixels - 1f),
            )
        }
        dispatchGesture(GestureDescription.Builder().addStroke(GestureDescription.StrokeDescription(path, 0, 200)).build(), null, null)
    }

    // ----------------------------------------------------------------- text

    private fun commitText(text: String) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val connection = inputMethod?.currentInputConnection
            if (connection != null) {
                connection.commitText(text, 1, null)
                return
            }
        }
        val node = focusedEditable() ?: return
        val current = if (node.isShowingHintText) "" else node.text?.toString().orEmpty()
        val start = node.textSelectionStart.takeIf { it in 0..current.length } ?: current.length
        val end = node.textSelectionEnd.takeIf { it in start..current.length } ?: start
        setText(node, current.substring(0, start) + text + current.substring(end))
    }

    private fun deleteBackward() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            inputMethod?.currentInputConnection?.let {
                it.deleteSurroundingText(1, 0)
                return
            }
        }
        val node = focusedEditable() ?: return
        if (node.isShowingHintText) return
        val current = node.text?.toString().orEmpty()
        val end = node.textSelectionEnd.takeIf { it in 0..current.length } ?: current.length
        if (end > 0) setText(node, current.substring(0, end - 1) + current.substring(end))
    }

    private fun pressEnter() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            focusedEditable()?.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.id)
        } else {
            commitText("\n")
        }
    }

    private fun focusedEditable(): AccessibilityNodeInfo? =
        rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)?.takeIf { it.isEditable }

    private fun setText(node: AccessibilityNodeInfo, text: String) {
        val args = Bundle().apply { putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, text) }
        node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)
    }

    companion object {
        private val DPAD = setOf(
            AndroidInput.KEYCODE_DPAD_UP,
            AndroidInput.KEYCODE_DPAD_DOWN,
            AndroidInput.KEYCODE_DPAD_LEFT,
            AndroidInput.KEYCODE_DPAD_RIGHT,
            23, // KEYCODE_DPAD_CENTER
        )

        @Volatile
        var instance: RemoteInputService? = null
            private set

        /** Set by [TargetService] while serving with the screen-capture backend. */
        @Volatile
        var servingActive = false

        val isConnected: Boolean get() = instance != null

        private val listeners = CopyOnWriteArrayList<() -> Unit>()

        fun addAvailabilityListener(listener: () -> Unit) {
            listeners += listener
        }

        fun removeAvailabilityListener(listener: () -> Unit) {
            listeners -= listener
        }

        private fun notifyAvailability() = listeners.forEach { it() }

        /** Whether the user switched the service on in system settings. */
        fun isEnabled(context: Context): Boolean {
            val enabled = Settings.Secure.getString(context.contentResolver, Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES) ?: return false
            val me = ComponentName(context, RemoteInputService::class.java)
            return enabled.split(':').any { ComponentName.unflattenFromString(it) == me }
        }
    }
}
