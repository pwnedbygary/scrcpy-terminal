package io.github.pwnedbygary.scterm.target

import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.media.projection.MediaProjectionManager
import android.net.wifi.WifiManager
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import androidx.core.content.IntentCompat
import io.github.pwnedbygary.scterm.R
import io.github.pwnedbygary.scterm.ScTermApp
import io.github.pwnedbygary.scterm.peer.PeerRecord
import io.github.pwnedbygary.scterm.peer.TargetServer
import io.github.pwnedbygary.scterm.ui.MainActivity
import io.github.pwnedbygary.scterm.util.Net

/**
 * Owns the serving side while the user has it switched on: the peer server,
 * the active backend, the ongoing notification with local Stop and
 * Disconnect-all controls, and a Wi-Fi lock while anyone is connected.
 * Everything a remote peer can do is bounded by what this service runs.
 */
class TargetService : Service() {
    private val main = Handler(Looper.getMainLooper())
    private var server: TargetServer? = null
    private var backend: ServingBackend? = null
    private var kind = BackendKind.PROJECTION
    private var port = 0
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> start(intent)
            ACTION_KICK -> server?.endAllSessions("disconnected on the serving device")
            ACTION_STOP -> shutdown(null)
            else -> if (server == null) stopSelf()
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        stopServing()
        Serving.publish(ServeState.Stopped)
        super.onDestroy()
    }

    // ServiceCompat drops the service type below Android 10, so the inlined
    // constants never reach an OS that does not know them.
    @SuppressLint("InlinedApi")
    private fun start(intent: Intent) {
        if (server != null) return
        kind = intent.getStringExtra(EXTRA_BACKEND)?.let(BackendKind::valueOf) ?: BackendKind.PROJECTION
        // Android 14+: the typed foreground service must be running before the
        // projection token is exchanged, and only after the user consented.
        val type = if (kind == BackendKind.PROJECTION) {
            ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION or ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE
        } else {
            ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE
        }
        ServiceCompat.startForeground(this, NOTIFICATION_ID, notification(getString(R.string.notification_serving)), type)
        Serving.publish(ServeState.Starting(kind))
        try {
            val app = ScTermApp.of(this)
            val created = createBackend(intent)
            backend = created
            val srv = TargetServer(
                identity = app.identity,
                store = app.peers,
                device = app.deviceInfo,
                backends = { backend?.takeIf { it.ready } },
                config = TargetServer.Config(port = app.servePort),
                events = events,
            )
            port = srv.start()
            server = srv
            Serving.server = srv
            if (kind == BackendKind.PROJECTION) RemoteInputService.servingActive = true
            publish()
        } catch (e: Exception) {
            Log.e(ScTermApp.TAG, "could not start serving", e)
            shutdown(e.message ?: e.javaClass.simpleName)
        }
    }

    private fun createBackend(intent: Intent): ServingBackend {
        val backend = when (kind) {
            BackendKind.PROJECTION -> {
                val data = IntentCompat.getParcelableExtra(intent, EXTRA_RESULT_DATA, Intent::class.java)
                    ?: error("screen capture was not approved")
                val manager = getSystemService(MediaProjectionManager::class.java)
                val projection = manager.getMediaProjection(intent.getIntExtra(EXTRA_RESULT_CODE, 0), data)
                    ?: error("screen capture was not approved")
                ProjectionBackend(this, projection)
            }
            BackendKind.HELPER -> HelperBackend(this)
        }
        backend.onChanged = { main.post { publish() } }
        return backend
    }

    private val events = object : TargetServer.Events {
        override fun onSessionStarted(session: TargetServer.SessionInfo) = changed()
        override fun onSessionEnded(session: TargetServer.SessionInfo, reason: String) = changed()
        override fun onLeaseChanged(holder: TargetServer.SessionInfo?) = changed()
        override fun onPaired(record: PeerRecord) = changed()

        override fun onBackendStopped(error: String?) {
            main.post {
                when (kind) {
                    // The consent token is spent: serving cannot continue without a new one.
                    BackendKind.PROJECTION -> shutdown(error ?: "screen capture stopped")
                    // The helper exited (unplugged, rebooted): wait for a new activation.
                    BackendKind.HELPER -> {
                        backend?.stop()
                        backend = HelperBackend(this@TargetService).also { it.onChanged = { main.post { publish() } } }
                        publish()
                    }
                }
            }
        }

        private fun changed() {
            main.post { publish() }
        }
    }

    private fun publish() {
        val srv = server ?: return
        val b = backend
        val sessions = srv.sessions()
        updateWifiLock(sessions.isNotEmpty())
        Serving.publish(
            ServeState.Serving(
                kind = kind,
                port = port,
                ready = b?.ready == true,
                status = b?.status ?: "",
                sessions = sessions,
                helperCommand = (b as? HelperBackend)?.activationCommand,
            ),
        )
        val text = when {
            b?.ready != true -> b?.status ?: getString(R.string.notification_serving)
            sessions.isEmpty() -> "Waiting on ${Net.localAddresses().firstOrNull() ?: "this device"}:$port"
            else -> sessions.joinToString { it.peerName + if (it.holdsLease) " (control)" else "" }
        }
        getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, notification(text))
    }

    private fun updateWifiLock(active: Boolean) {
        if (active && wifiLock == null) {
            wifiLock = Net.wifiLock(this, "scterm-serving", foreground = false)?.also { it.acquire() }
        } else if (!active) {
            wifiLock?.release()
            wifiLock = null
        }
    }

    private fun stopServing() {
        val srv = server
        val b = backend
        server = null
        backend = null
        Serving.server = null
        updateWifiLock(false)
        val finish = {
            // After the sessions' input releases went out through the backend.
            b?.stop()
            RemoteInputService.servingActive = false
        }
        if (srv != null) srv.stop(onStopped = finish) else finish()
    }

    private fun shutdown(error: String?) {
        stopServing()
        Serving.publish(if (error != null) ServeState.Failed(error) else ServeState.Stopped)
        ServiceCompat.stopForeground(this, ServiceCompat.STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun notification(text: String): Notification {
        val manager = getSystemService(NotificationManager::class.java)
        if (manager.getNotificationChannel(CHANNEL) == null) {
            manager.createNotificationChannel(
                NotificationChannel(CHANNEL, getString(R.string.notification_channel), NotificationManager.IMPORTANCE_LOW),
            )
        }
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), flags)
        val stop = PendingIntent.getService(this, 1, Intent(this, TargetService::class.java).setAction(ACTION_STOP), flags)
        val kick = PendingIntent.getService(this, 2, Intent(this, TargetService::class.java).setAction(ACTION_KICK), flags)
        return Notification.Builder(this, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_serving)
            .setContentTitle(getString(R.string.notification_serving))
            .setContentText(text)
            .setOngoing(true)
            .setContentIntent(open)
            .setCategory(Notification.CATEGORY_SERVICE)
            .addAction(Notification.Action.Builder(null, getString(R.string.notification_kick), kick).build())
            .addAction(Notification.Action.Builder(null, getString(R.string.notification_stop), stop).build())
            .build()
    }

    companion object {
        private const val CHANNEL = "serving"
        private const val NOTIFICATION_ID = 1
        private const val ACTION_START = "io.github.pwnedbygary.scterm.START"
        private const val ACTION_STOP = "io.github.pwnedbygary.scterm.STOP"
        private const val ACTION_KICK = "io.github.pwnedbygary.scterm.KICK"
        private const val EXTRA_BACKEND = "backend"
        private const val EXTRA_RESULT_CODE = "result_code"
        private const val EXTRA_RESULT_DATA = "result_data"

        /** [projectionData] is the screen-capture consent result (PROJECTION only). */
        fun start(context: Context, kind: BackendKind, projectionCode: Int = 0, projectionData: Intent? = null) {
            val intent = Intent(context, TargetService::class.java)
                .setAction(ACTION_START)
                .putExtra(EXTRA_BACKEND, kind.name)
                .putExtra(EXTRA_RESULT_CODE, projectionCode)
            projectionData?.let { intent.putExtra(EXTRA_RESULT_DATA, it) }
            ContextCompat.startForegroundService(context, intent)
        }

        fun stop(context: Context) {
            context.startService(Intent(context, TargetService::class.java).setAction(ACTION_STOP))
        }

        fun disconnectAll(context: Context) {
            context.startService(Intent(context, TargetService::class.java).setAction(ACTION_KICK))
        }
    }
}
