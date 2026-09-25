package io.github.pwnedbygary.scterm.target

import android.content.Context
import io.github.pwnedbygary.scterm.helper.HelperHandshake
import io.github.pwnedbygary.scterm.peer.BackendSink
import io.github.pwnedbygary.scterm.peer.PeerLog
import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceMessage
import io.github.pwnedbygary.scterm.protocol.MediaStreamReader
import io.github.pwnedbygary.scterm.protocol.MediaWire
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart
import java.io.BufferedInputStream
import java.io.DataInputStream
import java.io.IOException
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketTimeoutException
import java.security.SecureRandom
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.concurrent.thread

/**
 * Full-control serving through the vendored scrcpy v4.1 server, which this
 * APK contains. Started once per boot with shell identity by the activation
 * command, which runs [io.github.pwnedbygary.scterm.helper.HelperMain]:
 *
 *     adb shell 'CLASSPATH=<this apk> nohup app_process / …HelperMain <port> <token> 4.1 scid=… …'
 *
 * The helper relays the server's three connections to this backend over
 * loopback TCP (127.0.0.1 only), each opened with a 256-bit token that only
 * the activation command carries (other apps cannot read a shell process's
 * arguments). A direct abstract-socket connection would be simpler, but
 * SELinux denies the shell domain `connectto` on app sockets (observed on
 * Android 16). Everything peers send still passes the peer server's grants,
 * lease and validation before reaching the helper.
 */
class HelperBackend(context: Context) : ServingBackend {
    private val random = SecureRandom()
    private val scid = random.nextInt() and 0x7fffffff
    private val token = HelperHandshake.newToken(random)
    private val listener = ServerSocket(0, 4, InetAddress.getLoopbackAddress())
    private val sockets = CopyOnWriteArrayList<Socket>()

    private val lock = Any()
    private var sink: BackendSink? = null
    private var videoStart: StreamStart? = null
    private var audioStart: StreamStart? = null
    private var session: StreamItem.Session? = null
    private var videoConfig: StreamItem.Packet? = null
    private var audioConfig: StreamItem.Packet? = null

    @Volatile
    private var control: OutputStream? = null

    @Volatile
    private var stopped = false

    @Volatile
    private var audioAvailable = false

    @Volatile
    override var ready = false
        private set

    @Volatile
    override var status = "Waiting for the helper: run the activation command from a computer with adb"
        private set

    override var onChanged: (() -> Unit)? = null

    override val capabilities: Capabilities
        get() = Capabilities(
            backend = "helper",
            video = true,
            audio = audioAvailable,
            control = CONTROL,
            multitouch = true,
            clipboard = true,
            maxSessions = 4,
            notes = listOf("Full control through the ADB-activated scrcpy helper."),
        )

    /** The one command to run from a computer; valid until serving stops. */
    val activationCommand: String =
        "adb shell 'CLASSPATH=${context.applicationInfo.sourceDir} nohup app_process / " +
            "io.github.pwnedbygary.scterm.helper.HelperMain ${listener.localPort} ${HelperHandshake.formatToken(token)} " +
            "$SERVER_VERSION scid=%08x tunnel_forward=false cleanup=false power_on=false ".format(scid) +
            "video_codec=h264 video_codec_options=$LOW_LATENCY_ENCODER audio_codec=opus max_size=1920 log_level=info >/dev/null 2>&1 &'"

    init {
        thread(name = "helper-accept", isDaemon = true) { acceptHelper() }
    }

    override fun start(sink: BackendSink) {
        synchronized(lock) {
            this.sink = sink
            videoStart?.let(sink::videoStart)
            session?.let(sink::video)
            videoConfig?.let(sink::video)
            audioStart?.let(sink::audioStart)
            audioConfig?.let(sink::audio)
        }
    }

    override fun sendControl(message: ControlMessage): Boolean {
        val out = control ?: return false
        return try {
            synchronized(out) { out.write(message.toByteArray()) }
            true
        } catch (e: IOException) {
            false
        }
    }

    /** scrcpy has no sync-frame request; a reset restarts capture with session, config and keyframe. */
    override fun requestKeyFrame() {
        sendControl(ControlMessage.ResetVideo)
    }

    override fun stop() {
        if (stopped) return
        stopped = true
        try {
            listener.close()
        } catch (_: IOException) {
        }
        // Closing the relay makes the helper's server end and its process exit.
        sockets.forEach { closeQuietly(it) }
        control = null
    }

    private fun acceptHelper() {
        try {
            val channels = arrayOfNulls<Socket>(HelperHandshake.CHANNELS)
            while (channels.any { it == null }) {
                val socket = listener.accept()
                val channel = handshake(socket)
                if (channel == null || channels[channel] != null) {
                    PeerLog.w("refused a loopback connection without the helper token")
                    closeQuietly(socket)
                    continue
                }
                socket.tcpNoDelay = true
                sockets += socket
                channels[channel] = socket
            }
            val (video, audio, ctl) = channels.map { it!! }
            val videoIn = DataInputStream(BufferedInputStream(video.getInputStream(), 256 * 1024))
            // The server writes the device name field on its first socket.
            val name = ByteArray(MediaWire.DEVICE_NAME_FIELD).also { videoIn.readFully(it) }
            control = ctl.getOutputStream()
            thread(name = "helper-video", isDaemon = true) { pumpVideo(videoIn) }
            thread(name = "helper-audio", isDaemon = true) {
                pumpAudio(DataInputStream(BufferedInputStream(audio.getInputStream(), 64 * 1024)))
            }
            thread(name = "helper-control", isDaemon = true) {
                pumpDevice(DataInputStream(BufferedInputStream(ctl.getInputStream(), 16 * 1024)))
            }
            ready = true
            status = "Helper connected (${MediaWire.decodeDeviceName(name)}); full control available"
            onChanged?.invoke()
        } catch (e: IOException) {
            if (!stopped) died("the helper could not connect: ${e.message}")
        }
    }

    /** The channel a connection claims, or null if it did not present the token in time. */
    private fun handshake(socket: Socket): Int? = try {
        socket.soTimeout = HANDSHAKE_TIMEOUT_MS
        val bytes = ByteArray(HelperHandshake.SIZE)
        DataInputStream(socket.getInputStream()).readFully(bytes)
        socket.soTimeout = 0
        HelperHandshake.decode(bytes, token)
    } catch (e: SocketTimeoutException) {
        null
    } catch (e: IOException) {
        null
    }

    private fun pumpVideo(input: DataInputStream) = pump("video") {
        val reader = MediaStreamReader(input, MediaWire.MAX_VIDEO_PACKET)
        val start = reader.readStart()
        synchronized(lock) {
            videoStart = start
            sink?.videoStart(start)
        }
        while (true) {
            val item = reader.readItem()
            synchronized(lock) {
                if (item is StreamItem.Session) {
                    session = item
                    videoConfig = null
                } else if (item is StreamItem.Packet && item.config) {
                    videoConfig = item
                }
                sink?.video(item)
            }
        }
    }

    private fun pumpAudio(input: DataInputStream) = pump("audio") {
        val reader = MediaStreamReader(input, MediaWire.MAX_AUDIO_PACKET)
        val start = reader.readStart()
        audioAvailable = start is StreamStart.Enabled
        synchronized(lock) {
            audioStart = start
            sink?.audioStart(start)
        }
        if (start !is StreamStart.Enabled) return@pump
        while (true) {
            val item = reader.readItem()
            synchronized(lock) {
                if (item is StreamItem.Packet && item.config) audioConfig = item
                sink?.audio(item)
            }
        }
    }

    private fun pumpDevice(input: DataInputStream) = pump("control") {
        while (true) {
            val message = DeviceMessage.read(input)
            synchronized(lock) { sink }?.device(message)
        }
    }

    private inline fun pump(stream: String, block: () -> Unit) {
        try {
            block()
        } catch (e: IOException) {
            if (!stopped) died("the helper's $stream stream ended (${e.message})")
        }
    }

    private fun died(reason: String) {
        if (stopped) return
        PeerLog.w(reason)
        ready = false
        status = "Helper stopped: $reason"
        val s = synchronized(lock) { sink }
        stop()
        s?.stopped(reason)
        onChanged?.invoke()
    }

    private fun closeQuietly(socket: Socket) {
        try {
            socket.close()
        } catch (_: IOException) {
        }
    }

    private companion object {
        const val SERVER_VERSION = "4.1"
        const val HANDSHAKE_TIMEOUT_MS = 5_000

        /**
         * MediaFormat keys for the scrcpy server's encoder: realtime priority, at
         * most one frame held inside the encoder, no B-frame reordering, and
         * Qualcomm's low-latency mode. Codecs ignore keys they do not know.
         */
        const val LOW_LATENCY_ENCODER = "priority=0,latency=1,max-bframes=0,vendor.qti-ext-enc-low-latency.enable=1"

        val CONTROL = listOf(
            "inject_keycode", "inject_text", "inject_touch_event", "inject_scroll_event",
            "back_or_screen_on", "expand_notification_panel", "expand_settings_panel", "collapse_panels",
            "get_clipboard", "set_clipboard", "set_display_power", "rotate_device", "reset_video",
        )
    }
}
