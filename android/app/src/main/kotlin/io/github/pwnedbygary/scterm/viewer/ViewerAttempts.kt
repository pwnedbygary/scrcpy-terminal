package io.github.pwnedbygary.scterm.viewer

import java.util.concurrent.atomic.AtomicLong

/** A retry owns its callbacks/frames; results from a closed attempt are ignored. */
class ViewerAttempts {
    private val generation = AtomicLong()
    fun begin(): Long = generation.incrementAndGet()
    fun invalidate() { generation.incrementAndGet() }
    fun isCurrent(ticket: Long): Boolean = generation.get() == ticket
}

/** Retry is enabled only after every owned resource has completed teardown. */
object ViewerCleanup {
    fun release(closeConnection: () -> Unit, stopAudio: () -> Boolean, releaseDecoder: () -> Boolean): Boolean {
        val connectionClosed = runCatching(closeConnection).isSuccess
        val audioStopped = runCatching(stopAudio).getOrDefault(false)
        val decoderStopped = runCatching(releaseDecoder).getOrDefault(false)
        return connectionClosed && audioStopped && decoderStopped
    }
}
