package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.AndroidInput
import io.github.pwnedbygary.scterm.protocol.Channel
import io.github.pwnedbygary.scterm.protocol.ClientInfo
import io.github.pwnedbygary.scterm.protocol.Codec
import io.github.pwnedbygary.scterm.protocol.ControlMessage
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.DeviceMessage
import io.github.pwnedbygary.scterm.protocol.ErrorCodes
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.Invitation
import io.github.pwnedbygary.scterm.protocol.LeaseState
import io.github.pwnedbygary.scterm.protocol.MediaStreamReader
import io.github.pwnedbygary.scterm.protocol.MediaWire
import io.github.pwnedbygary.scterm.protocol.PeerFrames
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import io.github.pwnedbygary.scterm.protocol.Position
import io.github.pwnedbygary.scterm.protocol.RejectCodes
import io.github.pwnedbygary.scterm.protocol.StreamItem
import io.github.pwnedbygary.scterm.protocol.StreamRequest
import io.github.pwnedbygary.scterm.protocol.StreamStart
import java.io.DataInputStream
import java.net.InetAddress
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLHandshakeException
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlin.test.fail

/** Real TLS over loopback: the same code paths a phone and a Go client use. */
class PeerSessionTest {
    private val store = InMemoryPeerStore()
    private val backend = FixtureBackend()
    private val ended = LinkedBlockingQueue<String>()
    private val server = TargetServer(
        identity = TestIdentities.target,
        store = store,
        device = DeviceInfo("Target", "Fixture", "test", 37),
        backends = { backend },
        config = TargetServer.Config(port = 0, bindAddress = InetAddress.getLoopbackAddress(), idleTimeoutMs = 4_000),
        events = object : TargetServer.Events {
            override fun onSessionEnded(session: TargetServer.SessionInfo, reason: String) {
                ended.add(reason)
            }
        },
    )
    private val port = server.start()

    @AfterTest
    fun tearDown() = server.stop()

    private fun client(identity: PeerIdentity = TestIdentities.controller, name: String = "Controller") =
        ControllerClient(identity, ClientInfo(name, "scterm-test/1"))

    private fun pair(grants: Set<Grant>, identity: PeerIdentity = TestIdentities.controller, name: String = "Controller"): ControllerClient {
        val c = client(identity, name)
        val result = c.pair(server.invite("127.0.0.1", grants), servePort = 27301)
        assertEquals(TestIdentities.target.fingerprint, result.fingerprint)
        assertEquals(grants, result.grants)
        return c
    }

    private fun ControllerClient.open(request: StreamRequest = StreamRequest()) =
        connect("127.0.0.1", port, TestIdentities.target.fingerprint, request)

    /** Collects what the target tells a controller. */
    private class Probe : ControllerSession.Listener {
        val envelopes = LinkedBlockingQueue<Any>()
        val closed = LinkedBlockingQueue<String>()

        override fun onDeviceMessage(message: DeviceMessage) {
            envelopes.add(message)
        }

        override fun onLease(state: LeaseState, holder: String?) {
            envelopes.add(PeerMessage.Lease(state, holder))
        }

        override fun onError(error: PeerMessage.Error) {
            envelopes.add(error)
        }

        override fun onClosed(reason: String) {
            closed.add(reason)
        }

        inline fun <reified T> next(timeoutMs: Long = 3_000): T {
            val deadline = System.nanoTime() + timeoutMs * 1_000_000
            while (true) {
                val left = (deadline - System.nanoTime()) / 1_000_000
                if (left <= 0) fail("no ${T::class.simpleName} within ${timeoutMs}ms")
                val item = envelopes.poll(left, TimeUnit.MILLISECONDS) ?: continue
                if (item is T) return item
            }
        }
    }

    private fun touch(action: Int, id: Long, x: Int) =
        ControlMessage.InjectTouch(action, id, Position(x, 10, 1080, 2400), if (action == AndroidInput.MOTION_ACTION_UP) 0 else 0xffff, 0, 0)

    // ---------------------------------------------------------------- pairing

    @Test
    fun pairingRecordsBothIdentitiesAndTheGrantedDirection() {
        pair(Grant.FULL)
        val record = assertNotNull(store.find(TestIdentities.controller.fingerprint))
        assertEquals("Controller", record.name)
        assertEquals(Grant.FULL, record.inboundGrants)
        assertEquals(TargetAddress("127.0.0.1", 27301), record.target, "reverse address for B-controls-A")
    }

    @Test
    fun aWrongCodeIsRejectedAndRepeatedGuessesKillTheInvitation() {
        val real = server.invite("127.0.0.1", Grant.FULL)
        val wrong = Invitation(real.host, real.port, ByteArray(10), real.fingerprint)
        repeat(5) {
            val e = assertFailsWith<PeerException> { client().pair(wrong) }
            assertEquals(RejectCodes.BAD_PROOF, e.code)
        }
        val e = assertFailsWith<PeerException> { client().pair(real) }
        assertEquals(RejectCodes.NO_INVITATION, e.code, "five failures invalidate even the right code")
        assertNull(store.find(TestIdentities.controller.fingerprint))
    }

    @Test
    fun invitationsAreSingleUse() {
        val inv = server.invite("127.0.0.1", Grant.VIEW_ONLY)
        client().pair(inv)
        val e = assertFailsWith<PeerException> { client(TestIdentities.stranger, "Stranger").pair(inv) }
        assertEquals(RejectCodes.NO_INVITATION, e.code)
        assertNull(store.find(TestIdentities.stranger.fingerprint))
    }

    @Test
    fun aRelayWithAnotherCertificateIsRefusedByThePin() {
        // The invitation pins the target; anything else presenting itself fails TLS.
        val inv = server.invite("127.0.0.1", Grant.FULL)
        val forged = Invitation(inv.host, inv.port, inv.secret, TestIdentities.stranger.fingerprint)
        assertFailsWith<SSLHandshakeException> { client().pair(forged) }
        assertNull(store.find(TestIdentities.controller.fingerprint))
    }

    // --------------------------------------------------------------- sessions

    @Test
    fun anUnpairedControllerIsRejected() {
        val e = assertFailsWith<PeerException> { client().open() }
        assertEquals(RejectCodes.NOT_PAIRED, e.code)
    }

    @Test
    fun aSessionCarriesScrcpyMediaAndOrderedInput() {
        val session = pair(Grant.FULL).open()
        assertEquals(LeaseState.HELD, session.lease)
        assertEquals(Grant.FULL, session.grants)
        val probe = Probe()
        session.start(probe)

        val video = MediaStreamReader(session.video!!, MediaWire.MAX_VIDEO_PACKET)
        // The codec header proves the target registered this viewer, so nothing emitted next is missed.
        within { assertEquals(StreamStart.Enabled(Codec.H264), video.readStart()) }
        backend.emitSetup()
        backend.emitFrame(1, key = true)
        backend.emitFrame(2, key = false)
        within { assertEquals(StreamItem.Session(1080, 2400, false), video.readItem()) }
        within { assertTrue((video.readItem() as StreamItem.Packet).config) }
        val key = within { video.readItem() as StreamItem.Packet }
        assertTrue(key.keyFrame)
        assertEquals(1, key.ptsUs)

        val audio = MediaStreamReader(session.audio!!, MediaWire.MAX_AUDIO_PACKET)
        within { assertEquals(StreamStart.Enabled(Codec.OPUS), audio.readStart()) }

        session.send(touch(AndroidInput.MOTION_ACTION_DOWN, 0, 100))
        session.send(touch(AndroidInput.MOTION_ACTION_UP, 0, 100))
        assertEquals(AndroidInput.MOTION_ACTION_DOWN, (backend.nextControl() as ControlMessage.InjectTouch).action)
        assertEquals(AndroidInput.MOTION_ACTION_UP, (backend.nextControl() as ControlMessage.InjectTouch).action)
        assertEquals(true to true, backend.demand)

        session.disconnect()
        assertEquals("controller left", ended.poll(3, TimeUnit.SECONDS))
        eventually(message = "demand cleared") { backend.demand == (false to false) }
    }

    @Test
    fun viewOnlyGrantsNeverReachTheBackend() {
        val session = pair(Grant.VIEW_ONLY).open()
        assertEquals(LeaseState.VIEWER, session.lease)
        val probe = Probe()
        session.start(probe)
        session.send(touch(AndroidInput.MOTION_ACTION_DOWN, 0, 1))
        val error = probe.next<PeerMessage.Error>()
        assertEquals(ErrorCodes.FORBIDDEN, error.code)
        assertEquals("inject_touch_event", error.controlType)
        backend.assertNoControl()

        val before = backend.keyFrameRequests.get()
        Thread.sleep(1_100) // past the join's keyframe request interval
        session.send(ControlMessage.ResetVideo)
        eventually(message = "keyframe request") { backend.keyFrameRequests.get() > before }
        backend.assertNoControl() // reset video is served by the fanout, never forwarded raw
        session.close()
    }

    @Test
    fun takeoverReleasesThePreviousHoldersInputFirst() {
        val a = pair(Grant.FULL, name = "A").open()
        val b = pair(Grant.FULL, TestIdentities.stranger, "B").open()
        assertEquals(LeaseState.HELD, a.lease)
        assertEquals(LeaseState.VIEWER, b.lease)
        val probeA = Probe().also(a::start)
        val probeB = Probe().also(b::start)

        a.send(touch(AndroidInput.MOTION_ACTION_DOWN, 5, 300)) // A's finger stays down
        assertEquals(AndroidInput.MOTION_ACTION_DOWN, (backend.nextControl() as ControlMessage.InjectTouch).action)

        b.send(touch(AndroidInput.MOTION_ACTION_DOWN, 1, 10))
        assertEquals(ErrorCodes.NO_LEASE, probeB.next<PeerMessage.Error>().code)
        backend.assertNoControl()

        b.takeover()
        assertEquals(LeaseState.HELD, probeB.next<PeerMessage.Lease>().state)
        val lost = probeA.next<PeerMessage.Lease>()
        assertEquals(LeaseState.VIEWER, lost.state)
        assertEquals("B", lost.holder)
        val release = backend.nextControl() as ControlMessage.InjectTouch
        assertEquals(AndroidInput.MOTION_ACTION_UP to 5L, release.action to release.pointerId, "A's press released")

        b.send(touch(AndroidInput.MOTION_ACTION_DOWN, 1, 10))
        assertEquals(1L, (backend.nextControl() as ControlMessage.InjectTouch).pointerId)
        a.send(touch(AndroidInput.MOTION_ACTION_MOVE, 5, 400))
        assertEquals(ErrorCodes.NO_LEASE, probeA.next<PeerMessage.Error>().code)
        a.close()
        b.close()
    }

    @Test
    fun aDroppedConnectionReleasesHeldInput() {
        val session = pair(Grant.FULL).open()
        session.start(Probe())
        session.send(touch(AndroidInput.MOTION_ACTION_DOWN, 3, 50))
        session.send(ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, 59, 0, AndroidInput.META_SHIFT_ON))
        backend.nextControl()
        backend.nextControl()
        session.close()
        val releases = listOf(backend.nextControl(), backend.nextControl())
        assertTrue(releases.any { it is ControlMessage.InjectTouch && it.action == AndroidInput.MOTION_ACTION_UP && it.pointerId == 3L })
        assertTrue(releases.any { it is ControlMessage.InjectKeycode && it.action == AndroidInput.KEY_ACTION_UP && it.keycode == 59 })
    }

    @Test
    fun continuousInputDoesNotStarveTheKeepalive() {
        val session = pair(Grant.FULL).open()
        session.start(Probe())
        // Input more often than the ping interval, for longer than it: pings
        // must still go out, or the target never answers and the controller's
        // read timeout ends a healthy session.
        val until = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(ControllerSession.PING_INTERVAL_MS + 1_500)
        session.send(touch(AndroidInput.MOTION_ACTION_DOWN, 4, 0))
        var x = 0
        while (System.nanoTime() < until && session.roundTripMs < 0) {
            session.send(touch(AndroidInput.MOTION_ACTION_MOVE, 4, ++x % 1080))
            Thread.sleep(50)
        }
        session.send(touch(AndroidInput.MOTION_ACTION_UP, 4, x % 1080))
        assertTrue(session.roundTripMs >= 0, "a pong arrived while input was flowing")
        session.close()
    }

    @Test
    fun revokingAPeerEndsItsLiveSession() {
        val session = pair(Grant.FULL).open()
        val probe = Probe()
        session.start(probe)
        session.send(touch(AndroidInput.MOTION_ACTION_DOWN, 2, 1))
        backend.nextControl()
        store.remove(TestIdentities.controller.fingerprint)
        assertEquals("access revoked", probe.closed.poll(3, TimeUnit.SECONDS))
        assertEquals(AndroidInput.MOTION_ACTION_UP, (backend.nextControl() as ControlMessage.InjectTouch).action)
        assertEquals(RejectCodes.NOT_PAIRED, assertFailsWith<PeerException> { client().open() }.code)
    }

    @Test
    fun unsupportedAndForbiddenMessagesAreAnsweredNotSwallowed() {
        backend.capabilities = backend.capabilities.copy(control = listOf("inject_touch_event"))
        val session = pair(Grant.FULL).open()
        val probe = Probe()
        session.start(probe)
        session.send(ControlMessage.RotateDevice)
        val unsupported = probe.next<PeerMessage.Error>()
        assertEquals(ErrorCodes.UNSUPPORTED to "rotate_device", unsupported.code to unsupported.controlType)
        session.send(ControlMessage.StartApp("com.example"))
        assertEquals(ErrorCodes.FORBIDDEN, probe.next<PeerMessage.Error>().code)
        backend.assertNoControl()
        session.close()
    }

    @Test
    fun aMessageTheBackendCannotPerformIsReportedAndNotTracked() {
        // e.g. a keycode with no accessibility equivalent on the public backend
        backend.refuse = { it is ControlMessage.InjectKeycode && it.keycode == 131 }
        val session = pair(Grant.FULL).open()
        val probe = Probe().also(session::start)
        session.send(ControlMessage.InjectKeycode(AndroidInput.KEY_ACTION_DOWN, 131, 0, 0))
        val error = probe.next<PeerMessage.Error>()
        assertEquals(ErrorCodes.UNSUPPORTED to "inject_keycode", error.code to error.controlType)
        session.close()
        // Never performed, so no synthetic release follows either.
        backend.assertNoControl()
    }

    @Test
    fun clipboardAcksReturnToTheControllerThatAsked() {
        val a = pair(Grant.FULL, name = "A").open()
        val b = pair(Grant.FULL, TestIdentities.stranger, "B").open()
        val probeA = Probe().also(a::start)
        val probeB = Probe().also(b::start)
        a.send(ControlMessage.SetClipboard(1, paste = false, text = "from a"))
        val first = backend.nextControl() as ControlMessage.SetClipboard
        b.takeover()
        probeB.next<PeerMessage.Lease>()
        b.send(ControlMessage.SetClipboard(1, paste = false, text = "from b"))
        val second = backend.nextControl() as ControlMessage.SetClipboard
        assertTrue(first.sequence != second.sequence, "controller sequences are remapped to be unique")

        backend.sink.device(DeviceMessage.AckClipboard(second.sequence))
        assertEquals(DeviceMessage.AckClipboard(1), probeB.next<DeviceMessage.AckClipboard>())
        backend.sink.device(DeviceMessage.AckClipboard(first.sequence))
        assertEquals(DeviceMessage.AckClipboard(1), probeA.next<DeviceMessage.AckClipboard>())

        backend.sink.device(DeviceMessage.Clipboard("device text"))
        assertEquals("device text", probeA.next<DeviceMessage.Clipboard>().text)
        assertEquals("device text", probeB.next<DeviceMessage.Clipboard>().text)
        a.close()
        b.close()
    }

    @Test
    fun deviceClipboardOnlyReachesClipboardGrants() {
        val viewer = pair(setOf(Grant.VIEW, Grant.CONTROL)).open()
        val probe = Probe().also(viewer::start)
        backend.sink.device(DeviceMessage.Clipboard("secret"))
        viewer.send(ControlMessage.InjectKeycode(0, 3, 0, 0))
        backend.nextControl() // the control path is live, so the clipboard was not merely delayed
        assertNull(probe.envelopes.poll(300, TimeUnit.MILLISECONDS)?.takeIf { it is DeviceMessage.Clipboard })
        viewer.close()
    }

    @Test
    fun aProtocolViolationEndsTheSessionAndReleasesInput() {
        val session = pair(Grant.FULL).open()
        session.start(Probe())
        session.send(touch(AndroidInput.MOTION_ACTION_DOWN, 4, 1))
        backend.nextControl()
        // A message type scrcpy never assigns is a protocol error, not something to skip.
        val raw = session.javaClass.getDeclaredField("control").apply { isAccessible = true }.get(session) as java.net.Socket
        raw.getOutputStream().apply {
            write(byteArrayOf(0x60))
            flush()
        }
        assertTrue(ended.poll(3, TimeUnit.SECONDS)!!.startsWith("protocol error"))
        assertEquals(AndroidInput.MOTION_ACTION_UP, (backend.nextControl() as ControlMessage.InjectTouch).action)
    }

    @Test
    fun mediaTokensAreBoundToTheControllersIdentity() {
        pair(Grant.FULL)
        pair(Grant.FULL, TestIdentities.stranger, "Stranger")
        val session = client().open(StreamRequest(video = false, audio = false))
        // The other paired device replays the session token on a video channel.
        val socket = PeerTls.connect(PeerTls.clientContext(TestIdentities.stranger, TestIdentities.target.fingerprint), "127.0.0.1", port, 3_000)
        socket.use {
            PeerFrames.write(it.outputStream, PeerMessage.Hello(channel = Channel.VIDEO, session = session.welcome.session, token = session.welcome.token))
            val reply = assertIs<PeerMessage.Reject>(PeerFrames.read(DataInputStream(it.inputStream)))
            assertEquals(RejectCodes.BAD_TOKEN, reply.code)
        }
        session.close()
    }

    @Test
    fun aTargetWithoutABackendSaysSo() {
        val idle = TargetServer(
            TestIdentities.target,
            store,
            DeviceInfo("Idle"),
            backends = { null },
            config = TargetServer.Config(port = 0, bindAddress = InetAddress.getLoopbackAddress()),
        )
        val idlePort = idle.start()
        try {
            val c = client()
            c.pair(idle.invite("127.0.0.1", Grant.FULL))
            val e = assertFailsWith<PeerException> { c.connect("127.0.0.1", idlePort, TestIdentities.target.fingerprint) }
            assertEquals(RejectCodes.UNAVAILABLE, e.code)
        } finally {
            idle.stop()
        }
    }

    @Test
    fun theTargetsLocalStopEndsEverySessionWithAReason() {
        val session = pair(Grant.FULL).open()
        val probe = Probe().also(session::start)
        server.endAllSessions("stopped on the device")
        assertEquals("stopped on the device", probe.closed.poll(3, TimeUnit.SECONDS))
    }
}
