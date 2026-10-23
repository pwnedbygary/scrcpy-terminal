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
    /** What a probe found; logic tests this, never [label]'s wording. Null for statuses set without one. */
    val state: State? = null,
) {
    enum class Tone { GREEN, YELLOW, RED }

    /** A probe's outcome and its badge. */
    enum class State(val tone: Tone, val label: String) {
        READY(Tone.GREEN, "Ready"),
        CONTROL_TAKEN(Tone.GREEN, "Ready—another device has control"),
        VIEW_NOT_GRANTED(Tone.YELLOW, "Viewing permission missing"),
        HELPER_NOT_RUNNING(Tone.YELLOW, "Helper not running"),
        TOO_MANY_SESSIONS(Tone.YELLOW, "Too many viewers"),
        NOT_READY(Tone.YELLOW, "Serving not ready"),
        NO_VIDEO(Tone.YELLOW, "Video unavailable"),
        OLDER_PEER(Tone.YELLOW, "Status unavailable—update the peer"),
        IDENTITY_MISMATCH(Tone.RED, "Identity mismatch"),
        NOT_PAIRED(Tone.RED, "Pairing no longer accepted"),
        REFUSED(Tone.RED, "Peer refused the check"),
        UNREACHABLE(Tone.RED, "Not reachable"),
    }

    fun fresh(nowElapsedMs: Long, intervalMs: Long): Readiness =
        if (elapsedAtMs != null && nowElapsedMs - elapsedAtMs >= 2 * intervalMs) {
            copy(tone = Tone.YELLOW, label = "Status stale—refresh")
        } else this

    companion object {
        /**
         * Not-ready reasons this app's targets send. The Go client and older
         * apps show a reason as it is, so these stay whole sentences on the
         * wire; [classify] gives them short labels.
         */
        const val HELPER_NOT_RUNNING_REASON =
            "its full-control helper is not running. Start it on that device (Start with Shizuku) or from a computer with adb"
        const val TOO_MANY_SESSIONS_REASON = "too many sessions"

        fun checking() = Readiness(Tone.YELLOW, "Checking…")

        fun classify(result: PeerMessage.ProbeResult): Readiness {
            val state = when {
                Grant.VIEW !in Grant.parse(result.grants) -> State.VIEW_NOT_GRANTED
                !result.ready -> when (result.reason) {
                    HELPER_NOT_RUNNING_REASON -> State.HELPER_NOT_RUNNING
                    TOO_MANY_SESSIONS_REASON -> State.TOO_MANY_SESSIONS
                    else -> State.NOT_READY
                }
                result.caps?.video != true -> State.NO_VIDEO
                result.controlOccupied -> State.CONTROL_TAKEN
                else -> State.READY
            }
            val detail = when (state) {
                State.HELPER_NOT_RUNNING, State.TOO_MANY_SESSIONS, State.NOT_READY -> sentence(result.reason)
                else -> ""
            }
            return Readiness(state.tone, state.label, result = result, detail = detail, state = state)
        }

        /** A pinned peer's rejection is more authoritative than a failed discovery hint. */
        fun preferFailure(previous: Readiness?, error: Exception): Readiness {
            val current = failure(error)
            return if (error is PeerException || previous?.state != State.IDENTITY_MISMATCH) current else previous
        }

        fun failure(error: Exception): Readiness {
            val state = when {
                generateSequence<Throwable>(error) { it.cause }.any { it is CertificateException } -> State.IDENTITY_MISMATCH
                error is PeerException -> when (error.code) {
                    "bad_request", "unsupported", "version" -> State.OLDER_PEER
                    "not_paired" -> State.NOT_PAIRED
                    "forbidden" -> State.VIEW_NOT_GRANTED
                    else -> State.REFUSED
                }
                else -> State.UNREACHABLE
            }
            val detail = when (state) {
                State.UNREACHABLE -> "The app may be closed, serving stopped, or the network unavailable. Check the other device and refresh."
                State.REFUSED -> sentence(error.message.orEmpty()).ifEmpty { state.label }
                else -> state.label
            }
            return Readiness(state.tone, state.label, detail = detail, state = state)
        }

        /** A peer's reason, worded to follow "<device> isn't ready: ", as a sentence of its own. */
        private fun sentence(reason: String): String {
            val text = reason.trim().replaceFirstChar(Char::uppercaseChar)
            return if (text.isEmpty() || text.endsWith('.')) text else "$text."
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
        // A re-check keeps showing the last result until the new one arrives.
        if (id !in values) values[id] = Readiness.checking()
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
