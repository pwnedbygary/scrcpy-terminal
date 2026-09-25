package io.github.pwnedbygary.scterm.target

import io.github.pwnedbygary.scterm.peer.TargetBackend
import io.github.pwnedbygary.scterm.peer.TargetServer
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Invitation
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

enum class BackendKind {
    /** MediaProjection + Accessibility: works on a normal install, limited control. */
    PROJECTION,

    /** The vendored scrcpy server started with shell identity over ADB: full control. */
    HELPER,
}

/** An Android serving backend, observable by the UI. */
interface ServingBackend : TargetBackend {
    /** False while waiting on setup (the helper has not connected yet). */
    val ready: Boolean

    /** One line for the UI about what is working and what is not. */
    val status: String

    /** Set by the service; the backend calls it when [ready] or [status] change. */
    var onChanged: (() -> Unit)?
}

sealed interface ServeState {
    data object Stopped : ServeState

    data class Starting(val kind: BackendKind) : ServeState

    data class Serving(
        val kind: BackendKind,
        val port: Int,
        val ready: Boolean,
        val status: String,
        val sessions: List<TargetServer.SessionInfo>,
        /** HELPER only: the one-time ADB command that starts the helper. */
        val helperCommand: String? = null,
    ) : ServeState

    data class Failed(val message: String) : ServeState
}

/** Process-wide serving state, published by [TargetService] for the UI. */
object Serving {
    private val mutableState = MutableStateFlow<ServeState>(ServeState.Stopped)
    val state: StateFlow<ServeState> = mutableState

    @Volatile
    internal var server: TargetServer? = null

    internal fun publish(value: ServeState) {
        mutableState.value = value
    }

    /** A single-use invitation from the running server, or null when not serving. */
    fun invite(host: String, grants: Set<Grant>): Invitation? = server?.invite(host, grants)
}
