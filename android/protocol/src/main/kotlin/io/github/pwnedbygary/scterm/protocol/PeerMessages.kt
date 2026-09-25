package io.github.pwnedbygary.scterm.protocol

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.io.DataInputStream
import java.io.EOFException
import java.io.InputStream
import java.io.OutputStream

/**
 * scterm peer protocol, version 1. Specified in docs/PEER_PROTOCOL.md; the
 * JSON shapes below are the contract the Go client implements too.
 *
 * Every TCP+TLS connection opens with one length-prefixed [PeerMessage] each
 * way (the handshake). After a `welcome`, the channel carries raw scrcpy v4.1
 * bytes: media framing on video/audio, control and device messages on
 * control, plus [ControlChannel.TYPE_ENVELOPE] frames for peer-level events.
 */
const val PEER_PROTOCOL_VERSION = 1

@Serializable
enum class Channel {
    @SerialName("control") CONTROL,
    @SerialName("video") VIDEO,
    @SerialName("audio") AUDIO,
}

@Serializable
enum class LeaseState {
    /** This session may inject input. */
    @SerialName("held") HELD,

    /** Another controller holds the lease; this session only watches. */
    @SerialName("viewer") VIEWER,
}

@Serializable
data class ClientInfo(
    val name: String,
    val app: String,
    val platform: String? = null,
)

@Serializable
data class DeviceInfo(
    val name: String,
    val model: String? = null,
    val manufacturer: String? = null,
    val sdk: Int? = null,
)

/** What a controller asks for; the target clamps it to grants and capability. */
@Serializable
data class StreamRequest(
    val video: Boolean = true,
    val audio: Boolean = true,
    val control: Boolean = true,
    val maxSize: Int? = null,
    val maxFps: Float? = null,
    val videoBitRate: Int? = null,
    val videoCodecs: List<String> = listOf(Codec.H264.wireName),
    val audioCodecs: List<String> = listOf(Codec.OPUS.wireName, Codec.AAC.wireName, Codec.RAW.wireName),
)

/**
 * What the serving backend can actually do. [control] lists scrcpy control
 * message type names (see [ControlMessage.typeName]) the backend executes;
 * anything else is answered with an `unsupported` error, never silently eaten.
 */
@Serializable
data class Capabilities(
    val backend: String,
    val video: Boolean,
    val audio: Boolean,
    val control: List<String>,
    val multitouch: Boolean = false,
    val clipboard: Boolean = false,
    val maxSessions: Int = 1,
    val notes: List<String> = emptyList(),
) {
    fun supports(type: Int): Boolean = ControlMessage.typeName(type) in control
}

@Serializable
data class StreamsState(
    val video: Boolean,
    val audio: Boolean,
    val control: Boolean,
    val audioError: String? = null,
)

@Serializable
sealed class PeerMessage {
    /** First frame on every channel, controller -> target. */
    @Serializable
    @SerialName("hello")
    data class Hello(
        val v: Int = PEER_PROTOCOL_VERSION,
        val minV: Int = PEER_PROTOCOL_VERSION,
        val channel: Channel,
        /** Media channels: the session and token from the control `welcome`. */
        val session: String? = null,
        val token: String? = null,
        /** Control channel only. */
        val request: StreamRequest? = null,
        val client: ClientInfo? = null,
    ) : PeerMessage()

    @Serializable
    @SerialName("welcome")
    data class Welcome(
        val v: Int,
        val channel: Channel,
        val session: String,
        /** Control channel only: presented by the media channels of this session. */
        val token: String? = null,
        val grants: List<String> = emptyList(),
        val caps: Capabilities? = null,
        val device: DeviceInfo? = null,
        val streams: StreamsState? = null,
        val lease: LeaseState? = null,
    ) : PeerMessage()

    @Serializable
    @SerialName("reject")
    data class Reject(val code: String, val message: String = "") : PeerMessage()

    /** Pairing request, controller -> target, over a TLS channel in pairing mode. */
    @Serializable
    @SerialName("pair")
    data class Pair(
        val v: Int = PEER_PROTOCOL_VERSION,
        val proof: String,
        val client: ClientInfo,
        /** Where this controller serves, if it does, for reverse connections. */
        val servePort: Int? = null,
    ) : PeerMessage()

    @Serializable
    @SerialName("paired")
    data class Paired(
        val proof: String,
        val device: DeviceInfo,
        val grants: List<String>,
    ) : PeerMessage()

    @Serializable
    @SerialName("ping")
    data class Ping(val t: Long) : PeerMessage()

    @Serializable
    @SerialName("pong")
    data class Pong(val t: Long) : PeerMessage()

    /**
     * Orderly close. From a controller, [endSession] asks the target to stop
     * serving everyone (the legacy `quit`), which requires the control lease;
     * otherwise only this viewer leaves.
     */
    @Serializable
    @SerialName("bye")
    data class Bye(val reason: String = "", val endSession: Boolean = false) : PeerMessage()

    /** Target -> controller: this session's input lease changed. */
    @Serializable
    @SerialName("lease")
    data class Lease(val state: LeaseState, val holder: String? = null) : PeerMessage()

    /** Controller -> target: take the input lease from its current holder. */
    @Serializable
    @SerialName("takeover")
    data object Takeover : PeerMessage()

    /** Target -> controller: a request was refused. See [ErrorCodes]. */
    @Serializable
    @SerialName("error")
    data class Error(val code: String, val message: String = "", val controlType: String? = null) : PeerMessage()

    /** Target -> controller: capabilities, grants or stream availability changed. */
    @Serializable
    @SerialName("status")
    data class Status(
        val streams: StreamsState? = null,
        val caps: Capabilities? = null,
        val grants: List<String>? = null,
    ) : PeerMessage()
}

/** `reject.code` values (handshake). */
object RejectCodes {
    const val VERSION = "version"
    const val NOT_PAIRED = "not_paired"
    const val FORBIDDEN = "forbidden"
    const val BUSY = "busy"
    const val BAD_REQUEST = "bad_request"
    const val BAD_TOKEN = "bad_token"
    const val BAD_PROOF = "bad_proof"
    const val NO_INVITATION = "no_invitation"
    const val UNAVAILABLE = "unavailable"
}

/** `error.code` values (in session). */
object ErrorCodes {
    const val UNSUPPORTED = "unsupported"
    const val FORBIDDEN = "forbidden"
    const val NO_LEASE = "no_lease"
    const val BACKEND = "backend"
}

val PeerJson: Json = Json {
    classDiscriminator = "type"
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
}

/** Handshake framing: u32 big-endian length, then UTF-8 JSON. */
object PeerFrames {
    const val MAX_FRAME = 64 * 1024

    fun encode(msg: PeerMessage): ByteArray {
        val json = PeerJson.encodeToString(PeerMessage.serializer(), msg).toByteArray(Charsets.UTF_8)
        check(json.size <= MAX_FRAME) { "peer frame of ${json.size} bytes" }
        val out = ByteArray(4 + json.size)
        putInt(out, 0, json.size)
        System.arraycopy(json, 0, out, 4, json.size)
        return out
    }

    fun write(out: OutputStream, msg: PeerMessage) {
        out.write(encode(msg))
        out.flush()
    }

    fun read(input: InputStream): PeerMessage {
        val data = if (input is DataInputStream) input else DataInputStream(input)
        val len = data.readInt()
        if (len !in 0..MAX_FRAME) throw ProtocolException("peer frame length $len")
        val json = ByteArray(len)
        data.readFully(json)
        return decode(json) ?: throw ProtocolException("unknown handshake message")
    }

    /** Null for a well-formed message of a type this version does not know. */
    fun decode(json: ByteArray): PeerMessage? = try {
        PeerJson.decodeFromString(PeerMessage.serializer(), json.utf8())
    } catch (e: SerializationException) {
        if (hasTypeField(json)) null else throw ProtocolException("malformed peer message: ${e.message}")
    } catch (e: IllegalArgumentException) {
        throw ProtocolException("malformed peer message: ${e.message}")
    }

    private fun hasTypeField(json: ByteArray): Boolean = try {
        (PeerJson.parseToJsonElement(json.utf8()) as? JsonObject)?.get("type") is JsonPrimitive
    } catch (e: SerializationException) {
        false
    }
}

/**
 * The control channel after the handshake: scrcpy control messages one way,
 * device messages the other, and envelope frames (type 0xFE, which scrcpy
 * never assigns) both ways. Envelopes never reach a stock scrcpy server: the
 * target consumes them.
 */
object ControlChannel {
    const val TYPE_ENVELOPE = 0xFE

    sealed interface FromController {
        data class Control(val message: ControlMessage) : FromController
        data class Envelope(val message: PeerMessage) : FromController
        data object Ignored : FromController
    }

    sealed interface FromTarget {
        data class Device(val message: DeviceMessage) : FromTarget
        data class Envelope(val message: PeerMessage) : FromTarget
        data object Ignored : FromTarget
    }

    fun encodeEnvelope(msg: PeerMessage): ByteArray {
        val frame = PeerFrames.encode(msg)
        val out = ByteArray(1 + frame.size)
        out[0] = TYPE_ENVELOPE.toByte()
        System.arraycopy(frame, 0, out, 1, frame.size)
        return out
    }

    fun readFromController(input: DataInputStream): FromController {
        val type = input.readUnsignedByte()
        if (type == TYPE_ENVELOPE) {
            return readEnvelope(input)?.let { FromController.Envelope(it) } ?: FromController.Ignored
        }
        return FromController.Control(ControlMessage.readBody(type, input))
    }

    fun readFromTarget(input: DataInputStream): FromTarget {
        val type = input.readUnsignedByte()
        if (type == TYPE_ENVELOPE) {
            return readEnvelope(input)?.let { FromTarget.Envelope(it) } ?: FromTarget.Ignored
        }
        return FromTarget.Device(DeviceMessage.readBody(type, input))
    }

    private fun readEnvelope(input: DataInputStream): PeerMessage? {
        val len = input.readInt()
        if (len !in 0..PeerFrames.MAX_FRAME) throw ProtocolException("envelope length $len")
        val json = ByteArray(len)
        try {
            input.readFully(json)
        } catch (e: EOFException) {
            throw ProtocolException("truncated envelope")
        }
        return PeerFrames.decode(json)
    }
}

/** Directional permissions a target grants one paired controller. */
enum class Grant(val wire: String) {
    VIEW("view"),
    AUDIO("audio"),
    CONTROL("control"),
    CLIPBOARD("clipboard");

    companion object {
        val VIEW_ONLY: Set<Grant> = setOf(VIEW, AUDIO)
        val FULL: Set<Grant> = entries.toSet()

        /** Unknown names are ignored so newer peers stay compatible. */
        fun parse(names: Collection<String>): Set<Grant> =
            names.mapNotNullTo(LinkedHashSet()) { name -> entries.firstOrNull { it.wire == name } }

        fun toWire(grants: Set<Grant>): List<String> = entries.filter { it in grants }.map { it.wire }
    }
}
