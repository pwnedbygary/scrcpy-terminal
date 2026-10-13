package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import java.net.SocketTimeoutException
import java.security.cert.CertificateException
import javax.net.ssl.SSLHandshakeException
import kotlin.test.*

class ReadinessTest {
    private val ready = PeerMessage.ProbeResult(1, DeviceInfo("RP6"), true, listOf("view", "control"), Capabilities("helper", true, true, listOf("touch")))

    @Test fun readinessRequiresViewAndAvailableVideoButNotControl() {
        assertEquals(Readiness.Tone.GREEN, Readiness.classify(ready.copy(grants = listOf("view"))).tone)
        assertEquals("Ready—another device has control", Readiness.classify(ready.copy(controlOccupied = true)).label)
        assertEquals(Readiness.Tone.YELLOW, Readiness.classify(ready.copy(grants = listOf("control"))).tone)
        assertEquals("Viewing permission missing", Readiness.classify(ready.copy(grants = emptyList())).label)
        assertEquals("Helper needed", Readiness.classify(ready.copy(ready = false, reason = "Helper needed")).label)
        assertEquals("Video unavailable", Readiness.classify(ready.copy(caps = null)).label)
    }
    @Test fun failuresAndOlderPeersAreDistinct() {
        assertEquals(Readiness.Tone.RED, Readiness.failure(SocketTimeoutException()).tone)
        assertEquals("Not reachable", Readiness.failure(SocketTimeoutException()).label)
        assertEquals(Readiness.Tone.YELLOW, Readiness.failure(PeerException("bad_request", "expected hello")).tone)
        assertEquals("Pairing no longer accepted", Readiness.failure(PeerException("not_paired", "unknown")).label)
        val mismatch = SSLHandshakeException("TLS").apply { initCause(CertificateException("wrong identity")) }
        assertEquals("Identity mismatch", Readiness.failure(mismatch).label)
    }
    @Test fun freshnessUsesControlledMonotonicTime() {
        val status = Readiness.classify(ready).copy(checkedAtMs = 100, elapsedAtMs = 1_000)
        assertEquals(Readiness.Tone.GREEN, status.fresh(30_999, 15_000).tone)
        assertEquals(Readiness.Tone.YELLOW, status.fresh(31_000, 15_000).tone)
        assertEquals(Readiness.Tone.GREEN, status.fresh(120_999, 60_000).tone)
        assertEquals(Readiness.Tone.YELLOW, status.fresh(121_000, 60_000).tone)
    }
    @Test fun periodicPollingPreservesEightSlowPeersWithTwoWorkers() {
        val ledger = ReadinessLedger()
        val ids = (1..8).map { "peer$it" }
        val queue = ArrayDeque(ledger.schedule(ids))
        val completed = mutableSetOf<String>()
        // Each pair finishes after five simulated seconds. At 15 seconds the
        // next poll must leave peers 7/8 ahead of newly queued peers 1..6.
        for (seconds in listOf(5, 10, 15, 20)) {
            if (seconds == 15) queue.addAll(ledger.schedule(ids))
            repeat(2) {
                val (id, ticket) = queue.removeFirst()
                assertTrue(ledger.complete(id, ticket, Readiness.classify(ready)))
                completed += id
            }
        }
        assertEquals(ids.toSet(), completed)
        // An explicit refresh still supersedes all unfinished checks.
        ledger.invalidate()
        val afterRefresh = ledger.schedule(ids)
        assertEquals(8, afterRefresh.size)
        queue.forEach { (id, old) -> assertFalse(ledger.complete(id, old, Readiness.classify(ready))) }
    }

    @Test fun networkRefreshAndForgetRejectLateOrReorderedResults() {
        val ledger = ReadinessLedger()
        val first = ledger.begin("RP6")
        val second = ledger.begin("RP6")
        assertFalse(ledger.complete("RP6", first, Readiness.classify(ready)))
        assertTrue(ledger.complete("RP6", second, Readiness.classify(ready)))
        ledger.invalidate() // network/lifecycle cancellation uses this production path
        assertFalse(ledger.complete("RP6", second, Readiness.failure(SocketTimeoutException())))
        val third = ledger.begin("RP6")
        ledger.retain(emptySet()) // forget
        assertFalse(ledger.complete("RP6", third, Readiness.classify(ready)))
        assertTrue(ledger.snapshot().isEmpty())
    }
}
