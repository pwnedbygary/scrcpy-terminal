package io.github.pwnedbygary.scterm.protocol

import java.io.DataInputStream
import java.io.InputStream
import java.io.OutputStream

/** scrcpy v4.1 stream codec identifiers (the first 4 bytes of a media socket). */
enum class Codec(val id: Int, val wireName: String, val isVideo: Boolean) {
    H264(0x68323634, "h264", true),
    H265(0x68323635, "h265", true),
    AV1(0x00617631, "av1", true),
    OPUS(0x6f707573, "opus", false),
    AAC(0x00616163, "aac", false),
    FLAC(0x666c6163, "flac", false),
    RAW(0x00726177, "raw", false);

    companion object {
        fun fromId(id: Int): Codec? = entries.firstOrNull { it.id == id }
        fun fromName(name: String): Codec? = entries.firstOrNull { it.wireName == name }
    }
}

/** How a media stream opens: a codec, or one of the device's two refusal codes. */
sealed interface StreamStart {
    data class Enabled(val codec: Codec) : StreamStart

    /** The device could not capture this stream; mirroring continues without it. */
    data object Disabled : StreamStart

    /** The device hit a configuration error; the whole session must stop. */
    data object Failed : StreamStart
}

sealed interface StreamItem {
    /** A new capture geometry. Touch positions must carry exactly this size. */
    data class Session(val width: Int, val height: Int, val clientResize: Boolean) : StreamItem

    /**
     * One encoded unit. [config] packets (H.264 SPS/PPS, OpusHead, AAC ASC)
     * carry codec setup and no PTS; the decoder needs them before the next
     * media packet.
     */
    class Packet(
        val ptsUs: Long,
        val config: Boolean,
        val keyFrame: Boolean,
        val data: ByteArray,
    ) : StreamItem {
        override fun toString(): String =
            "Packet(pts=$ptsUs, config=$config, key=$keyFrame, ${data.size}B)"
    }
}

object MediaWire {
    const val HEADER_SIZE = 12
    const val FLAG_SESSION: Long = 1L shl 63
    const val FLAG_CONFIG: Long = 1L shl 62
    const val FLAG_KEY_FRAME: Long = 1L shl 61
    const val PTS_MASK: Long = FLAG_KEY_FRAME - 1
    const val DEVICE_NAME_FIELD = 64

    /** Upper bounds a receiver enforces before allocating a packet buffer. */
    const val MAX_VIDEO_PACKET = 16 shl 20
    const val MAX_AUDIO_PACKET = 1 shl 20

    /** Largest geometry a touch position can describe (u16 screen size). */
    const val MAX_DIMENSION = 0xffff

    fun encodeStart(start: StreamStart): ByteArray {
        val out = ByteArray(4)
        when (start) {
            is StreamStart.Enabled -> putInt(out, 0, start.codec.id)
            StreamStart.Disabled -> Unit
            StreamStart.Failed -> out[3] = 1
        }
        return out
    }

    fun decodeStart(word: Int): StreamStart = when (word) {
        0 -> StreamStart.Disabled
        1 -> StreamStart.Failed
        else -> Codec.fromId(word)?.let { StreamStart.Enabled(it) }
            ?: throw ProtocolException("unknown codec id 0x%08x".format(word))
    }

    fun encodeSession(width: Int, height: Int, clientResize: Boolean): ByteArray {
        val out = ByteArray(HEADER_SIZE)
        putInt(out, 0, (FLAG_SESSION ushr 32).toInt() or if (clientResize) 1 else 0)
        putInt(out, 4, width)
        putInt(out, 8, height)
        return out
    }

    /** Header followed by payload in one array: what a fanout writes per packet. */
    fun encodeItem(item: StreamItem): ByteArray = when (item) {
        is StreamItem.Session -> encodeSession(item.width, item.height, item.clientResize)
        is StreamItem.Packet -> {
            val out = ByteArray(HEADER_SIZE + item.data.size)
            val ptsAndFlags = if (item.config) {
                FLAG_CONFIG
            } else {
                (item.ptsUs and PTS_MASK) or if (item.keyFrame) FLAG_KEY_FRAME else 0L
            }
            putLong(out, 0, ptsAndFlags)
            putInt(out, 8, item.data.size)
            System.arraycopy(item.data, 0, out, HEADER_SIZE, item.data.size)
            out
        }
    }

    fun encodeDeviceName(name: String): ByteArray {
        val out = ByteArray(DEVICE_NAME_FIELD)
        val raw = name.toByteArray(Charsets.UTF_8)
        val len = utf8TruncationIndex(raw, DEVICE_NAME_FIELD - 1)
        System.arraycopy(raw, 0, out, 0, len)
        return out
    }

    fun decodeDeviceName(field: ByteArray): String {
        val end = field.indexOf(0).let { if (it < 0) field.size else it }
        return String(field, 0, end, Charsets.UTF_8)
    }

    /** Largest prefix length <= [max] that does not split a UTF-8 sequence. */
    fun utf8TruncationIndex(utf8: ByteArray, max: Int): Int {
        if (utf8.size <= max) return utf8.size
        var len = max
        // Back up over continuation bytes (10xxxxxx) to a sequence boundary.
        while (len > 0 && (utf8[len].toInt() and 0xc0) == 0x80) len--
        return len
    }
}

/**
 * Reads one scrcpy media socket: [readStart] once, then [readItem] until EOF.
 * Wrap the source in a buffered stream; reads are many small header reads.
 */
class MediaStreamReader(input: InputStream, private val maxPacketSize: Int) {
    private val input = input as? DataInputStream ?: DataInputStream(input)
    private val header = ByteArray(MediaWire.HEADER_SIZE)

    fun readStart(): StreamStart = MediaWire.decodeStart(input.readInt())

    fun readItem(): StreamItem {
        input.readFully(header)
        if (header[0].toInt() and 0x80 != 0) {
            val width = getInt(header, 4)
            val height = getInt(header, 8)
            if (width !in 1..MediaWire.MAX_DIMENSION || height !in 1..MediaWire.MAX_DIMENSION) {
                throw ProtocolException("invalid session geometry ${width}x$height")
            }
            return StreamItem.Session(width, height, header[3].toInt() and 1 != 0)
        }
        val ptsAndFlags = getLong(header, 0)
        val size = getInt(header, 8)
        if (size < 0 || size > maxPacketSize) {
            throw ProtocolException("packet size $size exceeds $maxPacketSize")
        }
        val data = ByteArray(size)
        input.readFully(data)
        val config = ptsAndFlags and MediaWire.FLAG_CONFIG != 0L
        return StreamItem.Packet(
            ptsUs = if (config) 0 else ptsAndFlags and MediaWire.PTS_MASK,
            config = config,
            keyFrame = !config && ptsAndFlags and MediaWire.FLAG_KEY_FRAME != 0L,
            data = data,
        )
    }
}

/** Writes one scrcpy media stream. Not thread-safe; one writer per socket. */
class MediaStreamWriter(private val out: OutputStream) {
    fun writeStart(start: StreamStart) = out.write(MediaWire.encodeStart(start))

    fun writeItem(item: StreamItem) = out.write(MediaWire.encodeItem(item))

    fun flush() = out.flush()
}
