package io.github.pwnedbygary.scterm.update

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.widget.Toast
import androidx.core.content.IntentCompat
import io.github.pwnedbygary.scterm.BuildConfig
import io.github.pwnedbygary.scterm.R
import io.github.pwnedbygary.scterm.ScTermApp
import io.github.pwnedbygary.scterm.ui.MainActivity

/**
 * Where Android's installer reports on an update: it asks to show its
 * confirmation prompt, or says why the update failed. Once an update is in,
 * the new version says so, as the screen that started it is gone.
 */
class InstallResultReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        when (intent.action) {
            ACTION_RESULT -> onResult(context, intent)
            Intent.ACTION_MY_PACKAGE_REPLACED -> onReplaced(context)
        }
    }

    private fun onResult(context: Context, intent: Intent) {
        when (val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)) {
            PackageInstaller.STATUS_PENDING_USER_ACTION ->
                IntentCompat.getParcelableExtra(intent, Intent.EXTRA_INTENT, Intent::class.java)
                    ?.let { context.startActivity(it.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            PackageInstaller.STATUS_SUCCESS -> Unit
            else -> {
                ScTermApp.of(context).updatingTo = null
                val why = when (status) {
                    PackageInstaller.STATUS_FAILURE_ABORTED -> "the update was cancelled"
                    PackageInstaller.STATUS_FAILURE_CONFLICT -> "it is signed with a different key"
                    PackageInstaller.STATUS_FAILURE_STORAGE -> "there is not enough storage"
                    else -> intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: "error $status"
                }
                Toast.makeText(context, context.getString(R.string.update_failed, why), Toast.LENGTH_LONG).show()
            }
        }
    }

    private fun onReplaced(context: Context) {
        val app = ScTermApp.of(context)
        // Only updates started here: not adb installs, not other installers.
        if (app.updatingTo != BuildConfig.VERSION_NAME) return
        app.updatingTo = null
        val manager = context.getSystemService(NotificationManager::class.java)
        if (manager.getNotificationChannel(CHANNEL) == null) {
            manager.createNotificationChannel(
                NotificationChannel(CHANNEL, context.getString(R.string.update_channel), NotificationManager.IMPORTANCE_DEFAULT),
            )
        }
        val open = PendingIntent.getActivity(context, 0, Intent(context, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val notification = Notification.Builder(context, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_serving)
            .setContentTitle(context.getString(R.string.update_done, BuildConfig.VERSION_NAME))
            .setContentText(context.getString(R.string.update_done_text))
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        manager.notify(NOTIFICATION_ID, notification)
    }

    companion object {
        const val ACTION_RESULT = "io.github.pwnedbygary.scterm.UPDATE_RESULT"
        private const val CHANNEL = "updates"
        private const val NOTIFICATION_ID = 3
    }
}
