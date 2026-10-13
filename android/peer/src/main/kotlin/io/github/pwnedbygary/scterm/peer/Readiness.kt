package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import java.security.cert.CertificateException

/** Directional readiness: this controller viewing the target, never the reverse. */
data class Readiness(
    val tone: Tone,
    val label: String,
    val checkedAtMs: Long? = null,
    val elapsedAtMs: Long? = null,
    val result: PeerMessage.ProbeResult? = null,
    val address: TargetAddress? = null,
    val detail: String = "",
    val lastContactMs: Long? = null,
) {
    enum class Tone { GREEN, YELLOW, RED }

    fun fresh(nowElapsedMs: Long, intervalMs: Long): Readiness =
        if (elapsedAtMs != null && nowElapsedMs - elapsedAtMs >= 2 * intervalMs) {
            copy(tone = Tone.YELLOW, label = "Status stale—refresh")
        } else this

    companion object {
        fun checking(previous: Readiness? = null) = Readiness(
            Tone.YELLOW, "Checking…", previous?.checkedAtMs, previous?.elapsedAtMs,
            previous?.result, previous?.address, lastContactMs = previous?.lastContactMs,
        )

        fun classify(result: PeerMessage.ProbeResult): Readiness {
            val label = when {
                Grant.VIEW !in Grant.parse(result.grants) -> "Viewing permission missing"
                !result.ready -> result.reason.ifBlank { "Serving not ready" }
                result.caps?.video != true -> "Video unavailable"
                result.controlOccupied -> "Ready—another device has control"
                else -> "Ready"
            }
            val ready = result.ready && Grant.VIEW in Grant.parse(result.grants) && result.caps?.video == true
            return Readiness(if (ready) Tone.GREEN else Tone.YELLOW, label, result = result)
        }

        /** A pinned peer's rejection is more authoritative than a failed discovery hint. */
        fun preferFailure(previous: Readiness?, error: Exception): Readiness {
            val current = failure(error)
            return if (error is PeerException || previous?.label != "Identity mismatch") current else previous
        }

        fun failure(error: Exception): Readiness {
            val label = when {
                generateSequence<Throwable>(error) { it.cause }.any { it is CertificateException } -> "Identity mismatch"
                error is PeerException -> when (error.code) {
                    "bad_request", "unsupported", "version" -> "Status unavailable—update the peer"
                    "not_paired" -> "Pairing no longer accepted"
                    "forbidden" -> "Viewing permission missing"
                    else -> error.message ?: "Peer refused the check"
                }
                else -> "Not reachable"
            }
            val limited = label.startsWith("Status unavailable") || label == "Viewing permission missing"
            return Readiness(if (limited) Tone.YELLOW else Tone.RED, label,
                detail = if (label == "Not reachable") "The app may be closed, serving stopped, or the network unavailable. Check the other device and refresh." else label)
        }
    }
}

/** Main-thread ledger rejecting late results after refresh, network changes or forget. */
class ReadinessLedger {
    private var generation = 0L
    private val pending = mutableSetOf<String>()
    private val tickets = mutableMapOf<String, Long>()
    private val values = mutableMapOf<String, Readiness>()
    fun snapshot(): Map<String, Readiness> = values.toMap()
    fun begin(id: String): Long {
        val ticket = ++generation
        tickets[id] = ticket
        pending += id
        values[id] = Readiness.checking(values[id])
        return ticket
    }
    fun complete(id: String, ticket: Long, value: Readiness): Boolean {
        if (tickets[id] != ticket) return false
        values[id] = value.copy(lastContactMs = if (value.result != null) value.checkedAtMs else values[id]?.lastContactMs)
        pending -= id
        return true
    }
    /** Periodic polls preserve unfinished tickets, including work waiting in the executor. */
    fun schedule(ids: List<String>): List<Pair<String, Long>> = ids.filter { it !in pending }.map { it to begin(it) }
    fun invalidate() { tickets.clear(); pending.clear() }
    fun retain(ids: Set<String>) { tickets.keys.retainAll(ids); pending.retainAll(ids); values.keys.retainAll(ids) }
}
