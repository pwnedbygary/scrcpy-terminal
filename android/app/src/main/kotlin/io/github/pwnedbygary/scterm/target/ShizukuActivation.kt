package io.github.pwnedbygary.scterm.target

import android.content.ComponentName
import android.content.Context
import android.content.ServiceConnection
import android.content.pm.PackageManager
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.Parcel
import io.github.pwnedbygary.scterm.BuildConfig
import io.github.pwnedbygary.scterm.helper.ShizukuStarter
import rikka.shizuku.Shizuku
import kotlin.concurrent.thread

/**
 * Starts the helper from the device itself when Shizuku runs there: Shizuku
 * starts [ShizukuStarter] with shell identity, which runs the command a
 * computer would send over adb. Shizuku can itself be started on the device
 * through Wireless debugging, so a reboot no longer needs a computer.
 */
object ShizukuActivation {
    private const val PACKAGE = "moe.shizuku.privileged.api"
    private const val PERMISSION_REQUEST = 7171
    private val main = Handler(Looper.getMainLooper())

    fun installed(context: Context): Boolean = try {
        context.packageManager.getPackageInfo(PACKAGE, 0)
        true
    } catch (_: PackageManager.NameNotFoundException) {
        false
    }

    /** [done] runs on the main thread: null once the command ran, else what to tell the user. */
    fun start(context: Context, shellCommand: String, done: (String?) -> Unit) {
        val running = try {
            Shizuku.pingBinder()
        } catch (_: Throwable) {
            false
        }
        when {
            !running -> done("Shizuku is not running. Open Shizuku, start it, then try again.")
            Shizuku.isPreV11() -> done("This Shizuku is too old. Update it, then try again.")
            Shizuku.checkSelfPermission() == PackageManager.PERMISSION_GRANTED -> run(context, shellCommand, done)
            Shizuku.shouldShowRequestPermissionRationale() -> done("scterm was denied in Shizuku. Allow it in Shizuku's list of apps.")
            else -> {
                Shizuku.addRequestPermissionResultListener(object : Shizuku.OnRequestPermissionResultListener {
                    override fun onRequestPermissionResult(requestCode: Int, grantResult: Int) {
                        if (requestCode != PERMISSION_REQUEST) return
                        Shizuku.removeRequestPermissionResultListener(this)
                        if (grantResult == PackageManager.PERMISSION_GRANTED) {
                            run(context, shellCommand, done)
                        } else {
                            done("Shizuku permission was not granted.")
                        }
                    }
                })
                Shizuku.requestPermission(PERMISSION_REQUEST)
            }
        }
    }

    private fun run(context: Context, shellCommand: String, done: (String?) -> Unit) {
        val args = Shizuku.UserServiceArgs(ComponentName(context.packageName, ShizukuStarter::class.java.name))
            .daemon(false)
            .processNameSuffix("starter")
            .debuggable(BuildConfig.DEBUG)
            .version(BuildConfig.VERSION_CODE)
        val connection = object : ServiceConnection {
            override fun onServiceConnected(name: ComponentName, binder: IBinder) {
                val self = this
                thread(name = "shizuku-start", isDaemon = true) {
                    val error = try {
                        call(binder, shellCommand)
                    } catch (e: Exception) {
                        "Shizuku could not run the helper command: ${e.message}"
                    }
                    main.post {
                        try {
                            Shizuku.unbindUserService(args, self, true)
                        } catch (_: Exception) {
                        }
                        done(error)
                    }
                }
            }

            override fun onServiceDisconnected(name: ComponentName) = Unit
        }
        try {
            Shizuku.bindUserService(args, connection)
        } catch (e: Exception) {
            done("Shizuku could not start: ${e.message}")
        }
    }

    private fun call(binder: IBinder, shellCommand: String): String? {
        val data = Parcel.obtain()
        val reply = Parcel.obtain()
        try {
            data.writeInterfaceToken(ShizukuStarter.DESCRIPTOR)
            data.writeString(shellCommand)
            binder.transact(ShizukuStarter.START, data, reply, 0)
            reply.readException()
            return reply.readString()
        } finally {
            data.recycle()
            reply.recycle()
        }
    }
}
