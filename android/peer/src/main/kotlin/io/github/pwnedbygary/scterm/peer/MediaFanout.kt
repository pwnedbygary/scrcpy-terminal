package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.MediaWire
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart
import java.io.BufferedOutputStream
import java.io.IOException
import java.io.OutputStream
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock

/**
 * Shares one encoded stream among viewers without letting any of them slow
 * the source down.
 *
 * Each item is encoded to wire bytes once and queued by reference to every
 * subscriber. A subscriber writes on its own thread; the producer never
 * blocks. H.264 packets depend on each other, so the JPEG-style "drop the old
 * frame" rule does not apply: a subscriber that falls behind its byte or age
 * budget is cut back to nothing, re-sent the current session and codec config,
 * and resumes at the next keyframe (one is requested on its behalf, globally
 * rate limited so one slow viewer cannot flood everyone with keyframes). New
 * viewers join the same way. Audio packets are independent, so an audio
 * subscriber sheds its oldest packets instead, keeping the codec config.
 */
class MediaFanout(
    private val video: Boolean,
    private val limits: Limits,
    private val requestKeyFrame: () -> Unit,
    private val clock: () -> Long = System::nanoTime,
) {
    data class Limits(
        val maxQueuedBytes: Int,
        val maxQueueAgeNanos: Long,
        val minKeyFrameIntervalNanos: Long = 1_000_000_000L,
    ) {
        companion object {
            val VIDEO = Limits(maxQueuedBytes = 8 shl 20, maxQueueAgeNanos = 250_000_000L)
            val AUDIO = Limits(maxQueuedBytes = 256 shl 10, maxQueueAgeNanos = 200_000_000L)
        }
    }

    interface Subscription {
        /** Media bytes dropped for this viewer (resyncs and audio shedding). */
        val droppedBytes: Long
        val resyncs: Int

        fun close()
    }

    private val lock = Any()
    private var header: ByteArray? = null
    private var ended = false
    private var session: ByteArray? = null
    private var config: ByteArray? = null
    private val subscribers = ArrayList<Subscriber>()
    private var lastKeyFrameRequest = Long.MIN_VALUE

    val subscriberCount: Int get() = synchronized(lock) { subscribers.size }

    fun onStart(value: StreamStart) {
        val wire = MediaWire.encodeStart(value)
        val stale: List<Subscriber>
        synchronized(lock) {
            // A stream carries one codec header: a restarted source needs new connections.
            stale = if (header != null) subscribers.toList() else emptyList()
            header = wire
            ended = value !is StreamStart.Enabled
            session = null
            config = null
            subscribers.filter { it !in stale }.forEach { it.begin(wire, ended) }
        }
        stale.forEach { it.close() }
    }

    fun onItem(item: StreamItem) {
        val wire = MediaWire.encodeItem(item)
        var askKeyFrame = false
        synchronized(lock) {
            when {
                item is StreamItem.Session -> {
                    session = wire
                    config = null
                    subscribers.forEach { it.offerSetup(wire) }
                }
                item is StreamItem.Packet && item.config -> {
                    config = wire
                    subscribers.forEach { it.offerSetup(wire) }
                }
                item is StreamItem.Packet -> {
                    subscribers.forEach { it.offerMedia(wire, item.keyFrame) }
                    // Viewers left waiting (they joined or resynced after the last
                    // keyframe) get another request once the interval allows.
                    askKeyFrame = video && subscribers.any { it.waitingForKeyFrame } && claimKeyFrameRequest()
                }
            }
        }
        if (askKeyFrame) requestKeyFrame()
    }

    /**
     * The source stopped encoding and will resume with a new session: a viewer
     * joining in between waits for it instead of receiving stale setup.
     */
    fun onPause() = synchronized(lock) {
        session = null
        config = null
    }

    /**
     * Streams to [out] on the calling thread until closed or the connection
     * fails. [abort] must tear the connection down without blocking (close the
     * raw TCP socket, not the TLS layer, which may try to write an alert).
     */
    fun serve(out: OutputStream, abort: () -> Unit, onReady: (Subscription) -> Unit = {}) {
        val sub = Subscriber(BufferedOutputStream(out, 64 * 1024), abort)
        val askKeyFrame: Boolean
        synchronized(lock) {
            subscribers += sub
            header?.let { sub.begin(it, ended) }
            session?.let { sub.offerSetup(it) }
            config?.let { sub.offerSetup(it) }
            askKeyFrame = video && header != null && claimKeyFrameRequest()
        }
        if (askKeyFrame) requestKeyFrame()
        onReady(sub)
        try {
            sub.pump()
        } finally {
            synchronized(lock) { subscribers -= sub }
        }
    }

    /** A viewer's explicit request (reset video); shares the global rate limit. */
    fun requestKeyFrameNow(): Boolean {
        val claimed = synchronized(lock) { video && claimKeyFrameRequest() }
        if (claimed) requestKeyFrame()
        return claimed
    }

    fun closeAll() {
        synchronized(lock) { subscribers.toList() }.forEach { it.close() }
    }

    /** Forgets the stream (its source stopped) and disconnects every viewer. */
    fun reset() {
        val all = synchronized(lock) {
            header = null
            ended = false
            session = null
            config = null
            subscribers.toList()
        }
        all.forEach { it.close() }
    }

    /** Caller holds [lock]. */
    private fun claimKeyFrameRequest(): Boolean {
        val now = clock()
        if (lastKeyFrameRequest != Long.MIN_VALUE && now - lastKeyFrameRequest < limits.minKeyFrameIntervalNanos) {
            return false
        }
        lastKeyFrameRequest = now
        return true
    }

    private inner class Subscriber(private val out: OutputStream, private val abort: () -> Unit) : Subscription {
        private inner class Entry(val bytes: ByteArray, val setup: Boolean, val enqueuedAt: Long)

        private val qlock = ReentrantLock()
        private val ready = qlock.newCondition()
        private val queue = ArrayDeque<Entry>()
        private var queuedBytes = 0
        private var header: ByteArray? = null
        private var headerSent = false
        private var endAfterHeader = false
        private var closed = false

        /** Video: nothing this subscriber receives is decodable before a keyframe. */
        @Volatile
        var waitingForKeyFrame = video
            private set

        override var droppedBytes = 0L
            private set
        override var resyncs = 0
            private set

        fun begin(startWire: ByteArray, endsStream: Boolean) = qlock.withLock {
            header = startWire
            endAfterHeader = endsStream
            ready.signal()
        }

        /** Session and config: tiny, rare, and required to decode anything after them. */
        fun offerSetup(wire: ByteArray) = qlock.withLock { enqueue(wire, setup = true) }

        fun offerMedia(wire: ByteArray, keyFrame: Boolean) {
            qlock.withLock {
                if (closed) return
                if (video) {
                    if (overBudget(wire.size)) resync()
                    if (waitingForKeyFrame) {
                        if (!keyFrame) {
                            droppedBytes += wire.size
                            return
                        }
                        waitingForKeyFrame = false
                    }
                } else {
                    while (overBudget(wire.size)) {
                        val victim = queue.firstOrNull { !it.setup } ?: break
                        queue.remove(victim)
                        queuedBytes -= victim.bytes.size
                        droppedBytes += victim.bytes.size
                    }
                }
                enqueue(wire, setup = false)
            }
        }

        /** Caller holds [qlock]. Drops everything queued and restarts from setup. */
        private fun resync() {
            resyncs++
            for (e in queue) droppedBytes += e.bytes.size
            queue.clear()
            queuedBytes = 0
            session?.let { enqueue(it, setup = true) }
            config?.let { enqueue(it, setup = true) }
            waitingForKeyFrame = true
        }

        private fun overBudget(incoming: Int): Boolean {
            val oldest = queue.firstOrNull() ?: return false
            return queuedBytes + incoming > limits.maxQueuedBytes || clock() - oldest.enqueuedAt > limits.maxQueueAgeNanos
        }

        private fun enqueue(wire: ByteArray, setup: Boolean) {
            if (closed) return
            queue.addLast(Entry(wire, setup, clock()))
            queuedBytes += wire.size
            ready.signal()
        }

        private fun hasWork(): Boolean = header != null && (!headerSent || queue.isNotEmpty() || endAfterHeader)

        fun pump() {
            val batch = ArrayList<ByteArray>()
            try {
                while (true) {
                    val finish: Boolean
                    qlock.withLock {
                        while (!closed && !hasWork()) ready.await()
                        if (closed) return
                        if (!headerSent) {
                            batch += header!!
                            headerSent = true
                        }
                        while (queue.isNotEmpty()) batch += queue.removeFirst().bytes
                        queuedBytes = 0
                        finish = endAfterHeader
                    }
                    for (b in batch) out.write(b)
                    out.flush()
                    batch.clear()
                    if (finish) return
                }
            } catch (e: IOException) {
                PeerLog.d("media subscriber ended: ${e.message}")
            } catch (e: InterruptedException) {
                Thread.currentThread().interrupt()
            } finally {
                close()
            }
        }

        override fun close() {
            qlock.withLock {
                if (closed) return
                closed = true
                queue.clear()
                queuedBytes = 0
                ready.signalAll()
            }
            abort()
        }
    }
}
