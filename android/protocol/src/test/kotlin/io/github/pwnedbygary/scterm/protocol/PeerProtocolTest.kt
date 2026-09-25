package io.github.pwnedbygary.scterm.protocol

import io.github.pwnedbygary.scterm.protocol.Fixtures.int
import io.github.pwnedbygary.scterm.protocol.Fixtures.str
import io.github.pwnedbygary.scterm.protocol.Fixtures.unhex
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PeerProtocolTest {
    @Test
    fun everyMessageRoundTripsThroughAFrame() {
        val caps = Capabilities(
            backend = "fixture",
            video = true,
            audio = false,
            control = listOf("inject_touch_event", "back_or_screen_on"),
            multitouch = true,
            notes = listOf("synthetic"),
        )
        val messages = listOf(
            PeerMessage.Hello(channel = Channel.CONTROL, request = StreamRequest(maxSize = 1280), client = ClientInfo("Pixel", "scterm-android/0.1.0", "android")),
            PeerMessage.Hello(channel = Channel.VIDEO, session = "s1", token = "t1"),
            PeerMessage.Welcome(
                v = 1, channel = Channel.CONTROL, session = "s1", token = "t1", grants = listOf("view", "control"),
                caps = caps, device = DeviceInfo("Galaxy", "SM-S921B", "samsung", 36),
                streams = StreamsState(video = true, audio = false, control = true, audioError = "no permission"),
                lease = LeaseState.HELD,
            ),
            PeerMessage.Reject(RejectCodes.NOT_PAIRED, "unknown certificate"),
            PeerMessage.Pair(proof = "ab".repeat(32), client = ClientInfo("Go", "scterm/1.0"), servePort = 27300),
            PeerMessage.Paired(proof = "cd".repeat(32), device = DeviceInfo("Galaxy"), grants = listOf("view")),
            PeerMessage.Ping(1),
            PeerMessage.Pong(1),
            PeerMessage.Bye("done", endSession = true),
            PeerMessage.Lease(LeaseState.VIEWER, "other"),
            PeerMessage.Takeover,
            PeerMessage.Error(ErrorCodes.UNSUPPORTED, "no key injection", "inject_keycode"),
            PeerMessage.Status(streams = StreamsState(true, true, true), caps = caps, grants = listOf("view")),
        )
        for (msg in messages) {
            val out = ByteArrayOutputStream()
            PeerFrames.write(out, msg)
            assertEquals(msg, PeerFrames.read(ByteArrayInputStream(out.toByteArray())))
        }
    }

    /** The field names are the cross-language contract; a rename must fail here. */
    @Test
    fun jsonShapeIsStable() {
        val hello = json(PeerMessage.Hello(channel = Channel.CONTROL, request = StreamRequest(), client = ClientInfo("n", "a")))
        assertEquals("hello", hello.str("type"))
        assertEquals(1, hello.int("v"))
        assertEquals(1, hello.int("minV"))
        assertEquals("control", hello.str("channel"))
        assertTrue("session" !in hello, "nulls are omitted")
        val request = hello.getValue("request").jsonObject
        assertEquals(listOf("h264"), request.getValue("videoCodecs").jsonArray.map { it.jsonPrimitive.content })

        val media = json(PeerMessage.Hello(channel = Channel.AUDIO, session = "s", token = "t"))
        assertEquals(setOf("type", "v", "minV", "channel", "session", "token"), media.keys)

        val bye = json(PeerMessage.Bye())
        assertEquals(setOf("type", "reason", "endSession"), bye.keys)
        assertEquals(setOf("type"), json(PeerMessage.Takeover).keys)
    }

    @Test
    fun framesRejectBadLengthsAndUnknownHandshakeTypes() {
        val tooLong = ByteArray(4).also { putInt(it, 0, PeerFrames.MAX_FRAME + 1) }
        assertFailsWith<ProtocolException> { PeerFrames.read(ByteArrayInputStream(tooLong)) }
        val unknown = """{"type":"teleport"}""".toByteArray()
        val frame = ByteArray(4).also { putInt(it, 0, unknown.size) } + unknown
        assertFailsWith<ProtocolException> { PeerFrames.read(ByteArrayInputStream(frame)) }
        assertNull(PeerFrames.decode(unknown))
    }

    @Test
    fun unknownFieldsAreIgnoredForForwardCompatibility() {
        val json = """{"type":"ping","t":5,"future":{"x":1}}""".toByteArray()
        assertEquals(PeerMessage.Ping(5), PeerFrames.decode(json))
    }

    @Test
    fun grantsParseIgnoringUnknownNames() {
        assertEquals(setOf(Grant.VIEW, Grant.CONTROL), Grant.parse(listOf("control", "view", "teleport")))
        assertEquals(listOf("view", "audio", "control", "clipboard"), Grant.toWire(Grant.FULL))
        assertEquals(listOf("view", "audio"), Grant.toWire(Grant.VIEW_ONLY))
    }

    @Test
    fun capabilitiesAnswerByControlType() {
        val caps = Capabilities("x", video = true, audio = true, control = listOf("inject_touch_event"))
        assertTrue(caps.supports(ControlMessage.TYPE_INJECT_TOUCH_EVENT))
        assertTrue(!caps.supports(ControlMessage.TYPE_INJECT_KEYCODE))
        assertEquals(ControlMessage.TYPE_SCAN_FILE, ControlMessage.typeForName("scan_file"))
    }

    @Test
    fun actionsMatchTheSharedCatalog() {
        val catalog = Fixtures.load("actions.json")
        val device = catalog.getValue("device").jsonArray.map { it.jsonObject }
        assertEquals(DeviceAction.entries.map { it.id }.toSet(), device.map { it.str("id") }.toSet())
        for (e in device) {
            val action = DeviceAction.byId(e.str("id"))!!
            assertEquals(e["mnemonic"]?.jsonPrimitive?.content?.single(), action.mnemonic, action.id)
            assertEquals(e["fkey"]?.jsonPrimitive?.content?.toInt(), action.fKey, action.id)
            val keycode = e["keycode"]?.jsonPrimitive?.content?.toInt()
            assertEquals(keycode, action.keycode, action.id)
            val messages = action.messages()
            if (keycode != null) {
                assertEquals(
                    listOf(
                        ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, keycode, 0, 0),
                        ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_UP, keycode, 0, 0),
                    ),
                    messages,
                    action.id,
                )
            } else {
                val names = e.getValue("control").jsonArray.map { it.jsonPrimitive.content }
                assertEquals(names, messages.map { it.typeName }, action.id)
            }
        }
        val local = catalog.getValue("local").jsonArray.map { it.jsonObject }
        assertEquals(LocalAction.entries.map { it.id }.toSet(), local.map { it.str("id") }.toSet())
        for (e in local) {
            val action = LocalAction.entries.single { it.id == e.str("id") }
            assertEquals(e["mnemonic"]?.jsonPrimitive?.content?.single(), action.mnemonic, action.id)
            assertEquals(e["fkey"]?.jsonPrimitive?.content?.toInt(), action.fKey, action.id)
        }
        val mnemonics = DeviceAction.entries.mapNotNull { it.mnemonic } + LocalAction.entries.mapNotNull { it.mnemonic }
        assertEquals(mnemonics.size, mnemonics.toSet().size, "mnemonics must be unique")
    }

    @Test
    fun backActionIsAFullPressLikeTheTerminal() {
        assertEquals(
            listOf(ControlMessage.BackOrScreenOn(0), ControlMessage.BackOrScreenOn(1)),
            DeviceAction.BACK.messages(),
        )
        assertContentEquals(unhex("0400"), DeviceAction.BACK.messages()[0].toByteArray())
    }

    private fun json(msg: PeerMessage): JsonObject =
        Json.parseToJsonElement(PeerJson.encodeToString(PeerMessage.serializer(), msg)).jsonObject
}
