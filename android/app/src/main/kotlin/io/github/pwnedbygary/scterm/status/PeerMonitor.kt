package io.github.pwnedbygary.scterm.status

import android.net.ConnectivityManager
import android.net.Network
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import io.github.pwnedbygary.scterm.ScTermApp
import io.github.pwnedbygary.scterm.peer.ControllerClient
import io.github.pwnedbygary.scterm.peer.ControllerSession
import io.github.pwnedbygary.scterm.protocol.PeerMessage
import io.github.pwnedbygary.scterm.protocol.DeviceInfo
import io.github.pwnedbygary.scterm.protocol.Grant
import io.github.pwnedbygary.scterm.protocol.LeaseState
import io.github.pwnedbygary.scterm.peer.PeerException
import io.github.pwnedbygary.scterm.peer.PeerRecord
import io.github.pwnedbygary.scterm.peer.Readiness
import io.github.pwnedbygary.scterm.peer.ReadinessLedger
import io.github.pwnedbygary.scterm.peer.TargetAddress
import io.github.pwnedbygary.scterm.util.Nearby
import io.github.pwnedbygary.scterm.util.Permissions
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import java.net.Socket
import java.util.concurrent.Future
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.atomic.AtomicReference

/** One monitor shared by the visible home screen and the existing serving service. */
class PeerMonitor(private val app: ScTermApp) {
    private val main = Handler(Looper.getMainLooper())
    private val workers = ThreadPoolExecutor(2, 2, 0L, TimeUnit.MILLISECONDS, LinkedBlockingQueue()) { task ->
        Thread(task, "peer-status").apply { isDaemon = true }
    }
    private val viewers = mutableMapOf<String, ControllerSession>()
    private val ledger = ReadinessLedger()
    private val mutable = MutableStateFlow<Map<String, Readiness>>(emptyMap())
    val statuses: StateFlow<Map<String, Readiness>> = mutable
    private var visible = false
    private var serving = false
    private var running = false
    private var epoch = 0L
    private var storeListener: AutoCloseable? = null
    private val candidates = mutableMapOf<String, TargetAddress>()
    private val candidateNames = mutableMapOf<String, String>()
    private val jobs = mutableMapOf<String, Pending>()
    private val connectivity = app.getSystemService(ConnectivityManager::class.java)
    private var networkRegistered = false
    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) { networkChanged() }
        override fun onLost(network: Network) { networkChanged() }
    }
    private val browser = Nearby.Browser(app, presence = true) { services ->
        val currentEpoch = epoch
        val names = services.map { it.name }.toSet()
        // Lost advertisements are hints only: never infer reachability from disappearance.
        candidateNames.filterValues { it !in names }.keys.toList().forEach { candidates.remove(it); candidateNames.remove(it) }
        services.take(32).forEach { service ->
            Nearby.resolvePresence(app, service) { endpoint ->
                if (!running || currentEpoch != epoch || endpoint == null) return@resolvePresence
                val id = endpoint.identityHint ?: return@resolvePresence
                if (app.peers.all().none { it.fingerprint == id }) return@resolvePresence
                candidateNames[id] = service.name
                val address = TargetAddress(endpoint.host, endpoint.port)
                if (candidates.put(id, address) != address) scheduleRefresh()
            }
        }
    }
    private class Pending {
        val socket = AtomicReference<Socket?>()
        var future: Future<*>? = null
        @Volatile var cancelled = false
        fun cancel() { cancelled = true; socket.getAndSet(null)?.let { runCatching { it.close() } }; future?.cancel(true) }
    }
    private val refreshTask = Runnable { refresh() }
    private val poll = object : Runnable {
        override fun run() {
            if (!running) return
            refresh(replacePending = false)
            main.postDelayed(this, interval())
        }
    }
    private val freshness = object : Runnable {
        override fun run() {
            if (!running) return
            emit()
            main.postDelayed(this, 1_000)
        }
    }

    /** Only outgoing authenticated viewing sessions establish this direction. */
    fun noteViewer(id: String, session: ControllerSession?) {
        if (session == null) {
            viewers.remove(id)
            if (app.peers.all().any { it.fingerprint == id }) {
                val ticket = ledger.begin(id)
                ledger.complete(id, ticket, Readiness(Readiness.Tone.YELLOW, "Session ended—refresh"))
            }
        } else {
            viewers[id] = session
            jobs.remove(id)?.cancel()
        }
        emit()
        if (running) scheduleRefresh()
    }

    fun setVisible(value: Boolean) { visible = value; reconcile() }
    fun setServing(value: Boolean) { serving = value; reconcile() }
    private fun interval() = if (visible) 15_000L else 60_000L

    private fun reconcile() {
        val wanted = visible || serving
        if (wanted != running) {
            running = wanted
            epoch++
            if (wanted) {
                storeListener = app.peers.addListener { main.post { peersChanged() } }
                if (Permissions.missingForNetworking(app, false).isEmpty()) {
                    try { browser.start() } catch (_: SecurityException) { /* Badge explains permission. */ }
                }
                try { connectivity.registerDefaultNetworkCallback(callback); networkRegistered = true } catch (_: SecurityException) { }
                main.post(freshness)
            } else {
                storeListener?.close(); storeListener = null
                browser.stop()
                candidates.clear(); candidateNames.clear()
                if (networkRegistered) { connectivity.unregisterNetworkCallback(callback); networkRegistered = false }
                cancelChecks()
                main.removeCallbacks(freshness)
            }
        }
        main.removeCallbacks(poll)
        main.removeCallbacks(refreshTask)
        if (wanted) main.post(poll)
    }

    private fun networkChanged() = main.post {
        if (running) { epoch++; candidates.clear(); candidateNames.clear(); cancelChecks(); scheduleRefresh() }
    }
    private fun scheduleRefresh() {
        main.removeCallbacks(refreshTask)
        main.postDelayed(refreshTask, 300)
    }
    private fun cancelChecks() {
        ledger.invalidate()
        jobs.values.forEach { it.cancel() }
        jobs.clear()
        workers.purge()
    }
    private fun peersChanged() {
        val ids = app.peers.all().map { it.fingerprint }.toSet()
        ledger.retain(ids)
        viewers.keys.retainAll(ids)
        jobs.keys.filter { it !in ids }.forEach { jobs.remove(it)?.cancel() }
        candidates.keys.retainAll(ids)
        emit()
        if (ids.any { it !in ledger.snapshot() }) scheduleRefresh()
    }
    private fun emit() {
        val now = SystemClock.elapsedRealtime()
        val values = ledger.snapshot().mapValues { (_, value) -> value.fresh(now, interval()) }.toMutableMap()
        for ((id, session) in viewers) {
            if (session.isClosed || app.peers.all().none { it.fingerprint == id }) continue
            val record = app.peers.all().first { it.fingerprint == id }
            val result = PeerMessage.ProbeResult(1, session.welcome.device ?: DeviceInfo(record.name), true,
                Grant.toWire(session.grants), session.capabilities, session.lease == LeaseState.VIEWER)
            val ready = Readiness.classify(result)
            values[id] = ready.copy(label = if (ready.tone != Readiness.Tone.GREEN) ready.label else
                if (session.lease == LeaseState.VIEWER && Grant.CONTROL in session.grants) "Watching—another device has control" else "Connected",
                checkedAtMs = System.currentTimeMillis(), elapsedAtMs = now, address = record.target)
        }
        mutable.value = values
    }

    /** Main-thread refresh supersedes all old results and closes their sockets. */
    fun refresh(replacePending: Boolean = true) {
        if (!running) return
        if (replacePending) cancelChecks()
        val peers = app.peers.all()
        ledger.retain(peers.map { it.fingerprint }.toSet())
        val permissionMissing = Permissions.missingForNetworking(app, false).isNotEmpty()
        if (!permissionMissing) { try { browser.start() } catch (_: SecurityException) { } }
        val scheduled = ledger.schedule(peers.filter { viewers[it.fingerprint]?.isClosed != false }.map { it.fingerprint }).toMap()
        for (peer in peers) {
            val ticket = scheduled[peer.fingerprint] ?: continue
            if (permissionMissing) {
                ledger.complete(peer.fingerprint, ticket, Readiness(Readiness.Tone.YELLOW, "Local network permission needed"))
                continue
            }
            val discovered = candidates[peer.fingerprint]
            val pending = Pending()
            jobs[peer.fingerprint] = pending
            pending.future = workers.submit {
                val started = SystemClock.elapsedRealtime()
                val addresses = listOfNotNull(discovered, peer.target).distinct()
                var value = Readiness(Readiness.Tone.YELLOW, "No serving address", detail = "Start serving on ${peer.name}, then refresh.")
                var verified: TargetAddress? = null
                var lastFailure: Readiness? = null
                for ((index, address) in addresses.withIndex()) {
                    if (pending.cancelled) return@submit
                    val remaining = 5_000 - (SystemClock.elapsedRealtime() - started).toInt()
                    if (remaining <= 0) break
                    try {
                        val timeout = if (index < addresses.lastIndex) minOf(remaining, 2_000) else remaining
                        val result = ControllerClient(app.identity, app.clientInfo).probe(address.host, address.port, peer.id, timeout) { socket ->
                            pending.socket.set(socket)
                            if (pending.cancelled) socket.close()
                        }
                        value = Readiness.classify(result)
                        verified = address
                        break
                    } catch (e: Exception) {
                        if (pending.cancelled) return@submit
                        // Authenticated rejections override failed discovery hints.
                        lastFailure = Readiness.preferFailure(lastFailure, e)
                        value = lastFailure
                        if (e is PeerException) break // authenticated rejection, not an address problem
                    } finally { pending.socket.set(null) }
                }
                val checked = System.currentTimeMillis()
                val complete = value.copy(checkedAtMs = checked, elapsedAtMs = SystemClock.elapsedRealtime(), address = verified ?: peer.target)
                main.post {
                    if (!running || pending.cancelled || app.peers.find(peer.id) == null) return@post
                    if (ledger.complete(peer.fingerprint, ticket, complete)) {
                        jobs.remove(peer.fingerprint)
                        if (verified != null && complete.result != null) persistContact(peer, verified, complete, checked)
                        emit()
                    }
                }
            }
        }
        emit()
    }

    private fun persistContact(peer: PeerRecord, address: TargetAddress, status: Readiness, now: Long) {
        val result = status.result ?: return
        // Coalesce routine contact writes to once per five minutes; metadata changes persist immediately.
        workers.execute {
            try {
                app.peers.update(peer.id) { current ->
                    current?.takeIf { it.target == peer.target && (it.lastSeenMs ?: 0) <= now }?.let {
                        val name = result.device.name.filter { c -> !c.isISOControl() }.trim().take(64).ifBlank { it.name }
                        val seen = if (now - (it.lastSeenMs ?: 0) >= 300_000) now else it.lastSeenMs
                        it.copy(name = name, target = address, grantedByPeer = result.grants, lastSeenMs = seen)
                    } ?: current
                }
            } catch (e: java.io.IOException) {
                io.github.pwnedbygary.scterm.peer.PeerLog.w("could not record peer contact", e)
            }
        }
    }
}
