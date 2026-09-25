package io.github.pwnedbygary.scterm.protocol

import io.github.pwnedbygary.scterm.protocol.Fixtures.str
import io.github.pwnedbygary.scterm.protocol.Fixtures.unhex
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import java.security.SecureRandom
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class PairingTest {
    private val v = Fixtures.load("pairing.json")
    private val secret = unhex(v.str("secretHex"))
    private val controller = Fingerprint.ofPublicKey(v.str("controllerSpkiUtf8").toByteArray())
    private val target = Fingerprint.ofPublicKey(v.str("targetSpkiUtf8").toByteArray())

    @Test
    fun base32MatchesTheReferenceVectors() {
        for (e in v.getValue("base32").jsonArray.map { it.jsonObject }) {
            val bytes = unhex(e.str("hex"))
            assertEquals(e.str("text"), Base32.encode(bytes))
            assertContentEquals(bytes, Base32.decode(e.str("text")))
        }
    }

    @Test
    fun base32DecodingIsForgivingAboutHowPeopleTypeCodes() {
        val code = v.str("code")
        assertContentEquals(secret, PairingCode.parse(code))
        assertContentEquals(secret, PairingCode.parse(code.lowercase()))
        assertContentEquals(secret, PairingCode.parse(code.replace("-", " ")))
        // O/0 and I/L/1 confusions map to the same value.
        assertContentEquals(Base32.decode("01"), Base32.decode("oI"))
        assertContentEquals(Base32.decode("01"), Base32.decode("OL"))
        assertFailsWith<IllegalArgumentException> { Base32.decode("U") }
        assertFailsWith<IllegalArgumentException> { PairingCode.parse("04HM-ASW9") }
    }

    @Test
    fun codeFormatting() {
        assertEquals(v.str("code"), PairingCode.format(secret))
        val fresh = PairingCode.newSecret(SecureRandom())
        assertEquals(PairingCode.SECRET_BYTES, fresh.size)
        assertContentEquals(fresh, PairingCode.parse(PairingCode.format(fresh)))
    }

    @Test
    fun fingerprintsMatchTheReferenceVectors() {
        assertEquals(v.str("controllerFingerprint"), controller.hex)
        assertEquals(v.str("targetFingerprint"), target.hex)
        assertEquals(v.str("controllerFingerprintShort"), controller.short)
        assertEquals(controller, Fingerprint.parse(controller.hex.uppercase().chunked(2).joinToString(":")))
        assertFailsWith<IllegalArgumentException> { Fingerprint.parse("abcd") }
    }

    @Test
    fun proofsMatchTheIndependentImplementation() {
        val controllerProof = PairingProof.compute(secret, PairingProof.Role.CONTROLLER, controller, target)
        val targetProof = PairingProof.compute(secret, PairingProof.Role.TARGET, target, controller)
        assertEquals(v.str("controllerProof"), PairingProof.encode(controllerProof))
        assertEquals(v.str("targetProof"), PairingProof.encode(targetProof))
        assertTrue(PairingProof.verify(secret, PairingProof.Role.CONTROLLER, controller, target, v.str("controllerProof")))
    }

    @Test
    fun proofsAreBoundToRoleIdentitiesAndSecret() {
        val proof = v.str("controllerProof")
        val other = Fingerprint.ofPublicKey("mitm".toByteArray())
        // Wrong role, swapped identities, a relay's certificate, a wrong secret.
        assertFalse(PairingProof.verify(secret, PairingProof.Role.TARGET, controller, target, proof))
        assertFalse(PairingProof.verify(secret, PairingProof.Role.CONTROLLER, target, controller, proof))
        assertFalse(PairingProof.verify(secret, PairingProof.Role.CONTROLLER, controller, other, proof))
        assertFalse(PairingProof.verify(secret.copyOf().also { it[0] = 9 }, PairingProof.Role.CONTROLLER, controller, target, proof))
        assertFalse(PairingProof.verify(secret, PairingProof.Role.CONTROLLER, controller, target, "zz"))
    }

    @Test
    fun invitationsRoundTripInBothForms() {
        val inv = Invitation("192.168.1.20", 27300, secret, target)
        assertEquals(v.str("invitationUri"), inv.toUri())
        assertEquals(v.str("invitationManual"), inv.manualText)

        val fromUri = Invitation.parse(v.str("invitationUri"))
        assertEquals("192.168.1.20", fromUri.host)
        assertEquals(27300, fromUri.port)
        assertContentEquals(secret, fromUri.secret)
        assertEquals(target, fromUri.fingerprint)

        val manual = Invitation.parse("  " + v.str("invitationManual").lowercase() + " ")
        assertEquals(27300, manual.port)
        assertContentEquals(secret, manual.secret)
        assertNull(manual.fingerprint)
    }

    @Test
    fun addressesIncludingIpv6() {
        assertEquals("10.0.0.2" to Invitation.DEFAULT_PORT, Invitation.parseAddress("10.0.0.2"))
        assertEquals("10.0.0.2" to 5555, Invitation.parseAddress("10.0.0.2:5555"))
        assertEquals("fe80::1" to 5555, Invitation.parseAddress("[fe80::1]:5555"))
        assertEquals("fe80::1" to Invitation.DEFAULT_PORT, Invitation.parseAddress("[fe80::1]"))
        assertEquals("fe80::1" to Invitation.DEFAULT_PORT, Invitation.parseAddress("fe80::1"))
        assertEquals("[fe80::1]:27300", Invitation("fe80::1", 27300, secret).address)
        val v6 = Invitation.parse(Invitation("fe80::1%wlan0", 27300, secret).toUri())
        assertEquals("fe80::1%wlan0", v6.host)
        assertFailsWith<IllegalArgumentException> { Invitation.parseAddress("host:99999") }
        assertFailsWith<IllegalArgumentException> { Invitation.parse("192.168.1.20:27300") }
    }
}
