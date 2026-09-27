package io.github.pwnedbygary.scterm

import android.app.Application
import android.content.Context
import android.content.SharedPreferences
import android.os.Build
import android.provider.Settings
import android.util.Log
import androidx.core.content.edit
import io.github.pwnedbygary.scterm.identity.KeystoreIdentity
import io.github.pwnedbygary.scterm.peer.FilePeerStore
import io.github.pwnedbygary.scterm.peer.PeerIdentity
import io.github.pwnedbygary.scterm.peer.PeerLog
import io.github.pwnedbygary.scterm.protocol.ClientInfo
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.Invitation
import io.github.pwnedbygary.scterm.target.BackendKind
import java.io.File

class ScTermApp : Application() {
    /** The long-term identity; the private key never leaves Android Keystore. */
    val identity: PeerIdentity by lazy { KeystoreIdentity.loadOrCreate() }

    val peers: FilePeerStore by lazy { FilePeerStore.open(File(filesDir, "peers.json")) }

    private val prefs: SharedPreferences by lazy { getSharedPreferences("scterm", MODE_PRIVATE) }

    var deviceName: String
        get() = prefs.getString(KEY_NAME, null)?.takeIf { it.isNotBlank() } ?: defaultName()
        set(value) = prefs.edit { putString(KEY_NAME, value.trim()) }

    var servePort: Int
        get() = prefs.getInt(KEY_PORT, Invitation.DEFAULT_PORT)
        set(value) = prefs.edit { putInt(KEY_PORT, value) }

    /** The serving mode last chosen on the main screen. */
    var serveBackend: BackendKind
        get() = prefs.getString(KEY_BACKEND, null).let { name -> BackendKind.entries.firstOrNull { it.name == name } } ?: BackendKind.PROJECTION
        set(value) = prefs.edit { putString(KEY_BACKEND, value.name) }

    val clientInfo: ClientInfo
        get() = ClientInfo(deviceName, "scterm-android/${BuildConfig.VERSION_NAME}", "android ${Build.VERSION.SDK_INT}")

    val deviceInfo: DeviceInfo
        get() = DeviceInfo(deviceName, Build.MODEL, Build.MANUFACTURER, Build.VERSION.SDK_INT)

    override fun onCreate() {
        super.onCreate()
        PeerLog.sink = PeerLog { level, message, error ->
            when (level) {
                PeerLog.Level.DEBUG -> Log.d(TAG, message, error)
                PeerLog.Level.INFO -> Log.i(TAG, message, error)
                PeerLog.Level.WARN -> Log.w(TAG, message, error)
                PeerLog.Level.ERROR -> Log.e(TAG, message, error)
            }
        }
    }

    private fun defaultName(): String =
        Settings.Global.getString(contentResolver, Settings.Global.DEVICE_NAME)?.takeIf { it.isNotBlank() }
            ?: "${Build.MANUFACTURER} ${Build.MODEL}"

    companion object {
        const val TAG = "scterm"
        private const val KEY_NAME = "device_name"
        private const val KEY_PORT = "serve_port"
        private const val KEY_BACKEND = "serve_backend"

        fun of(context: Context): ScTermApp = context.applicationContext as ScTermApp
    }
}
