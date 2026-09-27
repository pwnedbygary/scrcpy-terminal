package io.github.pwnedbygary.scterm.helper

import android.os.Binder
import android.os.Parcel
import kotlin.system.exitProcess

/**
 * Runs in a process Shizuku starts with its own identity (shell, or root on a
 * rooted device), the privilege the adb activation command has. Its one call
 * runs that command, so the helper starts from the device itself; the command
 * backgrounds the helper, which outlives this process.
 */
class ShizukuStarter : Binder() {
    override fun onTransact(code: Int, data: Parcel, reply: Parcel?, flags: Int): Boolean = when (code) {
        START -> {
            data.enforceInterface(DESCRIPTOR)
            val error = try {
                val process = ProcessBuilder("sh", "-c", data.readString().orEmpty()).redirectErrorStream(true).start()
                val output = process.inputStream.bufferedReader().readText().trim()
                val exit = process.waitFor()
                if (exit == 0) null else "the helper command exited with $exit: $output"
            } catch (e: Exception) {
                e.toString()
            }
            reply?.writeNoException()
            reply?.writeString(error)
            true
        }
        DESTROY -> exitProcess(0)
        else -> super.onTransact(code, data, reply, flags)
    }

    companion object {
        const val DESCRIPTOR = "io.github.pwnedbygary.scterm.helper.ShizukuStarter"
        const val START = FIRST_CALL_TRANSACTION

        /** Shizuku's USER_SERVICE_TRANSACTION_destroy: the service was unbound for good. */
        private const val DESTROY = 16777115
    }
}
