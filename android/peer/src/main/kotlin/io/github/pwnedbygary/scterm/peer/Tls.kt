package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Fingerprint
import java.io.Closeable
import java.net.InetSocketAddress
import java.net.Socket
import java.security.KeyStore
import java.security.Principal
import java.security.PrivateKey
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLEngine
import javax.net.ssl.SSLPeerUnverifiedException
import javax.net.ssl.SSLSocket
import javax.net.ssl.X509ExtendedKeyManager
import javax.net.ssl.X509ExtendedTrustManager

/** Logging hook; the app routes it to logcat. Never log secrets or clipboard text. */
fun interface PeerLog {
    fun log(level: Level, message: String, error: Throwable?)

    enum class Level { DEBUG, INFO, WARN, ERROR }

    companion object {
        @Volatile
        var sink: PeerLog = PeerLog { _, _, _ -> }

        fun d(message: String) = sink.log(Level.DEBUG, message, null)
        fun i(message: String) = sink.log(Level.INFO, message, null)
        fun w(message: String, error: Throwable? = null) = sink.log(Level.WARN, message, error)
        fun e(message: String, error: Throwable? = null) = sink.log(Level.ERROR, message, error)
    }
}

/**
 * This installation's long-term identity: a key pair and a self-signed
 * certificate. Peers pin the SHA-256 of the SubjectPublicKeyInfo, so the
 * certificate's names and dates carry no meaning.
 */
interface PeerIdentity {
    val certificate: X509Certificate
    val privateKey: PrivateKey
    val fingerprint: Fingerprint
}

class StaticIdentity(override val certificate: X509Certificate, override val privateKey: PrivateKey) : PeerIdentity {
    override val fingerprint: Fingerprint = Fingerprint.ofPublicKey(certificate.publicKey.encoded)

    companion object {
        fun fromKeyStore(keyStore: KeyStore, alias: String, password: CharArray?): StaticIdentity =
            StaticIdentity(keyStore.getCertificate(alias) as X509Certificate, keyStore.getKey(alias, password) as PrivateKey)
    }
}

object PeerTls {
    private val PROTOCOLS = listOf("TLSv1.3", "TLSv1.2")

    /** Accepts any client certificate: authorization happens after the handshake. */
    fun serverContext(identity: PeerIdentity): SSLContext = context(identity, AcceptAnyClient)

    /** [pin] null only while pairing, where the invitation secret authenticates the target. */
    fun clientContext(identity: PeerIdentity, pin: Fingerprint?): SSLContext = context(identity, PinnedServer(pin))

    fun wrapServer(context: SSLContext, raw: Socket, handshakeTimeoutMs: Int): SSLSocket {
        raw.tcpNoDelay = true
        val ssl = context.socketFactory.createSocket(raw, raw.inetAddress.hostAddress, raw.port, true) as SSLSocket
        ssl.useClientMode = false
        ssl.needClientAuth = true
        configure(ssl)
        ssl.soTimeout = handshakeTimeoutMs
        ssl.startHandshake()
        return ssl
    }

    fun connect(context: SSLContext, host: String, port: Int, timeoutMs: Int): SSLSocket {
        val raw = Socket()
        try {
            raw.tcpNoDelay = true
            raw.connect(InetSocketAddress(host, port), timeoutMs)
            val ssl = context.socketFactory.createSocket(raw, host, port, true) as SSLSocket
            ssl.useClientMode = true
            configure(ssl)
            ssl.soTimeout = timeoutMs
            ssl.startHandshake()
            return ssl
        } catch (e: Exception) {
            raw.closeQuietly()
            throw e
        }
    }

    fun peerFingerprint(socket: SSLSocket): Fingerprint {
        val leaf = socket.session.peerCertificates.firstOrNull() as? X509Certificate
            ?: throw SSLPeerUnverifiedException("peer presented no certificate")
        return Fingerprint.ofPublicKey(leaf.publicKey.encoded)
    }

    private fun configure(socket: SSLSocket) {
        // TLS 1.3 only exists on Android 10+; never ask for a protocol the stack lacks.
        socket.enabledProtocols = PROTOCOLS.filter { it in socket.supportedProtocols }.toTypedArray()
    }

    private fun context(identity: PeerIdentity, trust: X509ExtendedTrustManager): SSLContext =
        SSLContext.getInstance("TLS").apply { init(arrayOf(IdentityKeyManager(identity)), arrayOf(trust), SecureRandom()) }
}

internal fun Closeable.closeQuietly() {
    try {
        close()
    } catch (_: Exception) {
    }
}

private fun leaf(chain: Array<out X509Certificate>?): X509Certificate =
    chain?.firstOrNull() ?: throw CertificateException("empty certificate chain")

// Deliberately custom: peers are self-signed identities, trusted by pinned
// SPKI fingerprint, never by a CA chain. The TLS handshake still proves the
// client holds its key; TargetServer authorizes the fingerprint afterwards.
@Suppress("CustomX509TrustManager")
private object AcceptAnyClient : X509ExtendedTrustManager() {
    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {
        leaf(chain)
    }

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) {
        leaf(chain)
    }

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) {
        leaf(chain)
    }

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) = notAClient()

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = notAClient()

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = notAClient()

    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()

    private fun notAClient(): Nothing = throw CertificateException("server-side trust manager")
}

// Deliberately custom: the target must present exactly the pinned key; a
// null pin only occurs while pairing, where the invitation secret proves the
// target's identity instead (see ControllerClient.pair).
@Suppress("CustomX509TrustManager")
private class PinnedServer(private val pin: Fingerprint?) : X509ExtendedTrustManager() {
    private fun check(chain: Array<out X509Certificate>?) {
        val cert = leaf(chain)
        if (pin != null && Fingerprint.ofPublicKey(cert.publicKey.encoded) != pin) {
            throw CertificateException("target certificate does not match the paired identity")
        }
    }

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) = check(chain)

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = check(chain)

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = check(chain)

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) = notAServer()

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = notAServer()

    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = notAServer()

    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()

    private fun notAServer(): Nothing = throw CertificateException("client-side trust manager")
}

/** Always offers the one identity, whatever issuers the peer lists. */
private class IdentityKeyManager(private val identity: PeerIdentity) : X509ExtendedKeyManager() {
    private val algorithm = identity.privateKey.algorithm

    private fun matches(keyType: String?): Boolean =
        keyType == null || keyType == algorithm || keyType.startsWith("${algorithm}_") ||
            (algorithm == "EC" && keyType.contains("ECDSA"))

    override fun chooseClientAlias(keyType: Array<out String>?, issuers: Array<out Principal>?, socket: Socket?): String? =
        ALIAS.takeIf { keyType == null || keyType.any(::matches) }

    override fun chooseEngineClientAlias(keyType: Array<out String>?, issuers: Array<out Principal>?, engine: SSLEngine?): String? =
        ALIAS.takeIf { keyType == null || keyType.any(::matches) }

    override fun chooseServerAlias(keyType: String?, issuers: Array<out Principal>?, socket: Socket?): String? =
        ALIAS.takeIf { matches(keyType) }

    override fun chooseEngineServerAlias(keyType: String?, issuers: Array<out Principal>?, engine: SSLEngine?): String? =
        ALIAS.takeIf { matches(keyType) }

    override fun getCertificateChain(alias: String?): Array<X509Certificate>? =
        if (alias == ALIAS) arrayOf(identity.certificate) else null

    override fun getPrivateKey(alias: String?): PrivateKey? = if (alias == ALIAS) identity.privateKey else null

    override fun getClientAliases(keyType: String?, issuers: Array<out Principal>?): Array<String> =
        if (matches(keyType)) arrayOf(ALIAS) else emptyArray()

    override fun getServerAliases(keyType: String?, issuers: Array<out Principal>?): Array<String> =
        if (matches(keyType)) arrayOf(ALIAS) else emptyArray()

    private companion object {
        const val ALIAS = "scterm-peer"
    }
}
