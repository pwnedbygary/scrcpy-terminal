package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Capabilities
import io.github.pwnedbygary.scterm.protocol.Codec
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamStart
import java.security.KeyStore
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.fail

/** Throwaway EC identities generated with keytool for tests only. */
object TestIdentities {
    private fun load(name: String): StaticIdentity {
        val ks = KeyStore.getInstance("PKCS12")
        TestIdentities::class.java.getResourceAsStream("/test-identities/$name.p12")!!.use { ks.load(it, "testpass".toCharArray()) }
        return StaticIdentity.fromKeyStore(ks, "peer", "testpass".toCharArray())
    }

    val target by lazy { load("target") }
    val controller by lazy { load("controller") }
    val stranger by lazy { load("stranger") }
}

val ALL_CONTROL: List<String> = (0..22).map(ControlMessage::typeName)

/** A backend driven by the test: emits media on demand and records control. */
class FixtureBackend(caps: Capabilities = Capabilities("fixture", video = true, audio = true, control = ALL_CONTROL, multitouch = true)) :
    TargetBackend {
    @Volatile
    override var capabilities: Capabilities = caps

    val controls = LinkedBlockingQueue<ControlMessage>()
    val keyFrameRequests = AtomicInteger()

    /** Messages this backend pretends it cannot perform. */
    @Volatile
    var refuse: (ControlMessage) -> Boolean = { false }

    @Volatile
    var demand: Pair<Boolean, Boolean> = false to false

    @Volatile
    lateinit var sink: BackendSink

    override fun start(sink: BackendSink) {
        this.sink = sink
        sink.videoStart(StreamStart.Enabled(Codec.H264))
        sink.audioStart(StreamStart.Enabled(Codec.OPUS))
    }

    override fun sendControl(message: ControlMessage): Boolean {
        if (refuse(message)) return false
        controls.add(message)
        return true
    }

    override fun requestKeyFrame() {
        keyFrameRequests.incrementAndGet()
    }

    override fun setDemand(video: Boolean, audio: Boolean) {
        demand = video to audio
    }

    override fun stop() = Unit

    fun emitSetup(width: Int = 1080, height: Int = 2400) {
        sink.video(StreamItem.Session(width, height, false))
        sink.video(StreamItem.Packet(0, config = true, keyFrame = false, data = byteArrayOf(0, 0, 0, 1, 0x67)))
    }

    fun emitFrame(pts: Long, key: Boolean, size: Int = 100) =
        sink.video(StreamItem.Packet(pts, config = false, keyFrame = key, data = ByteArray(size) { pts.toByte() }))

    fun nextControl(timeoutMs: Long = 3_000): ControlMessage =
        controls.poll(timeoutMs, TimeUnit.MILLISECONDS) ?: fail("no control message within ${timeoutMs}ms")

    fun assertNoControl(waitMs: Long = 300) {
        controls.poll(waitMs, TimeUnit.MILLISECONDS)?.let { fail("unexpected control message $it") }
    }
}

private val executor = Executors.newCachedThreadPool { r -> Thread(r, "test-io").apply { isDaemon = true } }

/** Runs a blocking read with a deadline, so a protocol bug fails instead of hanging. */
fun <T> within(timeoutMs: Long = 3_000, block: () -> T): T = executor.submit(Callable(block)).get(timeoutMs, TimeUnit.MILLISECONDS)

fun eventually(timeoutMs: Long = 3_000, message: String = "condition", condition: () -> Boolean) {
    val deadline = System.nanoTime() + timeoutMs * 1_000_000
    while (!condition()) {
        if (System.nanoTime() > deadline) fail("timed out waiting for $message")
        Thread.sleep(10)
    }
}
