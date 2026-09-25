package io.github.pwnedbygary.scterm.helper

import java.security.MessageDigest
import java.security.SecureRandom

/**
 * First bytes of every relay connection from the helper to the app:
 * "SCTH", version 1, channel index, then the 32-byte token that only the
 * activation command carries. Loopback TCP is reachable by every app on the
 * device; the token is what makes a connection the helper's.
 */
object HelperHandshake {
    /** scrcpy connects video, audio, then control; the relay numbers them in that order. */
    const val CHANNELS = 3
    const val TOKEN_BYTES = 32
    const val SIZE = 4 + 1 + 1 + TOKEN_BYTES
    private const val VERSION: Byte = 1
    private val MAGIC = byteArrayOf('S'.code.toByte(), 'C'.code.toByte(), 'T'.code.toByte(), 'H'.code.toByte())

    fun newToken(random: SecureRandom = SecureRandom()): ByteArray = ByteArray(TOKEN_BYTES).also(random::nextBytes)

    fun formatToken(token: ByteArray): String = token.joinToString("") { "%02x".format(it) }

    fun parseToken(text: String): ByteArray {
        require(text.length == TOKEN_BYTES * 2) { "token must be ${TOKEN_BYTES * 2} hex digits" }
        return ByteArray(TOKEN_BYTES) { text.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
    }

    fun encode(channel: Int, token: ByteArray): ByteArray {
        require(channel in 0 until CHANNELS && token.size == TOKEN_BYTES)
        return MAGIC + VERSION + channel.toByte() + token
    }

    /** The channel index, or null unless [bytes] is a well-formed handshake carrying [token]. */
    fun decode(bytes: ByteArray, token: ByteArray): Int? {
        if (bytes.size != SIZE || !bytes.copyOfRange(0, 4).contentEquals(MAGIC) || bytes[4] != VERSION) return null
        val channel = bytes[5].toInt()
        if (channel !in 0 until CHANNELS) return null
        return channel.takeIf { MessageDigest.isEqual(bytes.copyOfRange(6, SIZE), token) }
    }
}
