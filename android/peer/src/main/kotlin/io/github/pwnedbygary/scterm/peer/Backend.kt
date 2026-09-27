package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceMessage
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart

/**
 * One way of serving this device: the ADB-activated scrcpy helper (full
 * control), MediaProjection + Accessibility (ordinary install, limited), or a
 * test fixture. Every backend speaks scrcpy v4.1 media and control semantics,
 * so sessions, fanout and policy never depend on which one is running.
 */
interface TargetBackend {
    /** Current abilities; changes are announced through [BackendSink.capabilitiesChanged]. */
    val capabilities: Capabilities

    /** Called once. The backend pushes media and device messages from its own threads. */
    fun start(sink: BackendSink)

    /**
     * Executes one already-authorized message. Calls are serialized by the
     * caller and must return quickly: never block on the capture pipeline.
     * Returns false when this particular message cannot be performed (a key
     * with no accessibility equivalent, say), so the sender is told instead of
     * being led to believe it worked.
     */
    fun sendControl(message: ControlMessage): Boolean

    /** Emit a decodable frame (config + keyframe) soon; calls are rate limited upstream. */
    fun requestKeyFrame()

    /** Whether anyone is watching, so capture can pause instead of burning battery. */
    fun setDemand(video: Boolean, audio: Boolean) = Unit

    fun stop()
}

interface BackendSink {
    fun videoStart(start: StreamStart)

    fun video(item: StreamItem)

    /** Video encoding paused for lack of viewers; it resumes with a new session. */
    fun videoPaused()

    fun audioStart(start: StreamStart)

    fun audio(item: StreamItem)

    fun device(message: DeviceMessage)

    fun capabilitiesChanged(capabilities: Capabilities)

    /** The backend ended by itself (capture revoked, helper died). */
    fun stopped(error: String?)
}
