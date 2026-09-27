package io.github.pwnedbygary.scterm.protocol

import java.io.ByteArrayInputStream
import java.io.DataInputStream
import java.io.EOFException

/** Android input constants that appear on the wire (this module has no android.jar). */
object AndroidInput {
    const val KEY_ACTION_DOWN = 0
    const val KEY_ACTION_UP = 1

    const val MOTION_ACTION_DOWN = 0
    const val MOTION_ACTION_UP = 1
    const val MOTION_ACTION_MOVE = 2
    const val MOTION_ACTION_CANCEL = 3
    const val MOTION_ACTION_HOVER_MOVE = 7
    const val MOTION_ACTION_BUTTON_PRESS = 11
    const val MOTION_ACTION_BUTTON_RELEASE = 12

    const val BUTTON_PRIMARY = 1
    const val BUTTON_SECONDARY = 2
    const val BUTTON_TERTIARY = 4

    const val META_SHIFT_ON = 0x1
    const val META_ALT_ON = 0x2
    const val META_CTRL_ON = 0x1000
    const val META_META_ON = 0x10000

    const val KEYCODE_HOME = 3
    const val KEYCODE_BACK = 4
    const val KEYCODE_DPAD_UP = 19
    const val KEYCODE_DPAD_DOWN = 20
    const val KEYCODE_DPAD_LEFT = 21
    const val KEYCODE_DPAD_RIGHT = 22
    const val KEYCODE_VOLUME_UP = 24
    const val KEYCODE_VOLUME_DOWN = 25
    const val KEYCODE_POWER = 26
    const val KEYCODE_SPACE = 62
    const val KEYCODE_ENTER = 66
    const val KEYCODE_DEL = 67
    const val KEYCODE_MENU = 82

    /** Microphone mute. Not what a "mute" button means; see KEYCODE_VOLUME_MUTE. */
    const val KEYCODE_MUTE = 91
    const val KEYCODE_FORWARD_DEL = 112
    const val KEYCODE_VOLUME_MUTE = 164
    const val KEYCODE_APP_SWITCH = 187
}

/** scrcpy's reserved pointer ids (control_msg.h); real fingers use ids >= 0. */
object PointerId {
    const val MOUSE = -1L
    const val GENERIC_FINGER = -2L
    const val VIRTUAL_FINGER = -3L
}

/**
 * A point in video coordinates plus the video size the sender saw. The
 * server drops the event if that size differs from the current capture, which
 * is what keeps a stale touch from landing after a rotation.
 */
data class Position(val x: Int, val y: Int, val screenWidth: Int, val screenHeight: Int) {
    init {
        require(screenWidth in 0..0xffff && screenHeight in 0..0xffff) {
            "screen size ${screenWidth}x$screenHeight does not fit u16"
        }
    }

    internal fun encode(out: ByteSink) {
        out.i32(x)
        out.i32(y)
        out.u16(screenWidth)
        out.u16(screenHeight)
    }

    internal companion object {
        fun read(input: DataInputStream) =
            Position(input.readInt(), input.readInt(), input.readUnsignedShort(), input.readUnsignedShort())
    }
}

/** The C client's fixed-point conversions (sc_float_to_u16fp / sc_float_to_i16fp). */
object FixedPoint {
    fun u16(value: Float): Int {
        val u = (value.coerceIn(0f, 1f) * 65536f).toLong()
        return if (u >= 0xffff) 0xffff else u.toInt()
    }

    fun u16ToFloat(raw: Int): Float = if (raw == 0xffff) 1f else raw / 65536f

    /** Scroll is sent in wheel notches, [-16, 16], as i16 fixed point of notches/16. */
    fun scroll(notches: Float): Short {
        val i = ((notches / 16f).coerceIn(-1f, 1f) * 32768f).toInt()
        return (if (i >= 0x7fff) 0x7fff else i).toShort()
    }

    fun scrollToFloat(raw: Short): Float = (if (raw.toInt() == 0x7fff) 1f else raw / 32768f) * 16f
}

/**
 * Controller -> device messages, byte-compatible with scrcpy v4.1
 * ControlMessageReader. Fields keep their raw wire values so parse and
 * serialize round-trip exactly, which the target's validating bridge relies on.
 */
sealed class ControlMessage(val type: Int) {
    val typeName: String get() = typeName(type)

    internal abstract fun encodeBody(out: ByteSink)

    fun toByteArray(): ByteArray {
        val sink = ByteSink()
        sink.u8(type)
        encodeBody(sink)
        return sink.toByteArray()
    }

    data class InjectKeycode(val action: Int, val keycode: Int, val repeat: Int, val metaState: Int) :
        ControlMessage(TYPE_INJECT_KEYCODE) {
        override fun encodeBody(out: ByteSink) {
            out.u8(action)
            out.i32(keycode)
            out.i32(repeat)
            out.i32(metaState)
        }
    }

    /** At most [MAX_INJECT_TEXT_BYTES] of UTF-8; use [textChunks] for longer input. */
    data class InjectText(val text: String) : ControlMessage(TYPE_INJECT_TEXT) {
        private val utf8 = text.toByteArray(Charsets.UTF_8)

        init {
            require(utf8.size <= MAX_INJECT_TEXT_BYTES) { "inject text of ${utf8.size} bytes exceeds $MAX_INJECT_TEXT_BYTES" }
        }

        override fun encodeBody(out: ByteSink) {
            out.i32(utf8.size)
            out.bytes(utf8)
        }
    }

    data class InjectTouch(
        val action: Int,
        val pointerId: Long,
        val position: Position,
        /** u16 fixed point; see [FixedPoint.u16]. */
        val pressure: Int,
        val actionButton: Int,
        val buttons: Int,
    ) : ControlMessage(TYPE_INJECT_TOUCH_EVENT) {
        override fun encodeBody(out: ByteSink) {
            out.u8(action)
            out.i64(pointerId)
            position.encode(out)
            out.u16(pressure)
            out.i32(actionButton)
            out.i32(buttons)
        }
    }

    data class InjectScroll(
        val position: Position,
        /** i16 fixed point of notches/16; see [FixedPoint.scroll]. */
        val hScroll: Short,
        val vScroll: Short,
        val buttons: Int,
    ) : ControlMessage(TYPE_INJECT_SCROLL_EVENT) {
        override fun encodeBody(out: ByteSink) {
            position.encode(out)
            out.u16(hScroll.toInt())
            out.u16(vScroll.toInt())
            out.i32(buttons)
        }
    }

    /** Back, or screen-on when the display is off (scrcpy's "back or screen on"). */
    data class BackOrScreenOn(val action: Int) : ControlMessage(TYPE_BACK_OR_SCREEN_ON) {
        override fun encodeBody(out: ByteSink) = out.u8(action)
    }

    data object ExpandNotificationPanel : ControlMessage(TYPE_EXPAND_NOTIFICATION_PANEL) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    data object ExpandSettingsPanel : ControlMessage(TYPE_EXPAND_SETTINGS_PANEL) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    data object CollapsePanels : ControlMessage(TYPE_COLLAPSE_PANELS) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    data class GetClipboard(val copyKey: Int) : ControlMessage(TYPE_GET_CLIPBOARD) {
        override fun encodeBody(out: ByteSink) = out.u8(copyKey)
    }

    data class SetClipboard(val sequence: Long, val paste: Boolean, val text: String) :
        ControlMessage(TYPE_SET_CLIPBOARD) {
        private val utf8 = text.toByteArray(Charsets.UTF_8)

        init {
            require(utf8.size <= MAX_CLIPBOARD_TEXT_BYTES) { "clipboard of ${utf8.size} bytes exceeds $MAX_CLIPBOARD_TEXT_BYTES" }
        }

        override fun encodeBody(out: ByteSink) {
            out.i64(sequence)
            out.u8(if (paste) 1 else 0)
            out.i32(utf8.size)
            out.bytes(utf8)
        }
    }

    data class SetDisplayPower(val on: Boolean) : ControlMessage(TYPE_SET_DISPLAY_POWER) {
        override fun encodeBody(out: ByteSink) = out.u8(if (on) 1 else 0)
    }

    data object RotateDevice : ControlMessage(TYPE_ROTATE_DEVICE) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    class UhidCreate(val id: Int, val vendorId: Int, val productId: Int, val name: String, val reportDesc: ByteArray) :
        ControlMessage(TYPE_UHID_CREATE) {
        private val nameUtf8 = name.toByteArray(Charsets.UTF_8)

        init {
            require(nameUtf8.size <= 0xff && reportDesc.size <= 0xffff) { "uhid create field too long" }
        }

        override fun encodeBody(out: ByteSink) {
            out.u16(id)
            out.u16(vendorId)
            out.u16(productId)
            out.u8(nameUtf8.size)
            out.bytes(nameUtf8)
            out.u16(reportDesc.size)
            out.bytes(reportDesc)
        }

        override fun equals(other: Any?) = other is UhidCreate && id == other.id && vendorId == other.vendorId &&
            productId == other.productId && name == other.name && reportDesc.contentEquals(other.reportDesc)

        override fun hashCode() = arrayOf<Any>(id, vendorId, productId, name, reportDesc.contentHashCode()).contentHashCode()
    }

    class UhidInput(val id: Int, val data: ByteArray) : ControlMessage(TYPE_UHID_INPUT) {
        init {
            require(data.size <= 0xffff) { "uhid input too long" }
        }

        override fun encodeBody(out: ByteSink) {
            out.u16(id)
            out.u16(data.size)
            out.bytes(data)
        }

        override fun equals(other: Any?) = other is UhidInput && id == other.id && data.contentEquals(other.data)

        override fun hashCode() = 31 * id + data.contentHashCode()
    }

    data class UhidDestroy(val id: Int) : ControlMessage(TYPE_UHID_DESTROY) {
        override fun encodeBody(out: ByteSink) = out.u16(id)
    }

    data object OpenHardKeyboardSettings : ControlMessage(TYPE_OPEN_HARD_KEYBOARD_SETTINGS) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    data class StartApp(val name: String) : ControlMessage(TYPE_START_APP) {
        private val utf8 = name.toByteArray(Charsets.UTF_8)

        init {
            require(utf8.size <= 0xff) { "app name too long" }
        }

        override fun encodeBody(out: ByteSink) {
            out.u8(utf8.size)
            out.bytes(utf8)
        }
    }

    /** Restart the capture: the device resends session, config and a keyframe. */
    data object ResetVideo : ControlMessage(TYPE_RESET_VIDEO) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    data class CameraSetTorch(val on: Boolean) : ControlMessage(TYPE_CAMERA_SET_TORCH) {
        override fun encodeBody(out: ByteSink) = out.u8(if (on) 1 else 0)
    }

    data object CameraZoomIn : ControlMessage(TYPE_CAMERA_ZOOM_IN) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    data object CameraZoomOut : ControlMessage(TYPE_CAMERA_ZOOM_OUT) {
        override fun encodeBody(out: ByteSink) = Unit
    }

    data class ResizeDisplay(val width: Int, val height: Int) : ControlMessage(TYPE_RESIZE_DISPLAY) {
        override fun encodeBody(out: ByteSink) {
            out.u16(width)
            out.u16(height)
        }
    }

    data class ScanFile(val path: String) : ControlMessage(TYPE_SCAN_FILE) {
        private val utf8 = path.toByteArray(Charsets.UTF_8)

        init {
            require(utf8.size <= MAX_PATH_BYTES) { "path too long" }
        }

        override fun encodeBody(out: ByteSink) {
            out.i32(utf8.size)
            out.bytes(utf8)
        }
    }

    companion object {
        const val TYPE_INJECT_KEYCODE = 0
        const val TYPE_INJECT_TEXT = 1
        const val TYPE_INJECT_TOUCH_EVENT = 2
        const val TYPE_INJECT_SCROLL_EVENT = 3
        const val TYPE_BACK_OR_SCREEN_ON = 4
        const val TYPE_EXPAND_NOTIFICATION_PANEL = 5
        const val TYPE_EXPAND_SETTINGS_PANEL = 6
        const val TYPE_COLLAPSE_PANELS = 7
        const val TYPE_GET_CLIPBOARD = 8
        const val TYPE_SET_CLIPBOARD = 9
        const val TYPE_SET_DISPLAY_POWER = 10
        const val TYPE_ROTATE_DEVICE = 11
        const val TYPE_UHID_CREATE = 12
        const val TYPE_UHID_INPUT = 13
        const val TYPE_UHID_DESTROY = 14
        const val TYPE_OPEN_HARD_KEYBOARD_SETTINGS = 15
        const val TYPE_START_APP = 16
        const val TYPE_RESET_VIDEO = 17
        const val TYPE_CAMERA_SET_TORCH = 18
        const val TYPE_CAMERA_ZOOM_IN = 19
        const val TYPE_CAMERA_ZOOM_OUT = 20
        const val TYPE_RESIZE_DISPLAY = 21
        const val TYPE_SCAN_FILE = 22

        const val COPY_KEY_NONE = 0
        const val COPY_KEY_COPY = 1
        const val COPY_KEY_CUT = 2

        private const val MAX_MESSAGE_SIZE = 1 shl 18

        /** The C client truncates injected text here; a peer may not exceed it. */
        const val MAX_INJECT_TEXT_BYTES = 300
        const val MAX_CLIPBOARD_TEXT_BYTES = MAX_MESSAGE_SIZE - 14
        const val MAX_PATH_BYTES = 4096

        private val NAMES = arrayOf(
            "inject_keycode", "inject_text", "inject_touch_event", "inject_scroll_event",
            "back_or_screen_on", "expand_notification_panel", "expand_settings_panel", "collapse_panels",
            "get_clipboard", "set_clipboard", "set_display_power", "rotate_device",
            "uhid_create", "uhid_input", "uhid_destroy", "open_hard_keyboard_settings",
            "start_app", "reset_video", "camera_set_torch", "camera_zoom_in",
            "camera_zoom_out", "resize_display", "scan_file",
        )

        fun typeName(type: Int): String = NAMES.getOrNull(type) ?: "unknown_$type"

        fun typeForName(name: String): Int? = NAMES.indexOf(name).takeIf { it >= 0 }

        fun read(input: DataInputStream): ControlMessage = readBody(input.readUnsignedByte(), input)

        fun readBody(type: Int, input: DataInputStream): ControlMessage = try {
            parseBody(type, input)
        } catch (e: IllegalArgumentException) {
            // Malformed UTF-8 decodes to U+FFFD (3 bytes), so an in-bounds
            // payload can re-encode past its limit: still the sender's fault.
            throw ProtocolException("invalid ${typeName(type)}: ${e.message}")
        }

        private fun parseBody(type: Int, input: DataInputStream): ControlMessage = when (type) {
            TYPE_INJECT_KEYCODE -> InjectKeycode(input.readUnsignedByte(), input.readInt(), input.readInt(), input.readInt())
            TYPE_INJECT_TEXT -> InjectText(input.readBlob(4, MAX_INJECT_TEXT_BYTES, "inject text").utf8())
            TYPE_INJECT_TOUCH_EVENT -> InjectTouch(
                action = input.readUnsignedByte(),
                pointerId = input.readLong(),
                position = Position.read(input),
                pressure = input.readUnsignedShort(),
                actionButton = input.readInt(),
                buttons = input.readInt(),
            )
            TYPE_INJECT_SCROLL_EVENT -> InjectScroll(Position.read(input), input.readShort(), input.readShort(), input.readInt())
            TYPE_BACK_OR_SCREEN_ON -> BackOrScreenOn(input.readUnsignedByte())
            TYPE_EXPAND_NOTIFICATION_PANEL -> ExpandNotificationPanel
            TYPE_EXPAND_SETTINGS_PANEL -> ExpandSettingsPanel
            TYPE_COLLAPSE_PANELS -> CollapsePanels
            TYPE_GET_CLIPBOARD -> GetClipboard(input.readUnsignedByte())
            TYPE_SET_CLIPBOARD -> SetClipboard(
                sequence = input.readLong(),
                paste = input.readByte().toInt() != 0,
                text = input.readBlob(4, MAX_CLIPBOARD_TEXT_BYTES, "clipboard").utf8(),
            )
            TYPE_SET_DISPLAY_POWER -> SetDisplayPower(input.readByte().toInt() != 0)
            TYPE_ROTATE_DEVICE -> RotateDevice
            TYPE_UHID_CREATE -> UhidCreate(
                id = input.readUnsignedShort(),
                vendorId = input.readUnsignedShort(),
                productId = input.readUnsignedShort(),
                name = input.readBlob(1, 0xff, "uhid name").utf8(),
                reportDesc = input.readBlob(2, 0xffff, "uhid report descriptor"),
            )
            TYPE_UHID_INPUT -> UhidInput(input.readUnsignedShort(), input.readBlob(2, 0xffff, "uhid input"))
            TYPE_UHID_DESTROY -> UhidDestroy(input.readUnsignedShort())
            TYPE_OPEN_HARD_KEYBOARD_SETTINGS -> OpenHardKeyboardSettings
            TYPE_START_APP -> StartApp(input.readBlob(1, 0xff, "app name").utf8())
            TYPE_RESET_VIDEO -> ResetVideo
            TYPE_CAMERA_SET_TORCH -> CameraSetTorch(input.readByte().toInt() != 0)
            TYPE_CAMERA_ZOOM_IN -> CameraZoomIn
            TYPE_CAMERA_ZOOM_OUT -> CameraZoomOut
            TYPE_RESIZE_DISPLAY -> ResizeDisplay(input.readUnsignedShort(), input.readUnsignedShort())
            TYPE_SCAN_FILE -> ScanFile(input.readBlob(4, MAX_PATH_BYTES, "path").utf8())
            else -> throw ProtocolException("unknown control message type $type")
        }

        /** Parses exactly one message; truncation or trailing bytes are errors. */
        fun fromBytes(bytes: ByteArray): ControlMessage {
            val input = DataInputStream(ByteArrayInputStream(bytes))
            val msg = try {
                read(input)
            } catch (e: EOFException) {
                throw ProtocolException("truncated control message")
            }
            if (input.available() != 0) throw ProtocolException("${input.available()} trailing bytes after ${msg.typeName}")
            return msg
        }

        /**
         * Splits text into injectable pieces of at most [maxBytes] UTF-8 bytes
         * without cutting a code point (surrogate pairs stay together).
         */
        fun textChunks(text: String, maxBytes: Int = MAX_INJECT_TEXT_BYTES): List<InjectText> {
            require(maxBytes >= 4) { "maxBytes must hold one code point" }
            val chunks = ArrayList<InjectText>()
            val current = StringBuilder()
            var currentBytes = 0
            var i = 0
            while (i < text.length) {
                val cp = text.codePointAt(i)
                val units = Character.charCount(cp)
                val size = when {
                    cp < 0x80 -> 1
                    cp < 0x800 -> 2
                    cp < 0x10000 -> 3
                    else -> 4
                }
                if (currentBytes + size > maxBytes) {
                    chunks += InjectText(current.toString())
                    current.setLength(0)
                    currentBytes = 0
                }
                current.append(text, i, i + units)
                currentBytes += size
                i += units
            }
            if (current.isNotEmpty()) chunks += InjectText(current.toString())
            return chunks
        }
    }
}

/** Device -> controller messages (scrcpy v4.1 DeviceMessageWriter). */
sealed class DeviceMessage(val type: Int) {
    internal abstract fun encodeBody(out: ByteSink)

    fun toByteArray(): ByteArray {
        val sink = ByteSink()
        sink.u8(type)
        encodeBody(sink)
        return sink.toByteArray()
    }

    /** Device clipboard text; the writer truncates at a UTF-8 boundary like the server. */
    data class Clipboard(val text: String) : DeviceMessage(TYPE_CLIPBOARD) {
        override fun encodeBody(out: ByteSink) {
            val raw = text.toByteArray(Charsets.UTF_8)
            val len = MediaWire.utf8TruncationIndex(raw, MAX_CLIPBOARD_TEXT_BYTES)
            out.i32(len)
            out.bytes(raw, 0, len)
        }

        override fun toString() = "Clipboard(${text.length} chars)"
    }

    data class AckClipboard(val sequence: Long) : DeviceMessage(TYPE_ACK_CLIPBOARD) {
        override fun encodeBody(out: ByteSink) = out.i64(sequence)
    }

    class UhidOutput(val id: Int, val data: ByteArray) : DeviceMessage(TYPE_UHID_OUTPUT) {
        override fun encodeBody(out: ByteSink) {
            out.u16(id)
            out.u16(data.size)
            out.bytes(data)
        }

        override fun equals(other: Any?) = other is UhidOutput && id == other.id && data.contentEquals(other.data)

        override fun hashCode() = 31 * id + data.contentHashCode()
    }

    companion object {
        const val TYPE_CLIPBOARD = 0
        const val TYPE_ACK_CLIPBOARD = 1
        const val TYPE_UHID_OUTPUT = 2

        const val MAX_CLIPBOARD_TEXT_BYTES = (1 shl 18) - 5

        fun read(input: DataInputStream): DeviceMessage = readBody(input.readUnsignedByte(), input)

        fun readBody(type: Int, input: DataInputStream): DeviceMessage = when (type) {
            TYPE_CLIPBOARD -> Clipboard(input.readBlob(4, MAX_CLIPBOARD_TEXT_BYTES, "clipboard").utf8())
            TYPE_ACK_CLIPBOARD -> AckClipboard(input.readLong())
            TYPE_UHID_OUTPUT -> UhidOutput(input.readUnsignedShort(), input.readBlob(2, 0xffff, "uhid output"))
            else -> throw ProtocolException("unknown device message type $type")
        }

        fun fromBytes(bytes: ByteArray): DeviceMessage {
            val input = DataInputStream(ByteArrayInputStream(bytes))
            val msg = try {
                read(input)
            } catch (e: EOFException) {
                throw ProtocolException("truncated device message")
            }
            if (input.available() != 0) throw ProtocolException("${input.available()} trailing bytes after device message")
            return msg
        }
    }
}
