package io.github.pwnedbygary.scterm.protocol

import io.github.pwnedbygary.scterm.protocol.ControlMessage.CameraSetTorch
import io.github.pwnedbygary.scterm.protocol.ControlMessage.CameraZoomIn
import io.github.pwnedbygary.scterm.protocol.ControlMessage.CameraZoomOut
import io.github.pwnedbygary.scterm.protocol.ControlMessage.CollapsePanels
import io.github.pwnedbygary.scterm.protocol.ControlMessage.ExpandNotificationPanel
import io.github.pwnedbygary.scterm.protocol.ControlMessage.ExpandSettingsPanel
import io.github.pwnedbygary.scterm.protocol.ControlMessage.OpenHardKeyboardSettings
import io.github.pwnedbygary.scterm.protocol.ControlMessage.ResetVideo
import io.github.pwnedbygary.scterm.protocol.ControlMessage.RotateDevice
import io.github.pwnedbygary.scterm.protocol.Fixtures.bool
import io.github.pwnedbygary.scterm.protocol.Fixtures.int
import io.github.pwnedbygary.scterm.protocol.Fixtures.long
import io.github.pwnedbygary.scterm.protocol.Fixtures.str
import io.github.pwnedbygary.scterm.protocol.Fixtures.unhex
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.double
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class WireFixturesTest {
    private val wire = Fixtures.load("scrcpy_wire.json")

    private fun controlFrom(type: Int, f: JsonObject): ControlMessage = when (type) {
        0 -> ControlMessage.InjectKeycode(f.int("action"), f.int("keycode"), f.int("repeat"), f.int("metaState"))
        1 -> ControlMessage.InjectText(f.str("text"))
        2 -> ControlMessage.InjectTouch(
            f.int("action"), f.long("pointerId"), position(f), f.int("pressure"), f.int("actionButton"), f.int("buttons"),
        )
        3 -> ControlMessage.InjectScroll(position(f), f.int("hScroll").toShort(), f.int("vScroll").toShort(), f.int("buttons"))
        4 -> ControlMessage.BackOrScreenOn(f.int("action"))
        5 -> ExpandNotificationPanel
        6 -> ExpandSettingsPanel
        7 -> CollapsePanels
        8 -> ControlMessage.GetClipboard(f.int("copyKey"))
        9 -> ControlMessage.SetClipboard(f.long("sequence"), f.bool("paste"), f.str("text"))
        10 -> ControlMessage.SetDisplayPower(f.bool("on"))
        11 -> RotateDevice
        12 -> ControlMessage.UhidCreate(f.int("id"), f.int("vendorId"), f.int("productId"), f.str("name"), unhex(f.str("data")))
        13 -> ControlMessage.UhidInput(f.int("id"), unhex(f.str("data")))
        14 -> ControlMessage.UhidDestroy(f.int("id"))
        15 -> OpenHardKeyboardSettings
        16 -> ControlMessage.StartApp(f.str("name"))
        17 -> ResetVideo
        18 -> CameraSetTorch(f.bool("on"))
        19 -> CameraZoomIn
        20 -> CameraZoomOut
        21 -> ControlMessage.ResizeDisplay(f.int("width"), f.int("height"))
        22 -> ControlMessage.ScanFile(f.str("path"))
        else -> error("no builder for type $type")
    }

    private fun position(f: JsonObject) = Position(f.int("x"), f.int("y"), f.int("screenWidth"), f.int("screenHeight"))

    @Test
    fun controlMessagesMatchGoldenBytes() {
        val seen = HashSet<Int>()
        for (entry in wire.getValue("control").jsonArray) {
            val e = entry.jsonObject
            val name = e.str("name")
            val type = e.int("type")
            val expected = unhex(e.str("hex"))
            val built = controlFrom(type, e.getValue("fields").jsonObject)
            assertEquals(type, built.type, name)
            assertContentEquals(expected, built.toByteArray(), "serialize $name")
            assertEquals(built, ControlMessage.fromBytes(expected), "parse $name")
            seen += type
        }
        assertEquals((0..22).toSet(), seen, "every scrcpy v4.1 control type needs a golden vector")
    }

    @Test
    fun deviceMessagesMatchGoldenBytes() {
        for (entry in wire.getValue("device").jsonArray) {
            val e = entry.jsonObject
            val f = e.getValue("fields").jsonObject
            val expected = unhex(e.str("hex"))
            val built = when (e.int("type")) {
                0 -> DeviceMessage.Clipboard(f.str("text"))
                1 -> DeviceMessage.AckClipboard(f.long("sequence"))
                2 -> DeviceMessage.UhidOutput(f.int("id"), unhex(f.str("data")))
                else -> error("unknown device type")
            }
            assertContentEquals(expected, built.toByteArray(), e.str("name"))
            assertEquals(built, DeviceMessage.fromBytes(expected), e.str("name"))
        }
    }

    @Test
    fun fixedPointMatchesTheCClient() {
        for (entry in wire.getValue("fixedPoint").jsonArray) {
            val e = entry.jsonObject
            val value = e.getValue("value").jsonPrimitive.double.toFloat()
            val raw = e.int("raw")
            when (e.str("kind")) {
                "u16" -> assertEquals(raw, FixedPoint.u16(value), "u16($value)")
                "scroll" -> assertEquals(raw, FixedPoint.scroll(value).toInt(), "scroll($value)")
            }
        }
        assertEquals(1f, FixedPoint.u16ToFloat(0xffff))
        assertEquals(16f, FixedPoint.scrollToFloat(0x7fff))
        assertEquals(-16f, FixedPoint.scrollToFloat(Short.MIN_VALUE))
    }

    @Test
    fun mediaFramingMatchesGoldenBytes() {
        for (entry in wire.getValue("media").jsonArray) {
            val e = entry.jsonObject
            val f = e.getValue("fields").jsonObject
            val expected = unhex(e.str("hex"))
            val name = e.str("name")
            when (e.str("kind")) {
                "codec" -> {
                    val start = StreamStart.Enabled(Codec.fromName(f.str("codec"))!!)
                    assertContentEquals(expected, MediaWire.encodeStart(start), name)
                    assertEquals(start, reader(expected).readStart(), name)
                }
                "disabled" -> {
                    val start = if (f.bool("error")) StreamStart.Failed else StreamStart.Disabled
                    assertContentEquals(expected, MediaWire.encodeStart(start), name)
                    assertEquals(start, reader(expected).readStart(), name)
                }
                "session" -> {
                    val item = StreamItem.Session(f.int("width"), f.int("height"), f.bool("clientResize"))
                    assertContentEquals(expected, MediaWire.encodeItem(item), name)
                    assertEquals(item, reader(expected).readItem(), name)
                }
                "packet" -> {
                    val data = unhex(f.str("data"))
                    val item = StreamItem.Packet(f.long("ptsUs"), f.bool("config"), f.bool("keyFrame"), data)
                    assertContentEquals(expected, MediaWire.encodeItem(item), name)
                    val read = assertIs<StreamItem.Packet>(reader(expected).readItem(), name)
                    assertEquals(item.ptsUs, read.ptsUs, name)
                    assertEquals(item.config, read.config, name)
                    assertEquals(item.keyFrame, read.keyFrame, name)
                    assertContentEquals(data, read.data, name)
                }
            }
        }
    }

    @Test
    fun wholeStreamRoundTrips() {
        val out = ByteArrayOutputStream()
        val writer = MediaStreamWriter(out)
        writer.writeStart(StreamStart.Enabled(Codec.H264))
        writer.writeItem(StreamItem.Session(1080, 2400, false))
        writer.writeItem(StreamItem.Packet(0, true, false, byteArrayOf(0, 0, 0, 1, 0x67)))
        writer.writeItem(StreamItem.Packet(16_666, false, true, ByteArray(1000) { it.toByte() }))
        writer.writeItem(StreamItem.Packet(33_333, false, false, ByteArray(10)))

        val r = reader(out.toByteArray())
        assertEquals(StreamStart.Enabled(Codec.H264), r.readStart())
        assertEquals(StreamItem.Session(1080, 2400, false), r.readItem())
        assertTrue(assertIs<StreamItem.Packet>(r.readItem()).config)
        val key = assertIs<StreamItem.Packet>(r.readItem())
        assertTrue(key.keyFrame)
        assertEquals(16_666, key.ptsUs)
        assertEquals(1000, key.data.size)
        assertEquals(33_333, assertIs<StreamItem.Packet>(r.readItem()).ptsUs)
    }

    @Test
    fun deviceNameFieldIsNulPaddedAndCutOnCodePointBoundary() {
        val field = MediaWire.encodeDeviceName("Pixel 8")
        assertEquals(64, field.size)
        assertEquals("Pixel 8", MediaWire.decodeDeviceName(field))
        // 62 ASCII bytes + a 2-byte 'é' crosses the 63-byte limit: drop it whole.
        val long = "a".repeat(62) + "é"
        assertEquals("a".repeat(62), MediaWire.decodeDeviceName(MediaWire.encodeDeviceName(long)))
    }

    private fun reader(bytes: ByteArray) = MediaStreamReader(ByteArrayInputStream(bytes), MediaWire.MAX_VIDEO_PACKET)
}
