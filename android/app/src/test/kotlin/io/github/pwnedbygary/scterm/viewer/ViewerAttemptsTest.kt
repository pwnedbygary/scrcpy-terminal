package io.github.pwnedbygary.scterm.viewer

import org.junit.Test
import org.junit.Assert.*

class ViewerAttemptsTest {
    @Test fun retryBarrierWaitsForDelayedAudioAndDecoder() {
        val audioEntered = java.util.concurrent.CountDownLatch(1)
        val decoderEntered = java.util.concurrent.CountDownLatch(1)
        val audioRelease = java.util.concurrent.CountDownLatch(1)
        val decoderRelease = java.util.concurrent.CountDownLatch(1)
        val connectionClosed = java.util.concurrent.atomic.AtomicBoolean(false)
        val worker = java.util.concurrent.Executors.newSingleThreadExecutor()
        try {
            val ready = worker.submit<Boolean> {
                ViewerCleanup.release({ connectionClosed.set(true) }, {
                    audioEntered.countDown(); audioRelease.await(); true
                }, {
                    decoderEntered.countDown(); decoderRelease.await(); true
                })
            }
            assertTrue(audioEntered.await(1, java.util.concurrent.TimeUnit.SECONDS))
            assertTrue(connectionClosed.get())
            assertFalse(ready.isDone)
            audioRelease.countDown()
            assertTrue(decoderEntered.await(1, java.util.concurrent.TimeUnit.SECONDS))
            assertFalse(ready.isDone)
            decoderRelease.countDown()
            assertTrue(ready.get(1, java.util.concurrent.TimeUnit.SECONDS))
        } finally { audioRelease.countDown(); decoderRelease.countDown(); worker.shutdownNow() }
    }

    @Test fun failedAudioCleanupStillReleasesDecoderAndDisablesRetry() {
        var decoderReleased = false
        assertFalse(ViewerCleanup.release({}, { error("audio teardown failed") }, { decoderReleased = true; true }))
        assertTrue(decoderReleased)
    }

    @Test fun retryRejectsOldDisconnectAndFrameCallbacks() {
        val attempts = ViewerAttempts()
        val first = attempts.begin()
        assertTrue(attempts.isCurrent(first))
        attempts.invalidate()
        assertFalse(attempts.isCurrent(first))
        val retry = attempts.begin()
        assertFalse(attempts.isCurrent(first))
        assertTrue(attempts.isCurrent(retry))
        attempts.invalidate() // Leaving closes all outstanding connection attempts.
        assertFalse(attempts.isCurrent(retry))
    }
}
