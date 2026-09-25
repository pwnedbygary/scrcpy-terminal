package io.github.pwnedbygary.scterm.peerctl

import io.github.pwnedbygary.scterm.peer.ControllerClient
import io.github.pwnedbygary.scterm.peer.ControllerSession
import io.github.pwnedbygary.scterm.peer.FilePeerStore
import io.github.pwnedbygary.scterm.peer.PeerRecord
import io.github.pwnedbygary.scterm.peer.StaticIdentity
import io.github.pwnedbygary.scterm.peer.TargetAddress
import io.github.pwnedbygary.scterm.peer.TargetServer
import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.ClientInfo
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceAction
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.DeviceMessage
import io.github.pwnedbygary.scterm.protocol.Fingerprint
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Invitation
import io.github.pwnedbygary.scterm.protocol.LeaseState
import io.github.pwnedbygary.scterm.protocol.MediaStreamReader
import io.github.pwnedbygary.scterm.protocol.MediaWire
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import io.github.pwnedbygary.scterm.protocol.Position
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamRequest
import io.github.pwnedbygary.scterm.protocol.StreamStart
import org.jcodec.codecs.h264.BufferH264ES
import org.jcodec.codecs.h264.H264Decoder
import org.jcodec.common.io.NIOUtils
import org.jcodec.common.model.ColorSpace
import org.jcodec.common.model.Picture
import org.jcodec.scale.AWTUtil
import java.io.File
import java.io.FileOutputStream
import java.io.IOException
import java.io.InputStream
import java.net.InetAddress
import java.security.KeyStore
import java.util.concurrent.CopyOnWriteArrayList
import javax.imageio.ImageIO
import kotlin.concurrent.thread
import kotlin.system.exitProcess

/**
 * A desktop controller for exercising real targets over the peer protocol:
 *
 *   peerctl --home DIR whoami
 *   peerctl --home DIR pair "HOST:PORT CODE" [--host H --port P]
 *   peerctl --home DIR session [--host H --port P] [--seconds N] [--video out.h264]
 *           [--frame out.png] [--no-audio] [--script "wait 1; action home; tap 0.5 0.5"]
 *   peerctl --home DIR serve [--port P] [--size WxH] [--invite-host H] [--grants view,control]
 *           [--seconds N]
 *
 * It uses the same :peer code as the Android controller, so what works here
 * is the protocol the phone speaks, not a reimplementation. `serve` is the
 * other half: a target streaming a test pattern (see [TestPatternBackend]),
 * listening on loopback only; reach it from a phone with
 * `adb reverse tcp:P tcp:P`.
 */
fun main(args: Array<String>) {
    val opts = Options(args.toList())
    val home = File(opts.value("--home") ?: "peerctl-home").apply { mkdirs() }
    val identity = identity(home)
    val store = FilePeerStore.open(File(home, "peers.json"))
    val client = ControllerClient(identity, ClientInfo(opts.value("--name") ?: "peerctl", "scterm-peerctl/0.1", "jvm"))
    try {
        when (opts.command) {
            "whoami" -> println("fingerprint ${identity.fingerprint.hex} (${identity.fingerprint.short})")
            "pair" -> pair(opts, client, store)
            "session" -> Session(opts, client, store).run()
            "serve" -> serve(opts, identity, store)
            else -> {
                System.err.println("usage: peerctl --home DIR (whoami | pair INVITATION | session [options] | serve [options])")
                exitProcess(2)
            }
        }
    } catch (e: Exception) {
        println("ERROR ${e.javaClass.simpleName}: ${e.message}")
        exitProcess(1)
    }
    exitProcess(0)
}

private const val PASS = "peerctl"

private fun identity(home: File): StaticIdentity {
    val file = File(home, "identity.p12")
    if (!file.exists()) {
        val keytool = File(System.getProperty("java.home"), "bin/keytool").path
        val process = ProcessBuilder(
            keytool, "-genkeypair", "-alias", "peer", "-keyalg", "EC", "-groupname", "secp256r1",
            "-sigalg", "SHA256withECDSA", "-dname", "CN=peerctl", "-validity", "36500",
            "-storetype", "PKCS12", "-keystore", file.path, "-storepass", PASS, "-keypass", PASS,
        ).redirectErrorStream(true).start()
        val output = process.inputStream.readBytes().decodeToString()
        check(process.waitFor() == 0) { "keytool failed: $output" }
    }
    val keyStore = KeyStore.getInstance("PKCS12")
    file.inputStream().use { keyStore.load(it, PASS.toCharArray()) }
    return StaticIdentity.fromKeyStore(keyStore, "peer", PASS.toCharArray())
}

private fun pair(opts: Options, client: ControllerClient, store: FilePeerStore) {
    val invitation = Invitation.parse(opts.positional.getOrNull(0) ?: error("pair needs an invitation"))
    val target = Invitation(
        opts.value("--host") ?: invitation.host,
        opts.value("--port")?.toInt() ?: invitation.port,
        invitation.secret,
        invitation.fingerprint,
    )
    val started = System.nanoTime()
    val result = client.pair(target)
    val now = System.currentTimeMillis()
    store.update(result.fingerprint) { old ->
        (old ?: PeerRecord(result.fingerprint.hex, result.device.name, pairedAtMs = now)).copy(
            name = result.device.name,
            target = TargetAddress(target.host, target.port),
            grantedByPeer = Grant.toWire(result.grants),
        )
    }
    println("PAIRED in ${ms(started)} ms: ${result.device.name} (${result.device.model}, sdk ${result.device.sdk}) " +
        "fingerprint ${result.fingerprint.short} grants ${Grant.toWire(result.grants)}")
}

private fun serve(opts: Options, identity: StaticIdentity, store: FilePeerStore) {
    System.setProperty("java.awt.headless", "true")
    val (width, height) = (opts.value("--size") ?: "576x1280").split('x').map(String::toInt)
    val t0 = System.nanoTime()
    val log = { text: String -> println("[${ms(t0)} ms] $text") }
    val server = TargetServer(
        identity,
        store,
        DeviceInfo(opts.value("--name") ?: "peerctl test pattern", model = "JVM", manufacturer = "scterm"),
        backends = { TestPatternBackend(width, height, log = log) },
        config = TargetServer.Config(port = opts.value("--port")?.toInt() ?: 27401, bindAddress = InetAddress.getLoopbackAddress()),
        events = object : TargetServer.Events {
            override fun onSessionStarted(session: TargetServer.SessionInfo) =
                log("SESSION started for ${session.peerName}: grants=${Grant.toWire(session.grants)} lease=${session.holdsLease}")

            override fun onSessionEnded(session: TargetServer.SessionInfo, reason: String) = log("SESSION ended for ${session.peerName}: $reason")
            override fun onLeaseChanged(holder: TargetServer.SessionInfo?) = log("LEASE ${holder?.peerName ?: "free"}")
            override fun onPaired(record: PeerRecord) = log("PAIRED ${record.name} (${record.id.short})")
            override fun onPairingRejected(reason: String) = log("PAIRING rejected: $reason")
            override fun onBackendStopped(error: String?) = log("BACKEND stopped: ${error ?: "no reason"}")
        },
    )
    server.start()
    log("SERVING ${width}x$height on 127.0.0.1:${server.port} as ${identity.fingerprint.short}")
    opts.value("--invite-host")?.let { host ->
        val grants = Grant.parse(opts.value("--grants")?.split(',') ?: listOf("view", "control"))
        val invitation = server.invite(host, grants)
        log("INVITE ${invitation.manualText} (grants ${Grant.toWire(grants)}, one use, 10 minutes)")
        log("INVITE ${invitation.toUri()}")
    }
    val seconds = opts.value("--seconds")?.toDouble()
    if (seconds != null) Thread.sleep((seconds * 1000).toLong()) else Thread.currentThread().join()
    server.stop()
}

private fun ms(since: Long) = (System.nanoTime() - since) / 1_000_000

private class Session(private val opts: Options, private val client: ControllerClient, private val store: FilePeerStore) {
    private val t0 = System.nanoTime()
    private val events = CopyOnWriteArrayList<String>()

    @Volatile
    private var width = 0

    @Volatile
    private var height = 0

    // video stats
    private var videoCodec = "-"
    private val sessions = CopyOnWriteArrayList<String>()
    private var configs = 0
    private var keyFrames = 0
    private var frames = 0
    private var videoBytes = 0L
    private var firstFrameMs = -1L
    private var firstKeyMs = -1L
    private var profile = "-"

    // audio stats
    private var audioCodec = "-"
    private var audioPackets = 0
    private var audioBytes = 0L
    private var firstAudioMs = -1L
    private val rtts = CopyOnWriteArrayList<Long>()

    private fun event(text: String) {
        val line = "[${ms(t0)} ms] $text"
        events += line
        println(line)
    }

    fun run() {
        val record = opts.value("--fp")?.let { store.find(Fingerprint.parse(it)) } ?: store.all().singleOrNull()
            ?: error("pair first, or pass --fp")
        val host = opts.value("--host") ?: record.target?.host ?: error("no address")
        val port = opts.value("--port")?.toInt() ?: record.target?.port ?: error("no port")
        val request = StreamRequest(video = !opts.flag("--no-video"), audio = !opts.flag("--no-audio"), control = !opts.flag("--no-control"))
        val session = client.connect(host, port, record.id, request)
        val caps = session.capabilities
        event("CONNECTED in ${ms(t0)} ms to ${session.welcome.device?.name}: grants=${session.welcome.grants} lease=${session.lease} " +
            "backend=${caps?.backend} control=${caps?.control?.size ?: 0} types audio=${caps?.audio} notes=${caps?.notes}")
        val closed = java.util.concurrent.CountDownLatch(1)
        session.start(object : ControllerSession.Listener {
            override fun onDeviceMessage(message: DeviceMessage) = event(
                when (message) {
                    is DeviceMessage.Clipboard -> "DEVICE clipboard (${message.text.length} chars; content not shown)"
                    is DeviceMessage.AckClipboard -> "DEVICE ack_clipboard ${message.sequence}"
                    else -> "DEVICE ${message.javaClass.simpleName}"
                },
            )

            override fun onLease(state: LeaseState, holder: String?) = event("LEASE $state holder=$holder")
            override fun onError(error: PeerMessage.Error) = event("ERROR ${error.code} ${error.controlType ?: ""} ${error.message}")
            override fun onStatus(status: PeerMessage.Status) =
                event("STATUS control=${status.caps?.control} streams=${status.streams}")

            override fun onClosed(reason: String) {
                event("CLOSED $reason")
                closed.countDown()
            }
        })
        val videoFile = opts.value("--video")?.let(::File)
        session.video?.let { stream -> thread(isDaemon = true, name = "video") { readVideo(stream, videoFile) } }
        session.audio?.let { stream -> thread(isDaemon = true, name = "audio") { readAudio(stream) } }
        thread(isDaemon = true, name = "rtt") {
            while (!session.isClosed) {
                Thread.sleep(1_000)
                session.roundTripMs.takeIf { it >= 0 }?.let { if (rtts.lastOrNull() != it || rtts.isEmpty()) rtts += it }
            }
        }
        opts.value("--script")?.let { runScript(it, session) }
        val seconds = opts.value("--seconds")?.toDouble() ?: 5.0
        closed.await((seconds * 1000).toLong(), java.util.concurrent.TimeUnit.MILLISECONDS)
        if (!session.isClosed) {
            session.disconnect(endSession = opts.flag("--end-session"))
            closed.await(2, java.util.concurrent.TimeUnit.SECONDS)
        }
        summary()
        val frame = opts.value("--frame")
        if (videoFile != null && frame != null) println("DECODE ${decode(videoFile, File(frame))}")
    }

    private fun readVideo(stream: InputStream, save: File?) {
        val out = save?.let { FileOutputStream(it) }
        try {
            val reader = MediaStreamReader(stream, MediaWire.MAX_VIDEO_PACKET)
            val start = reader.readStart()
            videoCodec = if (start is StreamStart.Enabled) start.codec.wireName else start.toString()
            event("VIDEO start $videoCodec")
            while (true) {
                when (val item = reader.readItem()) {
                    is StreamItem.Session -> {
                        width = item.width
                        height = item.height
                        sessions += "${item.width}x${item.height}@${ms(t0)}ms"
                        event("VIDEO session ${item.width}x${item.height}")
                    }
                    is StreamItem.Packet -> {
                        videoBytes += item.data.size
                        out?.write(item.data)
                        if (item.config) {
                            configs++
                            profile = profileOf(item.data)
                        } else {
                            frames++
                            if (firstFrameMs < 0) firstFrameMs = ms(t0)
                            if (item.keyFrame) {
                                keyFrames++
                                if (firstKeyMs < 0) firstKeyMs = ms(t0)
                            }
                        }
                    }
                }
            }
        } catch (_: IOException) {
        } finally {
            out?.close()
        }
    }

    private fun readAudio(stream: InputStream) {
        try {
            val reader = MediaStreamReader(stream, MediaWire.MAX_AUDIO_PACKET)
            val start = reader.readStart()
            audioCodec = if (start is StreamStart.Enabled) start.codec.wireName else start.toString()
            event("AUDIO start $audioCodec")
            while (true) {
                val item = reader.readItem() as? StreamItem.Packet ?: continue
                if (item.config) continue
                audioPackets++
                audioBytes += item.data.size
                if (firstAudioMs < 0) firstAudioMs = ms(t0)
            }
        } catch (_: IOException) {
        }
    }

    private fun runScript(script: String, session: ControllerSession) {
        for (raw in script.split(';')) {
            val words = raw.trim().split(Regex("\\s+")).filter { it.isNotEmpty() }
            if (words.isEmpty()) continue
            event("SCRIPT ${words.joinToString(" ")}")
            val arg = { i: Int -> words[i].toFloat() }
            when (words[0]) {
                "wait" -> Thread.sleep((arg(1) * 1000).toLong())
                "action" -> session.sendAll(DeviceAction.byId(words[1])?.messages() ?: error("unknown action ${words[1]}"))
                "key" -> {
                    val code = words[1].toInt()
                    session.send(ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, code, 0, 0))
                    session.send(ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_UP, code, 0, 0))
                }
                "tap" -> {
                    session.send(touch(AndroidInput.MOTION_ACTION_DOWN, arg(1), arg(2)))
                    session.send(touch(AndroidInput.MOTION_ACTION_UP, arg(1), arg(2)))
                }
                "hold" -> session.send(touch(AndroidInput.MOTION_ACTION_DOWN, arg(1), arg(2)))
                "swipe" -> {
                    val steps = maxOf(2, (arg(5) / 16).toInt())
                    session.send(touch(AndroidInput.MOTION_ACTION_DOWN, arg(1), arg(2)))
                    for (i in 1..steps) {
                        val f = i.toFloat() / steps
                        Thread.sleep(16)
                        session.send(touch(AndroidInput.MOTION_ACTION_MOVE, arg(1) + (arg(3) - arg(1)) * f, arg(2) + (arg(4) - arg(2)) * f))
                    }
                    session.send(touch(AndroidInput.MOTION_ACTION_UP, arg(3), arg(4)))
                }
                "text" -> ControlMessage.textChunks(words.drop(1).joinToString(" ")).forEach(session::send)
                "clip" -> session.send(ControlMessage.GetClipboard(ControlMessage.COPY_KEY_NONE))
                "setclip" -> session.send(ControlMessage.SetClipboard(1, paste = false, text = words.drop(1).joinToString(" ")))
                "rotate" -> session.send(ControlMessage.RotateDevice)
                "reset" -> session.send(ControlMessage.ResetVideo)
                "takeover" -> session.takeover()
                "forbidden" -> session.send(ControlMessage.StartApp("com.android.settings"))
                else -> error("unknown script command ${words[0]}")
            }
        }
    }

    private fun touch(action: Int, fx: Float, fy: Float): ControlMessage {
        val w = width
        val h = height
        val x = (fx * w).toInt().coerceIn(0, maxOf(0, w - 1))
        val y = (fy * h).toInt().coerceIn(0, maxOf(0, h - 1))
        val pressure = if (action == AndroidInput.MOTION_ACTION_UP) 0 else 0xffff
        return ControlMessage.InjectTouch(action, 0, Position(x, y, w, h), pressure, 0, 0)
    }

    private fun summary() {
        val secs = ms(t0) / 1000.0
        println("SUMMARY video codec=$videoCodec profile=$profile sessions=$sessions configs=$configs keyframes=$keyFrames " +
            "frames=$frames fps=%.1f kbps=%.0f firstFrame=${firstFrameMs}ms firstKeyFrame=${firstKeyMs}ms"
                .format(frames / secs, videoBytes * 8 / secs / 1000))
        println("SUMMARY audio codec=$audioCodec packets=$audioPackets pps=%.1f kbps=%.0f firstPacket=${firstAudioMs}ms"
            .format(audioPackets / secs, audioBytes * 8 / secs / 1000))
        println("SUMMARY rtt samples(ms)=$rtts")
    }

    private fun profileOf(config: ByteArray): String {
        // SPS NAL (type 7): profile_idc and level_idc follow the header byte.
        for (i in 0 until config.size - 4) {
            val startCode = config[i].toInt() == 0 && config[i + 1].toInt() == 0 && config[i + 2].toInt() == 1
            if (startCode && config[i + 3].toInt() and 0x1f == 7 && i + 6 < config.size) {
                val name = when (config[i + 4].toInt() and 0xff) {
                    66 -> "baseline"
                    77 -> "main"
                    100 -> "high"
                    else -> "profile ${config[i + 4].toInt() and 0xff}"
                }
                return "$name level ${(config[i + 6].toInt() and 0xff) / 10.0}"
            }
        }
        return "unknown"
    }

    /** Decodes the saved elementary stream in pure Java; writes the last frame. */
    private fun decode(file: File, png: File): String = try {
        val es = BufferH264ES(NIOUtils.fetchFromFile(file))
        val decoder = H264Decoder()
        val w = (width + 15) and 15.inv()
        val h = (height + 15) and 15.inv()
        val buffer = Picture.create(w, h, ColorSpace.YUV420J).data
        var last: Picture? = null
        var decoded = 0
        while (true) {
            val packet = es.nextFrame() ?: break
            val frame = decoder.decodeFrame(packet.data, buffer) ?: continue
            last = frame
            decoded++
        }
        if (last == null) {
            "no frame decoded"
        } else {
            ImageIO.write(AWTUtil.toBufferedImage(last), "png", png)
            "$decoded frames decoded, last written to ${png.path}"
        }
    } catch (e: Exception) {
        "failed: ${e.javaClass.simpleName} ${e.message}"
    }
}

private class Options(args: List<String>) {
    val command: String?
    val positional = ArrayList<String>()
    private val values = HashMap<String, String>()
    private val flags = HashSet<String>()

    init {
        var i = 0
        var cmd: String? = null
        while (i < args.size) {
            val a = args[i]
            when {
                a in FLAGS -> flags += a
                a.startsWith("--") -> {
                    values[a] = args.getOrElse(i + 1) { "" }
                    i++
                }
                cmd == null -> cmd = a
                else -> positional += a
            }
            i++
        }
        command = cmd
    }

    fun value(name: String): String? = values[name]
    fun flag(name: String) = name in flags

    companion object {
        val FLAGS = setOf("--no-audio", "--no-video", "--no-control", "--end-session")
    }
}
