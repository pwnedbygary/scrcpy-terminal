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

    /** In landscape, keep the viewer's controls up instead of hiding them. */
    var viewerControlsAlwaysVisible: Boolean
        get() = prefs.getBoolean(KEY_CONTROLS_ALWAYS, false)
        set(value) = prefs.edit { putBoolean(KEY_CONTROLS_ALWAYS, value) }

    /** Seconds after the last touch on the viewer's controls before they hide. */
    var viewerControlsHideSeconds: Int
        get() = prefs.getInt(KEY_CONTROLS_HIDE_SECONDS, DEFAULT_CONTROLS_HIDE_SECONDS).coerceIn(1, MAX_CONTROLS_HIDE_SECONDS)
        set(value) = prefs.edit { putInt(KEY_CONTROLS_HIDE_SECONDS, value.coerceIn(1, MAX_CONTROLS_HIDE_SECONDS)) }

    /** Keep the viewer's statistics up while its controls are hidden. */
    var viewerStatsAlwaysVisible: Boolean
        get() = prefs.getBoolean(KEY_STATS_ALWAYS, false)
        set(value) = prefs.edit { putBoolean(KEY_STATS_ALWAYS, value) }

    /** When the main screen last looked for a newer release. */
    var lastUpdateCheckMs: Long
        get() = prefs.getLong(KEY_UPDATE_CHECK, 0)
        set(value) = prefs.edit { putLong(KEY_UPDATE_CHECK, value) }

    /** The version an in-app update is installing, announced once it runs. */
    var updatingTo: String?
        get() = prefs.getString(KEY_UPDATING_TO, null)
        set(value) = prefs.edit { if (value == null) remove(KEY_UPDATING_TO) else putString(KEY_UPDATING_TO, value) }

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
        const val MAX_CONTROLS_HIDE_SECONDS = 30
        private const val DEFAULT_CONTROLS_HIDE_SECONDS = 4
        private const val KEY_CONTROLS_ALWAYS = "viewer_controls_always"
        private const val KEY_CONTROLS_HIDE_SECONDS = "viewer_controls_hide_seconds"
        private const val KEY_STATS_ALWAYS = "viewer_stats_always"
        private const val KEY_NAME = "device_name"
        private const val KEY_PORT = "serve_port"
        private const val KEY_BACKEND = "serve_backend"
        private const val KEY_UPDATE_CHECK = "update_checked_at"
        private const val KEY_UPDATING_TO = "updating_to"

        fun of(context: Context): ScTermApp = context.applicationContext as ScTermApp
    }
}
