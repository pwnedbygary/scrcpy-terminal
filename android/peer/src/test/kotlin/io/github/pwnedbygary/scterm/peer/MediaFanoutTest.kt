package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Codec
import io.github.pwnedbygary.scterm.protocol.MediaStreamReader
import io.github.pwnedbygary.scterm.protocol.MediaWire
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.EOFException
import java.io.OutputStream
import java.util.concurrent.CountDownLatch
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import kotlin.concurrent.thread
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

class MediaFanoutTest {
    private val now = AtomicLong(0)
    private val keyFrames = AtomicInteger()

    /** Captures what one viewer receives; can be stalled to simulate a slow link. */
    private class Viewer : OutputStream() {
        private val data = ByteArrayOutputStream()

        @Volatile
        var stalled = false
        val firstWrite = CountDownLatch(1)

        override fun write(b: Int) = write(byteArrayOf(b.toByte()), 0, 1)

        override fun write(b: ByteArray, off: Int, len: Int) {
            firstWrite.countDown()
            while (stalled) Thread.sleep(2)
            synchronized(data) { data.write(b, off, len) }
        }

        /** What arrived so far; a packet still in flight is simply not listed yet. */
        fun items(): Pair<StreamStart?, List<StreamItem>> {
            val bytes = synchronized(data) { data.toByteArray() }
            val reader = MediaStreamReader(ByteArrayInputStream(bytes), MediaWire.MAX_VIDEO_PACKET)
            val start = try {
                reader.readStart()
            } catch (e: EOFException) {
                return null to emptyList()
            }
            val items = ArrayList<StreamItem>()
            while (true) {
                try {
                    items += reader.readItem()
                } catch (e: EOFException) {
                    return start to items
                }
            }
        }
    }

    private fun fanout(video: Boolean, maxBytes: Int = 10_000, maxAgeMs: Long = 400) = MediaFanout(
        video = video,
        limits = MediaFanout.Limits(maxQueuedBytes = maxBytes, maxQueueAgeNanos = maxAgeMs * 1_000_000),
        requestKeyFrame = { keyFrames.incrementAndGet() },
        clock = now::get,
    )

    private fun attach(f: MediaFanout, viewer: Viewer): MediaFanout.Subscription {
        val ready = CountDownLatch(1)
        var sub: MediaFanout.Subscription? = null
        thread(isDaemon = true) { f.serve(viewer, abort = {}) { sub = it; ready.countDown() } }
        ready.await()
        return sub!!
    }

    private fun packet(pts: Long, key: Boolean, size: Int = 1000) =
        StreamItem.Packet(pts, config = false, keyFrame = key, data = ByteArray(size) { pts.toByte() })

    private val session = StreamItem.Session(1080, 2400, false)
    private val config = StreamItem.Packet(0, config = true, keyFrame = false, data = byteArrayOf(0, 0, 0, 1, 0x67))

    private fun List<StreamItem>.media() = filterIsInstance<StreamItem.Packet>().filter { !it.config }

    @Test
    fun lateJoinerGetsSetupThenStartsAtAKeyframe() {
        val f = fanout(video = true)
        f.onStart(StreamStart.Enabled(Codec.H264))
        f.onItem(session)
        f.onItem(config)
        f.onItem(packet(1, key = true))
        f.onItem(packet(2, key = false))

        val viewer = Viewer()
        attach(f, viewer)
        assertEquals(1, keyFrames.get(), "joining asks for a keyframe")
        f.onItem(packet(3, key = false)) // undecodable for the newcomer: dropped
        f.onItem(packet(4, key = true))
        f.onItem(packet(5, key = false))
        // closeAll() aborts, discarding anything unwritten: wait for delivery first.
        eventually(message = "viewer received 5") { viewer.items().second.media().lastOrNull()?.ptsUs == 5L }
        f.closeAll()

        val (start, items) = viewer.items()
        assertEquals(StreamStart.Enabled(Codec.H264), start)
        assertEquals(session, items[0])
        assertTrue((items[1] as StreamItem.Packet).config)
        assertEquals(listOf(4L, 5L), items.media().map { it.ptsUs })
        assertTrue(items.media().first().keyFrame)
    }

    @Test
    fun aViewerJoiningAPausedSourceWaitsForTheNextSession() {
        val f = fanout(video = true)
        f.onStart(StreamStart.Enabled(Codec.H264))
        f.onItem(session)
        f.onItem(config)
        f.onItem(packet(1, key = true))
        f.onPause()

        val viewer = Viewer()
        attach(f, viewer)
        val rotated = StreamItem.Session(2400, 1080, false)
        f.onItem(rotated)
        f.onItem(config)
        f.onItem(packet(2, key = true))
        eventually(message = "viewer received 2") { viewer.items().second.media().lastOrNull()?.ptsUs == 2L }
        f.closeAll()

        val items = viewer.items().second
        assertEquals(rotated, items[0], "nothing stale precedes the new session")
        assertEquals(1, items.count { it is StreamItem.Session })
        assertEquals(listOf(2L), items.media().map { it.ptsUs })
    }

    @Test
    fun aStalledViewerIsResyncedAndNeverDelaysAHealthyOne() {
        val f = fanout(video = true, maxBytes = 10_000)
        f.onStart(StreamStart.Enabled(Codec.H264))
        f.onItem(session)
        f.onItem(config)

        val healthy = Viewer()
        val slow = Viewer().apply { stalled = true }
        attach(f, healthy)
        val slowSub = attach(f, slow)
        f.onItem(packet(1, key = true))
        slow.firstWrite.await() // its writer is now stuck inside write()

        // 30 KB of deltas, paced like an encoder (each one lands before the next
        // is produced): far past the stalled viewer's 10 KB budget, while the
        // healthy viewer keeps up.
        for (pts in 2L..31L) {
            f.onItem(packet(pts, key = false))
            eventually(message = "healthy viewer received $pts") { healthy.items().second.media().lastOrNull()?.ptsUs == pts }
        }
        assertEquals(1, slowSub.resyncs)
        now.addAndGet(1_000_000_000L) // allow another keyframe request
        f.onItem(packet(32, key = false))
        assertTrue(keyFrames.get() >= 2, "the resynced viewer asked for a keyframe")
        f.onItem(packet(33, key = true))
        f.onItem(packet(34, key = false))

        slow.stalled = false
        // closeAll() discards anything unwritten, so both viewers must have drained first.
        eventually(message = "both viewers drained") {
            slow.items().second.media().lastOrNull()?.ptsUs == 34L && healthy.items().second.media().lastOrNull()?.ptsUs == 34L
        }
        f.closeAll()

        assertEquals((1L..34L).toList(), healthy.items().second.media().map { it.ptsUs }, "healthy viewer lost nothing")

        val slowItems = slow.items().second
        // After the resync the stream restarts with setup and a keyframe: no
        // delta that depends on dropped frames can reach the decoder.
        val restart = slowItems.indexOfLast { it is StreamItem.Session }
        assertTrue(restart > 0, "resync re-sent the session")
        val resumed = slowItems.drop(restart + 1)
        assertTrue((resumed[0] as StreamItem.Packet).config)
        assertEquals(listOf(33L, 34L), resumed.media().map { it.ptsUs })
        assertTrue(resumed.media().first().keyFrame)
        assertTrue(slowSub.droppedBytes > 0)
    }

    @Test
    fun ageBudgetBoundsLatencyEvenForSmallPackets() {
        val f = fanout(video = true, maxBytes = 1 shl 20, maxAgeMs = 400)
        f.onStart(StreamStart.Enabled(Codec.H264))
        f.onItem(session)
        f.onItem(config)
        val slow = Viewer().apply { stalled = true }
        val sub = attach(f, slow)
        f.onItem(packet(1, key = true, size = 10))
        slow.firstWrite.await()
        f.onItem(packet(2, key = false, size = 10))
        now.addAndGet(500_000_000L) // the queued packet is now 500 ms old
        f.onItem(packet(3, key = false, size = 10))
        assertEquals(1, sub.resyncs)
        slow.stalled = false
        f.closeAll()
    }

    @Test
    fun keyFrameRequestsAreGloballyRateLimited() {
        val f = fanout(video = true)
        f.onStart(StreamStart.Enabled(Codec.H264))
        repeat(5) { attach(f, Viewer()) }
        assertEquals(1, keyFrames.get(), "five joins inside one interval cost one keyframe")
        assertEquals(false, f.requestKeyFrameNow())
        now.addAndGet(1_000_000_000L)
        assertEquals(true, f.requestKeyFrameNow())
        assertEquals(2, keyFrames.get())
        f.closeAll()
    }

    @Test
    fun audioShedsOldPacketsButKeepsTheCodecConfig() {
        val f = fanout(video = false, maxBytes = 3_000)
        f.onStart(StreamStart.Enabled(Codec.OPUS))
        val slow = Viewer().apply { stalled = true }
        val sub = attach(f, slow)
        f.onItem(StreamItem.Packet(0, config = true, keyFrame = false, data = ByteArray(19)))
        slow.firstWrite.await()
        for (pts in 1L..10L) f.onItem(packet(pts, key = false))
        assertTrue(sub.droppedBytes > 0)
        slow.stalled = false
        eventually(message = "audio drained") { slow.items().second.media().lastOrNull()?.ptsUs == 10L }
        f.closeAll()
        val items = slow.items().second
        assertTrue(items.filterIsInstance<StreamItem.Packet>().any { it.config }, "OpusHead survives shedding")
        assertEquals(0, keyFrames.get(), "audio never asks for keyframes")
    }

    @Test
    fun aDisabledStreamSendsItsCodeAndEnds() {
        val f = fanout(video = false)
        val viewer = Viewer()
        val done = CountDownLatch(1)
        thread(isDaemon = true) {
            f.serve(viewer, abort = {})
            done.countDown()
        }
        f.onStart(StreamStart.Disabled)
        done.await()
        assertIs<StreamStart.Disabled>(viewer.items().first)
    }

    @Test
    fun aRestartedSourceDisconnectsViewersOfTheOldStream() {
        val f = fanout(video = true)
        f.onStart(StreamStart.Enabled(Codec.H264))
        val aborted = CountDownLatch(1)
        thread(isDaemon = true) { f.serve(Viewer(), abort = { aborted.countDown() }) }
        eventually { f.subscriberCount == 1 }
        f.onStart(StreamStart.Enabled(Codec.H264))
        aborted.await()
        eventually { f.subscriberCount == 0 }
    }
}
