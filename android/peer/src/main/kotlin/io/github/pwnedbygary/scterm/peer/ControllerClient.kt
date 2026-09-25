package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.Channel
import io.github.pwnedbygary.scterm.protocol.ClientInfo
import io.github.pwnedbygary.scterm.protocol.ControlChannel
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.DeviceMessage
import io.github.pwnedbygary.scterm.protocol.Fingerprint
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Invitation
import io.github.pwnedbygary.scterm.protocol.LeaseState
import io.github.pwnedbygary.scterm.protocol.PairingProof
import io.github.pwnedbygary.scterm.protocol.PeerFrames
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import io.github.pwnedbygary.scterm.protocol.ProtocolException
import io.github.pwnedbygary.scterm.protocol.RejectCodes
import io.github.pwnedbygary.scterm.protocol.StreamRequest
import java.io.BufferedInputStream
import java.io.ByteArrayOutputStream
import java.io.Closeable
import java.io.DataInputStream
import java.io.IOException
import java.io.InputStream
import java.net.SocketTimeoutException
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocket
import kotlin.concurrent.thread

/** The target refused a request; [code] is one of [RejectCodes]. */
class PeerException(val code: String, message: String) : IOException(message)

/** The controlling half of a peer: pairs with targets and opens sessions to them. */
class ControllerClient(private val identity: PeerIdentity, private val client: ClientInfo) {
    data class PairResult(val fingerprint: Fingerprint, val device: DeviceInfo, val grants: Set<Grant>)

    /**
     * Redeems an invitation. Both sides prove knowledge of its secret bound to
     * this TLS connection's two certificates; only then are identities kept.
     */
    fun pair(invitation: Invitation, servePort: Int? = null, timeoutMs: Int = 10_000): PairResult {
        val socket = PeerTls.connect(PeerTls.clientContext(identity, invitation.fingerprint), invitation.host, invitation.port, timeoutMs)
        socket.use {
            val target = PeerTls.peerFingerprint(socket)
            val proof = PairingProof.compute(invitation.secret, PairingProof.Role.CONTROLLER, identity.fingerprint, target)
            PeerFrames.write(socket.outputStream, PeerMessage.Pair(proof = PairingProof.encode(proof), client = client, servePort = servePort))
            return when (val reply = PeerFrames.read(DataInputStream(socket.inputStream))) {
                is PeerMessage.Paired -> {
                    if (!PairingProof.verify(invitation.secret, PairingProof.Role.TARGET, target, identity.fingerprint, reply.proof)) {
                        throw PeerException(RejectCodes.BAD_PROOF, "the target could not prove it issued this code")
                    }
                    PairResult(target, reply.device, Grant.parse(reply.grants))
                }
                is PeerMessage.Reject -> throw PeerException(reply.code, reply.message)
                else -> throw ProtocolException("unexpected pairing reply")
            }
        }
    }

    /** Opens a session to a paired target whose identity must match [pin]. */
    fun connect(
        host: String,
        port: Int,
        pin: Fingerprint,
        request: StreamRequest = StreamRequest(),
        timeoutMs: Int = 10_000,
    ): ControllerSession {
        val context = PeerTls.clientContext(identity, pin)
        val control = PeerTls.connect(context, host, port, timeoutMs)
        var video: SSLSocket? = null
        var audio: SSLSocket? = null
        try {
            val input = DataInputStream(BufferedInputStream(control.inputStream, 16 * 1024))
            PeerFrames.write(control.outputStream, PeerMessage.Hello(channel = Channel.CONTROL, request = request, client = client))
            val welcome = expectWelcome(PeerFrames.read(input), Channel.CONTROL)
            val grants = Grant.parse(welcome.grants)
            if (request.video && Grant.VIEW in grants && welcome.streams?.video != false) {
                video = openMedia(context, host, port, Channel.VIDEO, welcome, timeoutMs)
            }
            if (request.audio && Grant.AUDIO in grants && welcome.streams?.audio != false) {
                audio = openMedia(context, host, port, Channel.AUDIO, welcome, timeoutMs)
            }
            return ControllerSession(control, input, welcome, video, audio)
        } catch (e: Exception) {
            video?.closeQuietly()
            audio?.closeQuietly()
            control.closeQuietly()
            throw e
        }
    }

    private fun openMedia(
        context: SSLContext,
        host: String,
        port: Int,
        channel: Channel,
        welcome: PeerMessage.Welcome,
        timeoutMs: Int,
    ): SSLSocket {
        val socket = PeerTls.connect(context, host, port, timeoutMs)
        try {
            PeerFrames.write(socket.outputStream, PeerMessage.Hello(channel = channel, session = welcome.session, token = welcome.token))
            // Unbuffered read: the media bytes that follow belong to the decoder.
            expectWelcome(PeerFrames.read(DataInputStream(socket.inputStream)), channel)
            socket.soTimeout = 0
            return socket
        } catch (e: Exception) {
            socket.closeQuietly()
            throw e
        }
    }

    private fun expectWelcome(msg: PeerMessage, channel: Channel): PeerMessage.Welcome = when {
        msg is PeerMessage.Welcome && msg.channel == channel -> msg
        msg is PeerMessage.Reject -> throw PeerException(msg.code, msg.message)
        else -> throw ProtocolException("expected a ${channel.name.lowercase()} welcome")
    }
}

/**
 * A live session with a target. [video]/[audio] carry scrcpy v4.1 media
 * framing, starting with the codec header. Input is written on a dedicated
 * thread, so [send] never blocks the caller (and never touches the network
 * from Android's main thread).
 */
class ControllerSession internal constructor(
    private val control: SSLSocket,
    private val controlIn: DataInputStream,
    val welcome: PeerMessage.Welcome,
    private val videoSocket: SSLSocket?,
    private val audioSocket: SSLSocket?,
) : Closeable {
    interface Listener {
        fun onDeviceMessage(message: DeviceMessage) {}
        fun onLease(state: LeaseState, holder: String?) {}
        fun onError(error: PeerMessage.Error) {}
        fun onStatus(status: PeerMessage.Status) {}

        /** Called once, from a session thread, however the session ended. */
        fun onClosed(reason: String) {}
    }

    private sealed interface Outgoing {
        class Control(val message: ControlMessage) : Outgoing
        class Envelope(val message: PeerMessage) : Outgoing
        class Close(val endSession: Boolean) : Outgoing
    }

    val grants: Set<Grant> = Grant.parse(welcome.grants)
    val video: InputStream? = videoSocket?.let { BufferedInputStream(it.inputStream, 256 * 1024) }
    val audio: InputStream? = audioSocket?.let { BufferedInputStream(it.inputStream, 64 * 1024) }

    @Volatile
    var capabilities: Capabilities? = welcome.caps
        private set

    @Volatile
    var lease: LeaseState = welcome.lease ?: LeaseState.VIEWER
        private set

    /** Last measured control round trip, or -1 before the first pong. */
    @Volatile
    var roundTripMs: Long = -1
        private set

    private val outbox = LinkedBlockingQueue<Outgoing>(MAX_QUEUED)
    private val closed = AtomicBoolean(false)

    @Volatile
    private var listener: Listener = object : Listener {}

    @Volatile
    private var closeReason: String? = null

    fun start(listener: Listener) {
        this.listener = listener
        control.soTimeout = READ_TIMEOUT_MS
        thread(name = "peer-control-writer", isDaemon = true) { writeLoop() }
        thread(name = "peer-control-reader", isDaemon = true) { readLoop() }
    }

    fun send(message: ControlMessage) = offer(Outgoing.Control(message))

    fun sendAll(messages: List<ControlMessage>) = messages.forEach(::send)

    /** Ask the target for the input lease. */
    fun takeover() = offer(Outgoing.Envelope(PeerMessage.Takeover))

    /**
     * Leaves after the queued input is written. [endSession] asks the target
     * to disconnect every viewer (the legacy `quit`); it needs the lease.
     */
    fun disconnect(endSession: Boolean = false) {
        if (closed.get()) return
        if (!outbox.offer(Outgoing.Close(endSession))) shutdown("closed")
        thread(name = "peer-close-watchdog", isDaemon = true) {
            // A stalled network must not keep the sockets alive.
            Thread.sleep(1_000)
            shutdown("closed")
        }
    }

    override fun close() = disconnect()

    val isClosed: Boolean get() = closed.get()

    private fun offer(item: Outgoing) {
        if (closed.get()) return
        // A full queue means the connection is effectively dead; dropping
        // input would risk a stuck press, so end the session instead.
        if (!outbox.offer(item)) shutdown("input queue overflow: the connection stalled")
    }

    private fun writeLoop() {
        val out = control.outputStream
        val batch = ArrayList<Outgoing>()
        val buffer = ByteArrayOutputStream(1024)
        // Pings keep their own schedule: the target only answers, so pings
        // starved by continuous input would let the read timeout end a
        // healthy session.
        val pingIntervalNs = TimeUnit.MILLISECONDS.toNanos(PING_INTERVAL_MS)
        var nextPingAt = System.nanoTime() + pingIntervalNs
        try {
            while (!closed.get()) {
                val waitNs = nextPingAt - System.nanoTime()
                val first = if (waitNs > 0) outbox.poll(waitNs, TimeUnit.NANOSECONDS) else outbox.poll()
                buffer.reset()
                var closing: Outgoing.Close? = null
                if (first != null) {
                    batch += first
                    outbox.drainTo(batch)
                    for (i in batch.indices) {
                        when (val item = batch[i]) {
                            is Outgoing.Control -> if (!isSuperseded(batch, i)) buffer.write(item.message.toByteArray())
                            is Outgoing.Envelope -> buffer.write(ControlChannel.encodeEnvelope(item.message))
                            is Outgoing.Close -> closing = item
                        }
                        if (closing != null) break
                    }
                    batch.clear()
                }
                if (closing == null && System.nanoTime() - nextPingAt >= 0) {
                    buffer.write(ControlChannel.encodeEnvelope(PeerMessage.Ping(System.currentTimeMillis())))
                    nextPingAt = System.nanoTime() + pingIntervalNs
                }
                closing?.let { buffer.write(ControlChannel.encodeEnvelope(PeerMessage.Bye(endSession = it.endSession))) }
                if (buffer.size() == 0) continue
                // One write per burst: one TLS record instead of one per message.
                out.write(buffer.toByteArray())
                out.flush()
                if (closing != null) {
                    shutdown("closed")
                    return
                }
            }
        } catch (e: IOException) {
            shutdown(closeReason ?: "connection lost")
        } catch (e: InterruptedException) {
            shutdown("closed")
        }
    }

    private fun readLoop() {
        try {
            while (!closed.get()) {
                when (val item = ControlChannel.readFromTarget(controlIn)) {
                    is ControlChannel.FromTarget.Device -> listener.onDeviceMessage(item.message)
                    is ControlChannel.FromTarget.Envelope -> onEnvelope(item.message)
                    ControlChannel.FromTarget.Ignored -> Unit
                }
            }
        } catch (e: SocketTimeoutException) {
            shutdown("the target stopped responding")
        } catch (e: IOException) {
            shutdown(closeReason ?: "disconnected")
        }
    }

    private fun onEnvelope(msg: PeerMessage) {
        when (msg) {
            is PeerMessage.Pong -> roundTripMs = System.currentTimeMillis() - msg.t
            is PeerMessage.Lease -> {
                lease = msg.state
                listener.onLease(msg.state, msg.holder)
            }
            is PeerMessage.Error -> listener.onError(msg)
            is PeerMessage.Status -> {
                msg.caps?.let { capabilities = it }
                listener.onStatus(msg)
            }
            is PeerMessage.Bye -> {
                closeReason = msg.reason.ifEmpty { "the target ended the session" }
                shutdown(closeReason!!)
            }
            else -> Unit
        }
    }

    private fun shutdown(reason: String) {
        if (!closed.compareAndSet(false, true)) return
        control.closeQuietly()
        videoSocket?.closeQuietly()
        audioSocket?.closeQuietly()
        listener.onClosed(reason)
    }

    internal companion object {
        const val PING_INTERVAL_MS = 5_000L
        const val READ_TIMEOUT_MS = 15_000
        const val MAX_QUEUED = 2048

        /**
         * A pointer move is dropped when a later move of the same pointer
         * follows before any other kind of message: presses, releases and
         * everything else keep their exact order.
         */
        fun isSuperseded(batch: List<*>, index: Int): Boolean {
            val move = moveOf(batch[index]) ?: return false
            for (j in index + 1 until batch.size) {
                val next = moveOf(batch[j]) ?: return false
                if (next.pointerId == move.pointerId && next.action == move.action) return true
            }
            return false
        }

        /** A batch item as the writer queues it (for [isSuperseded] tests). */
        fun queued(message: ControlMessage): Any = Outgoing.Control(message)

        private fun moveOf(item: Any?): ControlMessage.InjectTouch? {
            val touch = (item as? Outgoing.Control)?.message as? ControlMessage.InjectTouch ?: return null
            return touch.takeIf {
                it.action == AndroidInput.MOTION_ACTION_MOVE || it.action == AndroidInput.MOTION_ACTION_HOVER_MOVE
            }
        }
    }
}
