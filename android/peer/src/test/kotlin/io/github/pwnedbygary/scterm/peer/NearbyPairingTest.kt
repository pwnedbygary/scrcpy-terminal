package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.ClientInfo
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.PeerFrames
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import io.github.pwnedbygary.scterm.protocol.ProtocolException
import io.github.pwnedbygary.scterm.protocol.RejectCodes
import io.github.pwnedbygary.scterm.protocol.ShortCode
import java.io.DataInputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.util.concurrent.CancellationException
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.ExecutionException
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertSame
import kotlin.test.assertTrue
import kotlin.test.fail

/** Pairing by comparing a six-digit code on both screens, over real TLS on loopback. */
class NearbyPairingTest {
    private val store = InMemoryPeerStore()
    private val requests = LinkedBlockingQueue<TargetServer.PairingRequest>()
    private val finished = LinkedBlockingQueue<TargetServer.PairingRequest>()
    private val rejections = LinkedBlockingQueue<String>()
    private val invitationChanges = CopyOnWriteArrayList<Long?>()
    private val servers = mutableListOf<TargetServer>()
    private val fakes = mutableListOf<ServerSocket>()

    private fun serve(answerMs: Long = 60_000): TargetServer {
        val server = TargetServer(
            identity = TestIdentities.target,
            store = store,
            device = DeviceInfo("Target", "Fixture", "test", 37),
            backends = { FixtureBackend() },
            config = TargetServer.Config(port = 0, bindAddress = InetAddress.getLoopbackAddress(), nearbyAnswerMs = answerMs),
            events = object : TargetServer.Events {
                override fun onPairingRequest(request: TargetServer.PairingRequest) {
                    requests.add(request)
                }

                override fun onPairingRequestEnded(request: TargetServer.PairingRequest) {
                    finished.add(request)
                }

                override fun onPairingRejected(reason: String) {
                    rejections.add(reason)
                }

                override fun onInvitationChanged(expiresAtMs: Long?) {
                    invitationChanges.add(expiresAtMs)
                }
            },
        )
        server.start()
        servers += server
        return server
    }

    @AfterTest
    fun tearDown() {
        servers.forEach { it.stop() }
        fakes.forEach { it.closeQuietly() }
    }

    private fun startPairing(port: Int, identity: PeerIdentity = TestIdentities.controller, name: String = "Controller") =
        ControllerClient(identity, ClientInfo(name, "scterm-test/1")).startNearbyPairing("127.0.0.1", port, servePort = 27301)

    private fun nextRequest(): TargetServer.PairingRequest = requests.poll(3, TimeUnit.SECONDS) ?: fail("no pairing request on the target")

    private fun <T> CompletableFuture<T>.failure(): Throwable = assertFailsWith<ExecutionException> { get(3, TimeUnit.SECONDS) }.cause!!

    @Test
    fun bothUsersAgreeingPairs() {
        val server = serve()
        server.invite("127.0.0.1", Grant.VIEW_ONLY)
        val pairing = startPairing(server.port)
        val request = nextRequest()
        assertEquals(pairing.code, request.code, "both screens show the same code")
        assertTrue(pairing.code.length == 6 && pairing.code.all(Char::isDigit))
        assertEquals(TestIdentities.target.fingerprint, pairing.fingerprint)
        assertEquals("Target", pairing.device.name)
        assertEquals(TestIdentities.controller.fingerprint, request.peer)
        assertEquals("Controller", request.peerName)

        pairing.confirm()
        Thread.sleep(200)
        assertFalse(pairing.result.isDone, "the target's user has not answered yet")
        request.accept()
        assertEquals(Grant.VIEW_ONLY, pairing.result.get(3, TimeUnit.SECONDS).grants)

        val record = assertNotNull(store.find(TestIdentities.controller.fingerprint))
        assertEquals(Grant.VIEW_ONLY, record.inboundGrants)
        assertEquals(TargetAddress("127.0.0.1", 27301), record.target, "reverse address for B-controls-A")
        assertSame(request, finished.poll(3, TimeUnit.SECONDS))
        assertEquals(2, invitationChanges.size)
        assertNull(invitationChanges.last(), "the invitation is used up")
    }

    @Test
    fun theTargetMayAnswerFirst() {
        val server = serve()
        server.invite("127.0.0.1", Grant.FULL)
        val pairing = startPairing(server.port)
        nextRequest().accept()
        Thread.sleep(200)
        assertFalse(pairing.result.isDone, "the controller's user has not confirmed yet")
        pairing.confirm()
        assertEquals(Grant.FULL, pairing.result.get(3, TimeUnit.SECONDS).grants)
    }

    @Test
    fun aDeclineReachesTheControllerAtOnceAndLeavesTheInvitationOpen() {
        val server = serve()
        server.invite("127.0.0.1", Grant.FULL)
        val pairing = startPairing(server.port)
        nextRequest().decline()
        val e = assertIs<PeerException>(pairing.result.failure())
        assertEquals(RejectCodes.DECLINED, e.code)
        assertNull(store.find(TestIdentities.controller.fingerprint))

        val again = startPairing(server.port)
        again.confirm()
        nextRequest().accept()
        again.result.get(3, TimeUnit.SECONDS)
        assertNotNull(store.find(TestIdentities.controller.fingerprint))
    }

    @Test
    fun withoutAnOpenInvitationNoOneIsAsked() {
        val server = serve()
        val e = assertFailsWith<PeerException> { startPairing(server.port) }
        assertEquals(RejectCodes.NO_INVITATION, e.code)
        assertTrue(requests.isEmpty())
    }

    @Test
    fun aControllerThatGivesUpDismissesTheRequest() {
        val server = serve()
        server.invite("127.0.0.1", Grant.FULL)
        val pairing = startPairing(server.port)
        val request = nextRequest()
        pairing.cancel()
        assertSame(request, finished.poll(3, TimeUnit.SECONDS), "the target stops asking")
        assertTrue(rejections.poll(3, TimeUnit.SECONDS).orEmpty().contains("cancelled"))
        request.accept() // too late: changes nothing
        assertTrue(pairing.result.isCancelled)
        assertFailsWith<CancellationException> { pairing.result.get() }
        Thread.sleep(200)
        assertNull(store.find(TestIdentities.controller.fingerprint))
    }

    @Test
    fun anUnansweredRequestExpires() {
        val server = serve(answerMs = 300)
        server.invite("127.0.0.1", Grant.FULL)
        val pairing = startPairing(server.port)
        val request = nextRequest()
        pairing.confirm()
        val e = assertIs<PeerException>(pairing.result.failure())
        assertEquals(RejectCodes.DECLINED, e.code)
        assertSame(request, finished.poll(3, TimeUnit.SECONDS))
        assertNull(store.find(TestIdentities.controller.fingerprint))
    }

    @Test
    fun oneNearbyPairingAtATime() {
        val server = serve()
        server.invite("127.0.0.1", Grant.FULL)
        val first = startPairing(server.port)
        nextRequest()
        val e = assertFailsWith<PeerException> { startPairing(server.port, TestIdentities.stranger, "Stranger") }
        assertEquals(RejectCodes.UNAVAILABLE, e.code)
        first.cancel()
    }

    @Test
    fun repeatedRefusalsCloseTheInvitation() {
        val server = serve()
        server.invite("127.0.0.1", Grant.FULL)
        repeat(5) {
            val pairing = startPairing(server.port, TestIdentities.stranger, "Stranger")
            nextRequest().decline()
            pairing.result.failure()
        }
        val e = assertFailsWith<PeerException> { startPairing(server.port) }
        assertEquals(RejectCodes.NO_INVITATION, e.code)
        assertNull(invitationChanges.last())
    }

    // ------------------------------------------------- a misbehaving target

    /** Follows the protocol up to the controller's nonce, then does [misbehave]. */
    private fun fakeTarget(misbehave: (ByteArray, OutputStream) -> Unit): Int {
        val socket = ServerSocket(0, 1, InetAddress.getLoopbackAddress())
        fakes += socket
        val tls = PeerTls.serverContext(TestIdentities.stranger)
        thread(name = "fake-target", isDaemon = true) {
            socket.accept().use { raw ->
                val ssl = PeerTls.wrapServer(tls, raw, 3_000)
                val input = DataInputStream(ssl.inputStream)
                val output = ssl.outputStream
                assertIs<PeerMessage.PairNearby>(PeerFrames.read(input))
                val nonce = ShortCode.newNonce()
                val commitment = ShortCode.commitment(nonce, TestIdentities.stranger.fingerprint, PeerTls.peerFingerprint(ssl))
                PeerFrames.write(output, PeerMessage.CodeCommit(ShortCode.encode(commitment)))
                assertIs<PeerMessage.CodeNonce>(PeerFrames.read(input))
                misbehave(nonce, output)
                Thread.sleep(500)
            }
        }
        return socket.localPort
    }

    private val fake = DeviceInfo("Fake", "Fixture", "test", 37)

    @Test
    fun aTargetThatDoesNotWaitForConfirmationIsRefused() {
        val port = fakeTarget { nonce, out ->
            PeerFrames.write(out, PeerMessage.CodeReveal(ShortCode.encode(nonce), fake))
            PeerFrames.write(out, PeerMessage.Paired(device = fake, grants = Grant.toWire(Grant.FULL)))
        }
        val pairing = startPairing(port)
        assertIs<ProtocolException>(pairing.result.failure())
    }

    @Test
    fun aTargetThatSwapsItsCommittedNonceIsCaught() {
        // A relay would need this to steer the code once it has seen the controller's nonce.
        val port = fakeTarget { _, out -> PeerFrames.write(out, PeerMessage.CodeReveal(ShortCode.encode(ShortCode.newNonce()), fake)) }
        val e = assertFailsWith<PeerException> { startPairing(port) }
        assertEquals(RejectCodes.BAD_PROOF, e.code)
    }
}
