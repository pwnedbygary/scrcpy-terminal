package io.github.pwnedbygary.scterm.util

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Log
import io.github.pwnedbygary.scterm.ScTermApp
import java.net.Inet4Address
import java.util.concurrent.atomic.AtomicBoolean

/**
 * DNS-SD on the local network, so pairing needs nothing typed: a device with
 * an invitation open advertises itself, and "Pair with a device" lists what it
 * finds. The advertisement carries only a name and a port; who answers is
 * settled by the code both users compare, never by the advertisement.
 */
object Nearby {
    private const val SERVICE_TYPE = "_scterm._tcp"
    private const val RESOLVE_TIMEOUT_MS = 8_000L

    /** What this device advertises as, so it can leave itself out of its own list. */
    @Volatile
    private var ownName: String? = null

    class Service internal constructor(val name: String, internal val info: NsdServiceInfo)

    class Advertiser(context: Context) {
        private val nsd = context.applicationContext.getSystemService(NsdManager::class.java)
        private val multicast = multicastLock(context, "scterm-nearby-advertise")
        private var listener: NsdManager.RegistrationListener? = null
        private var advertised: Pair<String, Int>? = null

        fun start(name: String, port: Int) {
            val wanted = instanceName(name) to port
            if (listener != null && advertised == wanted) return
            stop()
            multicast?.acquire()
            val info = NsdServiceInfo().apply {
                serviceName = wanted.first
                serviceType = SERVICE_TYPE
                setPort(port)
            }
            val l = object : NsdManager.RegistrationListener {
                override fun onServiceRegistered(info: NsdServiceInfo) {
                    ownName = info.serviceName // may differ: DNS-SD renames on conflicts
                }

                override fun onRegistrationFailed(info: NsdServiceInfo, errorCode: Int) {
                    Log.w(ScTermApp.TAG, "could not advertise for nearby pairing: error $errorCode")
                }

                override fun onServiceUnregistered(info: NsdServiceInfo) {}

                override fun onUnregistrationFailed(info: NsdServiceInfo, errorCode: Int) {}
            }
            listener = l
            advertised = wanted
            nsd.registerService(info, NsdManager.PROTOCOL_DNS_SD, l)
        }

        fun stop() {
            val l = listener ?: return
            listener = null
            advertised = null
            ownName = null
            multicast?.takeIf { it.isHeld }?.release()
            try {
                nsd.unregisterService(l)
            } catch (_: IllegalArgumentException) {
                // Registration had failed: nothing to withdraw.
            }
        }

        /** DNS-SD instance names are at most 63 bytes of UTF-8. */
        private fun instanceName(name: String): String {
            var out = name.ifBlank { "scterm device" }
            while (out.toByteArray(Charsets.UTF_8).size > 63) out = out.dropLast(1)
            return out
        }
    }

    /** Lists advertising devices while started; [onChanged] runs on the main thread. */
    class Browser(context: Context, private val onChanged: (List<Service>) -> Unit) {
        private val nsd = context.applicationContext.getSystemService(NsdManager::class.java)
        private val multicast = multicastLock(context, "scterm-nearby-browse")
        private val main = Handler(Looper.getMainLooper())
        private val found = LinkedHashMap<String, NsdServiceInfo>()
        private var listener: NsdManager.DiscoveryListener? = null

        fun start() {
            if (listener != null) return
            multicast?.acquire()
            val l = object : NsdManager.DiscoveryListener {
                override fun onServiceFound(info: NsdServiceInfo) {
                    main.post {
                        if (listener === this) {
                            found[info.serviceName] = info
                            publish()
                        }
                    }
                }

                override fun onServiceLost(info: NsdServiceInfo) {
                    main.post {
                        if (listener === this) {
                            found.remove(info.serviceName)
                            publish()
                        }
                    }
                }

                override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                    Log.w(ScTermApp.TAG, "nearby discovery failed: error $errorCode")
                }

                override fun onDiscoveryStarted(serviceType: String) {}

                override fun onDiscoveryStopped(serviceType: String) {}

                override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {}
            }
            listener = l
            nsd.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, l)
        }

        fun stop() {
            val l = listener ?: return
            listener = null
            found.clear()
            multicast?.takeIf { it.isHeld }?.release()
            try {
                nsd.stopServiceDiscovery(l)
            } catch (_: IllegalArgumentException) {
                // Discovery had failed to start.
            }
        }

        private fun publish() {
            val own = ownName
            onChanged(found.values.filter { it.serviceName != own }.map { Service(unescape(it.serviceName), it) })
        }
    }

    /**
     * Looks up where [service] is now; [done] gets host and port, or null if
     * it cannot be found, on the main thread.
     */
    fun resolve(context: Context, service: Service, done: (Pair<String, Int>?) -> Unit) {
        val nsd = context.applicationContext.getSystemService(NsdManager::class.java)
        val main = Handler(Looper.getMainLooper())
        val finished = AtomicBoolean(false)
        fun finish(address: Pair<String, Int>?) {
            if (finished.compareAndSet(false, true)) main.post { done(address) }
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            val callback = object : NsdManager.ServiceInfoCallback {
                override fun onServiceUpdated(info: NsdServiceInfo) {
                    val host = info.hostAddresses.sortedBy { if (it is Inet4Address) 0 else 1 }.firstOrNull()?.hostAddress ?: return
                    finish(host to info.port)
                    main.post { unregister(nsd, this) }
                }

                override fun onServiceInfoCallbackRegistrationFailed(errorCode: Int) = finish(null)

                override fun onServiceLost() {}

                override fun onServiceInfoCallbackUnregistered() {}
            }
            nsd.registerServiceInfoCallback(service.info, Runnable::run, callback)
            main.postDelayed({
                finish(null)
                unregister(nsd, callback)
            }, RESOLVE_TIMEOUT_MS)
        } else {
            @Suppress("DEPRECATION")
            nsd.resolveService(service.info, object : NsdManager.ResolveListener {
                override fun onServiceResolved(info: NsdServiceInfo) {
                    @Suppress("DEPRECATION")
                    finish(info.host?.hostAddress?.let { it to info.port })
                }

                override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) = finish(null)
            })
            main.postDelayed({ finish(null) }, RESOLVE_TIMEOUT_MS)
        }
    }

    /**
     * Wi-Fi drivers may drop multicast to save power, which hides mDNS queries
     * and answers; held only while advertising or browsing.
     */
    private fun multicastLock(context: Context, tag: String): WifiManager.MulticastLock? =
        context.applicationContext.getSystemService(WifiManager::class.java)?.createMulticastLock(tag)?.apply { setReferenceCounted(false) }

    private fun unregister(nsd: NsdManager, callback: NsdManager.ServiceInfoCallback) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return
        try {
            nsd.unregisterServiceInfoCallback(callback)
        } catch (_: IllegalArgumentException) {
            // Already unregistered.
        }
    }

    /** Some Android versions report names DNS-escaped, a space as `\032`. */
    private fun unescape(name: String): String = Regex("""\\(\d{3})""").replace(name) { m ->
        m.groupValues[1].toInt().takeIf { it < 128 }?.toChar()?.toString() ?: m.value
    }
}
