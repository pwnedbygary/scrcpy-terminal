package io.github.pwnedbygary.scterm.protocol

import java.io.DataInputStream
import java.io.IOException

/** The peer sent bytes that violate the wire contract; drop the connection. */
class ProtocolException(message: String) : IOException(message)

/** Growable big-endian builder, so each message serializes into one array. */
internal class ByteSink(initialCapacity: Int = 32) {
    private var buf = ByteArray(initialCapacity)
    var size = 0
        private set

    private fun ensure(extra: Int) {
        val needed = size + extra
        if (needed > buf.size) buf = buf.copyOf(maxOf(needed, buf.size * 2))
    }

    fun u8(v: Int) {
        ensure(1)
        buf[size++] = v.toByte()
    }

    fun u16(v: Int) {
        ensure(2)
        buf[size++] = (v ushr 8).toByte()
        buf[size++] = v.toByte()
    }

    fun i32(v: Int) {
        ensure(4)
        putInt(buf, size, v)
        size += 4
    }

    fun i64(v: Long) {
        ensure(8)
        putLong(buf, size, v)
        size += 8
    }

    fun bytes(b: ByteArray, off: Int = 0, len: Int = b.size - off) {
        ensure(len)
        System.arraycopy(b, off, buf, size, len)
        size += len
    }

    fun toByteArray(): ByteArray = if (size == buf.size) buf else buf.copyOf(size)
}

internal fun putInt(dst: ByteArray, off: Int, v: Int) {
    dst[off] = (v ushr 24).toByte()
    dst[off + 1] = (v ushr 16).toByte()
    dst[off + 2] = (v ushr 8).toByte()
    dst[off + 3] = v.toByte()
}

internal fun putLong(dst: ByteArray, off: Int, v: Long) {
    putInt(dst, off, (v ushr 32).toInt())
    putInt(dst, off + 4, v.toInt())
}

internal fun getInt(src: ByteArray, off: Int): Int =
    (src[off].toInt() and 0xff shl 24) or
        (src[off + 1].toInt() and 0xff shl 16) or
        (src[off + 2].toInt() and 0xff shl 8) or
        (src[off + 3].toInt() and 0xff)

internal fun getLong(src: ByteArray, off: Int): Long =
    (getInt(src, off).toLong() shl 32) or (getInt(src, off + 4).toLong() and 0xffffffffL)

/** Reads a length-prefixed blob, refusing lengths above [max] before allocating. */
internal fun DataInputStream.readBlob(lengthBytes: Int, max: Int, what: String): ByteArray {
    val len = when (lengthBytes) {
        1 -> readUnsignedByte()
        2 -> readUnsignedShort()
        4 -> readInt()
        else -> throw IllegalArgumentException("length prefix of $lengthBytes bytes")
    }
    if (len < 0 || len > max) throw ProtocolException("$what length $len exceeds $max")
    val data = ByteArray(len)
    readFully(data)
    return data
}

internal fun ByteArray.utf8(): String = String(this, Charsets.UTF_8)

internal fun hex(bytes: ByteArray): String {
    val digits = "0123456789abcdef"
    val out = CharArray(bytes.size * 2)
    for (i in bytes.indices) {
        val v = bytes[i].toInt() and 0xff
        out[i * 2] = digits[v ushr 4]
        out[i * 2 + 1] = digits[v and 0x0f]
    }
    return String(out)
}
