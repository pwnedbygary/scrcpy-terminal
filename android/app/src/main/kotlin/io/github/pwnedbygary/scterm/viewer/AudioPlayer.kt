package io.github.pwnedbygary.scterm.viewer

import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioTrack
import android.media.MediaCodec
import android.media.MediaFormat
import io.github.pwnedbygary.scterm.protocol.Codec
import io.github.pwnedbygary.scterm.protocol.MediaStreamReader
import io.github.pwnedbygary.scterm.protocol.MediaWire
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart
import java.io.IOException
import java.io.InputStream
import java.nio.ByteBuffer
import kotlin.concurrent.thread

/**
 * Plays the target's audio stream: Opus/AAC through MediaCodec, raw PCM
 * directly, into a low-latency AudioTrack. At most [MAX_QUEUED_MS] of audio is
 * ever queued; beyond that, packets are skipped so a clock drift between the
 * two devices can never turn into growing delay.
 */
class AudioPlayer(private val input: InputStream, private val onStatus: (String) -> Unit) {
    /** Local playback volume only; the target's own volume is a separate action. */
    @Volatile
    var gain = 1f
        set(value) {
            field = value.coerceIn(0f, 1f)
            track?.setVolume(field)
        }

    @Volatile
    var skipped = 0L
        private set

    @Volatile
    private var track: AudioTrack? = null
    private var framesWritten = 0L

    fun start() {
        thread(name = "audio-player", isDaemon = true) { run() }
    }

    private fun run() {
        val reader = MediaStreamReader(input, MediaWire.MAX_AUDIO_PACKET)
        try {
            val codec = when (val start = reader.readStart()) {
                is StreamStart.Enabled -> start.codec
                StreamStart.Disabled -> return onStatus("No audio: capture is unavailable on the target")
                StreamStart.Failed -> return onStatus("No audio: the target reported an audio error")
            }
            val out = createTrack()
            track = out
            out.setVolume(gain)
            out.play()
            when (codec) {
                Codec.RAW -> while (true) {
                    val item = reader.readItem()
                    if (item is StreamItem.Packet && !item.config) write(out, item.data, item.data.size)
                }
                Codec.OPUS, Codec.AAC -> decode(reader, out, codec)
                else -> onStatus("No audio: ${codec.wireName} is not supported by this viewer")
            }
        } catch (_: IOException) {
            // The session ended.
        } catch (e: IllegalStateException) {
            onStatus("Audio stopped: ${e.message}")
        } finally {
            track?.release()
            track = null
        }
    }

    private fun createTrack(): AudioTrack {
        val minBuffer = AudioTrack.getMinBufferSize(SAMPLE_RATE, AudioFormat.CHANNEL_OUT_STEREO, AudioFormat.ENCODING_PCM_16BIT)
        return AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_MEDIA)
                    .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                    .build(),
            )
            .setAudioFormat(
                AudioFormat.Builder()
                    .setSampleRate(SAMPLE_RATE)
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                    .setChannelMask(AudioFormat.CHANNEL_OUT_STEREO)
                    .build(),
            )
            .setBufferSizeInBytes(maxOf(minBuffer * 2, SAMPLE_RATE * FRAME_BYTES * 40 / 1000))
            .setTransferMode(AudioTrack.MODE_STREAM)
            .setPerformanceMode(AudioTrack.PERFORMANCE_MODE_LOW_LATENCY)
            .build()
    }

    private fun decode(reader: MediaStreamReader, out: AudioTrack, codec: Codec) {
        // The stream opens with the codec config: OpusHead or an AudioSpecificConfig.
        var config: ByteArray? = null
        while (config == null) {
            val item = reader.readItem()
            if (item is StreamItem.Packet && item.config) config = item.data
        }
        val format = if (codec == Codec.OPUS) {
            MediaFormat.createAudioFormat(MediaFormat.MIMETYPE_AUDIO_OPUS, SAMPLE_RATE, 2).apply {
                setByteBuffer("csd-0", ByteBuffer.wrap(config))
                setByteBuffer("csd-1", littleEndian(opusPreSkipNanos(config)))
                setByteBuffer("csd-2", littleEndian(OPUS_SEEK_PREROLL_NS))
            }
        } else {
            MediaFormat.createAudioFormat(MediaFormat.MIMETYPE_AUDIO_AAC, SAMPLE_RATE, 2).apply {
                setByteBuffer("csd-0", ByteBuffer.wrap(config))
                setInteger(MediaFormat.KEY_IS_ADTS, 0)
            }
        }
        val decoder = MediaCodec.createDecoderByType(format.getString(MediaFormat.KEY_MIME)!!)
        decoder.configure(format, null, null, 0)
        decoder.start()
        val info = MediaCodec.BufferInfo()
        var pcm = ByteArray(8192)
        try {
            while (true) {
                val item = reader.readItem() as? StreamItem.Packet ?: continue
                if (item.config) continue
                val inIndex = decoder.dequeueInputBuffer(INPUT_TIMEOUT_US)
                if (inIndex >= 0) {
                    val buffer = decoder.getInputBuffer(inIndex) ?: continue
                    buffer.clear()
                    buffer.put(item.data)
                    decoder.queueInputBuffer(inIndex, 0, item.data.size, item.ptsUs, 0)
                } else {
                    skipped++
                }
                while (true) {
                    val outIndex = decoder.dequeueOutputBuffer(info, 0)
                    if (outIndex == MediaCodec.INFO_TRY_AGAIN_LATER) break
                    if (outIndex < 0) continue // format or buffer change
                    val buffer = decoder.getOutputBuffer(outIndex)
                    if (buffer != null && info.size > 0) {
                        if (pcm.size < info.size) pcm = ByteArray(info.size)
                        buffer.get(pcm, 0, info.size)
                        write(out, pcm, info.size)
                    }
                    decoder.releaseOutputBuffer(outIndex, false)
                }
            }
        } finally {
            try {
                decoder.stop()
            } catch (_: IllegalStateException) {
            }
            decoder.release()
        }
    }

    private fun write(out: AudioTrack, pcm: ByteArray, length: Int) {
        val played = out.playbackHeadPosition.toLong() and 0xffffffffL
        if (framesWritten - played > MAX_QUEUED_FRAMES) {
            skipped++
            return
        }
        val n = out.write(pcm, 0, length, AudioTrack.WRITE_BLOCKING)
        if (n > 0) framesWritten += n / FRAME_BYTES
    }

    private companion object {
        const val SAMPLE_RATE = 48_000
        const val FRAME_BYTES = 4 // s16 stereo
        const val MAX_QUEUED_MS = 80
        const val MAX_QUEUED_FRAMES = SAMPLE_RATE * MAX_QUEUED_MS / 1000
        const val INPUT_TIMEOUT_US = 20_000L
        const val OPUS_SEEK_PREROLL_NS = 80_000_000L

        /** OpusHead pre-skip (bytes 10-11, little endian, 48 kHz samples) in nanoseconds. */
        fun opusPreSkipNanos(head: ByteArray): Long {
            if (head.size < 12) return 0
            val samples = (head[10].toInt() and 0xff) or (head[11].toInt() and 0xff shl 8)
            return samples * 1_000_000_000L / SAMPLE_RATE
        }

        fun littleEndian(value: Long): ByteBuffer = ByteBuffer.wrap(ByteArray(8) { i -> (value ushr (8 * i)).toByte() })
    }
}
