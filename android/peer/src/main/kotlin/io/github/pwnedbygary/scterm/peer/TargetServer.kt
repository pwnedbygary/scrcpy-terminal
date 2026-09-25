package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Base32
import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.Channel
import io.github.pwnedbygary.scterm.protocol.ControlChannel
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.DeviceMessage
import io.github.pwnedbygary.scterm.protocol.ErrorCodes
import io.github.pwnedbygary.scterm.protocol.Fingerprint
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Invitation
import io.github.pwnedbygary.scterm.protocol.LeaseState
import io.github.pwnedbygary.scterm.protocol.PEER_PROTOCOL_VERSION
import io.github.pwnedbygary.scterm.protocol.PairingCode
import io.github.pwnedbygary.scterm.protocol.PairingProof
import io.github.pwnedbygary.scterm.protocol.PeerFrames
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import io.github.pwnedbygary.scterm.protocol.ProtocolException
import io.github.pwnedbygary.scterm.protocol.RejectCodes
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamRequest
import io.github.pwnedbygary.scterm.protocol.StreamStart
import io.github.pwnedbygary.scterm.protocol.StreamsState
import java.io.BufferedInputStream
import java.io.DataInputStream
import java.io.IOException
import java.io.OutputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketTimeoutException
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import javax.net.ssl.SSLSocket
import kotlin.concurrent.thread

/**
 * The serving half of a peer: accepts paired controllers over mutual TLS,
 * enforces their grants and the single input lease, and bridges them to one
 * [TargetBackend] shared by every session.
 */
class TargetServer(
    private val identity: PeerIdentity,
    private val store: PeerStore,
    private val device: DeviceInfo,
    private val backends: BackendProvider,
    private val config: Config = Config(),
    private val events: Events = object : Events {},
) {
    /** Creates the backend on first use; null means serving is not ready yet. */
    fun interface BackendProvider {
        fun create(): TargetBackend?
    }

    data class Config(
        val port: Int = Invitation.DEFAULT_PORT,
        val bindAddress: InetAddress? = null,
        val maxSessions: Int = 4,
        val maxConnections: Int = 32,
        val handshakeTimeoutMs: Int = 10_000,
        /** Controllers ping every 5 s; a silent control channel for this long is dead. */
        val idleTimeoutMs: Int = 20_000,
        /**
         * Kernel send buffers for media connections. Left to autotuning (up to
         * 4 MB on Android), a slow link would hide seconds of video in the kernel,
         * out of reach of the fanout's age budget; small buffers keep the backlog
         * in the fanout, which skips ahead to a keyframe instead.
         */
        val videoSendBufferBytes: Int = 64 * 1024,
        val audioSendBufferBytes: Int = 8 * 1024,
        val tokenTtlMs: Long = 15_000,
        val invitationTtlMs: Long = 10 * 60_000L,
        val maxPairingFailures: Int = 5,
    )

    data class SessionInfo(
        val id: String,
        val peer: Fingerprint,
        val peerName: String,
        val address: String,
        val grants: Set<Grant>,
        val holdsLease: Boolean,
    )

    interface Events {
        fun onSessionStarted(session: SessionInfo) {}
        fun onSessionEnded(session: SessionInfo, reason: String) {}
        fun onLeaseChanged(holder: SessionInfo?) {}
        fun onPaired(record: PeerRecord) {}
        fun onPairingRejected(reason: String) {}
        fun onBackendStopped(error: String?) {}
    }

    private class PendingInvitation(val secret: ByteArray, val grants: Set<Grant>, val expiresAtMs: Long) {
        var failures = 0
    }

    private val tls = PeerTls.serverContext(identity)
    private val random = SecureRandom()

    /**
     * Revocations, local disconnects and shutdown write to sockets (bye,
     * input releases), and their callers include Android's main thread, where
     * network I/O kills the app. They all run here instead.
     */
    private val admin = Executors.newSingleThreadExecutor { task -> Thread(task, "peer-admin").apply { isDaemon = true } }

    /** Guards sessions, the backend reference, the invitation and stream state. */
    private val lock = Any()

    /** Orders everything written to the backend's control path, plus the lease. */
    private val inputLock = Any()

    private var serverSocket: ServerSocket? = null
    private var storeListener: AutoCloseable? = null
    private val connections: MutableSet<Socket> = ConcurrentHashMap.newKeySet()

    @Volatile
    private var running = false
    private val sessions = LinkedHashMap<String, Session>()
    private var invitation: PendingInvitation? = null

    @Volatile
    private var backend: TargetBackend? = null

    @Volatile
    private var capabilities: Capabilities? = null
    private var audioError: String? = null

    private var leaseHolder: Session? = null // guarded by inputLock
    private var clipboardSequence = 0L // guarded by inputLock
    private val clipboardAcks = HashMap<Long, Pair<Session, Long>>() // guarded by inputLock

    private val videoFanout = MediaFanout(video = true, limits = MediaFanout.Limits.VIDEO, requestKeyFrame = { backend?.requestKeyFrame() })
    private val audioFanout = MediaFanout(video = false, limits = MediaFanout.Limits.AUDIO, requestKeyFrame = {})

    val port: Int get() = serverSocket?.localPort ?: -1

    val fingerprint: Fingerprint get() = identity.fingerprint

    fun start(): Int {
        check(!running) { "already started" }
        val socket = ServerSocket()
        socket.reuseAddress = true
        socket.bind(InetSocketAddress(config.bindAddress, config.port), 16)
        serverSocket = socket
        running = true
        storeListener = store.addListener(::onPeerChanged)
        thread(name = "peer-accept", isDaemon = true) { acceptLoop(socket) }
        PeerLog.i("serving on port ${socket.localPort} as ${identity.fingerprint.short}")
        return socket.localPort
    }

    /**
     * Stops listening at once; then, off the caller's thread, ends every
     * session (releasing its input through the backend first), stops the
     * backend and runs [onStopped]. Safe to call from any thread.
     */
    fun stop(onStopped: () -> Unit = {}) {
        running = false
        serverSocket?.closeQuietly()
        storeListener?.close()
        submit {
            endAllSessionsNow("serving stopped")
            connections.forEach { it.closeQuietly() }
            val stopping = synchronized(lock) {
                val b = backend
                backend = null
                capabilities = null
                b
            }
            stopping?.stop()
            videoFanout.reset()
            audioFanout.reset()
            onStopped()
        }
        admin.shutdown()
    }

    /** Local emergency stop: disconnect everyone but keep serving. Safe from any thread. */
    fun endAllSessions(reason: String) = submit { endAllSessionsNow(reason) }

    private fun endAllSessionsNow(reason: String) {
        synchronized(lock) { sessions.values.toList() }.forEach { it.end(reason, notifyPeer = true) }
    }

    private fun submit(task: () -> Unit) {
        try {
            admin.execute(task)
        } catch (_: RejectedExecutionException) {
            // Already stopped: stop() ended every session.
        }
    }

    fun sessions(): List<SessionInfo> = synchronized(lock) { sessions.values.map { it.info() } }

    /**
     * A single-use pairing invitation valid for [Config.invitationTtlMs];
     * replaces any earlier one. [host] is the address the other device should dial.
     */
    fun invite(host: String, grants: Set<Grant>): Invitation {
        check(running) { "not serving" }
        val secret = PairingCode.newSecret(random)
        synchronized(lock) {
            invitation = PendingInvitation(secret, grants, System.currentTimeMillis() + config.invitationTtlMs)
        }
        return Invitation(host, port, secret, identity.fingerprint)
    }

    fun cancelInvitation() = synchronized(lock) { invitation = null }

    // ---------------------------------------------------------------- accept

    private fun acceptLoop(socket: ServerSocket) {
        while (running) {
            val raw = try {
                socket.accept()
            } catch (e: IOException) {
                if (running) PeerLog.e("accept failed", e)
                break
            }
            if (connections.size >= config.maxConnections) {
                raw.closeQuietly()
                continue
            }
            connections += raw
            thread(name = "peer-conn", isDaemon = true) {
                try {
                    handle(raw)
                } catch (e: Exception) {
                    PeerLog.d("connection from ${raw.inetAddress.hostAddress} ended: ${e.message}")
                } finally {
                    connections -= raw
                    raw.closeQuietly()
                }
            }
        }
    }

    private fun handle(raw: Socket) {
        val ssl = PeerTls.wrapServer(tls, raw, config.handshakeTimeoutMs)
        val peer = PeerTls.peerFingerprint(ssl)
        val input = DataInputStream(BufferedInputStream(ssl.inputStream, 16 * 1024))
        val output = ssl.outputStream
        when (val first = PeerFrames.read(input)) {
            is PeerMessage.Pair -> handlePair(raw, output, peer, first)
            is PeerMessage.Hello -> {
                val version = negotiate(first) ?: return reject(output, RejectCodes.VERSION, "peer protocol $PEER_PROTOCOL_VERSION required")
                when (first.channel) {
                    Channel.CONTROL -> handleControl(raw, ssl, input, output, peer, first, version)
                    Channel.VIDEO, Channel.AUDIO -> handleMedia(raw, ssl, output, peer, first, version)
                }
            }
            else -> reject(output, RejectCodes.BAD_REQUEST, "expected hello or pair")
        }
    }

    private fun negotiate(hello: PeerMessage.Hello): Int? =
        PEER_PROTOCOL_VERSION.takeIf { hello.minV <= it && hello.v >= it }

    private fun reject(output: OutputStream, code: String, message: String) {
        try {
            PeerFrames.write(output, PeerMessage.Reject(code, message))
        } catch (_: IOException) {
        }
    }

    // --------------------------------------------------------------- pairing

    private fun handlePair(raw: Socket, output: OutputStream, peer: Fingerprint, msg: PeerMessage.Pair) {
        val now = System.currentTimeMillis()
        var failure: Pair<String, String>? = null
        var accepted: PendingInvitation? = null
        synchronized(lock) {
            val inv = invitation
            when {
                inv == null || now > inv.expiresAtMs -> {
                    invitation = null
                    failure = RejectCodes.NO_INVITATION to "this device is not waiting to pair"
                }
                !PairingProof.verify(inv.secret, PairingProof.Role.CONTROLLER, peer, identity.fingerprint, msg.proof) -> {
                    inv.failures++
                    if (inv.failures >= config.maxPairingFailures) invitation = null
                    failure = RejectCodes.BAD_PROOF to "wrong pairing code"
                }
                else -> {
                    invitation = null // single use
                    accepted = inv
                }
            }
        }
        failure?.let { (code, text) ->
            events.onPairingRejected(text)
            return reject(output, code, text)
        }
        val inv = accepted!!
        val name = sanitizeName(msg.client.name)
        val host = raw.inetAddress.hostAddress ?: ""
        store.update(peer) { old ->
            (old ?: PeerRecord(fingerprint = peer.hex, name = name, pairedAtMs = now)).copy(
                name = name,
                inbound = Grant.toWire(inv.grants),
                target = msg.servePort?.let { TargetAddress(host, it) } ?: old?.target,
                lastSeenMs = now,
            )
        }
        val proof = PairingProof.encode(PairingProof.compute(inv.secret, PairingProof.Role.TARGET, identity.fingerprint, peer))
        PeerFrames.write(output, PeerMessage.Paired(proof, device, Grant.toWire(inv.grants)))
        store.find(peer)?.let(events::onPaired)
        PeerLog.i("paired with ${peer.short} ($name)")
    }

    // --------------------------------------------------------------- control

    private fun handleControl(
        raw: Socket,
        ssl: SSLSocket,
        input: DataInputStream,
        output: OutputStream,
        peer: Fingerprint,
        hello: PeerMessage.Hello,
        version: Int,
    ) {
        val record = store.find(peer) ?: return reject(output, RejectCodes.NOT_PAIRED, "this device is not paired with the target")
        val request = hello.request ?: StreamRequest()
        val granted = record.inboundGrants
        val effective = buildSet {
            if (request.video && Grant.VIEW in granted) add(Grant.VIEW)
            if (request.audio && Grant.AUDIO in granted) add(Grant.AUDIO)
            if (request.control && Grant.CONTROL in granted) add(Grant.CONTROL)
            if (request.control && Grant.CLIPBOARD in granted) add(Grant.CLIPBOARD)
        }
        if (effective.isEmpty()) return reject(output, RejectCodes.FORBIDDEN, "nothing requested is granted to this device")
        if (synchronized(lock) { sessions.size } >= config.maxSessions) {
            return reject(output, RejectCodes.BUSY, "too many sessions")
        }
        val backend = obtainBackend() ?: return reject(output, RejectCodes.UNAVAILABLE, "serving is not ready on the target")
        val caps = backend.capabilities

        val session = Session(
            id = Base32.encode(ByteArray(16).also(random::nextBytes)),
            token = ByteArray(32).also(random::nextBytes),
            peer = peer,
            peerName = sanitizeName(hello.client?.name ?: record.name),
            address = raw.inetAddress.hostAddress ?: "",
            grants = effective,
            capabilities = caps,
            raw = raw,
            ssl = ssl,
            input = input,
            output = output,
        )
        synchronized(lock) {
            if (sessions.size >= config.maxSessions) return reject(output, RejectCodes.BUSY, "too many sessions")
            sessions[session.id] = session
            updateDemandLocked()
        }
        try {
            val lease = synchronized(inputLock) {
                if (Grant.CONTROL in effective && leaseHolder == null) leaseHolder = session
                if (leaseHolder === session) LeaseState.HELD else LeaseState.VIEWER
            }
            PeerFrames.write(
                output,
                PeerMessage.Welcome(
                    v = version,
                    channel = Channel.CONTROL,
                    session = session.id,
                    token = hexOf(session.token),
                    grants = Grant.toWire(effective),
                    caps = caps,
                    device = device,
                    streams = streamsState(effective, caps),
                    lease = lease,
                ),
            )
            ssl.soTimeout = config.idleTimeoutMs
            try {
                store.update(peer) { it?.copy(lastSeenMs = System.currentTimeMillis()) }
            } catch (e: IOException) {
                PeerLog.w("could not record last-seen time", e)
            }
            events.onSessionStarted(session.info())
            if (lease == LeaseState.HELD) {
                synchronized(lock) { sessions.values.filter { it !== session && Grant.CONTROL in it.grants } }
                    .forEach { it.sendEnvelope(PeerMessage.Lease(LeaseState.VIEWER, session.peerName)) }
                events.onLeaseChanged(session.info())
            }
            PeerLog.i("session ${session.id.take(6)} for ${peer.short} grants=${Grant.toWire(effective)}")
            session.runControl()
        } finally {
            session.end("handshake failed", notifyPeer = false) // no-op once runControl has ended it
        }
    }

    private fun streamsState(grants: Set<Grant>, caps: Capabilities) = StreamsState(
        video = Grant.VIEW in grants && caps.video,
        audio = Grant.AUDIO in grants && caps.audio,
        control = Grant.CONTROL in grants && caps.control.isNotEmpty(),
        audioError = synchronized(lock) { audioError },
    )

    private fun obtainBackend(): TargetBackend? {
        backend?.let { return it }
        val created = backends.create() ?: return null
        synchronized(lock) {
            backend?.let {
                created.stop()
                return it
            }
            backend = created
            capabilities = created.capabilities
        }
        created.start(Sink(created))
        return created
    }

    /** Caller holds [lock]. */
    private fun updateDemandLocked() {
        val b = backend ?: return
        b.setDemand(
            video = sessions.values.any { Grant.VIEW in it.grants },
            audio = sessions.values.any { Grant.AUDIO in it.grants },
        )
    }

    // ----------------------------------------------------------------- media

    private fun handleMedia(
        raw: Socket,
        ssl: SSLSocket,
        output: OutputStream,
        peer: Fingerprint,
        hello: PeerMessage.Hello,
        version: Int,
    ) {
        val session = synchronized(lock) { hello.session?.let { sessions[it] } }
        val token = hello.token?.let(::bytesOfHex)
        if (session == null || token == null || !session.accepts(peer, token)) {
            return reject(output, RejectCodes.BAD_TOKEN, "unknown or expired session")
        }
        val (grant, fanout) = when (hello.channel) {
            Channel.VIDEO -> Grant.VIEW to videoFanout
            else -> Grant.AUDIO to audioFanout
        }
        if (grant !in session.grants) return reject(output, RejectCodes.FORBIDDEN, "${hello.channel.name.lowercase()} is not granted")
        if (!session.claimChannel(hello.channel, raw)) return reject(output, RejectCodes.BAD_REQUEST, "channel already open")
        PeerFrames.write(output, PeerMessage.Welcome(v = version, channel = hello.channel, session = session.id))
        ssl.soTimeout = 0
        raw.sendBufferSize = if (hello.channel == Channel.VIDEO) config.videoSendBufferBytes else config.audioSendBufferBytes
        fanout.serve(output, abort = { raw.closeQuietly() }) { session.addSubscription(it) }
    }

    // ---------------------------------------------------------------- events

    private inner class Sink(private val owner: TargetBackend) : BackendSink {
        private fun current() = backend === owner

        override fun videoStart(start: StreamStart) {
            if (current()) videoFanout.onStart(start)
        }

        override fun video(item: StreamItem) {
            if (current()) videoFanout.onItem(item)
        }

        override fun videoPaused() {
            if (current()) videoFanout.onPause()
        }

        override fun audioStart(start: StreamStart) {
            if (!current()) return
            synchronized(lock) {
                audioError = if (start is StreamStart.Enabled) null else "audio capture unavailable on the target"
            }
            audioFanout.onStart(start)
        }

        override fun audio(item: StreamItem) {
            if (current()) audioFanout.onItem(item)
        }

        override fun device(message: DeviceMessage) {
            if (current()) onDeviceMessage(message)
        }

        override fun capabilitiesChanged(capabilities: Capabilities) {
            if (!current()) return
            this@TargetServer.capabilities = capabilities
            synchronized(lock) { sessions.values.toList() }.forEach { it.updateCapabilities(capabilities) }
        }

        override fun stopped(error: String?) {
            val wasCurrent = synchronized(lock) {
                if (backend !== owner) return
                backend = null
                capabilities = null
                true
            }
            if (!wasCurrent) return
            PeerLog.w("backend stopped: ${error ?: "no reason"}")
            endAllSessions(if (error != null) "serving stopped: $error" else "serving stopped")
            videoFanout.reset()
            audioFanout.reset()
            events.onBackendStopped(error)
        }
    }

    private fun onDeviceMessage(msg: DeviceMessage) {
        when (msg) {
            is DeviceMessage.Clipboard -> {
                val bytes = msg.toByteArray()
                synchronized(lock) { sessions.values.filter { Grant.CLIPBOARD in it.grants } }.forEach { it.sendRaw(bytes) }
            }
            is DeviceMessage.AckClipboard -> {
                val route = synchronized(inputLock) { clipboardAcks.remove(msg.sequence) } ?: return
                route.first.sendRaw(DeviceMessage.AckClipboard(route.second).toByteArray())
            }
            is DeviceMessage.UhidOutput -> Unit // UHID is never forwarded to peers
        }
    }

    /** Store listener: runs on whichever thread changed the store (often the UI's). */
    private fun onPeerChanged(fingerprint: Fingerprint) = submit {
        val record = store.find(fingerprint)
        val affected = synchronized(lock) { sessions.values.filter { it.peer == fingerprint } }
        for (s in affected) {
            if (record == null || !record.inboundGrants.containsAll(s.grants)) s.end("access revoked", notifyPeer = true)
        }
    }

    // --------------------------------------------------------------- session

    private inner class Session(
        val id: String,
        val token: ByteArray,
        val peer: Fingerprint,
        val peerName: String,
        val address: String,
        val grants: Set<Grant>,
        capabilities: Capabilities,
        private val raw: Socket,
        private val ssl: SSLSocket,
        private val input: DataInputStream,
        private val output: OutputStream,
    ) {
        private val tokenExpiresAt = System.currentTimeMillis() + config.tokenTtlMs
        private val tracker = InputTracker() // guarded by inputLock
        private val writeLock = Any()
        private val openChannels = HashSet<Channel>() // guarded by this
        private val mediaSockets = CopyOnWriteArrayList<Socket>()
        private val subscriptions = CopyOnWriteArrayList<MediaFanout.Subscription>()
        private val lastError = HashMap<String, Long>()

        @Volatile
        private var policy = ControlPolicy(grants, capabilities)

        @Volatile
        var ended = false
            private set

        fun info() = SessionInfo(id, peer, peerName, address, grants, synchronized(inputLock) { leaseHolder === this })

        fun accepts(peer: Fingerprint, presented: ByteArray): Boolean =
            !ended && peer == this.peer && System.currentTimeMillis() < tokenExpiresAt && MessageDigest.isEqual(token, presented)

        fun claimChannel(channel: Channel, socket: Socket): Boolean = synchronized(this) {
            if (ended || !openChannels.add(channel)) return false
            mediaSockets += socket
            true
        }

        fun addSubscription(sub: MediaFanout.Subscription) {
            subscriptions += sub
            if (ended) sub.close()
        }

        fun updateCapabilities(caps: Capabilities) {
            policy = ControlPolicy(grants, caps)
            sendEnvelope(PeerMessage.Status(caps = caps, streams = streamsState(grants, caps)))
        }

        fun runControl() {
            var reason = "disconnected"
            var tellPeer = false
            try {
                while (!ended) {
                    when (val item = ControlChannel.readFromController(input)) {
                        is ControlChannel.FromController.Control -> onControl(item.message)
                        is ControlChannel.FromController.Envelope -> if (!onEnvelope(item.message)) {
                            reason = "controller left"
                            break
                        }
                        ControlChannel.FromController.Ignored -> Unit
                    }
                }
            } catch (e: SocketTimeoutException) {
                reason = "controller went silent"
            } catch (e: ProtocolException) {
                reason = "protocol error: ${e.message}"
                tellPeer = true
            } catch (e: IOException) {
                reason = "disconnected"
            } finally {
                end(reason, notifyPeer = tellPeer)
            }
        }

        private fun onControl(msg: ControlMessage) {
            val holds = synchronized(inputLock) { leaseHolder === this }
            when (policy.evaluate(msg, holds)) {
                ControlPolicy.Verdict.ALLOW -> forward(msg)
                ControlPolicy.Verdict.KEYFRAME -> videoFanout.requestKeyFrameNow()
                ControlPolicy.Verdict.NO_LEASE -> error(ErrorCodes.NO_LEASE, "another controller has the input lease", msg)
                ControlPolicy.Verdict.FORBIDDEN -> error(ErrorCodes.FORBIDDEN, "not permitted for this device", msg)
                ControlPolicy.Verdict.UNSUPPORTED -> error(ErrorCodes.UNSUPPORTED, "the serving backend cannot do this", msg)
            }
        }

        private fun forward(msg: ControlMessage) {
            val performed = synchronized(inputLock) {
                // The lease may have moved since evaluate(); input must never follow a takeover.
                val plainRead = msg is ControlMessage.GetClipboard && msg.copyKey == ControlMessage.COPY_KEY_NONE
                if (leaseHolder !== this && !plainRead) return
                val outgoing = if (msg is ControlMessage.SetClipboard && msg.sequence != 0L) {
                    // Sequences are per controller; route the device's ack back to its sender.
                    val global = ++clipboardSequence
                    clipboardAcks[global] = this to msg.sequence
                    if (clipboardAcks.size > 64) clipboardAcks.remove(clipboardAcks.keys.first())
                    msg.copy(sequence = global)
                } else {
                    msg
                }
                val ok = backend?.sendControl(outgoing) ?: false
                if (ok) tracker.observe(msg)
                ok
            }
            if (!performed) error(ErrorCodes.UNSUPPORTED, "the serving backend could not perform this", msg)
        }

        private fun onEnvelope(msg: PeerMessage): Boolean {
            when (msg) {
                is PeerMessage.Ping -> sendEnvelope(PeerMessage.Pong(msg.t))
                is PeerMessage.Takeover -> takeover()
                is PeerMessage.Bye -> {
                    val holds = synchronized(inputLock) { leaseHolder === this }
                    if (msg.endSession && holds && Grant.CONTROL in grants) {
                        // Remote "quit": everyone is disconnected, serving stays on (only
                        // the local user can turn serving off).
                        endAllSessions("ended by $peerName")
                    }
                    return false
                }
                else -> Unit
            }
            return true
        }

        private fun takeover() {
            if (Grant.CONTROL !in grants) {
                sendEnvelope(PeerMessage.Error(ErrorCodes.FORBIDDEN, "control is not granted to this device"))
                return
            }
            val previous = synchronized(inputLock) {
                val old = leaseHolder
                if (old !== this) {
                    old?.releaseInputLocked()
                    leaseHolder = this
                }
                old
            }
            if (previous !== this) {
                previous?.sendEnvelope(PeerMessage.Lease(LeaseState.VIEWER, peerName))
                events.onLeaseChanged(info())
            }
            sendEnvelope(PeerMessage.Lease(LeaseState.HELD))
        }

        /** Caller holds [inputLock]. */
        fun releaseInputLocked() {
            val b = backend
            for (release in tracker.releaseAll()) b?.sendControl(release)
        }

        fun end(reason: String, notifyPeer: Boolean) {
            synchronized(this) {
                if (ended) return
                ended = true
            }
            val freedLease = synchronized(inputLock) {
                releaseInputLocked()
                if (leaseHolder === this) {
                    leaseHolder = null
                    true
                } else {
                    false
                }
            }
            val others = synchronized(lock) {
                sessions.remove(id)
                updateDemandLocked()
                sessions.values.toList()
            }
            subscriptions.forEach { it.close() }
            mediaSockets.forEach { it.closeQuietly() }
            if (notifyPeer) {
                sendEnvelope(PeerMessage.Bye(reason))
                // Closing with unread bytes (a ping in flight) sends a TCP reset
                // that can destroy the bye; half-close and linger instead.
                thread(name = "peer-linger", isDaemon = true) { lingerClose(raw) }
            } else {
                raw.closeQuietly()
            }
            if (freedLease) {
                others.filter { Grant.CONTROL in it.grants }.forEach { it.sendEnvelope(PeerMessage.Lease(LeaseState.VIEWER)) }
                events.onLeaseChanged(null)
            }
            events.onSessionEnded(info(), reason)
            PeerLog.i("session ${id.take(6)} ended: $reason")
        }

        private fun error(code: String, message: String, msg: ControlMessage) {
            val now = System.nanoTime()
            val key = code + msg.type
            synchronized(lastError) {
                val last = lastError[key]
                if (last != null && now - last < 1_000_000_000L) return
                lastError[key] = now
            }
            sendEnvelope(PeerMessage.Error(code, message, msg.typeName))
        }

        fun sendEnvelope(msg: PeerMessage) = sendRaw(ControlChannel.encodeEnvelope(msg))

        fun sendRaw(bytes: ByteArray) {
            if (raw.isClosed) return
            try {
                synchronized(writeLock) {
                    output.write(bytes)
                    output.flush()
                }
            } catch (e: IOException) {
                raw.closeQuietly() // the control loop sees the failure and ends the session
            }
        }
    }

    private companion object {
        fun lingerClose(socket: Socket) {
            try {
                socket.shutdownOutput()
                socket.soTimeout = 500
                val sink = ByteArray(4096)
                val input = socket.getInputStream()
                @Suppress("ControlFlowWithEmptyBody")
                while (input.read(sink) >= 0) {
                }
            } catch (_: IOException) {
            } finally {
                socket.closeQuietly()
            }
        }

        fun sanitizeName(name: String): String =
            name.filter { !it.isISOControl() }.trim().take(64).ifEmpty { "unnamed device" }

        fun hexOf(bytes: ByteArray): String = bytes.joinToString("") { "%02x".format(it) }

        fun bytesOfHex(text: String): ByteArray? {
            if (text.length % 2 != 0 || text.length > 128) return null
            return try {
                ByteArray(text.length / 2) { text.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
            } catch (e: NumberFormatException) {
                null
            }
        }
    }
}
