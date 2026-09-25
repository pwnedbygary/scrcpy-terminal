package io.github.pwnedbygary.scterm.helper

import android.net.LocalServerSocket
import com.genymobile.scrcpy.Server
import java.io.Closeable
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import kotlin.system.exitProcess

/**
 * Started by the activation command through `app_process`, with shell
 * identity: `HelperMain <app port> <token> 4.1 scid=… [scrcpy options]`.
 *
 * SELinux does not let the shell domain connect to an app's abstract socket
 * (observed on Android 16), so the vendored server, unchanged, connects to an
 * abstract socket in this same process instead, and each of its three
 * connections is relayed to the app over loopback TCP after a token
 * handshake. The raw control socket never leaves the device.
 */
object HelperMain {
    @JvmStatic
    fun main(args: Array<String>) {
        require(args.size >= 3) { "usage: HelperMain <port> <token> <scrcpy version> [scrcpy options]" }
        val port = args[0].toInt()
        val token = HelperHandshake.parseToken(args[1])
        val serverArgs = args.copyOfRange(2, args.size)
        val scid = serverArgs.firstOrNull { it.startsWith("scid=") }?.substringAfter('=') ?: error("scid= is required")
        val local = LocalServerSocket("scrcpy_$scid")
        Thread({ relayAll(local, port, token) }, "scterm-relay").apply {
            isDaemon = true
            start()
        }
        // Owns the main looper and exits the process when its session ends.
        Server.main(*serverArgs)
    }

    private fun relayAll(local: LocalServerSocket, port: Int, token: ByteArray) {
        try {
            for (channel in 0 until HelperHandshake.CHANNELS) {
                val scrcpy = local.accept()
                val app = Socket()
                app.tcpNoDelay = true
                app.connect(InetSocketAddress(InetAddress.getLoopbackAddress(), port), CONNECT_TIMEOUT_MS)
                app.getOutputStream().write(HelperHandshake.encode(channel, token))
                val pair = listOf<Closeable>(scrcpy, app)
                pump(scrcpy.inputStream, app.getOutputStream(), pair, "relay-$channel-up")
                pump(app.getInputStream(), scrcpy.outputStream, pair, "relay-$channel-down")
            }
        } catch (e: Exception) {
            System.err.println("scterm helper: cannot reach the app on 127.0.0.1:$port: ${e.message}")
            exitProcess(1)
        }
    }

    /** Copies until either side ends, then closes both so the server shuts down. */
    private fun pump(input: InputStream, output: OutputStream, pair: List<Closeable>, name: String) {
        Thread({
            val buffer = ByteArray(64 * 1024)
            try {
                while (true) {
                    val n = input.read(buffer)
                    if (n < 0) break
                    output.write(buffer, 0, n)
                }
            } catch (_: Exception) {
            } finally {
                pair.forEach { runCatching { it.close() } }
            }
        }, name).apply {
            isDaemon = true
            start()
        }
    }

    private const val CONNECT_TIMEOUT_MS = 5_000
}
