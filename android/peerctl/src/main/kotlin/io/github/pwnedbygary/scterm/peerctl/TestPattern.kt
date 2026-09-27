package io.github.pwnedbygary.scterm.peerctl

import io.github.pwnedbygary.scterm.peer.BackendSink
import io.github.pwnedbygary.scterm.peer.TargetBackend
import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.Codec
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart
import org.jcodec.codecs.h264.H264Encoder
import org.jcodec.common.model.ColorSpace
import org.jcodec.scale.AWTUtil
import java.awt.BasicStroke
import java.awt.Color
import java.awt.Font
import java.awt.RenderingHints
import java.awt.image.BufferedImage
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.time.LocalTime
import java.time.format.DateTimeFormatter
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.thread
import kotlin.concurrent.withLock

/**
 * A target with no device behind it: streams a JCodec-encoded test pattern
 * and draws every touch it receives, so a controller's rendering, cropping,
 * rotation and touch mapping can be checked by eye and against the log.
 */
class TestPatternBackend(
    width: Int,
    height: Int,
    private val fps: Int = 15,
    private val log: (String) -> Unit,
) : TargetBackend {
    override val capabilities = Capabilities(
        backend = "test-pattern",
        video = true,
        audio = false,
        control = listOf(
            ControlMessage.TYPE_INJECT_KEYCODE,
            ControlMessage.TYPE_INJECT_TEXT,
            ControlMessage.TYPE_INJECT_TOUCH_EVENT,
            ControlMessage.TYPE_INJECT_SCROLL_EVENT,
            ControlMessage.TYPE_BACK_OR_SCREEN_ON,
            ControlMessage.TYPE_ROTATE_DEVICE,
            ControlMessage.TYPE_RESET_VIDEO,
        ).map(ControlMessage::typeName),
        multitouch = true,
        notes = listOf("peerctl test pattern: touches are drawn on the pattern and logged on the computer."),
    )

    private class Mark(val x: Int, val y: Int, val kind: Char, val atMs: Long)

    private val lock = ReentrantLock()
    private val changed = lock.newCondition()
    private var sink: BackendSink? = null
    private var width = width
    private var height = height
    private var wantVideo = false
    private var newSession = true
    private var forceKey = false
    private var stopped = false
    private val marks = ArrayDeque<Mark>()
    private var lastInput = "none yet"

    override fun start(sink: BackendSink) {
        this.sink = sink
        sink.videoStart(StreamStart.Enabled(Codec.H264))
        sink.audioStart(StreamStart.Disabled)
        thread(name = "test-pattern", isDaemon = true) { encodeLoop(sink) }
    }

    override fun setDemand(video: Boolean, audio: Boolean) = lock.withLock {
        if (video && !wantVideo) newSession = true
        wantVideo = video
        changed.signalAll()
    }

    override fun requestKeyFrame() = lock.withLock { forceKey = true }

    override fun stop() = lock.withLock {
        stopped = true
        changed.signalAll()
    }

    override fun sendControl(message: ControlMessage): Boolean {
        val now = System.currentTimeMillis()
        val line = when (message) {
            is ControlMessage.InjectTouch -> {
                val p = message.position
                val kind = when (message.action and 0xff) {
                    AndroidInput.MOTION_ACTION_DOWN, ACTION_POINTER_DOWN -> 'D'
                    AndroidInput.MOTION_ACTION_UP, ACTION_POINTER_UP -> 'U'
                    AndroidInput.MOTION_ACTION_MOVE -> 'M'
                    else -> '?'
                }
                lock.withLock {
                    if (p.screenWidth != width || p.screenHeight != height) {
                        return true.also { log("TOUCH ignored: aimed at ${p.screenWidth}x${p.screenHeight}, frame is ${width}x$height") }
                    }
                    marks.addLast(Mark(p.x, p.y, kind, now))
                    while (marks.size > MAX_MARKS) marks.removeFirst()
                }
                // Moves are drawn but not logged: a drag would flood the log.
                if (kind == 'M') null else "TOUCH ${if (kind == 'D') "down" else if (kind == 'U') "up" else "action ${message.action}"} " +
                    "pointer=${message.pointerId} at ${p.x},${p.y} of ${p.screenWidth}x${p.screenHeight}"
            }
            is ControlMessage.InjectKeycode ->
                "KEY ${message.keycode} ${if (message.action == AndroidInput.KEY_ACTION_DOWN) "down" else "up"}"
            is ControlMessage.InjectText -> "TEXT ${message.text.length} chars"
            is ControlMessage.InjectScroll ->
                "SCROLL at ${message.position.x},${message.position.y} h=${message.hScroll} v=${message.vScroll}"
            is ControlMessage.BackOrScreenOn -> "BACK_OR_SCREEN_ON ${if (message.action == AndroidInput.KEY_ACTION_DOWN) "down" else "up"}"
            ControlMessage.RotateDevice -> lock.withLock {
                width = height.also { height = width }
                newSession = true
                "ROTATE to ${width}x$height"
            }
            ControlMessage.ResetVideo -> {
                requestKeyFrame()
                "RESET_VIDEO"
            }
            else -> return false
        }
        if (line != null) {
            lock.withLock { lastInput = line }
            log(line)
        }
        return true
    }

    // A fresh encoder starts with an IDR frame, which is how a keyframe is forced.
    private fun newEncoder() = H264Encoder.createH264Encoder().apply { keyInterval = fps * KEY_INTERVAL_S }

    private fun encodeLoop(sink: BackendSink) {
        var encoder = newEncoder()
        var sentConfig: ByteArray? = null
        var sessionStartNs = 0L
        var frame = 0L
        var nextAt = System.nanoTime()
        var paused = true
        while (true) {
            val w: Int
            val h: Int
            val restart: Boolean
            val forced: Boolean
            lock.withLock {
                if (!wantVideo && !paused && !stopped) {
                    paused = true
                    sink.videoPaused()
                }
                while (!wantVideo && !stopped) changed.await()
                if (stopped) return
                paused = false
                w = width
                h = height
                restart = newSession
                forced = forceKey
                newSession = false
                forceKey = false
            }
            if (restart || forced) encoder = newEncoder()
            if (restart) {
                sentConfig = null
                sessionStartNs = System.nanoTime()
                frame = 0
                nextAt = sessionStartNs
                sink.video(StreamItem.Session(w, h, clientResize = false))
            }
            val picture = AWTUtil.fromBufferedImage(render(w, h, frame), ColorSpace.YUV420J)
            val encoded = encoder.encodeFrame(picture, ByteBuffer.allocate(encoder.estimateBufferSize(picture)))
            val data = encoded.data
            val (config, slices) = splitConfig(ByteArray(data.remaining()).also { data.get(it) })
            if (config != null && !config.contentEquals(sentConfig)) {
                sink.video(StreamItem.Packet(0, config = true, keyFrame = false, data = config))
                sentConfig = config
            }
            val ptsUs = (System.nanoTime() - sessionStartNs) / 1_000
            sink.video(StreamItem.Packet(ptsUs, config = false, keyFrame = encoded.isKeyFrame, data = slices))
            frame++
            nextAt += 1_000_000_000L / fps
            val sleepNs = nextAt - System.nanoTime()
            if (sleepNs > 0) Thread.sleep(sleepNs / 1_000_000, (sleepNs % 1_000_000).toInt()) else nextAt = System.nanoTime()
        }
    }

    private fun render(w: Int, h: Int, frame: Long): BufferedImage {
        val image = BufferedImage(w, h, BufferedImage.TYPE_3BYTE_BGR)
        val g = image.createGraphics()
        g.setRenderingHint(RenderingHints.KEY_ANTIALIASING, RenderingHints.VALUE_ANTIALIAS_ON)
        g.setRenderingHint(RenderingHints.KEY_TEXT_ANTIALIASING, RenderingHints.VALUE_TEXT_ANTIALIAS_ON)
        g.color = Color(0x10, 0x18, 0x24)
        g.fillRect(0, 0, w, h)
        val unit = minOf(w, h) / 30
        val small = Font(Font.MONOSPACED, Font.PLAIN, unit)
        val big = Font(Font.SANS_SERIF, Font.BOLD, unit * 2)

        g.font = small
        for (i in 1..9) {
            val x = w * i / 10
            val y = h * i / 10
            g.color = if (i == 5) Color(0x70, 0x80, 0x90) else Color(0x30, 0x3c, 0x48)
            g.drawLine(x, 0, x, h)
            g.drawLine(0, y, w, y)
            g.color = Color(0x90, 0xa0, 0xb0)
            g.drawString("${i * 10}%", x + 4, unit * 4)
            g.drawString("${i * 10}%", unit * 4, y - 4)
        }

        // A border and corner labels: any cropping on the viewer shows immediately.
        g.color = Color(0x3d, 0xdc, 0x84)
        g.stroke = BasicStroke(4f)
        g.drawRect(2, 2, w - 5, h - 5)
        g.font = big
        val metrics = g.fontMetrics
        g.drawString("TL", unit, unit + metrics.ascent)
        g.drawString("TR", w - unit - metrics.stringWidth("TR"), unit + metrics.ascent)
        g.drawString("BL", unit, h - unit - metrics.descent)
        g.drawString("BR", w - unit - metrics.stringWidth("BR"), h - unit - metrics.descent)

        g.color = Color.WHITE
        for ((row, text) in listOf("${w}x$h  frame $frame", LocalTime.now().format(CLOCK)).withIndex()) {
            g.drawString(text, (w - metrics.stringWidth(text)) / 2, h / 2 - unit * (5 - row * 3))
        }

        // One sweep per second: judders or stalls are visible at a glance.
        val sweepX = ((frame % fps) * w / fps).toInt()
        g.color = Color(0x4a, 0x9e, 0xff)
        g.fillRect(sweepX, h * 3 / 4, maxOf(4, w / fps), unit)

        val now = System.currentTimeMillis()
        val (recent, last) = lock.withLock {
            while (marks.isNotEmpty() && now - marks.first().atMs > MARK_LIFETIME_MS) marks.removeFirst()
            marks.toList() to lastInput
        }
        g.font = small
        g.color = Color(0xe0, 0xe0, 0xe0)
        g.drawString("last input: $last", w / 10 + unit / 2, h / 2 + unit * 3)
        g.stroke = BasicStroke(3f)
        for (m in recent) {
            val r = if (m.kind == 'M') unit / 3 else unit
            g.color = when (m.kind) {
                'D' -> Color(0x3d, 0xdc, 0x84)
                'U' -> Color(0xff, 0x5a, 0x5a)
                else -> Color(0xff, 0xd2, 0x4a)
            }
            if (m.kind == 'M') g.fillOval(m.x - r, m.y - r, r * 2, r * 2) else g.drawOval(m.x - r, m.y - r, r * 2, r * 2)
            if (m.kind == 'D') g.drawString("${m.x},${m.y}", m.x + r + 4, m.y - r)
        }
        g.dispose()
        return image
    }

    private companion object {
        const val ACTION_POINTER_DOWN = 5
        const val ACTION_POINTER_UP = 6
        const val KEY_INTERVAL_S = 2
        const val MAX_MARKS = 600
        const val MARK_LIFETIME_MS = 4_000L
        val CLOCK: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm:ss.SSS")

        /** Splits JCodec's Annex B output into SPS+PPS (sent as the config packet) and the slices. */
        fun splitConfig(annexB: ByteArray): Pair<ByteArray?, ByteArray> {
            val config = ByteArrayOutputStream()
            val slices = ByteArrayOutputStream()
            val starts = ArrayList<Int>()
            var i = 0
            while (i + 3 < annexB.size) {
                if (annexB[i].toInt() == 0 && annexB[i + 1].toInt() == 0 && annexB[i + 2].toInt() == 0 && annexB[i + 3].toInt() == 1) {
                    starts += i
                    i += 4
                } else {
                    i++
                }
            }
            for ((n, start) in starts.withIndex()) {
                val end = starts.getOrElse(n + 1) { annexB.size }
                val type = annexB[start + 4].toInt() and 0x1f
                (if (type == 7 || type == 8) config else slices).write(annexB, start, end - start)
            }
            return (if (config.size() > 0) config.toByteArray() else null) to slices.toByteArray()
        }
    }
}
