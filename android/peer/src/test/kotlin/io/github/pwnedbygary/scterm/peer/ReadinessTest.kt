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
        assertEquals("Video unavailable", Readiness.classify(ready.copy(caps = null)).label)
    }
    @Test fun notReadyBadgeIsShortAndTheTargetsSentenceStaysAsDetail() {
        // What a helper target that is not running yet answers, word for word.
        val helper = "its full-control helper is not running. Start it on that device (Start with Shizuku) or from a computer with adb"
        val status = Readiness.classify(ready.copy(ready = false, reason = helper))
        assertEquals("Helper not running", status.label)
        assertEquals(Readiness.Tone.YELLOW, status.tone)
        assertTrue(status.detail.contains(helper, ignoreCase = true))
        val full = Readiness.classify(ready.copy(ready = false, reason = "too many sessions"))
        assertEquals("Too many viewers", full.label)
        assertTrue(full.detail.contains("too many sessions", ignoreCase = true))
        val other = Readiness.classify(ready.copy(ready = false, reason = "Helper needed"))
        assertEquals("Serving not ready", other.label)
        assertTrue(other.detail.contains("Helper needed"))
        assertEquals("Serving not ready", Readiness.classify(ready.copy(ready = false, reason = "")).label)
    }
    @Test fun failuresAndOlderPeersAreDistinct() {
        assertEquals(Readiness.Tone.RED, Readiness.failure(SocketTimeoutException()).tone)
        assertEquals("Not reachable", Readiness.failure(SocketTimeoutException()).label)
        assertEquals(Readiness.Tone.YELLOW, Readiness.failure(PeerException("bad_request", "expected hello")).tone)
        assertEquals("Pairing no longer accepted", Readiness.failure(PeerException("not_paired", "unknown")).label)
        val mismatch = SSLHandshakeException("TLS").apply { initCause(CertificateException("wrong identity")) }
        assertEquals("Identity mismatch", Readiness.failure(mismatch).label)
        val refused = Readiness.failure(PeerException("busy", "the target is restarting its capture"))
        assertEquals("Peer refused the check", refused.label)
        assertEquals(Readiness.Tone.RED, refused.tone)
        assertTrue(refused.detail.contains("the target is restarting its capture", ignoreCase = true))
    }
    @Test fun authenticatedSavedPeerRejectionOverridesMismatchedDiscoveryCandidate() {
        val mismatch = Readiness.failure(SSLHandshakeException("TLS").apply { initCause(CertificateException("candidate identity")) })
        assertEquals("Identity mismatch", mismatch.label)
        val revoked = Readiness.preferFailure(mismatch, PeerException("not_paired", "unknown"))
        assertEquals("Pairing no longer accepted", revoked.label)
        val older = Readiness.preferFailure(mismatch, PeerException("bad_request", "expected hello"))
        assertEquals(Readiness.Tone.YELLOW, older.tone)
        assertEquals("Status unavailable—update the peer", older.label)
        val forbidden = Readiness.preferFailure(mismatch, PeerException("forbidden", "denied"))
        assertEquals("Viewing permission missing", forbidden.label)
        assertEquals("Identity mismatch", Readiness.preferFailure(mismatch, SocketTimeoutException()).label)
    }

    @Test fun freshnessUsesControlledMonotonicTime() {
        val status = Readiness.classify(ready).copy(checkedAtMs = 100, elapsedAtMs = 1_000)
        assertEquals(Readiness.Tone.GREEN, status.fresh(30_999, 15_000).tone)
        assertEquals(Readiness.Tone.YELLOW, status.fresh(31_000, 15_000).tone)
        assertEquals(Readiness.Tone.GREEN, status.fresh(120_999, 60_000).tone)
        assertEquals(Readiness.Tone.YELLOW, status.fresh(121_000, 60_000).tone)
    }
    @Test fun failedCheckKeepsLatestSuccessfulContactInMemory() {
        val ledger = ReadinessLedger()
        val first = ledger.begin("RP6")
        ledger.complete("RP6", first, Readiness.classify(ready).copy(checkedAtMs = 1_000))
        val second = ledger.begin("RP6")
        ledger.complete("RP6", second, Readiness.failure(SocketTimeoutException()).copy(checkedAtMs = 2_000))
        assertEquals(1_000L, ledger.snapshot().getValue("RP6").lastContactMs)
        assertEquals(2_000L, ledger.snapshot().getValue("RP6").checkedAtMs)
    }
    @Test fun onlyANeverCheckedPeerShowsCheckingWhileARecheckKeepsTheLastResult() {
        val ledger = ReadinessLedger()
        val first = ledger.begin("RP6")
        assertEquals("Checking…", ledger.snapshot().getValue("RP6").label)
        assertTrue(ledger.complete("RP6", first, Readiness.classify(ready).copy(checkedAtMs = 1_000)))
        val second = ledger.begin("RP6")
        assertEquals("Ready", ledger.snapshot().getValue("RP6").label)
        assertEquals(1_000L, ledger.snapshot().getValue("RP6").checkedAtMs)
        assertTrue(ledger.complete("RP6", second, Readiness.failure(SocketTimeoutException()).copy(checkedAtMs = 2_000)))
        assertEquals("Not reachable", ledger.snapshot().getValue("RP6").label)
        ledger.invalidate() // a manual refresh
        ledger.schedule(listOf("RP6"))
        assertEquals("Not reachable", ledger.snapshot().getValue("RP6").label)
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
