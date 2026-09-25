package io.github.pwnedbygary.scterm.identity

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import io.github.pwnedbygary.scterm.peer.PeerIdentity
import io.github.pwnedbygary.scterm.peer.StaticIdentity
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.cert.X509Certificate
import java.security.spec.ECGenParameterSpec
import java.util.Date
import javax.security.auth.x500.X500Principal

/**
 * An EC P-256 key generated inside Android Keystore (non-exportable), with the
 * self-signed certificate Keystore issues for it. Peers pin the public key's
 * SHA-256, so the certificate's subject and dates are placeholders.
 */
object KeystoreIdentity {
    private const val ALIAS = "scterm-peer-identity"
    private const val PROVIDER = "AndroidKeyStore"

    @Synchronized
    fun loadOrCreate(): PeerIdentity {
        val keyStore = KeyStore.getInstance(PROVIDER).apply { load(null) }
        if (!keyStore.containsAlias(ALIAS)) generate()
        val entry = keyStore.getEntry(ALIAS, null) as KeyStore.PrivateKeyEntry
        return StaticIdentity(entry.certificate as X509Certificate, entry.privateKey)
    }

    /** Deleting the key changes this device's identity: every peer must pair again. */
    @Synchronized
    fun reset() {
        KeyStore.getInstance(PROVIDER).apply { load(null) }.deleteEntry(ALIAS)
    }

    private fun generate() {
        val spec = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            // TLS 1.2 stacks may ask Keystore to sign a precomputed hash (NONE).
            .setDigests(
                KeyProperties.DIGEST_NONE,
                KeyProperties.DIGEST_SHA256,
                KeyProperties.DIGEST_SHA384,
                KeyProperties.DIGEST_SHA512,
            )
            .setCertificateSubject(X500Principal("CN=scterm peer"))
            .setCertificateSerialNumber(BigInteger.ONE)
            .setCertificateNotBefore(Date(0))
            .setCertificateNotAfter(Date(4_102_444_800_000L)) // 2100-01-01
            .build()
        KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, PROVIDER).run {
            initialize(spec)
            generateKeyPair()
        }
    }
}
