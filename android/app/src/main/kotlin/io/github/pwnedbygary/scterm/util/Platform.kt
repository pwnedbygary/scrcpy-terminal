package io.github.pwnedbygary.scterm.util

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.net.wifi.WifiManager
import android.os.Build
import androidx.core.content.ContextCompat
import java.net.Inet4Address
import java.net.NetworkInterface
import java.net.SocketException

object Net {
    /** Non-loopback IPv4 addresses, Wi-Fi first: what another device on the LAN dials. */
    fun localAddresses(): List<String> = try {
        NetworkInterface.getNetworkInterfaces()?.toList().orEmpty()
            .filter { it.isUp && !it.isLoopback && !it.isVirtual }
            .sortedBy {
                when {
                    it.name.startsWith("wlan") -> 0
                    it.name.startsWith("eth") -> 1
                    else -> 2
                }
            }
            .flatMap { nif ->
                nif.inetAddresses.toList()
                    .filterIsInstance<Inet4Address>()
                    .filter { !it.isLoopbackAddress && !it.isLinkLocalAddress }
                    .mapNotNull { it.hostAddress }
            }
            .distinct()
    } catch (e: SocketException) {
        emptyList()
    }

    /**
     * Keeps Wi-Fi out of power save while frames flow; power save adds tens of
     * milliseconds of jitter per packet. Low-latency mode applies while the
     * holder is in the foreground; otherwise high-performance mode is used.
     */
    fun wifiLock(context: Context, tag: String, foreground: Boolean): WifiManager.WifiLock? {
        val wifi = context.applicationContext.getSystemService(WifiManager::class.java) ?: return null
        @Suppress("DEPRECATION")
        val mode = if (foreground && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            WifiManager.WIFI_MODE_FULL_LOW_LATENCY
        } else {
            WifiManager.WIFI_MODE_FULL_HIGH_PERF
        }
        return wifi.createWifiLock(mode, tag).apply { setReferenceCounted(false) }
    }
}

object Permissions {
    /** Android 17 blocks LAN sockets for apps targeting it until this is granted. */
    const val LOCAL_NETWORK = "android.permission.ACCESS_LOCAL_NETWORK"
    private const val LOCAL_NETWORK_SDK = 37

    /** Runtime permissions this device needs before LAN networking (and for the notification). */
    fun missingForNetworking(context: Context, serving: Boolean): List<String> = buildList {
        if (Build.VERSION.SDK_INT >= LOCAL_NETWORK_SDK && !granted(context, LOCAL_NETWORK)) add(LOCAL_NETWORK)
        if (serving && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            !granted(context, Manifest.permission.POST_NOTIFICATIONS)
        ) {
            add(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    fun granted(context: Context, permission: String): Boolean =
        ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED
}
