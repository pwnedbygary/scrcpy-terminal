package io.github.pwnedbygary.scterm.viewer

import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaCodecList
import android.media.MediaFormat
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.Process
import android.util.Log
import android.view.Surface
import io.github.pwnedbygary.scterm.ScTermApp
import io.github.pwnedbygary.scterm.protocol.StreamItem
import java.util.concurrent.CountDownLatch

/** Which H.264 decoder was chosen, shown in the viewer's stats. */
data class DecoderChoice(val name: String, val hardware: Boolean, val lowLatency: Boolean) {
    companion object {
        /** Hardware first, then decoders that advertise the low-latency feature. */
        fun pick(mime: String): DecoderChoice? {
            val candidates = MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos.filter { info ->
                !info.isEncoder && info.supportedTypes.any { it.equals(mime, ignoreCase = true) } &&
                    !(Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q && info.isAlias)
            }
            return candidates
                .map { DecoderChoice(it.name, isHardware(it), supportsLowLatency(it, mime)) }
                .sortedWith(compareByDescending<DecoderChoice> { it.hardware }.thenByDescending { it.lowLatency })
                .firstOrNull()
        }

        private fun isHardware(info: MediaCodecInfo): Boolean =
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                info.isHardwareAccelerated
            } else {
                val name = info.name.lowercase()
                !name.startsWith("omx.google.") && !name.startsWith("c2.android.") && !name.contains(".sw.")
            }

        private fun supportsLowLatency(info: MediaCodecInfo, mime: String): Boolean =
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.R &&
                info.getCapabilitiesForType(mime).isFeatureSupported(MediaCodecInfo.CodecCapabilities.FEATURE_LowLatency)
    }
}

/**
 * H.264 straight to a Surface, in MediaCodec's async mode on a dedicated
 * thread. Every decoded frame is released for rendering immediately: there is
 * no presentation-time pacing to add delay, and the compositor keeps only the
 * newest buffer if frames arrive faster than the display refreshes.
 *
 * Packets for a codec that is not configured (no surface yet, geometry
 * change) are dropped until the next session/config/keyframe; the viewer asks
 * the target for a keyframe whenever that happens.
 */
class VideoDecoder(private val listener: Listener) {
    interface Listener {
        fun onVideoSize(width: Int, height: Int)
        fun onKeyFrameNeeded()
        fun onError(message: String)
    }

    val choice: DecoderChoice? = DecoderChoice.pick(MediaFormat.MIMETYPE_VIDEO_AVC)

    @Volatile
    var framesRendered = 0L
        private set

    @Volatile
    var packetsDropped = 0L
        private set

    private val thread = HandlerThread("video-decoder", Process.THREAD_PRIORITY_DISPLAY).apply { start() }
    private val handler = Handler(thread.looper)

    // Decoder-thread state.
    private var surface: Surface? = null
    private var codec: MediaCodec? = null
    private var width = 0
    private var height = 0
    private var config: ByteArray? = null
    private var needKeyFrame = true
    private val freeInputs = ArrayDeque<Int>()
    private val pending = ArrayDeque<StreamItem.Packet>()

    /** Called on the network thread for every item after the codec header. */
    fun offer(item: StreamItem) {
        handler.post { handle(item) }
    }

    /** The surface appeared (non-null) or is being destroyed (null). */
    fun setSurface(value: Surface?) {
        val done = CountDownLatch(1)
        handler.post {
            surface = value
            if (value == null) releaseCodec() else if (width > 0) configure()
            done.countDown()
        }
        // surfaceDestroyed must not return while the codec still renders into it.
        done.await()
    }

    fun release() {
        handler.post {
            releaseCodec()
            thread.quitSafely()
        }
    }

    private fun handle(item: StreamItem) {
        when (item) {
            is StreamItem.Session -> {
                if (item.width != width || item.height != height || codec == null) {
                    width = item.width
                    height = item.height
                    config = null
                    listener.onVideoSize(width, height)
                    configure()
                }
            }
            is StreamItem.Packet -> when {
                item.config -> {
                    config = item.data
                    enqueue(item)
                }
                codec == null -> packetsDropped++
                needKeyFrame && !item.keyFrame -> packetsDropped++
                else -> {
                    if (item.keyFrame) needKeyFrame = false
                    enqueue(item)
                }
            }
        }
    }

    private fun enqueue(packet: StreamItem.Packet) {
        if (codec == null) return
        if (pending.size >= MAX_PENDING) {
            // Decoding cannot keep up: skip ahead instead of letting latency grow.
            packetsDropped += pending.size.toLong()
            pending.clear()
            needKeyFrame = true
            listener.onKeyFrameNeeded()
            if (!packet.keyFrame && !packet.config) return
            needKeyFrame = !packet.keyFrame
        }
        pending.addLast(packet)
        pump()
    }

    private fun pump() {
        val c = codec ?: return
        try {
            while (freeInputs.isNotEmpty() && pending.isNotEmpty()) {
                val index = freeInputs.removeFirst()
                val packet = pending.removeFirst()
                val buffer = c.getInputBuffer(index) ?: continue
                if (packet.data.size > buffer.capacity()) {
                    freeInputs.addFirst(index)
                    packetsDropped++
                    needKeyFrame = true
                    listener.onKeyFrameNeeded()
                    continue
                }
                buffer.clear()
                buffer.put(packet.data)
                val flags = if (packet.config) MediaCodec.BUFFER_FLAG_CODEC_CONFIG else 0
                c.queueInputBuffer(index, 0, packet.data.size, packet.ptsUs, flags)
            }
        } catch (e: IllegalStateException) {
            recover("decoder rejected input: ${e.message}")
        }
    }

    private fun configure() {
        releaseCodec()
        val target = surface ?: return
        val pick = choice ?: return listener.onError("this device has no H.264 decoder")
        val format = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
            setInteger(MediaFormat.KEY_PRIORITY, 0)
            setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, maxOf(width * height, 1 shl 20))
            if (pick.lowLatency && Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
            // Vendor spellings of the same mode, for decoders that predate KEY_LOW_LATENCY.
            // Safe because scterm targets encode without B-frames; unknown keys are ignored.
            setInteger("vendor.qti-ext-dec-low-latency.enable", 1)
            setInteger("vendor.rtc-ext-dec-low-latency.enable", 1)
        }
        val created = try {
            MediaCodec.createByCodecName(pick.name)
        } catch (e: Exception) {
            return listener.onError("could not open ${pick.name}: ${e.message}")
        }
        try {
            created.setCallback(callback, handler)
            created.configure(format, target, null, 0)
            created.start()
        } catch (e: Exception) {
            created.release()
            return listener.onError("decoder setup failed: ${e.message}")
        }
        codec = created
        needKeyFrame = true
        config?.let { pending.addLast(StreamItem.Packet(0, config = true, keyFrame = false, data = it)) }
        listener.onKeyFrameNeeded()
    }

    private fun releaseCodec() {
        val c = codec ?: return
        codec = null
        freeInputs.clear()
        pending.clear()
        try {
            c.stop()
        } catch (_: IllegalStateException) {
        }
        c.release()
    }

    private fun recover(reason: String) {
        packetsDropped++
        Log.w(ScTermApp.TAG, reason)
        configure()
    }

    private val callback = object : MediaCodec.Callback() {
        override fun onInputBufferAvailable(c: MediaCodec, index: Int) {
            if (c !== codec) return
            freeInputs.addLast(index)
            pump()
        }

        override fun onOutputBufferAvailable(c: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
            if (c !== codec) return
            val render = info.size > 0
            c.releaseOutputBuffer(index, render)
            if (render) framesRendered++
        }

        override fun onError(c: MediaCodec, e: MediaCodec.CodecException) {
            if (c === codec) recover("decoder error ${e.diagnosticInfo}")
        }

        override fun onOutputFormatChanged(c: MediaCodec, format: MediaFormat) = Unit
    }

    private companion object {
        /** A hardware decoder keeps this near zero; a backlog this long means skip ahead. */
        const val MAX_PENDING = 8
    }
}
