package io.github.pwnedbygary.scterm.peer

import io.github.pwnedbygary.scterm.protocol.Fingerprint
import io.github.pwnedbygary.scterm.protocol.Grant
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import java.io.File
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList

/** Where a paired peer serves, so this device can connect to it as a controller. */
@Serializable
data class TargetAddress(val host: String, val port: Int)

/**
 * One paired identity. Pairing records both directions, but each is
 * authorized separately: [inbound] is what the peer may do on this device;
 * [target] and [grantedByPeer] describe what this device may do on the peer
 * (which that peer enforces, not us).
 */
@Serializable
data class PeerRecord(
    val fingerprint: String,
    val name: String,
    val inbound: List<String> = emptyList(),
    val target: TargetAddress? = null,
    val grantedByPeer: List<String> = emptyList(),
    val pairedAtMs: Long,
    val lastSeenMs: Long? = null,
) {
    val id: Fingerprint get() = Fingerprint.parse(fingerprint)
    val inboundGrants: Set<Grant> get() = Grant.parse(inbound)
}

interface PeerStore {
    fun all(): List<PeerRecord>

    fun find(fingerprint: Fingerprint): PeerRecord?

    /** Atomically replaces the record; [transform] returning null removes it. */
    fun update(fingerprint: Fingerprint, transform: (PeerRecord?) -> PeerRecord?)

    fun remove(fingerprint: Fingerprint) = update(fingerprint) { null }

    /** Notified after any change to [fingerprint]'s record, on the writer's thread. */
    fun addListener(listener: (Fingerprint) -> Unit): AutoCloseable
}

open class InMemoryPeerStore(initial: Collection<PeerRecord> = emptyList()) : PeerStore {
    private val lock = Any()
    private val records = LinkedHashMap<String, PeerRecord>().apply { initial.forEach { put(it.fingerprint, it) } }
    private val listeners = CopyOnWriteArrayList<(Fingerprint) -> Unit>()

    override fun all(): List<PeerRecord> = synchronized(lock) { records.values.toList() }

    override fun find(fingerprint: Fingerprint): PeerRecord? = synchronized(lock) { records[fingerprint.hex] }

    override fun update(fingerprint: Fingerprint, transform: (PeerRecord?) -> PeerRecord?) {
        val changed = synchronized(lock) {
            val before = records[fingerprint.hex]
            val after = transform(before)
            require(after == null || after.fingerprint == fingerprint.hex) { "record fingerprint mismatch" }
            if (after == before) return
            if (after == null) records.remove(fingerprint.hex) else records[fingerprint.hex] = after
            persist(records.values.toList())
            true
        }
        if (changed) listeners.forEach { it(fingerprint) }
    }

    override fun addListener(listener: (Fingerprint) -> Unit): AutoCloseable {
        listeners += listener
        return AutoCloseable { listeners -= listener }
    }

    /** Called under the store lock after every change. */
    protected open fun persist(records: List<PeerRecord>) = Unit
}

/**
 * JSON file store in app-private storage. Each change rewrites a temp file and
 * renames it over the old one, so a crash never leaves half a peer list.
 */
class FilePeerStore private constructor(private val file: File, initial: List<PeerRecord>) : InMemoryPeerStore(initial) {
    @Serializable
    private data class Document(val version: Int = 1, val peers: List<PeerRecord> = emptyList())

    override fun persist(records: List<PeerRecord>) {
        val tmp = File(file.parentFile, file.name + ".tmp")
        tmp.writeText(JSON.encodeToString(Document.serializer(), Document(peers = records)))
        if (!tmp.renameTo(file)) {
            tmp.delete()
            throw IOException("could not replace ${file.name}")
        }
    }

    companion object {
        private val JSON = Json {
            ignoreUnknownKeys = true
            prettyPrint = true
        }

        fun open(file: File): FilePeerStore {
            val peers = if (file.exists()) {
                try {
                    JSON.decodeFromString(Document.serializer(), file.readText()).peers
                } catch (e: SerializationException) {
                    // Keep the unreadable file for inspection rather than silently losing pairings.
                    file.renameTo(File(file.parentFile, file.name + ".corrupt"))
                    PeerLog.e("peer store unreadable; starting empty", e)
                    emptyList()
                }
            } else {
                emptyList()
            }
            return FilePeerStore(file, peers)
        }
    }
}
