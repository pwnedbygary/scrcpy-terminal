package io.github.pwnedbygary.scterm.helper

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull

class HelperHandshakeTest {
    private val token = HelperHandshake.newToken()

    @Test
    fun roundTripsEachChannel() {
        for (channel in 0 until HelperHandshake.CHANNELS) {
            assertEquals(channel, HelperHandshake.decode(HelperHandshake.encode(channel, token), token))
        }
        assertEquals(token.toList(), HelperHandshake.parseToken(HelperHandshake.formatToken(token)).toList())
    }

    @Test
    fun anythingButTheExactTokenIsRefused() {
        val valid = HelperHandshake.encode(1, token)
        assertNull(HelperHandshake.decode(valid, HelperHandshake.newToken()), "another token")
        assertNull(HelperHandshake.decode(valid.copyOf(valid.size - 1), token), "truncated")
        assertNull(HelperHandshake.decode(valid.clone().also { it[0] = 'X'.code.toByte() }, token), "bad magic")
        assertNull(HelperHandshake.decode(valid.clone().also { it[4] = 2 }, token), "unknown version")
        assertNull(HelperHandshake.decode(valid.clone().also { it[5] = 3 }, token), "unknown channel")
        assertFailsWith<IllegalArgumentException> { HelperHandshake.encode(3, token) }
        assertFailsWith<IllegalArgumentException> { HelperHandshake.parseToken("abc") }
    }
}
