package io.github.pwnedbygary.scterm.protocol

import java.io.ByteArrayOutputStream
import java.net.URLDecoder
import java.net.URLEncoder
import java.security.MessageDigest
import java.security.SecureRandom
import javax.crypto.Mac
import javax.crypto.spec.SecretKeySpec

/** Crockford base32: no padding, case-insensitive, I/L read as 1 and O as 0. */
object Base32 {
    private const val ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

    fun encode(data: ByteArray): String {
        val out = StringBuilder((data.size * 8 + 4) / 5)
        var buffer = 0
        var bits = 0
        for (b in data) {
            buffer = (buffer shl 8) or (b.toInt() and 0xff)
            bits += 8
            while (bits >= 5) {
                bits -= 5
                out.append(ALPHABET[(buffer ushr bits) and 31])
            }
            buffer = buffer and ((1 shl bits) - 1)
        }
        if (bits > 0) out.append(ALPHABET[(buffer shl (5 - bits)) and 31])
        return out.toString()
    }

    /** Ignores '-' and whitespace so grouped codes can be typed as shown. */
    fun decode(text: String): ByteArray {
        val out = ByteArrayOutputStream(text.length * 5 / 8 + 1)
        var buffer = 0
        var bits = 0
        for (c in text) {
            if (c == '-' || c.isWhitespace()) continue
            val v = valueOf(c) ?: throw IllegalArgumentException("invalid code character '$c'")
            buffer = (buffer shl 5) or v
            bits += 5
            if (bits >= 8) {
                bits -= 8
                out.write((buffer ushr bits) and 0xff)
            }
            buffer = buffer and ((1 shl bits) - 1)
        }
        return out.toByteArray()
    }

    private fun valueOf(c: Char): Int? = when (val u = c.uppercaseChar()) {
        'O' -> 0
        'I', 'L' -> 1
        else -> ALPHABET.indexOf(u).takeIf { it >= 0 }
    }
}

/** SHA-256 of a certificate's SubjectPublicKeyInfo: the stable peer identity. */
class Fingerprint private constructor(private val bytes: ByteArray) {
    /** Canonical form for storage and the wire. */
    val hex: String = hex(bytes)

    /** 80-bit prefix, grouped: what people compare between two screens. */
    val short: String get() = Base32.encode(bytes.copyOf(10)).chunked(4).joinToString("-")

    fun toByteArray(): ByteArray = bytes.copyOf()

    internal fun bytesUnsafe(): ByteArray = bytes

    override fun equals(other: Any?) = other is Fingerprint && MessageDigest.isEqual(bytes, other.bytes)

    override fun hashCode() = bytes.contentHashCode()

    override fun toString() = hex

    companion object {
        const val SIZE = 32

        fun ofPublicKey(spkiDer: ByteArray) = Fingerprint(MessageDigest.getInstance("SHA-256").digest(spkiDer))

        fun fromBytes(bytes: ByteArray): Fingerprint {
            require(bytes.size == SIZE) { "fingerprint must be $SIZE bytes" }
            return Fingerprint(bytes.copyOf())
        }

        /** Accepts the hex form, with or without ':' separators. */
        fun parse(text: String): Fingerprint {
            val clean = text.replace(":", "").trim().lowercase()
            require(clean.length == SIZE * 2 && clean.all { it in '0'..'9' || it in 'a'..'f' }) {
                "fingerprint must be ${SIZE * 2} hex digits"
            }
            return Fingerprint(ByteArray(SIZE) { i -> clean.substring(i * 2, i * 2 + 2).toInt(16).toByte() })
        }
    }
}

/**
 * The one-time pairing secret a target shows. 80 bits: a man in the middle
 * would have to brute-force it offline from one captured proof within the
 * invitation's lifetime, which is far out of reach, so no PAKE is needed.
 */
object PairingCode {
    const val SECRET_BYTES = 10

    fun newSecret(random: SecureRandom = SecureRandom()): ByteArray = ByteArray(SECRET_BYTES).also(random::nextBytes)

    fun format(secret: ByteArray): String = Base32.encode(secret).chunked(4).joinToString("-")

    fun parse(code: String): ByteArray {
        val secret = Base32.decode(code)
        require(secret.size == SECRET_BYTES) { "pairing code must be 16 characters" }
        return secret
    }
}

/** How to reach a target that is waiting to pair, plus its one-time secret. */
class Invitation(
    val host: String,
    val port: Int,
    secret: ByteArray,
    /** Optional pin: when present the controller refuses any other certificate. */
    val fingerprint: Fingerprint? = null,
) {
    private val secretBytes = secret.copyOf()

    init {
        require(host.isNotBlank()) { "missing host" }
        require(port in 1..65535) { "invalid port $port" }
        require(secretBytes.size == PairingCode.SECRET_BYTES) { "invalid secret" }
    }

    val secret: ByteArray get() = secretBytes.copyOf()

    val code: String get() = PairingCode.format(secretBytes)

    val address: String get() = if (':' in host) "[$host]:$port" else "$host:$port"

    /** What to type on another device: `host:port CODE`. */
    val manualText: String get() = "$address $code"

    fun toUri(): String = buildString {
        append("scterm://pair?h=").append(enc(host))
        append("&p=").append(port)
        append("&c=").append(Base32.encode(secretBytes))
        fingerprint?.let { append("&f=").append(it.hex) }
    }

    companion object {
        /** Outside 27183..27282, which the Go client binds for its adb tunnels. */
        const val DEFAULT_PORT = 27300

        /** Parses [toUri] output or the manual `host[:port] CODE` form. */
        fun parse(text: String): Invitation {
            val trimmed = text.trim()
            if (trimmed.startsWith("scterm://", ignoreCase = true)) return parseUri(trimmed)
            val parts = trimmed.split(Regex("\\s+"), limit = 2)
            require(parts.size == 2) { "expected \"host:port CODE\"" }
            val (host, port) = parseAddress(parts[0])
            return Invitation(host, port, PairingCode.parse(parts[1]))
        }

        /** `host`, `host:port`, `[v6]` or `[v6]:port`. */
        fun parseAddress(text: String, defaultPort: Int = DEFAULT_PORT): Pair<String, Int> {
            val t = text.trim()
            if (t.startsWith("[")) {
                val end = t.indexOf(']')
                require(end > 1) { "invalid address $t" }
                val host = t.substring(1, end)
                val rest = t.substring(end + 1)
                return host to if (rest.isEmpty()) defaultPort else parsePort(rest.removePrefix(":"))
            }
            val colons = t.count { it == ':' }
            return when (colons) {
                0 -> t to defaultPort
                1 -> t.substringBefore(':') to parsePort(t.substringAfter(':'))
                else -> t to defaultPort // bare IPv6 literal
            }
        }

        private fun parsePort(s: String): Int =
            s.toIntOrNull()?.takeIf { it in 1..65535 } ?: throw IllegalArgumentException("invalid port $s")

        private fun parseUri(uri: String): Invitation {
            val query = uri.substringAfter('?', "")
            val params = query.split('&').filter { it.isNotEmpty() }.associate {
                val key = it.substringBefore('=')
                key to URLDecoder.decode(it.substringAfter('=', ""), "UTF-8")
            }
            val host = params["h"] ?: throw IllegalArgumentException("invitation has no host")
            val port = parsePort(params["p"] ?: DEFAULT_PORT.toString())
            val secret = PairingCode.parse(params["c"] ?: throw IllegalArgumentException("invitation has no code"))
            return Invitation(host, port, secret, params["f"]?.let(Fingerprint::parse))
        }

        private fun enc(s: String): String = URLEncoder.encode(s, "UTF-8")
    }
}

/**
 * Mutual proof of the invitation secret, bound to both TLS identities:
 * HMAC-SHA256(secret, "scterm-pair-v1\0" + role + "\0" + self + peer), where
 * self/peer are SPKI fingerprints as seen on this TLS connection. A relay that
 * terminates TLS presents different certificates on each leg, so a proof made
 * for one leg is useless on the other without the secret.
 */
object PairingProof {
    enum class Role(val label: String) {
        CONTROLLER("controller"),
        TARGET("target"),
    }

    fun compute(secret: ByteArray, role: Role, self: Fingerprint, peer: Fingerprint): ByteArray {
        val mac = Mac.getInstance("HmacSHA256")
        mac.init(SecretKeySpec(secret, "HmacSHA256"))
        mac.update("scterm-pair-v1\u0000${role.label}\u0000".toByteArray(Charsets.US_ASCII))
        mac.update(self.bytesUnsafe())
        mac.update(peer.bytesUnsafe())
        return mac.doFinal()
    }

    fun encode(proof: ByteArray): String = hex(proof)

    fun verify(secret: ByteArray, role: Role, self: Fingerprint, peer: Fingerprint, proofHex: String): Boolean {
        val expected = encode(compute(secret, role, self, peer)).toByteArray(Charsets.US_ASCII)
        return MessageDigest.isEqual(expected, proofHex.lowercase().toByteArray(Charsets.US_ASCII))
    }
}
