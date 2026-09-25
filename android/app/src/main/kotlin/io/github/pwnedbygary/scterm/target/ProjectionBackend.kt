package io.github.pwnedbygary.scterm.target

import android.content.Context
import android.graphics.PointF
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.media.projection.MediaProjection
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.HandlerThread
import android.os.Process
import android.util.DisplayMetrics
import android.view.Display
import android.view.Surface
import io.github.pwnedbygary.scterm.peer.BackendSink
import io.github.pwnedbygary.scterm.peer.PeerLog
import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.Codec
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.Position
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart

/**
 * Serves with public APIs only: MediaProjection feeds a hardware H.264
 * encoder (the vendored server's low-latency settings), and control goes
 * through [RemoteInputService] when the user has enabled it. No audio yet;
 * capabilities say exactly what works, so controllers never guess.
 */
class ProjectionBackend(context: Context, private val projection: MediaProjection) : ServingBackend {
    private class Geometry(val videoWidth: Int, val videoHeight: Int, val screenWidth: Int, val screenHeight: Int)

    private val displays = context.applicationContext.getSystemService(DisplayManager::class.java)
    private val thread = HandlerThread("projection-encoder", Process.THREAD_PRIORITY_DISPLAY).apply { start() }
    private val handler = Handler(thread.looper)

    /** Written on the encoder thread, read by session threads mapping touches. */
    @Volatile
    private var geometry: Geometry? = null

    // Encoder-thread state.
    private var sink: BackendSink? = null
    private var codec: MediaCodec? = null
    private var inputSurface: Surface? = null
    private var display: VirtualDisplay? = null
    private var wantVideo = false
    private var stopped = false

    override val ready = true
    override var onChanged: (() -> Unit)? = null

    @Volatile
    override var capabilities: Capabilities = capabilitiesNow()
        private set

    override val status: String
        get() = if (RemoteInputService.isConnected) {
            "Screen capture ready; remote input enabled"
        } else {
            "Screen capture ready; view only until the scterm accessibility service is enabled"
        }

    private val projectionCallback = object : MediaProjection.Callback() {
        override fun onStop() {
            handler.post { onProjectionStopped() }
        }
    }

    private val displayListener = object : DisplayManager.DisplayListener {
        override fun onDisplayAdded(displayId: Int) = Unit
        override fun onDisplayRemoved(displayId: Int) = Unit
        override fun onDisplayChanged(displayId: Int) {
            if (displayId == Display.DEFAULT_DISPLAY) handler.post { onScreenChanged() }
        }
    }

    private val inputAvailability: () -> Unit = { handler.post { refreshCapabilities() } }

    init {
        // Android 14+ refuses createVirtualDisplay without a registered callback.
        projection.registerCallback(projectionCallback, handler)
        displays.registerDisplayListener(displayListener, handler)
        RemoteInputService.addAvailabilityListener(inputAvailability)
    }

    override fun start(sink: BackendSink) {
        handler.post {
            this.sink = sink
            sink.videoStart(StreamStart.Enabled(Codec.H264))
            sink.audioStart(StreamStart.Disabled)
            if (wantVideo) startEncoder()
        }
    }

    override fun setDemand(video: Boolean, audio: Boolean) {
        handler.post {
            wantVideo = video
            if (video) {
                startEncoder()
            } else if (codec != null) {
                stopEncoder()
                sink?.videoPaused()
            }
        }
    }

    override fun requestKeyFrame() {
        handler.post {
            try {
                codec?.setParameters(Bundle().apply { putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0) })
            } catch (e: IllegalStateException) {
                PeerLog.w("keyframe request ignored: ${e.message}")
            }
        }
    }

    override fun sendControl(message: ControlMessage): Boolean {
        val service = RemoteInputService.instance ?: return false
        return service.perform(message, ::toScreen)
    }

    override fun stop() {
        handler.post {
            if (stopped) return@post
            stopped = true
            stopEncoder()
            display?.release()
            display = null
            projection.unregisterCallback(projectionCallback)
            projection.stop()
            displays.unregisterDisplayListener(displayListener)
            RemoteInputService.removeAvailabilityListener(inputAvailability)
            thread.quitSafely()
        }
    }

    /** Video coordinates to screen pixels; null for a touch aimed at an older geometry. */
    private fun toScreen(position: Position): PointF? {
        val g = geometry ?: return null
        if (position.screenWidth != g.videoWidth || position.screenHeight != g.videoHeight) return null
        return PointF(
            (position.x + 0.5f) * g.screenWidth / g.videoWidth,
            (position.y + 0.5f) * g.screenHeight / g.videoHeight,
        )
    }

    private fun startEncoder() {
        if (codec != null || stopped || sink == null) return
        val metrics = realMetrics()
        val encoder = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
        val caps = encoder.codecInfo.getCapabilitiesForType(MediaFormat.MIMETYPE_VIDEO_AVC).videoCapabilities
        val align = maxOf(2, caps?.widthAlignment ?: 2, caps?.heightAlignment ?: 2)
        for (maxSize in MAX_SIZES) {
            val (w, h) = fit(metrics.widthPixels, metrics.heightPixels, maxSize, align)
            try {
                encoder.setCallback(encoderCallback, handler)
                encoder.configure(format(w, h), null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
                val surface = encoder.createInputSurface()
                encoder.start()
                val vd = display
                if (vd == null) {
                    display = projection.createVirtualDisplay(
                        "scterm", w, h, metrics.densityDpi,
                        DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, surface, null, handler,
                    )
                } else {
                    vd.resize(w, h, metrics.densityDpi)
                    vd.surface = surface
                }
                codec = encoder
                inputSurface = surface
                geometry = Geometry(w, h, metrics.widthPixels, metrics.heightPixels)
                // Posted callbacks run after this returns, so the session precedes the config packet.
                sink?.video(StreamItem.Session(w, h, clientResize = false))
                PeerLog.i("screen encoder ${encoder.name} at ${w}x$h")
                return
            } catch (e: Exception) {
                PeerLog.w("encoder rejected ${w}x$h: ${e.message}")
                encoder.reset()
            }
        }
        encoder.release()
        sink?.stopped("the screen encoder refused every tried size")
    }

    private fun format(width: Int, height: Int) = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
        setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
        setInteger(MediaFormat.KEY_BIT_RATE, BIT_RATE)
        // Required to configure; the real rate follows screen changes.
        setInteger(MediaFormat.KEY_FRAME_RATE, 60)
        setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 10)
        // A static screen still yields a frame every 100 ms, so joins and resyncs never stall.
        setLong(MediaFormat.KEY_REPEAT_PREVIOUS_FRAME_AFTER, 100_000L)
        setInteger(MediaFormat.KEY_PRIORITY, 0)
        setInteger(MediaFormat.KEY_LATENCY, 1)
        // No reordering delay, and Qualcomm's low-latency mode; unknown keys are ignored.
        setInteger("max-bframes", 0)
        setInteger("vendor.qti-ext-enc-low-latency.enable", 1)
        setInteger(MediaFormat.KEY_COLOR_RANGE, MediaFormat.COLOR_RANGE_LIMITED)
    }

    private val encoderCallback = object : MediaCodec.Callback() {
        override fun onInputBufferAvailable(codec: MediaCodec, index: Int) = Unit

        override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
            if (codec !== this@ProjectionBackend.codec) return
            try {
                if (info.size > 0 && info.flags and MediaCodec.BUFFER_FLAG_END_OF_STREAM == 0) {
                    // getOutputBuffer() positions the buffer on exactly the valid bytes.
                    val buffer = codec.getOutputBuffer(index) ?: return
                    val data = ByteArray(buffer.remaining()).also { buffer.get(it) }
                    val config = info.flags and MediaCodec.BUFFER_FLAG_CODEC_CONFIG != 0
                    val key = info.flags and MediaCodec.BUFFER_FLAG_KEY_FRAME != 0
                    sink?.video(StreamItem.Packet(if (config) 0 else info.presentationTimeUs, config, key && !config, data))
                }
            } finally {
                codec.releaseOutputBuffer(index, false)
            }
        }

        override fun onError(codec: MediaCodec, e: MediaCodec.CodecException) {
            if (codec !== this@ProjectionBackend.codec) return
            PeerLog.e("screen encoder failed", e)
            stopEncoder()
            if (wantVideo) startEncoder()
        }

        override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) = Unit
    }

    private fun stopEncoder() {
        val c = codec ?: return
        codec = null
        display?.surface = null
        try {
            c.stop()
        } catch (_: IllegalStateException) {
        }
        c.release()
        inputSurface?.release()
        inputSurface = null
    }

    private fun onScreenChanged() {
        val g = geometry ?: return
        val m = realMetrics()
        if (codec != null && (m.widthPixels != g.screenWidth || m.heightPixels != g.screenHeight)) {
            // Rotation or resolution change: a new session with fresh config and keyframe.
            stopEncoder()
            startEncoder()
        }
    }

    private fun onProjectionStopped() {
        if (stopped) return
        stopEncoder()
        sink?.stopped("screen capture was stopped on this device")
    }

    private fun refreshCapabilities() {
        capabilities = capabilitiesNow()
        sink?.capabilitiesChanged(capabilities)
        onChanged?.invoke()
    }

    private fun realMetrics(): DisplayMetrics {
        val metrics = DisplayMetrics()
        @Suppress("DEPRECATION")
        displays.getDisplay(Display.DEFAULT_DISPLAY).getRealMetrics(metrics)
        return metrics
    }

    private companion object {
        const val BIT_RATE = 8_000_000
        val MAX_SIZES = intArrayOf(1920, 1600, 1280, 1024)

        fun fit(width: Int, height: Int, maxSize: Int, align: Int): Pair<Int, Int> {
            val scale = minOf(1f, maxSize.toFloat() / maxOf(width, height))
            fun round(v: Int) = maxOf(align, (v * scale).toInt() / align * align)
            return round(width) to round(height)
        }

        fun capabilitiesNow(): Capabilities {
            val input = RemoteInputService.isConnected
            val control = if (!input) {
                emptyList()
            } else {
                buildList {
                    add("inject_touch_event")
                    add("inject_scroll_event")
                    add("inject_keycode")
                    add("inject_text")
                    add("back_or_screen_on")
                    add("expand_notification_panel")
                    add("expand_settings_panel")
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) add("collapse_panels")
                }
            }
            return Capabilities(
                backend = "projection",
                video = true,
                audio = false,
                control = control,
                multitouch = false,
                clipboard = false,
                maxSessions = 4,
                notes = buildList {
                    add("Standard install: no audio, single-finger gestures.")
                    if (input) {
                        add("Keys: home, back, recents, power (locks), volume, typing; D-pad on Android 13+.")
                    } else {
                        add("View only: remote input needs the scterm accessibility service on the serving device.")
                    }
                },
            )
        }
    }
}
