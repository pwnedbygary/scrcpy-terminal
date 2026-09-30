package io.github.pwnedbygary.scterm.update

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.content.pm.PackageInfoCompat
import io.github.pwnedbygary.scterm.BuildConfig
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.longOrNull
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest

/**
 * Updates from GitHub releases: finds the newest release's APK, downloads it
 * checked against the digest GitHub publishes, and hands it to Android's
 * installer, which asks the user before replacing this app. Android only
 * accepts an update signed with the installed app's key; [problemWith]
 * checks that first, so a mismatch is explained rather than just refused.
 */
object AppUpdates {
    data class Release(
        val version: String,
        val versionCode: Int,
        val apkName: String,
        val apkUrl: String,
        val size: Long,
        val sha256: String?,
        val notes: String,
        val page: String,
    )

    class Cancelled : IOException("cancelled")

    /** The download must not be installed; the message says why, for the user. */
    class Refused(message: String) : IOException(message)

    /** Development builds are version code 1 and update from source instead. */
    val isReleaseBuild: Boolean get() = BuildConfig.VERSION_CODE > 1

    const val DIFFERENT_KEY = "This copy of scterm is signed with a different key than the release (it was built from source), so " +
        "Android won't let the release replace it. To switch, uninstall scterm, which removes its pairings, then install the release APK."

    /** Whether a release may replace this install, as far as can be told before downloading it. */
    fun releaseCanReplace(context: Context): Boolean {
        if (BuildConfig.RELEASE_CERT_SHA256.isEmpty()) return true
        val installed = packageInfo(context.packageManager, name = context.packageName) ?: return true
        return BuildConfig.RELEASE_CERT_SHA256 in signers(installed)
    }

    /** "2.1.0" -> 20100, the same numbering as release builds (app/build.gradle.kts). */
    fun versionCodeOf(version: String): Int? {
        val parts = version.removePrefix("v").split('.').map { it.toIntOrNull() ?: -1 }
        if (parts.size != 3 || parts.any { it !in 0..99 }) return null
        return parts[0] * 10_000 + parts[1] * 100 + parts[2]
    }

    /** The release in GitHub's "latest release" JSON, or null if it has no APK. */
    fun parseRelease(json: String): Release? {
        val root = Json.parseToJsonElement(json).jsonObject
        val version = root.string("tag_name")?.removePrefix("v") ?: return null
        val code = versionCodeOf(version) ?: return null
        val apk = root["assets"]?.jsonArray?.map { it.jsonObject }
            ?.firstOrNull { it.string("name").orEmpty().let { n -> n.startsWith("scterm-android-") && n.endsWith(".apk") } }
            ?: return null
        val url = apk.string("browser_download_url") ?: return null
        val digest = apk.string("digest")?.takeIf { it.startsWith("sha256:") }?.removePrefix("sha256:")?.lowercase()
        val size = (apk["size"] as? JsonPrimitive)?.longOrNull ?: -1
        return Release(version, code, apk.string("name")!!, url, size, digest, root.string("body").orEmpty(), root.string("html_url").orEmpty())
    }

    private fun JsonObject.string(key: String): String? = (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.content

    /** Asks GitHub for the newest release. Network: call off the main thread. */
    fun fetchLatest(): Release? {
        val conn = open(BuildConfig.UPDATE_URL, "application/vnd.github+json")
        try {
            return parseRelease(conn.inputStream.use { it.readAtMost(1 shl 20) }.decodeToString())
        } finally {
            conn.disconnect()
        }
    }

    private fun InputStream.readAtMost(limit: Int): ByteArray {
        val out = ByteArrayOutputStream()
        val buf = ByteArray(8192)
        while (out.size() < limit) {
            val n = read(buf, 0, minOf(buf.size, limit - out.size()))
            if (n < 0) break
            out.write(buf, 0, n)
        }
        return out.toByteArray()
    }

    /**
     * Downloads [release]'s APK into the cache, checking its size and digest.
     * [progress] gets 0..1 when the size is known. Network: off the main thread.
     */
    fun download(context: Context, release: Release, progress: (Float) -> Unit, cancelled: () -> Boolean): File {
        val dir = File(context.cacheDir, "updates").apply { mkdirs() }
        dir.listFiles()?.forEach { it.delete() }
        val file = File(dir, "update.apk")
        val conn = open(release.apkUrl, "application/octet-stream")
        try {
            val digest = MessageDigest.getInstance("SHA-256")
            var total = 0L
            conn.inputStream.use { input ->
                file.outputStream().use { out ->
                    val buf = ByteArray(64 * 1024)
                    while (true) {
                        if (cancelled()) throw Cancelled()
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        digest.update(buf, 0, n)
                        total += n
                        if (release.size > 0) {
                            if (total > release.size) throw IOException("the download is larger than published")
                            progress(total.toFloat() / release.size)
                        }
                    }
                }
            }
            if (release.size > 0 && total != release.size) throw IOException("the download was cut short")
            val sha = digest.digest().joinToString("") { "%02x".format(it) }
            if (release.sha256 != null && sha != release.sha256) throw IOException("the download does not match the published digest")
            return file
        } catch (e: Exception) {
            file.delete()
            throw e
        } finally {
            conn.disconnect()
        }
    }

    /** Why [apk] may not replace this app, or null if it may. */
    fun problemWith(context: Context, apk: File): String? {
        val pm = context.packageManager
        val archive = packageInfo(pm, apk = apk) ?: return "The download is not a valid app."
        if (archive.packageName != context.packageName) return "The download is not scterm."
        val installed = packageInfo(pm, name = context.packageName) ?: return "scterm's own package information is unavailable."
        if (PackageInfoCompat.getLongVersionCode(archive) <= PackageInfoCompat.getLongVersionCode(installed)) {
            return "The download is not newer than this version."
        }
        val theirs = signers(archive)
        if (theirs.isEmpty() || theirs != signers(installed)) return DIFFERENT_KEY
        return null
    }

    /**
     * Hands [apk] to Android's installer. The outcome, including the prompt
     * asking the user to confirm, arrives in [InstallResultReceiver].
     */
    fun install(context: Context, apk: File) {
        val installer = context.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(context.packageName)
            setSize(apk.length())
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
        }
        val id = installer.createSession(params)
        try {
            installer.openSession(id).use { session ->
                session.openWrite("scterm.apk", 0, apk.length()).use { out ->
                    apk.inputStream().use { it.copyTo(out) }
                    session.fsync(out)
                }
                // Mutable: the installer adds the outcome to this intent.
                val flags = PendingIntent.FLAG_UPDATE_CURRENT or if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) PendingIntent.FLAG_MUTABLE else 0
                val result = Intent(context, InstallResultReceiver::class.java).setAction(InstallResultReceiver.ACTION_RESULT)
                session.commit(PendingIntent.getBroadcast(context, id, result, flags).intentSender)
            }
        } catch (e: Exception) {
            installer.abandonSession(id)
            throw e
        }
    }

    private fun open(url: String, accept: String): HttpURLConnection {
        val conn = URL(url).openConnection() as HttpURLConnection
        conn.connectTimeout = 10_000
        conn.readTimeout = 20_000
        conn.setRequestProperty("Accept", accept)
        conn.setRequestProperty("User-Agent", "scterm-android/${BuildConfig.VERSION_NAME}")
        val code = conn.responseCode
        if (code != HttpURLConnection.HTTP_OK) {
            conn.disconnect()
            throw IOException("$url answered HTTP $code")
        }
        return conn
    }

    @Suppress("DEPRECATION") // the older calls are the only ones below API 33 (and 28 for signing info)
    private fun packageInfo(pm: PackageManager, name: String? = null, apk: File? = null): PackageInfo? {
        val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            PackageManager.GET_SIGNING_CERTIFICATES or PackageManager.GET_SIGNATURES
        } else {
            PackageManager.GET_SIGNATURES
        }
        return try {
            when {
                apk != null && Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ->
                    pm.getPackageArchiveInfo(apk.path, PackageManager.PackageInfoFlags.of(flags.toLong()))
                apk != null -> pm.getPackageArchiveInfo(apk.path, flags)
                Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ->
                    pm.getPackageInfo(name!!, PackageManager.PackageInfoFlags.of(flags.toLong()))
                else -> pm.getPackageInfo(name!!, flags)
            }
        } catch (_: PackageManager.NameNotFoundException) {
            null
        }
    }

    /** SHA-256 digests of the certificates an APK is signed with. */
    @Suppress("DEPRECATION")
    private fun signers(info: PackageInfo): Set<String> {
        val certs = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            info.signingInfo?.let { if (it.hasMultipleSigners()) it.apkContentsSigners else it.signingCertificateHistory }
        } else {
            null
        } ?: info.signatures
        val sha = MessageDigest.getInstance("SHA-256")
        return certs.orEmpty().map { cert -> sha.digest(cert.toByteArray()).joinToString("") { "%02x".format(it) } }.toSet()
    }
}
