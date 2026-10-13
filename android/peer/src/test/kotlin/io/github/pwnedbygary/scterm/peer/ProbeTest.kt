package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.*
import java.net.InetAddress
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class ProbeTest {
    @Test fun checksNeverCreateBackendOrSessionsAndRespectRevocation() {
        val store = InMemoryPeerStore()
        val creates = AtomicInteger()
        val backend = FixtureBackend()
        var ready = false
        val server = TargetServer(TestIdentities.target, store, DeviceInfo("RP6"), object : TargetServer.BackendProvider {
            override fun create(): TargetBackend { creates.incrementAndGet(); return backend }
            override fun status() = TargetServer.BackendStatus(ready, backend.capabilities, if (ready) "" else "Helper needed")
        }, TargetServer.Config(port = 0, bindAddress = InetAddress.getLoopbackAddress()))
        val port = server.start()
        val client = ControllerClient(TestIdentities.controller, ClientInfo("Phone", "test"))
        fun probe() = client.probe("127.0.0.1", port, TestIdentities.target.fingerprint)
        try {
            assertEquals(RejectCodes.NOT_PAIRED, assertFailsWith<PeerException> { probe() }.code)
            client.pair(server.invite("127.0.0.1", setOf(Grant.VIEW, Grant.CONTROL)))
            assertFalse(probe().ready)
            assertEquals("Helper needed", probe().reason)
            ready = true
            server.updateDeviceInfo(DeviceInfo("Renamed RP6"))
            assertEquals("Renamed RP6", probe().device.name)
            assertTrue(probe().ready)
            assertEquals(0, creates.get())
            assertTrue(server.sessions().isEmpty())
            client.connect("127.0.0.1", port, TestIdentities.target.fingerprint, StreamRequest(video = false, audio = false)).let { session ->
                assertTrue(probe().controlOccupied)
                assertEquals(1, creates.get())
                session.disconnect()
            }
            store.update(TestIdentities.controller.fingerprint) { it?.copy(inbound = emptyList()) }
            assertTrue(probe().grants.isEmpty())
            store.remove(TestIdentities.controller.fingerprint)
            assertEquals(RejectCodes.NOT_PAIRED, assertFailsWith<PeerException> { probe() }.code)
            assertFails { client.probe("127.0.0.1", port, TestIdentities.stranger.fingerprint) }
        } finally { server.stop() }
    }
}
