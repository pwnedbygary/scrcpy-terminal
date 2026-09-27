package io.github.pwnedbygary.scterm.protocol

import java.io.ByteArrayInputStream
import java.io.DataInputStream
import java.io.EOFException
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertTrue

/** What a buggy or malicious peer can send, and how the parsers must react. */
class HostileInputTest {
    @Test
    fun truncatedMessagesAreProtocolErrors() {
        val touch = ControlMessage.InjectTouch(0, -1, Position(1, 2, 3, 4), 0xffff, 1, 1).toByteArray()
        for (cut in 1 until touch.size) {
            assertFailsWith<ProtocolException>("cut at $cut") { ControlMessage.fromBytes(touch.copyOf(cut)) }
        }
        assertFailsWith<ProtocolException> { ControlMessage.fromBytes(touch + 0) }
    }

    @Test
    fun unknownTypesAreRejected() {
        assertFailsWith<ProtocolException> { ControlMessage.fromBytes(byteArrayOf(23)) }
        assertFailsWith<ProtocolException> { ControlMessage.fromBytes(byteArrayOf(0x7f)) }
        assertFailsWith<ProtocolException> { DeviceMessage.fromBytes(byteArrayOf(3)) }
    }

    @Test
    fun oversizedTextIsRefusedBeforeAllocation() {
        // A length prefix of 2^31-1 must fail on the bound, not try to allocate.
        val huge = byteArrayOf(ControlMessage.TYPE_INJECT_TEXT.toByte(), 0x7f, -1, -1, -1)
        assertFailsWith<ProtocolException> { ControlMessage.fromBytes(huge) }
        val negative = byteArrayOf(ControlMessage.TYPE_SET_CLIPBOARD.toByte(), 0, 0, 0, 0, 0, 0, 0, 1, 0, -1, -1, -1, -1)
        assertFailsWith<ProtocolException> { ControlMessage.fromBytes(negative) }
        val text301 = ByteArray(1 + 4 + 301).also {
            it[0] = ControlMessage.TYPE_INJECT_TEXT.toByte()
            putInt(it, 1, 301)
            it.fill('a'.code.toByte(), 5)
        }
        assertFailsWith<ProtocolException> { ControlMessage.fromBytes(text301) }
    }

    @Test
    fun malformedUtf8ThatExpandsPastTheLimitIsAProtocolError() {
        // 200 bytes of 0xff decode to 200 U+FFFD = 600 bytes when re-encoded.
        val bytes = ByteArray(1 + 4 + 200).also {
            it[0] = ControlMessage.TYPE_INJECT_TEXT.toByte()
            putInt(it, 1, 200)
            it.fill(-1, 5)
        }
        assertFailsWith<ProtocolException> { ControlMessage.fromBytes(bytes) }
    }

    @Test
    fun textChunksRespectTheByteLimitAndNeverSplitACodePoint() {
        val emoji = "\uD83D\uDE00" // 4 UTF-8 bytes, 2 UTF-16 units
        val text = "a".repeat(298) + emoji + "é".repeat(200) + emoji
        val chunks = ControlMessage.textChunks(text)
        assertEquals(text, chunks.joinToString("") { it.text })
        for (c in chunks) {
            val size = c.text.toByteArray(Charsets.UTF_8).size
            assertTrue(size <= ControlMessage.MAX_INJECT_TEXT_BYTES, "chunk of $size bytes")
            assertTrue(!Character.isLowSurrogate(c.text.first()), "chunk starts mid surrogate pair")
        }
        // The emoji does not fit after 298 bytes, so it opens chunk two.
        assertEquals("a".repeat(298), chunks[0].text)
        assertEquals(emptyList(), ControlMessage.textChunks(""))
    }

    @Test
    fun injectTextConstructorRefusesOversizePayload() {
        assertFailsWith<IllegalArgumentException> { ControlMessage.InjectText("x".repeat(301)) }
    }

    @Test
    fun mediaReaderBoundsPacketsAndGeometry() {
        val big = ByteArray(12).also { putLong(it, 0, 0); putInt(it, 8, 5000) }
        assertFailsWith<ProtocolException> {
            MediaStreamReader(ByteArrayInputStream(big), maxPacketSize = 4096).readItem()
        }
        val zeroSession = MediaWire.encodeSession(0, 100, false)
        assertFailsWith<ProtocolException> {
            MediaStreamReader(ByteArrayInputStream(zeroSession), MediaWire.MAX_VIDEO_PACKET).readItem()
        }
        assertFailsWith<ProtocolException> {
            MediaStreamReader(ByteArrayInputStream(byteArrayOf(1, 2, 3, 4)), 16).readStart()
        }
        assertFailsWith<EOFException> {
            MediaStreamReader(ByteArrayInputStream(ByteArray(0)), 16).readItem()
        }
    }

    @Test
    fun configPacketIgnoresPtsBitsAndNeverClaimsKeyFrame() {
        val header = ByteArray(12)
        putLong(header, 0, MediaWire.FLAG_CONFIG or MediaWire.FLAG_KEY_FRAME or 1234)
        val packet = assertIs<StreamItem.Packet>(
            MediaStreamReader(ByteArrayInputStream(header), 16).readItem(),
        )
        assertTrue(packet.config)
        assertEquals(false, packet.keyFrame)
        assertEquals(0, packet.ptsUs)
    }

    @Test
    fun envelopeFramesInterleaveWithControlMessages() {
        val bytes = ControlMessage.ResetVideo.toByteArray() +
            ControlChannel.encodeEnvelope(PeerMessage.Ping(42)) +
            ControlMessage.BackOrScreenOn(0).toByteArray() +
            ControlChannel.encodeEnvelope(PeerMessage.Takeover)
        val input = DataInputStream(ByteArrayInputStream(bytes))
        assertEquals(ControlChannel.FromController.Control(ControlMessage.ResetVideo), ControlChannel.readFromController(input))
        assertEquals(ControlChannel.FromController.Envelope(PeerMessage.Ping(42)), ControlChannel.readFromController(input))
        assertEquals(ControlChannel.FromController.Control(ControlMessage.BackOrScreenOn(0)), ControlChannel.readFromController(input))
        assertEquals(ControlChannel.FromController.Envelope(PeerMessage.Takeover), ControlChannel.readFromController(input))
    }

    @Test
    fun envelopesOfUnknownTypeAreSkippedButGarbageIsNot() {
        val future = """{"type":"hologram","depth":3}""".toByteArray()
        val frame = byteArrayOf(ControlChannel.TYPE_ENVELOPE.toByte()) + ByteArray(4).also { putInt(it, 0, future.size) } + future
        val input = DataInputStream(ByteArrayInputStream(frame + DeviceMessage.AckClipboard(7).toByteArray()))
        assertEquals(ControlChannel.FromTarget.Ignored, ControlChannel.readFromTarget(input))
        assertEquals(ControlChannel.FromTarget.Device(DeviceMessage.AckClipboard(7)), ControlChannel.readFromTarget(input))

        val garbage = "not json".toByteArray()
        val bad = byteArrayOf(ControlChannel.TYPE_ENVELOPE.toByte()) + ByteArray(4).also { putInt(it, 0, garbage.size) } + garbage
        assertFailsWith<ProtocolException> { ControlChannel.readFromTarget(DataInputStream(ByteArrayInputStream(bad))) }

        val oversized = byteArrayOf(ControlChannel.TYPE_ENVELOPE.toByte()) + ByteArray(4).also { putInt(it, 0, PeerFrames.MAX_FRAME + 1) }
        assertFailsWith<ProtocolException> { ControlChannel.readFromTarget(DataInputStream(ByteArrayInputStream(oversized))) }
    }

    @Test
    fun deviceClipboardIsTruncatedOnACodePointBoundary() {
        val text = "é".repeat(DeviceMessage.MAX_CLIPBOARD_TEXT_BYTES) // twice the limit in bytes
        val bytes = DeviceMessage.Clipboard(text).toByteArray()
        val decoded = DeviceMessage.fromBytes(bytes) as DeviceMessage.Clipboard
        assertEquals(DeviceMessage.MAX_CLIPBOARD_TEXT_BYTES / 2, decoded.text.length)
        assertContentEquals(bytes, decoded.toByteArray())
    }
}
