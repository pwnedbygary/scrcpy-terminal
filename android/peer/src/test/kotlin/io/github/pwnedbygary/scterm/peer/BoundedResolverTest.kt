package io.github.pwnedbygary.scterm.peer

import java.net.InetAddress
import java.net.SocketTimeoutException
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.*

class BoundedResolverTest {
    @Test fun numericAddressesBypassDnsWorkers() {
        val resolver = BoundedResolver { error("numeric endpoint must not use lookup worker") }
        assertEquals("127.0.0.1", resolver.resolve("127.0.0.1", 100).hostAddress)
        assertTrue(resolver.resolve("::1", 100).isLoopbackAddress)
    }

    @Test fun blockedLookupIsBoundedEvenIfDnsIgnoresInterrupts() {
        val release = CountDownLatch(1)
        val resolver = BoundedResolver {
            while (release.count > 0) {
                try { release.await() } catch (_: InterruptedException) { }
            }
            InetAddress.getLoopbackAddress()
        }
        val start = System.nanoTime()
        try {
            assertFailsWith<SocketTimeoutException> { resolver.resolve("blocked.example", 100) }
            assertTrue(TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start) < 1_000)
        } finally { release.countDown() }
    }
    @Test fun cancellationInterruptsCallerWaitingForLookup() {
        val entered = CountDownLatch(1)
        val release = CountDownLatch(1)
        val returned = CountDownLatch(1)
        val resolver = BoundedResolver { entered.countDown(); release.await(); InetAddress.getLoopbackAddress() }
        val caller = Executors.newSingleThreadExecutor()
        try {
            val task = caller.submit { try { resolver.resolve("blocked.example", 5_000) } catch (_: InterruptedException) { } finally { returned.countDown() } }
            assertTrue(entered.await(1, TimeUnit.SECONDS))
            task.cancel(true)
            assertTrue(returned.await(1, TimeUnit.SECONDS))
        } finally { release.countDown(); caller.shutdownNow() }
    }
}
